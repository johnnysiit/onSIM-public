package sipgateway

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/emiago/diago"
	"github.com/emiago/diago/audio"
	diagomedia "github.com/emiago/diago/media"
	"github.com/emiago/sipgo"
	"github.com/emiago/sipgo/sip"

	"onsim/internal/app"
	"onsim/internal/domain"
	"onsim/internal/media"
	"onsim/internal/store"
)

type Config struct {
	Listen, Asterisk, Target, GeneratedConfig string
}

type Gateway struct {
	state  *store.State
	app    *app.Service
	media  *media.Manager
	cfg    Config
	log    *slog.Logger
	ua     *sipgo.UserAgent
	server *sipgo.Server
	client *sipgo.Client
	diago  *diago.Diago

	mu     sync.Mutex
	legs   map[string]activeLeg
	wakeup chan struct{}
}

type activeLeg struct {
	dialog diago.DialogSession
	cancel context.CancelFunc
}

func New(state *store.State, service *app.Service, mediaManager *media.Manager, cfg Config, log *slog.Logger) (*Gateway, error) {
	host, port, err := splitHostPort(cfg.Listen)
	if err != nil {
		return nil, fmt.Errorf("sip listen: %w", err)
	}
	// sipgo also uses the UA name when it builds a dialog Contact URI.
	// Keep it URI-token-safe: a literal space produced
	// `Contact: <sip:onSIM Gateway@...>`, which strict PJSIP peers reject.
	ua, err := sipgo.NewUA(sipgo.WithUserAgent("onSIM-Gateway"))
	if err != nil {
		return nil, err
	}
	server, err := sipgo.NewServer(ua, sipgo.WithServerLogger(log))
	if err != nil {
		_ = ua.Close()
		return nil, err
	}
	client, err := sipgo.NewClient(ua, sipgo.WithClientAddr(cfg.Listen), sipgo.WithClientLogger(log))
	if err != nil {
		_ = ua.Close()
		return nil, err
	}
	dg := diago.NewDiago(ua,
		diago.WithServer(server),
		diago.WithLogger(log),
		diago.WithTransport(diago.Transport{
			Transport: "udp",
			BindHost:  host,
			BindPort:  port,
		}),
	)
	g := &Gateway{
		state: state, app: service, media: mediaManager, cfg: cfg, log: log,
		ua: ua, server: server, client: client, diago: dg,
		legs: map[string]activeLeg{}, wakeup: make(chan struct{}, 1),
	}
	server.OnMessage(g.handleMessage)
	return g, nil
}

func (g *Gateway) Start(ctx context.Context) {
	if err := g.ensureCredentials(); err != nil {
		g.log.Error("prepare SIP credentials", "error", err)
	}
	g.reloadAsterisk()
	if err := g.diago.ServeBackground(ctx, g.handleDialog); err != nil {
		g.log.Error("start SIP gateway", "error", err)
		g.setStatus("asteriskOffline")
		return
	}
	g.log.Info("SIP gateway listening", "address", g.cfg.Listen, "asterisk", g.cfg.Asterisk, "target", g.cfg.Target)
	go g.deliveryLoop(ctx)
	go g.healthLoop(ctx)
	go func() {
		<-ctx.Done()
		g.mu.Lock()
		for _, leg := range g.legs {
			leg.cancel()
		}
		g.mu.Unlock()
		_ = g.ua.Close()
	}()
}

func (g *Gateway) IncomingCall(call *domain.Call) {
	if !g.state.Settings().SIPEnabled {
		return
	}
	go g.originateIncoming(call)
}

func (g *Gateway) IncomingSMS(*domain.Message) { g.signalDelivery() }

func (g *Gateway) CallEnded(call *domain.Call) {
	g.mu.Lock()
	leg, ok := g.legs[call.ID]
	if ok {
		delete(g.legs, call.ID)
	}
	g.mu.Unlock()
	if ok {
		leg.cancel()
		go func() { _ = leg.dialog.Hangup(context.Background()); _ = leg.dialog.Close() }()
	}
}

