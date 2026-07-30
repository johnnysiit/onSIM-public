package modem

import (
	"bufio"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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
	"sync/atomic"
	"time"
)

const androidProtocolVersion = 1

type AndroidConfig struct {
	ADB             string
	ADBHome         string
	ADBServerSocket string
	Serial          string
	SubscriptionID  string
	TokenPath       string
	ControlAddr     string
	AudioAddr       string
}

type androidController struct {
	cfg    AndroidConfig
	log    *slog.Logger
	events chan Event

	mu      sync.RWMutex
	healthy bool
	audio   bool
	status  Status
	conn    net.Conn
	writer  *bufio.Writer

	writeMu sync.Mutex
	reqMu   sync.Mutex
	pending map[string]chan wireMessage
	nextID  atomic.Uint64
	token   string
}

type wireMessage struct {
	Version int             `json:"version"`
	Type    string          `json:"type"`
	ID      string          `json:"id,omitempty"`
	Action  string          `json:"action,omitempty"`
	Nonce   string          `json:"nonce,omitempty"`
	MAC     string          `json:"mac,omitempty"`
	Error   string          `json:"error,omitempty"`
	Data    json.RawMessage `json:"data,omitempty"`
}

type androidStatus struct {
	SIMReady            bool           `json:"simReady"`
	Registered          bool           `json:"registered"`
	VoiceReady          bool           `json:"voiceReady"`
	Operator            string         `json:"operator"`
	AccessTechnology    string         `json:"accessTechnology"`
	Signal              int            `json:"signal"`
	SignalDBm           int            `json:"signalDbm"`
	PhoneNumber         string         `json:"phoneNumber"`
	ICCID               string         `json:"iccid"`
	IMSI                string         `json:"imsi"`
	Manufacturer        string         `json:"manufacturer"`
	Model               string         `json:"model"`
	IMEI                string         `json:"imei"`
	AndroidVersion      string         `json:"androidVersion"`
	BuildID             string         `json:"buildId"`
	SecurityPatch       string         `json:"securityPatch"`
	BasebandVersion     string         `json:"basebandVersion"`
	BatteryLevel        int            `json:"batteryLevel"`
	BatteryCharging     bool           `json:"batteryCharging"`
	SubscriptionID      int            `json:"subscriptionId"`
	SIMSlot             int            `json:"simSlot"`
	IMSRegistered       bool           `json:"imsRegistered"`
	VoLTE               bool           `json:"volte"`
	CompanionVersion    string         `json:"companionVersion"`
	AudioDownlinkOK     bool           `json:"audioDownlinkOk"`
	AudioUplinkOK       bool           `json:"audioUplinkOk"`
	AudioDownlinkFrames int64          `json:"audioDownlinkFrames"`
	AudioUplinkFrames   int64          `json:"audioUplinkFrames"`
	AudioUplinkBytes    int64          `json:"audioUplinkBytes"`
	AudioLastError      string         `json:"audioLastError"`
	Subscriptions       []Subscription `json:"subscriptions"`
}

type androidEvent struct {
	Event          string `json:"event"`
	Number         string `json:"number,omitempty"`
	Body           string `json:"body,omitempty"`
	ClientID       string `json:"clientId,omitempty"`
	ProviderID     string `json:"providerId,omitempty"`
	Reason         string `json:"reason,omitempty"`
	SubscriptionID int    `json:"subscriptionId,omitempty"`
}

func NewAndroid(cfg AndroidConfig, log *slog.Logger) Controller {
	if cfg.ADB == "" {
		cfg.ADB = "adb"
	}
	if cfg.ControlAddr == "" {
		cfg.ControlAddr = "127.0.0.1:47100"
	}
	if cfg.AudioAddr == "" {
		cfg.AudioAddr = "127.0.0.1:47101"
	}
	return &androidController{
		cfg: cfg, log: log, events: make(chan Event, 128),
		pending: make(map[string]chan wireMessage),
		status: Status{
			Provider: "android", Transport: "usb+adb", Signal: -1, SignalDBm: -1,
			ADBState: "disconnected", ProtocolVersion: androidProtocolVersion,
		},
	}
}

