package app

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"onsim/internal/domain"
	"onsim/internal/filter"
	"onsim/internal/modem"
	"onsim/internal/store"
)

type testModem struct {
	mu           sync.Mutex
	events       chan modem.Event
	dialed       string
	audioEnabled bool
	answered     bool
}

type testCallMedia struct {
	waiting []string
	held    map[string]bool
	mailbox []string
}

func (m *testCallMedia) BeginWaitingMedia(id string) { m.waiting = append(m.waiting, id) }
func (m *testCallMedia) SetHeld(id string, held bool) {
	if m.held == nil {
		m.held = map[string]bool{}
	}
	m.held[id] = held
}
func (m *testCallMedia) StartVoicemail(id string) { m.mailbox = append(m.mailbox, id) }

func (m *testModem) Start(context.Context)      {}
func (m *testModem) Events() <-chan modem.Event { return m.events }
func (m *testModem) Dial(_ context.Context, number string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.dialed = number
	return nil
}
func (m *testModem) Answer(context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.answered = true
	return nil
}
func (m *testModem) Hangup(context.Context) error                          { return nil }
func (m *testModem) DTMF(context.Context, string) error                    { return nil }
func (m *testModem) SendSMS(context.Context, string, string, string) error { return nil }
func (m *testModem) DeleteSMS(context.Context, int) error                  { return nil }
func (m *testModem) SetMicMute(context.Context, bool) error                { return nil }
func (m *testModem) EnableAudio(_ context.Context, enabled bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.audioEnabled = enabled
	return nil
}
func (m *testModem) Healthy() bool      { return true }
func (m *testModem) AudioCapable() bool { return true }
func (m *testModem) OpenAudio(context.Context) (io.ReadWriteCloser, error) {
	return nil, errors.New("unavailable")
}
func (m *testModem) Probe(context.Context) modem.Status { return modem.Status{SIMReady: true} }
func (m *testModem) values() (string, bool, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.dialed, m.answered, m.audioEnabled
}

