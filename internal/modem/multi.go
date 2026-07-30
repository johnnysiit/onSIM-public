package modem

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sort"
	"strconv"
	"sync"
)

type AndroidGateway struct {
	ID     string
	Config AndroidConfig
}

// multiController fans events in from independent phones and keeps the
// currently selected route for call-scoped operations and PCM. SMS can be
// submitted on any route without changing an active call.
type multiController struct {
	log         *slog.Logger
	controllers map[string]*androidController
	events      chan Event
	mu          sync.RWMutex
	selected    string
}

func NewMultiAndroid(gateways []AndroidGateway, log *slog.Logger) Controller {
	m := &multiController{log: log, controllers: map[string]*androidController{}, events: make(chan Event, 256)}
	for _, gateway := range gateways {
		id := gateway.ID
		if id == "" {
			id = gateway.Config.Serial
		}
		if id == "" {
			continue
		}
		controller := NewAndroid(gateway.Config, log).(*androidController)
		controller.status.GatewayID = id
		m.controllers[id] = controller
	}
	if len(m.controllers) == 1 {
		for id := range m.controllers {
			m.selected = id
		}
	}
	return m
}

func (m *multiController) Start(ctx context.Context) {
	for id, controller := range m.controllers {
		controller.Start(ctx)
		go func(gatewayID string, events <-chan Event) {
			for {
				select {
				case event := <-events:
					event.GatewayID = gatewayID
					select {
					case m.events <- event:
					case <-ctx.Done():
						return
					}
				case <-ctx.Done():
					return
				}
			}
		}(id, controller.Events())
	}
}
func (m *multiController) Events() <-chan Event { return m.events }
func (m *multiController) Healthy() bool {
	for _, c := range m.controllers {
		if c.Healthy() {
			return true
		}
	}
	return false
}
func (m *multiController) AudioCapable() bool {
	c, _ := m.current()
	return c != nil && c.AudioCapable()
}

func (m *multiController) current() (*androidController, string) {
	m.mu.RLock()
	id := m.selected
	m.mu.RUnlock()
	if c := m.controllers[id]; c != nil {
		return c, id
	}
	var found *androidController
	var foundID string
	for candidateID, c := range m.controllers {
		if !c.Healthy() {
			continue
		}
		if found != nil {
			return nil, ""
		}
		found, foundID = c, candidateID
	}
	return found, foundID
}

func (m *multiController) resolve(route Route) (*androidController, string, error) {
	if route.GatewayID != "" {
		c := m.controllers[route.GatewayID]
		if c == nil {
			return nil, "", errors.New("GATEWAY_NOT_FOUND")
		}
		if !c.Healthy() {
			return nil, "", errors.New("ANDROID_GATEWAY_OFFLINE")
		}
		return c, route.GatewayID, nil
	}
	c, id := m.current()
	if c == nil {
		return nil, "", errors.New("GATEWAY_SELECTION_REQUIRED")
	}
	return c, id, nil
}
func (m *multiController) SelectRoute(route Route) error {
	_, id, err := m.resolve(route)
	if err != nil {
		return err
	}
	m.mu.Lock()
	m.selected = id
	m.mu.Unlock()
	return nil
}
func routeSubscription(route Route, fallback string) string {
	if route.SubscriptionID > 0 {
		return strconv.Itoa(route.SubscriptionID)
	}
	return fallback
}
func (m *multiController) DialRoute(ctx context.Context, route Route, number string) error {
	c, id, err := m.resolve(route)
	if err != nil {
		return err
	}
	if route.SubscriptionID <= 0 && len(c.Probe(ctx).Subscriptions) > 1 {
		return errors.New("SUBSCRIPTION_SELECTION_REQUIRED")
	}
	m.mu.Lock()
	m.selected = id
	m.mu.Unlock()
	return c.dialSubscription(ctx, number, routeSubscription(route, c.cfg.SubscriptionID))
}
func (m *multiController) SendSMSRoute(ctx context.Context, route Route, clientID, number, body string) error {
	c, _, err := m.resolve(route)
	if err != nil {
		return err
	}
	if route.SubscriptionID <= 0 && len(c.Probe(ctx).Subscriptions) > 1 {
		return errors.New("SUBSCRIPTION_SELECTION_REQUIRED")
	}
	return c.sendSMSSubscription(ctx, clientID, number, body, routeSubscription(route, c.cfg.SubscriptionID))
}
func (m *multiController) Dial(ctx context.Context, number string) error {
	return m.DialRoute(ctx, Route{}, number)
}
func (m *multiController) SendSMS(ctx context.Context, id, number, body string) error {
	return m.SendSMSRoute(ctx, Route{}, id, number, body)
}
func (m *multiController) Answer(ctx context.Context) error {
	c, _ := m.current()
	if c == nil {
		return errors.New("GATEWAY_SELECTION_REQUIRED")
	}
	return c.Answer(ctx)
}
func (m *multiController) Hangup(ctx context.Context) error {
	c, _ := m.current()
	if c == nil {
		return errors.New("GATEWAY_SELECTION_REQUIRED")
	}
	return c.Hangup(ctx)
}
func (m *multiController) DTMF(ctx context.Context, key string) error {
	c, _ := m.current()
	if c == nil {
		return errors.New("GATEWAY_SELECTION_REQUIRED")
	}
	return c.DTMF(ctx, key)
}
func (m *multiController) DeleteSMS(ctx context.Context, index int) error {
	c, _ := m.current()
	if c == nil {
		return errors.New("GATEWAY_SELECTION_REQUIRED")
	}
	return c.DeleteSMS(ctx, index)
}
func (m *multiController) SetMicMute(ctx context.Context, v bool) error {
	c, _ := m.current()
	if c == nil {
		return errors.New("GATEWAY_SELECTION_REQUIRED")
	}
	return c.SetMicMute(ctx, v)
}
func (m *multiController) EnableAudio(ctx context.Context, v bool) error {
	c, _ := m.current()
	if c == nil {
		return errors.New("GATEWAY_SELECTION_REQUIRED")
	}
	return c.EnableAudio(ctx, v)
}
func (m *multiController) OpenAudio(ctx context.Context) (io.ReadWriteCloser, error) {
	c, _ := m.current()
	if c == nil {
		return nil, errors.New("GATEWAY_SELECTION_REQUIRED")
	}
	return c.OpenAudio(ctx)
}
func (m *multiController) Probe(ctx context.Context) Status {
	statuses := m.Statuses(ctx)
	if len(statuses) == 0 {
		return Status{Provider: "android", Signal: -1, SignalDBm: -1}
	}
	m.mu.RLock()
	selected := m.selected
	m.mu.RUnlock()
	for _, s := range statuses {
		if s.GatewayID == selected {
			return s
		}
	}
	return statuses[0]
}
func (m *multiController) Statuses(ctx context.Context) []Status {
	out := make([]Status, 0, len(m.controllers))
	for id, c := range m.controllers {
		status := c.Probe(ctx)
		status.GatewayID = id
		out = append(out, status)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].GatewayID < out[j].GatewayID })
	return out
}