func (a *androidController) Start(ctx context.Context) {
	if err := a.prepare(); err != nil {
		a.setOffline(err)
	}
	go a.run(ctx)
}

func (a *androidController) Events() <-chan Event { return a.events }
func (a *androidController) Healthy() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.healthy
}
func (a *androidController) AudioCapable() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.audio
}

func (a *androidController) prepare() error {
	if a.cfg.ADBHome == "" {
		return errors.New("ANDROID_ADB_HOME_NOT_CONFIGURED")
	}
	if err := os.MkdirAll(filepath.Join(a.cfg.ADBHome, ".android"), 0o700); err != nil {
		return fmt.Errorf("create adb home: %w", err)
	}
	token, err := os.ReadFile(a.cfg.TokenPath)
	if err != nil {
		return fmt.Errorf("read android token: %w", err)
	}
	secret := strings.TrimSpace(string(token))
	if len(secret) < 32 {
		return errors.New("ANDROID_TOKEN_INVALID")
	}
	if err = a.startADBServer(); err != nil {
		return err
	}
	a.token = secret
	return nil
}

func (a *androidController) startADBServer() error {
	if a.cfg.ADBServerSocket == "" {
		return nil
	}
	port, err := a.adbServerPort()
	if err != nil {
		return err
	}
	cmd := exec.Command(a.cfg.ADB, "-P", port, "start-server")
	cmd.Env = append(os.Environ(), "HOME="+a.cfg.ADBHome)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("start private adb server: %s: %w", strings.TrimSpace(string(out)), err)
	}
	return nil
}

func GenerateAndroidToken(path string) error {
	if _, err := os.Stat(path); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(hex.EncodeToString(buf)+"\n"), 0o600)
}

func (a *androidController) run(ctx context.Context) {
	delay := time.Second
	for ctx.Err() == nil {
		if a.token == "" {
			if err := a.prepare(); err != nil {
				a.setOffline(err)
				if !waitContext(ctx, delay) {
					return
				}
				continue
			}
		}
		if err := a.connect(ctx); err != nil {
			a.setOffline(err)
			if !waitContext(ctx, delay) {
				return
			}
			if delay < 30*time.Second {
				delay *= 2
			}
			continue
		}
		delay = time.Second
		err := a.readLoop(ctx)
		a.closeControl()
		a.setOffline(err)
	}
}

func (a *androidController) connect(ctx context.Context) error {
	state, err := a.adb(ctx, "get-state")
	if err != nil || strings.TrimSpace(state) != "device" {
		if err == nil {
			err = fmt.Errorf("adb state %q", strings.TrimSpace(state))
		}
		return fmt.Errorf("ANDROID_ADB_UNAVAILABLE: %w", err)
	}
	if err = a.forward(ctx, a.cfg.ControlAddr, "onsim-control"); err != nil {
		return err
	}
	if err = a.forward(ctx, a.cfg.AudioAddr, "onsim-audio"); err != nil {
		return err
	}
	conn, err := net.DialTimeout("tcp", a.cfg.ControlAddr, 5*time.Second)
	if err != nil {
		return fmt.Errorf("connect companion: %w", err)
	}
	writer := bufio.NewWriter(conn)
	nonce, err := secureNonce()
	if err != nil {
		conn.Close()
		return fmt.Errorf("create authentication nonce: %w", err)
	}
	auth := wireMessage{
		Version: androidProtocolVersion, Type: "auth", Nonce: nonce,
		MAC: hmacHex(a.token, "control:"+nonce),
	}
	if err = json.NewEncoder(writer).Encode(auth); err == nil {
		err = writer.Flush()
	}
	if err != nil {
		conn.Close()
		return fmt.Errorf("authenticate companion: %w", err)
	}
	a.mu.Lock()
	a.conn, a.writer, a.healthy = conn, writer, true
	a.status.ADBState = "device"
	a.status.LastError = ""
	a.mu.Unlock()
	a.emit(Event{Type: "device.online"})
	return nil
}

