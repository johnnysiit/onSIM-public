package app

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"time"

	"onsim/internal/buildinfo"
	"onsim/internal/domain"
	"onsim/internal/filter"
	"onsim/internal/modem"
	"onsim/internal/store"
)

type Service struct {
	state       *store.State
	modem       modem.Controller
	filter      *filter.Engine
	log         *slog.Logger
	actions     sync.Mutex
	observersMu sync.RWMutex
	observers   []Observer
	infoMu      sync.RWMutex
	lastProbe   modem.Status
	startedAt   time.Time
	media       CallMedia
}

type CallMedia interface {
	BeginWaitingMedia(string)
	SetHeld(string, bool)
	StartVoicemail(string)
}

type Observer interface {
	IncomingCall(*domain.Call)
	IncomingSMS(*domain.Message)
	CallEnded(*domain.Call)
}

func New(state *store.State, m modem.Controller, f *filter.Engine, log *slog.Logger) *Service {
	return &Service{
		state: state, modem: m, filter: f, log: log, startedAt: time.Now().UTC(),
		lastProbe: modem.Status{Signal: -1, SignalDBm: -1},
	}
}

func (s *Service) SetCallMedia(media CallMedia) {
	s.media = media
}

func (s *Service) Start(ctx context.Context) {
	s.modem.Start(ctx)
	go func() {
		for {
			select {
			case e := <-s.modem.Events():
				s.handleModem(ctx, e)
			case <-ctx.Done():
				return
			}
		}
	}()
	go s.probeLoop(ctx)
}

func (s *Service) probeLoop(ctx context.Context) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			s.probe(ctx)
		case <-ctx.Done():
			return
		}
	}
}

func (s *Service) probe(ctx context.Context) {
	if !s.modem.Healthy() {
		return
	}
	p := s.modem.Probe(ctx)
	s.infoMu.Lock()
	s.lastProbe = p
	s.infoMu.Unlock()
	snap := s.state.Snapshot(false)
	d := snap.Device
	if p.Provider != "" {
		d.GatewayType = p.Provider
	}
	d.ATConnected = s.modem.Healthy()
	d.AudioCapable = s.modem.AudioCapable()
	d.SIMReady, d.Registered, d.VoiceReady, d.Operator, d.AccessTech = p.SIMReady, p.Registered, p.VoiceReady, p.Operator, p.AccessTech
	d.Signal, d.SignalDBm = p.Signal, p.SignalDBm
	d.Degraded = nil
	if !d.AudioCapable {
		if p.Provider == "android" {
			d.Degraded = append(d.Degraded, "ANDROID_AUDIO_UNAVAILABLE")
		} else {
			d.Degraded = append(d.Degraded, "USB_AUDIO_UNAVAILABLE")
		}
	}
	if !p.Registered {
		d.Degraded = append(d.Degraded, "CELLULAR_NETWORK_UNAVAILABLE")
	} else if !p.VoiceReady {
		// CREG only describes the legacy circuit-switched domain. VoLTE may
		// still place calls while CREG is unregistered, so do not classify the
		// whole voice service as unavailable or block ATD.
		d.Degraded = append(d.Degraded, "CS_VOICE_FALLBACK_UNAVAILABLE")
	}
	_, _ = s.state.UpdateDevice(d)
}