func (g *Gateway) handleDialog(dialog *diago.DialogServerSession) {
	_ = dialog.Trying()
	if !g.state.Settings().SIPEnabled {
		_ = dialog.Respond(sip.StatusServiceUnavailable, "SIP gateway disabled", nil)
		return
	}
	number := dialog.ToUser()
	call, err := g.app.DialFrom(dialog.Context(), number, "sip")
	if err != nil {
		code := sip.StatusBadRequest
		switch err.Error() {
		case "CALL_STATE_CONFLICT":
			code = sip.StatusBusyHere
		case "MODEM_OFFLINE":
			code = sip.StatusServiceUnavailable
		case "NUMBER_BLOCKED", "FEATURE_DISABLED":
			code = sip.StatusForbidden
		}
		_ = dialog.Respond(code, err.Error(), nil)
		return
	}
	ctx, cancel := context.WithCancel(dialog.Context())
	g.setLeg(call.ID, dialog, cancel)
	defer g.clearLeg(call.ID, dialog)
	_ = dialog.Ringing()

	if !g.waitForActive(ctx, call.ID, "sip") {
		_ = dialog.Respond(sip.StatusTemporarilyUnavailable, "Cellular call ended", nil)
		return
	}
	if err = dialog.AnswerOptions(diago.AnswerOptions{Codecs: []diagomedia.Codec{
		diagomedia.CodecAudioUlaw, diagomedia.CodecTelephoneEvent8000,
	}}); err != nil {
		_, _ = g.app.Hangup(context.Background(), call.ID, "sip_answer_failed")
		return
	}
	g.bridgeDialog(ctx, call.ID, dialog)
	if active := g.state.Call(call.ID); active != nil && active.State != domain.CallEnded && active.State != domain.CallFailed {
		_, _ = g.app.Hangup(context.Background(), call.ID, "sip_hangup")
	}
}

func (g *Gateway) originateIncoming(call *domain.Call) {
	host, port, err := splitHostPort(g.cfg.Asterisk)
	if err != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go g.cancelIfClaimedElsewhere(ctx, cancel, call.ID)
	from := &sip.FromHeader{
		DisplayName: firstNonEmpty(call.DisplayName, call.Filter.Label, call.Number),
		Address:     sip.Uri{Scheme: "sip", User: call.Number, Host: "onsim.local"},
		Params:      tagParams(),
	}
	var lastStatus int
	dialog, err := g.diago.Invite(ctx, sip.Uri{Scheme: "sip", User: g.cfg.Target, Host: host, Port: port}, diago.InviteOptions{
		Transport: "udp",
		Headers:   []sip.Header{from, sip.NewHeader("X-onSIM-Call-ID", call.ID)},
		OnResponse: func(response *sip.Response) error {
			lastStatus = response.StatusCode
			return nil
		},
	})
	if err != nil {
		var responseErr sipgo.ErrDialogResponse
		if errors.As(err, &responseErr) && responseErr.Res != nil {
			lastStatus = responseErr.Res.StatusCode
		}
		if lastStatus == sip.StatusBusyHere || lastStatus == sip.StatusGlobalDecline {
			_, _ = g.app.Hangup(context.Background(), call.ID, "sip_rejected")
		}
		return
	}
	g.setLeg(call.ID, dialog, cancel)
	defer g.clearLeg(call.ID, dialog)
	if _, err = g.app.AnswerFrom(context.Background(), call.ID, "sip"); err != nil {
		_ = dialog.Hangup(context.Background())
		return
	}
	if !g.waitForActive(ctx, call.ID, "sip") {
		_ = dialog.Hangup(context.Background())
		return
	}
	g.bridgeDialog(ctx, call.ID, dialog)
}

func (g *Gateway) bridgeDialog(ctx context.Context, callID string, dialog diago.DialogSession) {
	props := diago.MediaProps{}
	dtmf := &diago.DTMFReader{}
	reader, err := dialog.Media().AudioReader(
		diago.WithAudioReaderMediaProps(&props),
		diago.WithAudioReaderDTMF(dtmf),
	)
	if err != nil {
		return
	}
	dtmf.OnDTMF(func(key rune) error {
		return g.app.DTMF(context.Background(), callID, string(key))
	})
	decoder, err := audio.NewPCMDecoderReader(props.Codec.PayloadType, reader)
	if err != nil {
		return
	}
	outProps := diago.MediaProps{}
	writer, err := dialog.Media().AudioWriter(diago.WithAudioWriterMediaProps(&outProps))
	if err != nil {
		return
	}
	encoder, err := audio.NewPCMEncoderWriter(outProps.Codec.PayloadType, writer)
	if err != nil {
		return
	}
	if err = g.media.BridgePCM(ctx, callID, "sip", decoder, encoder); err != nil && !errors.Is(err, context.Canceled) {
		g.log.Warn("SIP media ended", "call", callID, "error", err)
	}
}