func (a *androidController) readLoop(ctx context.Context) error {
	a.mu.RLock()
	conn := a.conn
	a.mu.RUnlock()
	if conn == nil {
		return errors.New("ANDROID_CONTROL_DISCONNECTED")
	}
	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, 4096), 1024*1024)
	for scanner.Scan() {
		var msg wireMessage
		if err := json.Unmarshal(scanner.Bytes(), &msg); err != nil {
			a.log.Warn("invalid android gateway message", "error", err)
			continue
		}
		switch msg.Type {
		case "response":
			a.reqMu.Lock()
			ch := a.pending[msg.ID]
			delete(a.pending, msg.ID)
			a.reqMu.Unlock()
			if ch != nil {
				ch <- msg
			}
		case "event":
			var ev androidEvent
			if err := json.Unmarshal(msg.Data, &ev); err == nil {
				a.handleEvent(ev)
			}
		case "status":
			a.updateStatus(msg.Data)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	return io.EOF
}

func (a *androidController) handleEvent(ev androidEvent) {
	switch ev.Event {
	case "status.changed":
		_, _ = a.request(context.Background(), "status", nil)
	case "audio.ready":
		a.mu.Lock()
		a.audio = true
		a.status.AudioDownlinkOK = true
		a.status.AudioUplinkOK = true
		a.status.LastError = ""
		a.mu.Unlock()
	case "audio.failed":
		a.mu.Lock()
		a.audio = false
		a.status.AudioUplinkOK = false
		a.status.LastError = ev.Reason
		a.mu.Unlock()
		a.emit(Event{Type: ev.Event, Reason: ev.Reason})
	default:
		a.emit(Event{
			Type: ev.Event, Number: ev.Number, Body: ev.Body, ClientID: ev.ClientID,
			ProviderID: ev.ProviderID, Reason: ev.Reason, SubscriptionID: ev.SubscriptionID,
		})
	}
}

func (a *androidController) updateStatus(raw json.RawMessage) {
	var in androidStatus
	if json.Unmarshal(raw, &in) != nil {
		return
	}
	a.mu.Lock()
	a.status.SIMReady = in.SIMReady
	a.status.Registered = in.Registered
	a.status.VoiceReady = in.VoiceReady
	a.status.Operator = in.Operator
	a.status.AccessTech = in.AccessTechnology
	a.status.Signal = in.Signal
	a.status.SignalDBm = in.SignalDBm
	a.status.PhoneNumber = in.PhoneNumber
	a.status.ICCID = in.ICCID
	a.status.IMSI = in.IMSI
	a.status.Manufacturer = in.Manufacturer
	a.status.Model = in.Model
	a.status.IMEI = in.IMEI
	a.status.AndroidVersion = in.AndroidVersion
	a.status.BuildID = in.BuildID
	a.status.SecurityPatch = in.SecurityPatch
	a.status.BasebandVersion = in.BasebandVersion
	a.status.BatteryLevel = in.BatteryLevel
	a.status.BatteryCharging = in.BatteryCharging
	a.status.SubscriptionID = in.SubscriptionID
	a.status.SIMSlot = in.SIMSlot
	a.status.IMSRegistered = in.IMSRegistered
	a.status.VoLTE = in.VoLTE
	a.status.CompanionVersion = in.CompanionVersion
	a.status.AudioDownlinkOK = in.AudioDownlinkOK
	a.status.AudioUplinkOK = in.AudioUplinkOK
	a.status.AudioDownlinkFrames = in.AudioDownlinkFrames
	a.status.AudioUplinkFrames = in.AudioUplinkFrames
	a.status.AudioUplinkBytes = in.AudioUplinkBytes
	a.status.LastError = in.AudioLastError
	a.status.Subscriptions = append([]Subscription(nil), in.Subscriptions...)
	a.audio = in.AudioDownlinkOK && in.AudioUplinkOK
	a.mu.Unlock()
}

func (a *androidController) request(ctx context.Context, action string, data any) (wireMessage, error) {
	a.mu.RLock()
	healthy := a.healthy
	a.mu.RUnlock()
	if !healthy {
		return wireMessage{}, errors.New("ANDROID_GATEWAY_OFFLINE")
	}
	id := strconv.FormatUint(a.nextID.Add(1), 10)
	raw, err := json.Marshal(data)
	if err != nil {
		return wireMessage{}, err
	}
	ch := make(chan wireMessage, 1)
	a.reqMu.Lock()
	a.pending[id] = ch
	a.reqMu.Unlock()
	msg := wireMessage{Version: androidProtocolVersion, Type: "request", ID: id, Action: action, Data: raw}
	a.writeMu.Lock()
	a.mu.RLock()
	writer := a.writer
	a.mu.RUnlock()
	if writer == nil {
		err = errors.New("ANDROID_GATEWAY_OFFLINE")
	} else if err = json.NewEncoder(writer).Encode(msg); err == nil {
		err = writer.Flush()
	}
	a.writeMu.Unlock()
	if err != nil {
		a.reqMu.Lock()
		delete(a.pending, id)
		a.reqMu.Unlock()
		return wireMessage{}, err
	}
	select {
	case response, ok := <-ch:
		if !ok {
			return wireMessage{}, errors.New("ANDROID_GATEWAY_DISCONNECTED")
		}
		if response.Error != "" {
			return response, errors.New(response.Error)
		}
		return response, nil
	case <-ctx.Done():
		a.reqMu.Lock()
		delete(a.pending, id)
		a.reqMu.Unlock()
		return wireMessage{}, ctx.Err()
	case <-time.After(15 * time.Second):
		a.reqMu.Lock()
		delete(a.pending, id)
		a.reqMu.Unlock()
		return wireMessage{}, errors.New("ANDROID_GATEWAY_TIMEOUT")
	}
}

func (a *androidController) Probe(ctx context.Context) Status {
	if response, err := a.request(ctx, "status", nil); err == nil {
		a.updateStatus(response.Data)
	}
	a.mu.RLock()
	out := a.status
	out.LastCheckedAt = time.Now().UTC()
	a.mu.RUnlock()
	return out
}

func (a *androidController) Dial(ctx context.Context, number string) error {
	return a.dialSubscription(ctx, number, a.cfg.SubscriptionID)
}
func (a *androidController) dialSubscription(ctx context.Context, number, subscriptionID string) error {
	_, err := a.request(ctx, "dial", map[string]any{"number": number, "subscriptionId": subscriptionID})
	return err
}
func (a *androidController) Answer(ctx context.Context) error {
	_, err := a.request(ctx, "answer", nil)
	return err
}
func (a *androidController) Hangup(ctx context.Context) error {
	_, err := a.request(ctx, "hangup", nil)
	return err
}
func (a *androidController) DTMF(ctx context.Context, key string) error {
	_, err := a.request(ctx, "dtmf", map[string]string{"key": key})
	return err
}
func (a *androidController) SendSMS(ctx context.Context, clientID, number, body string) error {
	return a.sendSMSSubscription(ctx, clientID, number, body, a.cfg.SubscriptionID)
}
func (a *androidController) sendSMSSubscription(ctx context.Context, clientID, number, body, subscriptionID string) error {
	_, err := a.request(ctx, "sms.send", map[string]any{
		"clientId": clientID, "number": number, "body": body, "subscriptionId": subscriptionID,
	})
	return err
}
func (a *androidController) DeleteSMS(ctx context.Context, index int) error {
	_, err := a.request(ctx, "sms.delete", map[string]int{"id": index})
	return err
}
func (a *androidController) SetMicMute(ctx context.Context, muted bool) error {
	_, err := a.request(ctx, "mute", map[string]bool{"muted": muted})
	return err
}
func (a *androidController) EnableAudio(ctx context.Context, enabled bool) error {
	if _, err := a.request(ctx, "audio.enable", map[string]bool{"enabled": enabled}); err != nil {
		return err
	}
	// An earlier failed media session marks the cached capability unavailable.
	// audio.enable re-runs qualification in the Companion, so synchronously
	// refresh that result before OpenAudio performs its preflight check. This
	// is especially important when a browser refresh replaces an active peer.
	response, err := a.request(ctx, "status", nil)
	if err != nil {
		return err
	}
	a.updateStatus(response.Data)
	return nil
}

func (a *androidController) OpenAudio(ctx context.Context) (io.ReadWriteCloser, error) {
	if !a.AudioCapable() {
		return nil, errors.New("ANDROID_AUDIO_UNAVAILABLE")
	}
	dialer := net.Dialer{Timeout: 5 * time.Second}
	conn, err := dialer.DialContext(ctx, "tcp", a.cfg.AudioAddr)
	if err != nil {
		return nil, err
	}
	nonce, nonceErr := secureNonce()
	if nonceErr != nil {
		conn.Close()
		return nil, nonceErr
	}
	if _, err = io.WriteString(conn, nonce+":"+hmacHex(a.token, "audio:"+nonce)+"\n"); err != nil {
		conn.Close()
		return nil, err
	}
	return conn, nil
}

func secureNonce() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func hmacHex(token, message string) string {
	mac := hmac.New(sha256.New, []byte(token))
	_, _ = mac.Write([]byte(message))
	return hex.EncodeToString(mac.Sum(nil))
}

func (a *androidController) adb(ctx context.Context, args ...string) (string, error) {
	all := make([]string, 0, len(args)+4)
	if a.cfg.ADBServerSocket != "" {
		port, err := a.adbServerPort()
		if err != nil {
			return "", err
		}
		all = append(all, "-P", port)
	}
	if a.cfg.Serial != "" {
		all = append(all, "-s", a.cfg.Serial)
	}
	all = append(all, args...)
	cmd := exec.CommandContext(ctx, a.cfg.ADB, all...)
	cmd.Env = append(os.Environ(), "HOME="+a.cfg.ADBHome)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("%s: %w", strings.TrimSpace(string(out)), err)
	}
	return string(out), nil
}