func (s *Service) SystemInfo() domain.SystemInfo {
	s.infoMu.RLock()
	p := s.lastProbe
	s.infoMu.RUnlock()
	device := s.state.Snapshot(false).Device
	now := time.Now().UTC()
	checked := p.LastCheckedAt
	if checked.IsZero() {
		checked = device.LastCheckedAt
	}
	gatewayInfo := gatewayInfoFromStatus(p, device)
	gateways := []domain.GatewayInfo{gatewayInfo}
	if routed, ok := s.modem.(modem.RoutedController); ok {
		statuses := routed.Statuses(context.Background())
		gateways = make([]domain.GatewayInfo, 0, len(statuses))
		for _, status := range statuses {
			gateways = append(gateways, gatewayInfoFromStatus(status, device))
		}
	}
	return domain.SystemInfo{
		SIM: domain.SIMInfo{Ready: p.SIMReady, PhoneNumber: p.PhoneNumber, ICCID: p.ICCID, IMSI: p.IMSI},
		Network: domain.NetworkInfo{
			Registered: p.Registered, VoiceRegistered: p.VoiceReady,
			Operator: p.Operator, AccessTechnology: p.AccessTech,
			Signal: p.Signal, SignalDBm: p.SignalDBm,
		},
		Modem: domain.ModemInfo{
			Connected: device.ATConnected, AudioCapable: device.AudioCapable,
			Manufacturer: p.Manufacturer, Model: p.Model, IMEI: p.IMEI, Firmware: p.Firmware,
			SubVersion: p.SubVersion, QCN: p.QCN, VoLTEControl: p.VoLTEControl,
			ATPort: device.ATPort, AudioPort: device.AudioPort,
		},
		Gateway: gatewayInfo, Gateways: gateways,
		Runtime: domain.RuntimeInfo{
			Version: buildinfo.Version, Revision: buildinfo.Revision, BuildTime: buildinfo.BuildTime,
			StartedAt: s.startedAt, UptimeSeconds: int64(now.Sub(s.startedAt).Seconds()),
		},
		LastCheckedAt: checked,
	}
}

func gatewayInfoFromStatus(p modem.Status, device domain.DeviceStatus) domain.GatewayInfo {
	subscriptions := make([]domain.GatewaySubscription, 0, len(p.Subscriptions))
	for _, sub := range p.Subscriptions {
		subscriptions = append(subscriptions, domain.GatewaySubscription{
			ID: sub.ID, SIMSlot: sub.SIMSlot, DisplayName: sub.DisplayName, CarrierName: sub.CarrierName,
			PhoneNumber: sub.PhoneNumber, IMEI: sub.IMEI, Ready: sub.Ready,
		})
	}
	connected := device.ATConnected
	if p.Provider == "android" {
		connected = p.ADBState == "device"
	}
	return domain.GatewayInfo{
		ID: p.GatewayID, Type: p.Provider, Connected: connected, Transport: p.Transport,
		AudioCapable: p.AudioDownlinkOK && p.AudioUplinkOK, ADBState: p.ADBState, Manufacturer: p.Manufacturer,
		Model: p.Model, AndroidVersion: p.AndroidVersion, BuildID: p.BuildID, SecurityPatch: p.SecurityPatch,
		BasebandVersion: p.BasebandVersion, BatteryLevel: p.BatteryLevel, BatteryCharging: p.BatteryCharging,
		SubscriptionID: p.SubscriptionID, SIMSlot: p.SIMSlot, IMEI: p.IMEI, IMSRegistered: p.IMSRegistered,
		VoLTE: p.VoLTE, CompanionVersion: p.CompanionVersion, ProtocolVersion: p.ProtocolVersion,
		AudioDownlinkOK: p.AudioDownlinkOK, AudioUplinkOK: p.AudioUplinkOK, AudioDownlinkFrames: p.AudioDownlinkFrames,
		AudioUplinkFrames: p.AudioUplinkFrames, AudioUplinkBytes: p.AudioUplinkBytes, LastError: p.LastError,
		Subscriptions: subscriptions,
	}
}

func (s *Service) SetHooks(inCall func(*domain.Call), inSMS func(*domain.Message), ended func(*domain.Call)) {
	s.AddObserver(observerFuncs{incomingCall: inCall, incomingSMS: inSMS, callEnded: ended})
}

func (s *Service) AddObserver(observer Observer) {
	if observer == nil {
		return
	}
	s.observersMu.Lock()
	s.observers = append(s.observers, observer)
	s.observersMu.Unlock()
}

func (s *Service) Dial(ctx context.Context, raw string) (*domain.Call, error) {
	return s.DialRoute(ctx, raw, "web", modem.Route{})
}

func (s *Service) DialFrom(ctx context.Context, raw, owner string) (*domain.Call, error) {
	return s.DialRoute(ctx, raw, owner, modem.Route{})
}

