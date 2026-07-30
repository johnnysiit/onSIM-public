package filter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/nyaruka/phonenumbers"

	"onsim/internal/domain"
	"onsim/internal/store"
)

type Engine struct {
	state  *store.State
	client *http.Client
}

func New(state *store.State) *Engine {
	return &Engine{state: state, client: &http.Client{Timeout: 1500 * time.Millisecond}}
}

func Normalize(raw, country string) (string, error) {
	number, err := phonenumbers.Parse(strings.TrimSpace(raw), country)
	if err != nil || !phonenumbers.IsPossibleNumber(number) {
		return "", fmt.Errorf("INVALID_NUMBER")
	}
	return phonenumbers.Format(number, phonenumbers.E164), nil
}

func (e *Engine) Decide(ctx context.Context, number, body, scope string) domain.Decision {
	settings := e.state.Settings()
	rules := e.state.Rules()
	// Explicit allow rules always win.
	for _, r := range rules {
		if r.Enabled && r.Action == "allow" && applies(r, number, body, scope) {
			return domain.Decision{Action: "allow", Label: r.Label, Category: r.Category, Source: "local", RuleID: r.ID, Reason: "whitelist"}
		}
	}
	// Explicit local blocks and annotations.
	for _, r := range rules {
		if !r.Enabled || !applies(r, number, body, scope) {
			continue
		}
		action := r.Action
		if action == "" {
			action = "label"
		}
		return domain.Decision{Action: action, Label: r.Label, Category: r.Category, Source: "local", RuleID: r.ID, Confidence: 1}
	}
	if settings.ProviderURL != "" {
		if d, ok := e.lookup(ctx, settings, number); ok {
			if slices.Contains(settings.AutoBlock, d.Category) {
				d.Action = "block"
			} else {
				d.Action = "label"
			}
			return d
		}
	}
	return domain.Decision{Action: "allow"}
}

func applies(r *domain.FilterRule, number, body, scope string) bool {
	if r.Scope != "" && r.Scope != "both" && r.Scope != scope {
		return false
	}
	switch r.Kind {
	case "exact":
		return number == r.Pattern
	case "prefix":
		return strings.HasPrefix(number, r.Pattern)
	case "keyword":
		return body != "" && strings.Contains(strings.ToLower(body), strings.ToLower(r.Pattern))
	case "regex":
		re, err := regexp.Compile(r.Pattern)
		return err == nil && re.MatchString(number+"\n"+body)
	default:
		return false
	}
}

type providerResponse struct {
	Label      string  `json:"label"`
	Category   string  `json:"category"`
	Confidence float64 `json:"confidence"`
	Source     string  `json:"source"`
}

func (e *Engine) lookup(ctx context.Context, settings domain.Settings, number string) (domain.Decision, bool) {
	u, err := url.Parse(settings.ProviderURL)
	if err != nil || u.Scheme != "https" {
		return domain.Decision{}, false
	}
	q := u.Query()
	q.Set("phone", number)
	u.RawQuery = q.Encode()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if settings.ProviderAPIKey != "" {
		req.Header.Set("Authorization", "Bearer "+settings.ProviderAPIKey)
	}
	resp, err := e.client.Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		if resp != nil {
			resp.Body.Close()
		}
		return domain.Decision{}, false
	}
	defer resp.Body.Close()
	var out providerResponse
	if json.NewDecoder(resp.Body).Decode(&out) != nil || out.Category == "" || out.Confidence < .5 {
		return domain.Decision{}, false
	}
	if out.Source == "" {
		out.Source = u.Host
	}
	return domain.Decision{Label: out.Label, Category: out.Category, Confidence: out.Confidence, Source: out.Source}, true
}