func (a *androidController) adbServerPort() (string, error) {
	address := strings.TrimPrefix(a.cfg.ADBServerSocket, "tcp:")
	_, port, err := net.SplitHostPort(address)
	if err != nil {
		return "", fmt.Errorf("invalid adb server socket %q: %w", a.cfg.ADBServerSocket, err)
	}
	return port, nil
}

func (a *androidController) forward(ctx context.Context, address, socket string) error {
	_, port, err := net.SplitHostPort(address)
	if err != nil {
		return err
	}
	_, err = a.adb(ctx, "forward", "tcp:"+port, "localabstract:"+socket)
	if err != nil {
		return fmt.Errorf("forward %s: %w", socket, err)
	}
	return nil
}

func (a *androidController) closeControl() {
	a.mu.Lock()
	if a.conn != nil {
		_ = a.conn.Close()
	}
	a.conn, a.writer, a.healthy, a.audio = nil, nil, false, false
	a.mu.Unlock()
	a.reqMu.Lock()
	for id, ch := range a.pending {
		delete(a.pending, id)
		close(ch)
	}
	a.reqMu.Unlock()
}

func (a *androidController) setOffline(err error) {
	a.mu.Lock()
	wasOnline := a.healthy
	a.healthy, a.audio = false, false
	a.status.ADBState = "disconnected"
	if err != nil {
		a.status.LastError = err.Error()
	}
	reason := a.status.LastError
	a.mu.Unlock()
	if wasOnline {
		a.emit(Event{Type: "device.offline", Reason: reason})
	}
}

func (a *androidController) emit(event Event) {
	select {
	case a.events <- event:
	default:
		a.log.Error("android event overflow", "event", event.Type)
	}
}

func waitContext(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-ctx.Done():
		return false
	}
}