func (s *Service) DialRoute(ctx context.Context, raw, owner string, route modem.Route) (*domain.Call, error) {
	s.actions.Lock()
	defer s.actions.Unlock()
	if !s.state.Settings().Calls {
		return nil, errors.New("FEATURE_DISABLED")
	}
	if active := s.state.ActiveCall(); active != nil {
		return nil, errors.New("CALL_STATE_CONFLICT")
	}
	number, err := filter.Normalize(raw, s.state.Settings().Country)
	if err != nil {
		return nil, err
	}
	decision := s.filter.Decide(ctx, number, "", "call")
	if decision.Action == "block" {
		return nil, errors.New("NUMBER_BLOCKED")
	}
	call := &domain.Call{ID: domain.NewID("call"), Direction: domain.Outgoing, Number: number, State: domain.CallDialing, Filter: decision, StartedAt: time.Now().UTC(), Version: 1, MediaOwner: owner, GatewayID: route.GatewayID, SubscriptionID: route.SubscriptionID}
	if _, err = s.state.UpsertCall(call); err != nil {
		return nil, err
	}
	if routed, ok := s.modem.(modem.RoutedController); ok {
		err = routed.DialRoute(ctx, route, number)
	} else {
		err = s.modem.Dial(ctx, number)
	}
	if err != nil {
		now := time.Now().UTC()
		call.Version++
		call.State = domain.CallFailed
		call.EndedAt = &now
		call.EndReason = err.Error()
		_, _ = s.state.UpsertCall(call)
		return call, err
	}
	return call, nil
}

func (s *Service) Answer(ctx context.Context, id string) (*domain.Call, error) {
	return s.AnswerFrom(ctx, id, "web")
}

func (s *Service) AnswerFrom(ctx context.Context, id, owner string) (*domain.Call, error) {
	s.actions.Lock()
	defer s.actions.Unlock()
	call := s.state.Call(id)
	if call == nil || call.State != domain.CallIncoming {
		return nil, errors.New("CALL_STATE_CONFLICT")
	}
	if call.MediaOwner != "" && call.MediaOwner != owner {
		return nil, errors.New("MEDIA_BUSY")
	}
	if routed, ok := s.modem.(modem.RoutedController); ok {
		if err := routed.SelectRoute(modem.Route{GatewayID: call.GatewayID, SubscriptionID: call.SubscriptionID}); err != nil {
			return nil, err
		}
	}
	call.Version++
	call.MediaOwner = owner
	if _, err := s.state.UpsertCall(call); err != nil {
		return nil, err
	}
	if err := s.modem.Answer(ctx); err != nil {
		call.Version++
		call.MediaOwner = ""
		_, _ = s.state.UpsertCall(call)
		return nil, err
	}
	if owner == "web" && s.media != nil {
		s.media.BeginWaitingMedia(call.ID)
	}
	call.Version++
	call.State = domain.CallAlerting
	_, err := s.state.UpsertCall(call)
	return call, err
}

// Hold answers an incoming call when necessary and keeps the persistent
// telephony media route open while replacing both web directions with the
// HKTA 2201 ringback cadence.
func (s *Service) Hold(ctx context.Context, id string) (*domain.Call, error) {
	s.actions.Lock()
	defer s.actions.Unlock()
	call := s.state.Call(id)
	if call == nil || (call.State != domain.CallIncoming && call.State != domain.CallAlerting && call.State != domain.CallActive) {
		return nil, errors.New("CALL_STATE_CONFLICT")
	}
	if call.Held {
		return call, nil
	}
	if call.MediaOwner != "" && call.MediaOwner != "web" {
		return nil, errors.New("MEDIA_BUSY")
	}
	if routed, ok := s.modem.(modem.RoutedController); ok {
		if err := routed.SelectRoute(modem.Route{GatewayID: call.GatewayID, SubscriptionID: call.SubscriptionID}); err != nil {
			return nil, err
		}
	}
	needsAnswer := call.State == domain.CallIncoming
	call.Version++
	call.MediaOwner = "web"
	call.Held = true
	if _, err := s.state.UpsertCall(call); err != nil {
		return nil, err
	}
	if needsAnswer {
		if err := s.modem.Answer(ctx); err != nil {
			call.Version++
			call.MediaOwner = ""
			call.Held = false
			_, _ = s.state.UpsertCall(call)
			return nil, err
		}
		call.Version++
		call.State = domain.CallAlerting
		if _, err := s.state.UpsertCall(call); err != nil {
			return nil, err
		}
	}
	if s.media != nil {
		s.media.SetHeld(call.ID, true)
		s.media.BeginWaitingMedia(call.ID)
	}
	return call, nil
}