func (g *Gateway) waitForActive(ctx context.Context, callID, owner string) bool {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		call := g.state.Call(callID)
		if call == nil || call.State == domain.CallEnded || call.State == domain.CallFailed || (call.MediaOwner != "" && call.MediaOwner != owner) {
			return false
		}
		if call.State == domain.CallActive {
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-ticker.C:
		}
	}
}

func (g *Gateway) cancelIfClaimedElsewhere(ctx context.Context, cancel context.CancelFunc, callID string) {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		call := g.state.Call(callID)
		if call == nil || call.State == domain.CallEnded || call.State == domain.CallFailed || (call.MediaOwner != "" && call.MediaOwner != "sip") {
			cancel()
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (g *Gateway) handleMessage(req *sip.Request, tx sip.ServerTransaction) {
	if !g.state.Settings().SIPEnabled {
		_ = tx.Respond(sip.NewResponseFromRequest(req, sip.StatusServiceUnavailable, "SIP gateway disabled", nil))
		return
	}
	contentType := req.ContentType()
	if contentType == nil || !strings.HasPrefix(strings.ToLower(contentType.Value()), "text/plain") {
		_ = tx.Respond(sip.NewResponseFromRequest(req, sip.StatusUnsupportedMediaType, "text/plain required", nil))
		return
	}
	if _, err := g.app.SendSMS(context.Background(), req.Recipient.User, string(req.Body())); err != nil {
		code := sip.StatusBadRequest
		if err.Error() == "NUMBER_BLOCKED" || err.Error() == "FEATURE_DISABLED" {
			code = sip.StatusForbidden
		}
		if err.Error() == "MODEM_OFFLINE" {
			code = sip.StatusServiceUnavailable
		}
		_ = tx.Respond(sip.NewResponseFromRequest(req, code, err.Error(), nil))
		return
	}
	_ = tx.Respond(sip.NewResponseFromRequest(req, sip.StatusAccepted, "Accepted", nil))
}

func (g *Gateway) deliveryLoop(ctx context.Context) {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	g.deliverPending(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-g.wakeup:
			g.deliverPending(ctx)
		case <-ticker.C:
			g.deliverPending(ctx)
		}
	}
}

func (g *Gateway) deliverPending(ctx context.Context) {
	now := time.Now().UTC()
	pending := 0
	for _, msg := range g.state.Snapshot(false).Messages {
		delivery := msg.SIPDelivery
		if delivery == nil || delivery.Status == "delivered" || delivery.Status == "expired" || msg.Deleted {
			continue
		}
		pending++
		if now.After(delivery.ExpiresAt) {
			g.updateDelivery(msg.ID, func(d *domain.SIPDelivery) {
				d.Status, d.LastError, d.NextAt = "expired", "DELIVERY_EXPIRED", nil
			})
			continue
		}
		if !g.state.Settings().SIPEnabled || delivery.NextAt == nil || now.Before(*delivery.NextAt) {
			continue
		}
		err := g.sendMessage(ctx, msg)
		g.updateDelivery(msg.ID, func(d *domain.SIPDelivery) {
			d.Attempts++
			if err == nil {
				d.Status, d.LastError, d.NextAt = "delivered", "", nil
				return
			}
			next := now.Add(retryDelay(d.Attempts))
			d.Status, d.LastError, d.NextAt = "queued", err.Error(), &next
		})
	}
	g.updatePending(pending)
}

func (g *Gateway) sendMessage(parent context.Context, msg *domain.Message) error {
	host, port, err := splitHostPort(g.cfg.Asterisk)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(parent, 8*time.Second)
	defer cancel()
	req := sip.NewRequest(sip.MESSAGE, sip.Uri{Scheme: "sip", User: g.cfg.Target, Host: host, Port: port})
	req.SetTransport("UDP")
	req.AppendHeader(&sip.FromHeader{
		DisplayName: msg.Filter.Label,
		Address:     sip.Uri{Scheme: "sip", User: msg.Number, Host: "onsim.local"},
		Params:      tagParams(),
	})
	req.AppendHeader(sip.NewHeader("Content-Type", "text/plain; charset=utf-8"))
	req.AppendHeader(sip.NewHeader("X-onSIM-Message-ID", msg.ID))
	req.SetBody([]byte(msg.Body))
	response, err := g.client.Do(ctx, req)
	if err != nil {
		return err
	}
	if !response.IsSuccess() {
		return fmt.Errorf("SIP_%d", response.StatusCode)
	}
	return nil
}

func (g *Gateway) healthLoop(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	g.checkHealth(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			g.checkHealth(ctx)
		}
	}
}

