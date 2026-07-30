package modem

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"time"
)

type mockController struct {
	log    *slog.Logger
	events chan Event
	mu     sync.Mutex
	active bool
}

func newMock(log *slog.Logger) *mockController {
	return &mockController{log: log, events: make(chan Event, 128)}
}
func (m *mockController) Start(ctx context.Context) {
	go func() {
		select {
		case <-time.After(150 * time.Millisecond):
			m.events <- Event{Type: "device.online"}
		case <-ctx.Done():
		}
	}()
}
func (m *mockController) Events() <-chan Event { return m.events }
func (m *mockController) Healthy() bool        { return true }
func (m *mockController) AudioCapable() bool   { return true }
func (m *mockController) Probe(context.Context) Status {
	return Status{
		Provider: "mock", Transport: "memory",
		SIMReady: true, Registered: true, VoiceReady: true, Operator: "Mock Telecom", AccessTech: "LTE",
		Signal: 24, SignalDBm: -65, PhoneNumber: "+8613800138000",
		ICCID: "89860000000000000000", IMSI: "460000000000000",
		Manufacturer: "SIMCOM", Model: "SIM7600", IMEI: "860000000000000",
		Firmware: "MOCK_FIRMWARE", SubVersion: "MOCK_B01V01",
		QCN: "MOCK_QCN", VoLTEControl: true, LastCheckedAt: time.Now().UTC(),
	}
}
func (m *mockController) Dial(ctx context.Context, number string) error {
	m.mu.Lock()
	m.active = true
	m.mu.Unlock()
	go func() {
		m.events <- Event{Type: "call.alerting", Number: number}
		time.Sleep(800 * time.Millisecond)
		m.events <- Event{Type: "call.active", Number: number}
	}()
	return nil
}
func (m *mockController) Answer(context.Context) error {
	m.mu.Lock()
	m.active = true
	m.mu.Unlock()
	go func() { m.events <- Event{Type: "call.active"} }()
	return nil
}
func (m *mockController) Hangup(context.Context) error {
	m.mu.Lock()
	m.active = false
	m.mu.Unlock()
	go func() { m.events <- Event{Type: "call.ended", Reason: "local_hangup"} }()
	return nil
}
func (m *mockController) DTMF(context.Context, string) error                    { return nil }
func (m *mockController) SetMicMute(context.Context, bool) error                { return nil }
func (m *mockController) EnableAudio(context.Context, bool) error               { return nil }
func (m *mockController) SendSMS(context.Context, string, string, string) error { return nil }
func (m *mockController) DeleteSMS(context.Context, int) error                  { return nil }
func (m *mockController) OpenAudio(context.Context) (io.ReadWriteCloser, error) {
	return nil, errors.New("mock audio stream unavailable")
}

func (m *mockController) InjectCall(number string) {
	m.events <- Event{Type: "call.incoming", Number: number}
}
func (m *mockController) InjectSMS(number, body string) {
	m.events <- Event{Type: "sms.received", Number: number, Body: body, ModemIndex: 1}
}