func (s *Service) Resume(ctx context.Context, id string) (*domain.Call, error) {
	s.actions.Lock()
	defer s.actions.Unlock()
	call := s.state.Call(id)
	if call == nil || call.State != domain.CallActive || !call.Held || call.MediaOwner != "web" {
		return nil, errors.New("CALL_STATE_CONFLICT")
	}
	call.Version++
	call.Held = false
	if _, err := s.state.UpsertCall(call); err != nil {
		return nil, err
	}
	if s.media != nil {
		s.media.SetHeld(call.ID, false)
		s.media.BeginWaitingMedia(call.ID)
	}
	return call, nil
}

func (s *Service) TransferToVoicemail(ctx context.Context, id string) (*domain.Call, error) {
	s.actions.Lock()
	defer s.actions.Unlock()
	call := s.state.Call(id)
	if call == nil || (call.State != domain.CallIncoming && call.State != domain.CallAlerting && call.State != domain.CallActive) {
		return nil, errors.New("CALL_STATE_CONFLICT")
	}
	if call.State == domain.CallActive && !call.Held {
		return nil, errors.New("CALL_STATE_CONFLICT")
	}
	if routed, ok := s.modem.(modem.RoutedController); ok {
		if err := routed.SelectRoute(modem.Route{GatewayID: call.GatewayID, SubscriptionID: call.SubscriptionID}); err != nil {
			return nil, err
		}
	}
	needsAnswer := call.State == domain.CallIncoming
	call.Version++
	call.MediaOwner = "voicemail"
	call.Held = false
	if _, err := s.state.UpsertCall(call); err != nil {
		return nil, err
	}
	if needsAnswer {
		if err := s.modem.Answer(ctx); err != nil {
			call.Version++
			call.MediaOwner = ""
			_, _ = s.state.UpsertCall(call)
			return nil, err
		}
		call.Version++
		call.State = domain.CallAlerting
		if _, err := s.state.UpsertCall(call); err != nil {
			return nil, err
		}
	} else if call.State == domain.CallActive && s.media != nil {
		s.media.StartVoicemail(call.ID)
	}
	return call, nil
}

func (s *Service) Hangup(ctx context.Context, id, reason string) (*domain.Call, error) {
	s.actions.Lock()
	defer s.actions.Unlock()
	call := s.state.Call(id)
	if call == nil || call.State == domain.CallEnded || call.State == domain.CallFailed {
		return nil, errors.New("CALL_STATE_CONFLICT")
	}
	if routed, ok := s.modem.(modem.RoutedController); ok {
		_ = routed.SelectRoute(modem.Route{GatewayID: call.GatewayID, SubscriptionID: call.SubscriptionID})
	}
	err := s.modem.Hangup(ctx)
	now := time.Now().UTC()
	call.Version++
	call.State = domain.CallEnded
	call.Held = false
	call.EndedAt = &now
	if reason == "" {
		if call.Direction == domain.Incoming && call.ConnectedAt == nil {
			reason = "rejected"
		} else {
			reason = "local_hangup"
		}
	}
	call.EndReason = reason
	_, persistErr := s.state.UpsertCall(call)
	if persistErr != nil {
		return nil, persistErr
	}
	s.notifyCallEnded(call)
	return call, err
}

func (s *Service) DTMF(ctx context.Context, id, key string) error {
	call := s.state.Call(id)
	if call == nil || call.State != domain.CallActive {
		return errors.New("CALL_STATE_CONFLICT")
	}
	if routed, ok := s.modem.(modem.RoutedController); ok {
		if err := routed.SelectRoute(modem.Route{GatewayID: call.GatewayID, SubscriptionID: call.SubscriptionID}); err != nil {
			return err
		}
	}
	return s.modem.DTMF(ctx, key)
}