func (g *Gateway) checkHealth(parent context.Context) {
	if !g.state.Settings().SIPEnabled {
		g.setStatus("disabled")
		return
	}
	host, port, err := splitHostPort(g.cfg.Asterisk)
	if err != nil {
		g.setStatus("asteriskOffline")
		return
	}
	ctx, cancel := context.WithTimeout(parent, 3*time.Second)
	defer cancel()
	req := sip.NewRequest(sip.OPTIONS, sip.Uri{Scheme: "sip", User: g.cfg.Target, Host: host, Port: port})
	req.SetTransport("UDP")
	response, err := g.client.Do(ctx, req)
	if err != nil || !response.IsSuccess() {
		g.setStatus("asteriskOffline")
		return
	}
	if g.state.ActiveCall() != nil && g.state.ActiveCall().MediaOwner == "sip" {
		g.setStatus("active")
		return
	}
	g.setStatus("ready")
}

func (g *Gateway) setStatus(status string) {
	snapshot := g.state.Snapshot(false)
	device := snapshot.Device
	device.SIPStatus = status
	_, _ = g.state.UpdateDevice(device)
}

func (g *Gateway) updatePending(pending int) {
	snapshot := g.state.Snapshot(false)
	device := snapshot.Device
	device.SIPPending = pending
	_, _ = g.state.UpdateDevice(device)
}

func (g *Gateway) updateDelivery(id string, update func(*domain.SIPDelivery)) {
	msg := g.state.Message(id)
	if msg == nil || msg.SIPDelivery == nil {
		return
	}
	update(msg.SIPDelivery)
	msg.Version++
	_, _ = g.state.UpsertMessage(msg)
}

func (g *Gateway) signalDelivery() {
	select {
	case g.wakeup <- struct{}{}:
	default:
	}
}

func retryDelay(attempt int) time.Duration {
	switch attempt {
	case 1:
		return 30 * time.Second
	case 2:
		return 2 * time.Minute
	case 3:
		return 10 * time.Minute
	default:
		return time.Hour
	}
}

func (g *Gateway) setLeg(callID string, dialog diago.DialogSession, cancel context.CancelFunc) {
	g.mu.Lock()
	g.legs[callID] = activeLeg{dialog: dialog, cancel: cancel}
	g.mu.Unlock()
}

func (g *Gateway) clearLeg(callID string, dialog diago.DialogSession) {
	g.mu.Lock()
	if leg, ok := g.legs[callID]; ok && leg.dialog == dialog {
		delete(g.legs, callID)
	}
	g.mu.Unlock()
	_ = dialog.Close()
}

func (g *Gateway) ensureCredentials() error {
	settings := g.state.Settings()
	if settings.SIPPassword == "" {
		settings.SIPPassword = randomSecret()
		settings.SIPPasswordSeen = false
		if _, err := g.state.UpdateSettings(settings); err != nil {
			return err
		}
	}
	return g.writeAsteriskConfig(settings.SIPPassword)
}