func TestFirstAnswerClaimsMedia(t *testing.T) {
	state, err := store.Open(t.TempDir() + "/state.db")
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	controller := &testModem{events: make(chan modem.Event, 4)}
	service := New(state, controller, filter.New(state), slog.Default())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	service.Start(ctx)
	controller.events <- modem.Event{Type: "call.incoming", Number: "13800138000"}

	var callID string
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if call := state.ActiveCall(); call != nil {
			callID = call.ID
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if callID == "" {
		t.Fatal("incoming call was not created")
	}
	call, err := service.AnswerFrom(ctx, callID, "sip")
	if err != nil {
		t.Fatal(err)
	}
	if call.MediaOwner != "sip" {
		t.Fatalf("media owner = %q", call.MediaOwner)
	}
	if _, err = service.AnswerFrom(ctx, callID, "web"); err == nil {
		t.Fatal("second answer unexpectedly succeeded")
	}
}

func TestDialIsNotBlockedByMissingCircuitVoiceRegistration(t *testing.T) {
	state, err := store.Open(t.TempDir() + "/state.db")
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	controller := &testModem{events: make(chan modem.Event, 1)}
	service := New(state, controller, filter.New(state), slog.Default())
	if _, err = service.Dial(context.Background(), "+8613800138000"); err != nil {
		t.Fatal(err)
	}
	if dialed, _, _ := controller.values(); dialed != "+8613800138000" {
		t.Fatalf("modem dialed %q", dialed)
	}
}

func TestVoicemailClaimsUnansweredIncomingCall(t *testing.T) {
	state, err := store.Open(t.TempDir() + "/state.db")
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	settings := state.Settings()
	settings.VoicemailEnabled = true
	settings.VoicemailTimeout = 30
	if _, err = state.UpdateSettings(settings); err != nil {
		t.Fatal(err)
	}
	controller := &testModem{events: make(chan modem.Event, 1)}
	service := New(state, controller, filter.New(state), slog.Default())
	call := &domain.Call{ID: "call-voicemail", Version: 1, Direction: domain.Incoming, Number: "+8613800138000", State: domain.CallIncoming, StartedAt: time.Now()}
	if _, err = state.UpsertCall(call); err != nil {
		t.Fatal(err)
	}
	service.voicemailAfter(context.Background(), call.ID, time.Millisecond)
	got := state.Call(call.ID)
	if _, answered, _ := controller.values(); !answered || got.MediaOwner != "voicemail" || got.State != domain.CallAlerting {
		t.Fatalf("voicemail did not claim call: answered=%v call=%#v", answered, got)
	}
}

func TestIncomingHoldAnswersAndResumeKeepsWebMediaOwner(t *testing.T) {
	state, err := store.Open(t.TempDir() + "/state.db")
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	controller := &testModem{events: make(chan modem.Event, 1)}
	service := New(state, controller, filter.New(state), slog.Default())
	callMedia := &testCallMedia{}
	service.SetCallMedia(callMedia)
	call := &domain.Call{ID: "call-hold", Version: 1, Direction: domain.Incoming, Number: "+8613800138000", State: domain.CallIncoming, StartedAt: time.Now()}
	if _, err = state.UpsertCall(call); err != nil {
		t.Fatal(err)
	}
	held, err := service.Hold(context.Background(), call.ID)
	if err != nil {
		t.Fatal(err)
	}
	_, answered, _ := controller.values()
	if !answered || !held.Held || held.MediaOwner != "web" || held.State != domain.CallAlerting || !callMedia.held[call.ID] {
		t.Fatalf("incoming hold did not answer into persistent web media: %#v media=%#v", held, callMedia)
	}
	held.State = domain.CallActive
	held.Version++
	if _, err = state.UpsertCall(held); err != nil {
		t.Fatal(err)
	}
	resumed, err := service.Resume(context.Background(), call.ID)
	if err != nil {
		t.Fatal(err)
	}
	if resumed.Held || callMedia.held[call.ID] || resumed.MediaOwner != "web" {
		t.Fatalf("resume changed media ownership: %#v media=%#v", resumed, callMedia)
	}
}

func TestHeldCallCanTransferDirectlyToVoicemail(t *testing.T) {
	state, err := store.Open(t.TempDir() + "/state.db")
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	controller := &testModem{events: make(chan modem.Event, 1)}
	service := New(state, controller, filter.New(state), slog.Default())
	callMedia := &testCallMedia{}
	service.SetCallMedia(callMedia)
	call := &domain.Call{ID: "call-held-mailbox", Version: 1, Direction: domain.Incoming, Number: "+8613800138000", State: domain.CallActive, StartedAt: time.Now(), MediaOwner: "web", Held: true}
	if _, err = state.UpsertCall(call); err != nil {
		t.Fatal(err)
	}
	got, err := service.TransferToVoicemail(context.Background(), call.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Held || got.MediaOwner != "voicemail" || len(callMedia.mailbox) != 1 || callMedia.mailbox[0] != call.ID {
		t.Fatalf("held call was not transferred to voicemail: %#v media=%#v", got, callMedia)
	}
}

func TestHangupReasonDistinguishesRejectedAndAnsweredCalls(t *testing.T) {
	state, err := store.Open(t.TempDir() + "/state.db")
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	controller := &testModem{events: make(chan modem.Event, 8)}
	service := New(state, controller, filter.New(state), slog.Default())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	service.Start(ctx)

	controller.events <- modem.Event{Type: "call.incoming", Number: "13800138000"}
	waitForCallState(t, state, domain.CallIncoming)
	rejected, err := service.Hangup(ctx, state.ActiveCall().ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if rejected.EndReason != "rejected" {
		t.Fatalf("unanswered incoming call reason = %q", rejected.EndReason)
	}

	controller.events <- modem.Event{Type: "call.incoming", Number: "13800138000"}
	waitForCallState(t, state, domain.CallIncoming)
	controller.events <- modem.Event{Type: "call.active", Number: "13800138000"}
	waitForCallState(t, state, domain.CallActive)
	answered, err := service.Hangup(ctx, state.ActiveCall().ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if answered.EndReason != "local_hangup" {
		t.Fatalf("answered call reason = %q", answered.EndReason)
	}
	if answered.ConnectedAt == nil || answered.EndedAt == nil {
		t.Fatalf("answered call timestamps are incomplete: %#v", answered)
	}
}

func TestPhoneOriginatedCallJoinsWebLifecycle(t *testing.T) {
	state, err := store.Open(t.TempDir() + "/state.db")
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	controller := &testModem{events: make(chan modem.Event, 4)}
	service := New(state, controller, filter.New(state), slog.Default())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	service.Start(ctx)
	controller.events <- modem.Event{Type: "call.alerting", Number: "13800138000"}
	waitForCallState(t, state, domain.CallAlerting)
	call := state.ActiveCall()
	if call == nil || call.Direction != domain.Outgoing || call.Number != "+8613800138000" {
		t.Fatalf("unexpected phone-originated call: %#v", call)
	}
	controller.events <- modem.Event{Type: "call.active", Number: "13800138000"}
	waitForCallState(t, state, domain.CallActive)
	if _, _, audioEnabled := controller.values(); !audioEnabled {
		t.Fatal("active phone-originated call did not enable gateway audio")
	}
}

func waitForCallState(t *testing.T, state *store.State, wanted domain.CallState) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if call := state.ActiveCall(); call != nil && call.State == wanted {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("call did not reach state %s", wanted)
}