func (s *Service) Mute(ctx context.Context, id string, muted bool) (*domain.Call, error) {
	call := s.state.Call(id)
	if call == nil || call.State != domain.CallActive {
		return nil, errors.New("CALL_STATE_CONFLICT")
	}
	if routed, ok := s.modem.(modem.RoutedController); ok {
		if err := routed.SelectRoute(modem.Route{GatewayID: call.GatewayID, SubscriptionID: call.SubscriptionID}); err != nil {
			return nil, err
		}
	}
	if err := s.modem.SetMicMute(ctx, muted); err != nil {
		return nil, err
	}
	call.Version++
	call.Muted = muted
	_, err := s.state.UpsertCall(call)
	return call, err
}

func (s *Service) SendSMS(ctx context.Context, raw, body string) (*domain.Message, error) {
	return s.SendSMSRoute(ctx, raw, body, modem.Route{})
}

func (s *Service) SendSMSRoute(ctx context.Context, raw, body string, route modem.Route) (*domain.Message, error) {
	if !s.state.Settings().SMS {
		return nil, errors.New("FEATURE_DISABLED")
	}
	body = strings.TrimSpace(body)
	if body == "" || len([]rune(body)) > 1000 {
		return nil, errors.New("INVALID_MESSAGE")
	}
	number, err := filter.Normalize(raw, s.state.Settings().Country)
	if err != nil {
		return nil, err
	}
	d := s.filter.Decide(ctx, number, body, "sms")
	if d.Action == "block" {
		return nil, errors.New("NUMBER_BLOCKED")
	}
	msg := &domain.Message{ID: domain.NewID("msg"), Conversation: number, Direction: domain.Outgoing, Number: number, Body: body, Status: "sending", Filter: d, CreatedAt: time.Now().UTC(), Version: 1, GatewayID: route.GatewayID, SubscriptionID: route.SubscriptionID}
	if _, err = s.state.UpsertMessage(msg); err != nil {
		return nil, err
	}
	if routed, ok := s.modem.(modem.RoutedController); ok {
		err = routed.SendSMSRoute(ctx, route, msg.ID, number, body)
	} else {
		err = s.modem.SendSMS(ctx, msg.ID, number, body)
	}
	if err != nil {
		msg.Version++
		msg.Status = "failed"
		_, _ = s.state.UpsertMessage(msg)
		return msg, err
	}
	msg.Version++
	msg.Status = "sent"
	_, err = s.state.UpsertMessage(msg)
	return msg, err
}

func (s *Service) DeleteMessage(ctx context.Context, id string) (*domain.Message, error) {
	msg := s.state.Message(id)
	if msg == nil {
		return nil, errors.New("NOT_FOUND")
	}
	if msg.ModemIndex > 0 {
		_ = s.modem.DeleteSMS(ctx, msg.ModemIndex)
	}
	msg.Version++
	msg.Deleted = true
	msg.Body = ""
	msg.Unread = false
	_, err := s.state.UpsertMessage(msg)
	return msg, err
}

func (s *Service) MarkRead(id string) (*domain.Message, error) {
	msg := s.state.Message(id)
	if msg == nil {
		return nil, errors.New("NOT_FOUND")
	}
	if !msg.Unread {
		return msg, nil
	}
	msg.Version++
	msg.Unread = false
	_, err := s.state.UpsertMessage(msg)
	return msg, err
}

func (s *Service) MarkAllRead() (int, error) {
	return s.state.MarkAllMessagesRead()
}