func (g *Gateway) RevealCredentials() (map[string]string, error) {
	settings := g.state.Settings()
	if settings.SIPPasswordSeen {
		return nil, errors.New("CREDENTIAL_ALREADY_REVEALED")
	}
	settings.SIPPasswordSeen = true
	if _, err := g.state.UpdateSettings(settings); err != nil {
		return nil, err
	}
	return map[string]string{"username": g.cfg.Target, "password": settings.SIPPassword, "server": lanAddress(), "transport": "UDP"}, nil
}

func (g *Gateway) ResetCredentials() (map[string]string, error) {
	settings := g.state.Settings()
	settings.SIPPassword = randomSecret()
	settings.SIPPasswordSeen = true
	if _, err := g.state.UpdateSettings(settings); err != nil {
		return nil, err
	}
	if err := g.writeAsteriskConfig(settings.SIPPassword); err != nil {
		return nil, err
	}
	g.reloadAsterisk()
	return map[string]string{"username": g.cfg.Target, "password": settings.SIPPassword, "server": lanAddress(), "transport": "UDP"}, nil
}

func (g *Gateway) reloadAsterisk() {
	if path, err := exec.LookPath("asterisk"); err == nil {
		command := exec.Command(path, "-rx", "module reload res_pjsip.so")
		if output, runErr := command.CombinedOutput(); runErr != nil {
			g.log.Warn("reload Asterisk PJSIP", "error", runErr, "output", strings.TrimSpace(string(output)))
		}
	}
}

func (g *Gateway) ApplySettings() error {
	if err := g.ensureCredentials(); err != nil {
		return err
	}
	g.reloadAsterisk()
	g.signalDelivery()
	g.checkHealth(context.Background())
	return nil
}

func (g *Gateway) writeAsteriskConfig(password string) error {
	if g.cfg.GeneratedConfig == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(g.cfg.GeneratedConfig), 0o750); err != nil {
		return err
	}
	content := fmt.Sprintf(`; Generated by onSIM. Local edits are overwritten.
[transport-udp]
type=transport
protocol=udp
bind=0.0.0.0:5060

[1001-auth]
type=auth
auth_type=userpass
username=1001
password=%s

[1001]
type=aor
max_contacts=1
remove_existing=yes
qualify_frequency=30

[1001]
type=endpoint
transport=transport-udp
context=from-groundwire
message_context=from-groundwire-message
disallow=all
allow=ulaw
auth=1001-auth
aors=1001
direct_media=no
dtmf_mode=rfc4733

[onsim]
type=aor
contact=sip:%s
qualify_frequency=30

[onsim]
type=endpoint
transport=transport-udp
context=from-onsim
message_context=from-onsim-message
disallow=all
allow=ulaw
aors=onsim
direct_media=no
dtmf_mode=rfc4733

[onsim-identify]
type=identify
endpoint=onsim
match=127.0.0.1
`, password, g.cfg.Listen)
	temp, err := os.CreateTemp(filepath.Dir(g.cfg.GeneratedConfig), ".pjsip-generated-*")
	if err != nil {
		return err
	}
	name := temp.Name()
	defer os.Remove(name)
	if err = temp.Chmod(0o640); err == nil {
		_, err = io.WriteString(temp, content)
	}
	closeErr := temp.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(name, g.cfg.GeneratedConfig)
}

func randomSecret() string {
	raw := make([]byte, 24)
	_, _ = rand.Read(raw)
	return base64.RawURLEncoding.EncodeToString(raw)
}

func splitHostPort(value string) (string, int, error) {
	host, rawPort, err := net.SplitHostPort(value)
	if err != nil {
		return "", 0, err
	}
	port, err := strconv.Atoi(rawPort)
	return host, port, err
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return "onSIM"
}

func tagParams() sip.HeaderParams {
	params := sip.NewParams()
	params.Add("tag", sip.GenerateTagN(16))
	return params
}

func lanAddress() string {
	interfaces, _ := net.Interfaces()
	for _, networkInterface := range interfaces {
		if networkInterface.Flags&net.FlagUp == 0 || networkInterface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addresses, _ := networkInterface.Addrs()
		for _, address := range addresses {
			ip, _, err := net.ParseCIDR(address.String())
			if err == nil && ip.To4() != nil && ip.IsPrivate() {
				return ip.String()
			}
		}
	}
	return "127.0.0.1"
}