func (s *Service) handleModem(ctx context.Context, e modem.Event) {
	switch e.Type {
	case "device.online":
		snap := s.state.Snapshot(false)
		d := snap.Device
		d.Mode = "online"
		s.infoMu.RLock()
		provider := s.lastProbe.Provider
		s.infoMu.RUnlock()
		if provider != "" {
			d.GatewayType = provider
		}
		d.ATConnected = s.modem.Healthy()
		d.AudioCapable = s.modem.AudioCapable()
		d.SIMReady = true
		d.Degraded = nil
		if !d.AudioCapable {
			d.Degraded = []string{"USB_AUDIO_UNAVAILABLE"}
		}
		_, _ = s.state.UpdateDevice(d)
		go s.probe(ctx)
	case "device.offline":
		snap := s.state.Snapshot(false)
		d := snap.Device
		d.Mode = "offline"
		d.ATConnected = false
		d.AudioCapable = false
		d.Degraded = []string{"GATEWAY_OFFLINE"}
		if e.Reason != "" {
			d.Degraded = append(d.Degraded, e.Reason)
		}
		_, _ = s.state.UpdateDevice(d)
	case "call.incoming":
		if current := s.state.ActiveCall(); current != nil {
			if current.State == domain.CallIncoming && current.Number == "" && e.Number != "" {
				current.Number = e.Number
				current.Version++
				_, _ = s.state.UpsertCall(current)
			}
			return
		}
		if routed, ok := s.modem.(modem.RoutedController); ok {
			if err := routed.SelectRoute(modem.Route{GatewayID: e.GatewayID, SubscriptionID: e.SubscriptionID}); err != nil {
				s.log.Error("select incoming call gateway", "gateway_id", e.GatewayID, "error", err)
				return
			}
		}
		number, err := filter.Normalize(e.Number, s.state.Settings().Country)
		if err != nil {
			number = e.Number
		}
		d := s.filter.Decide(ctx, number, "", "call")
		call := &domain.Call{ID: domain.NewID("call"), Direction: domain.Incoming, Number: number, State: domain.CallIncoming, StartedAt: time.Now().UTC(), Version: 1, Filter: d, GatewayID: e.GatewayID, SubscriptionID: e.SubscriptionID}
		_, _ = s.state.UpsertCall(call)
		if !s.state.Settings().Calls || d.Action == "block" {
			_, _ = s.Hangup(ctx, call.ID, "filtered")
			return
		}
		s.notifyIncomingCall(call)
		if settings := s.state.Settings(); settings.VoicemailEnabled && s.media != nil {
			timeout := settings.VoicemailTimeout
			if timeout == 0 {
				timeout = 30
			}
			go s.voicemailAfter(ctx, call.ID, time.Duration(timeout)*time.Second)
		}
	case "call.alerting":
		if call := s.state.ActiveCall(); call != nil {
			call.Version++
			call.State = domain.CallAlerting
			_, _ = s.state.UpsertCall(call)
		} else {
			if routed, ok := s.modem.(modem.RoutedController); ok {
				if err := routed.SelectRoute(modem.Route{GatewayID: e.GatewayID, SubscriptionID: e.SubscriptionID}); err != nil {
					return
				}
			}
			// Calls started from the phone's emergency UI must still appear in
			// the web application and acquire the normal media lifecycle.
			number, err := filter.Normalize(e.Number, s.state.Settings().Country)
			if err != nil {
				number = e.Number
			}
			call := &domain.Call{
				ID: domain.NewID("call"), Direction: domain.Outgoing, Number: number,
				State: domain.CallAlerting, StartedAt: time.Now().UTC(), Version: 1,
				Filter:    s.filter.Decide(ctx, number, "", "call"),
				GatewayID: e.GatewayID, SubscriptionID: e.SubscriptionID,
			}
			_, _ = s.state.UpsertCall(call)
		}
	case "call.active":
		if call := s.state.ActiveCall(); call != nil {
			if routed, ok := s.modem.(modem.RoutedController); ok {
				_ = routed.SelectRoute(modem.Route{GatewayID: call.GatewayID, SubscriptionID: call.SubscriptionID})
			}
			now := time.Now().UTC()
			call.Version++
			call.State = domain.CallActive
			call.ConnectedAt = &now
			_, _ = s.state.UpsertCall(call)
			if call.MediaOwner == "voicemail" && s.media != nil {
				s.media.StartVoicemail(call.ID)
			} else if call.Held && s.media != nil {
				s.media.SetHeld(call.ID, true)
			} else {
				_ = s.modem.EnableAudio(ctx, true)
			}
		}
	case "call.ended":
		if call := s.state.ActiveCall(); call != nil {
			now := time.Now().UTC()
			call.Version++
			call.State = domain.CallEnded
			call.Held = false
			call.EndedAt = &now
			if e.Reason != "" {
				call.EndReason = e.Reason
			} else {
				call.EndReason = "remote_hangup"
			}
			_, _ = s.state.UpsertCall(call)
			_ = s.modem.EnableAudio(ctx, false)
			s.notifyCallEnded(call)
		}
	case "sms.received":
		if e.ProviderID != "" {
			for _, existing := range s.state.Snapshot(false).Messages {
				if existing.ProviderID == e.ProviderID {
					return
				}
			}
		}
		number, err := filter.Normalize(e.Number, s.state.Settings().Country)
		if err != nil {
			number = e.Number
		}
		d := s.filter.Decide(ctx, number, e.Body, "sms")
		msg := &domain.Message{ID: domain.NewID("msg"), Conversation: number, Direction: domain.Incoming, Number: number, Body: e.Body, Status: "received", Unread: true, Filtered: d.Action == "block", Filter: d, ModemIndex: e.ModemIndex, ProviderID: e.ProviderID, CreatedAt: time.Now().UTC(), Version: 1, GatewayID: e.GatewayID, SubscriptionID: e.SubscriptionID}
		if s.state.Settings().SIPEnabled && !msg.Filtered {
			now := time.Now().UTC()
			expires := now.Add(7 * 24 * time.Hour)
			msg.SIPDelivery = &domain.SIPDelivery{Status: "queued", NextAt: &now, ExpiresAt: expires}
		}
		_, _ = s.state.UpsertMessage(msg)
		if s.state.Settings().SMS && !msg.Filtered {
			s.notifyIncomingSMS(msg)
		}
	case "sms.sent", "sms.delivered", "sms.failed":
		if e.ClientID == "" {
			return
		}
		msg := s.state.Message(e.ClientID)
		if msg == nil || msg.Direction != domain.Outgoing {
			return
		}
		msg.Version++
		msg.ProviderID = e.ProviderID
		switch e.Type {
		case "sms.sent":
			msg.Status = "sent"
		case "sms.delivered":
			msg.Status = "delivered"
		case "sms.failed":
			msg.Status = "failed"
		}
		_, _ = s.state.UpsertMessage(msg)
	}
}

func (s *Service) voicemailAfter(ctx context.Context, callID string, delay time.Duration) {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return
	case <-timer.C:
	}
	s.actions.Lock()
	defer s.actions.Unlock()
	call := s.state.Call(callID)
	if call == nil || call.State != domain.CallIncoming || !s.state.Settings().VoicemailEnabled {
		return
	}
	if routed, ok := s.modem.(modem.RoutedController); ok {
		if err := routed.SelectRoute(modem.Route{GatewayID: call.GatewayID, SubscriptionID: call.SubscriptionID}); err != nil {
			s.log.Error("select voicemail gateway", "call_id", callID, "error", err)
			return
		}
	}
	call.Version++
	call.MediaOwner = "voicemail"
	if _, err := s.state.UpsertCall(call); err != nil {
		return
	}
	if err := s.modem.Answer(ctx); err != nil {
		call.Version++
		call.MediaOwner = ""
		_, _ = s.state.UpsertCall(call)
		s.log.Error("answer voicemail call", "call_id", callID, "error", err)
		return
	}
	call.Version++
	call.State = domain.CallAlerting
	_, _ = s.state.UpsertCall(call)
}

func (s *Service) notifyIncomingCall(call *domain.Call) {
	s.observersMu.RLock()
	observers := append([]Observer(nil), s.observers...)
	s.observersMu.RUnlock()
	for _, observer := range observers {
		go observer.IncomingCall(call)
	}
}

func (s *Service) notifyIncomingSMS(msg *domain.Message) {
	s.observersMu.RLock()
	observers := append([]Observer(nil), s.observers...)
	s.observersMu.RUnlock()
	for _, observer := range observers {
		go observer.IncomingSMS(msg)
	}
}

func (s *Service) notifyCallEnded(call *domain.Call) {
	s.observersMu.RLock()
	observers := append([]Observer(nil), s.observers...)
	s.observersMu.RUnlock()
	for _, observer := range observers {
		go observer.CallEnded(call)
	}
}

type observerFuncs struct {
	incomingCall func(*domain.Call)
	incomingSMS  func(*domain.Message)
	callEnded    func(*domain.Call)
}

func (o observerFuncs) IncomingCall(call *domain.Call) {
	if o.incomingCall != nil {
		o.incomingCall(call)
	}
}
func (o observerFuncs) IncomingSMS(msg *domain.Message) {
	if o.incomingSMS != nil {
		o.incomingSMS(msg)
	}
}
func (o observerFuncs) CallEnded(call *domain.Call) {
	if o.callEnded != nil {
		o.callEnded(call)
	}
}
