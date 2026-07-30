package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"onsim/internal/app"
	"onsim/internal/auth"
	"onsim/internal/domain"
	"onsim/internal/filter"
	"onsim/internal/media"
	"onsim/internal/modem"
	"onsim/internal/store"
)

func TestSetupLoginAndProtectedState(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "api.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	log := slog.New(slog.NewTextHandler(testWriter{t}, nil))
	am := auth.New(st.DB(), time.Hour)
	md := modem.New("mock", "", "", "", log)
	svc := app.New(st, md, filter.New(st), log)
	mm := media.New(st, md, filepath.Join(dir, "recordings"), log)
	server := httptest.NewServer(New(st, am, svc, mm, log).Handler())
	defer server.Close()
	body, _ := json.Marshal(map[string]string{"password": "correct horse battery staple"})
	res, err := http.Post(server.URL+"/api/v1/auth/setup", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != 200 {
		t.Fatalf("setup status=%d", res.StatusCode)
	}
	var cookie *http.Cookie
	for _, c := range res.Cookies() {
		if c.Name == "onsim_session" {
			cookie = c
		}
	}
	if cookie == nil {
		t.Fatal("missing session cookie")
	}
	req, _ := http.NewRequest(http.MethodGet, server.URL+"/api/v1/state", nil)
	req.AddCookie(cookie)
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("state status=%d", res.StatusCode)
	}
	res.Body.Close()
	req, _ = http.NewRequest(http.MethodGet, server.URL+"/api/v1/info", nil)
	req.AddCookie(cookie)
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var systemInfo struct {
		Runtime struct {
			Version       string `json:"version"`
			UptimeSeconds int64  `json:"uptimeSeconds"`
		} `json:"runtime"`
	}
	if err = json.NewDecoder(res.Body).Decode(&systemInfo); err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusOK || systemInfo.Runtime.Version == "" || systemInfo.Runtime.UptimeSeconds < 0 {
		t.Fatalf("invalid system info: status=%d body=%+v", res.StatusCode, systemInfo)
	}
	dialBody := []byte(`{"number":"13800000000"}`)
	var first []byte
	for i := 0; i < 2; i++ {
		req, _ = http.NewRequest(http.MethodPost, server.URL+"/api/v1/calls", bytes.NewReader(dialBody))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", "same-command-key")
		req.AddCookie(cookie)
		res, err = http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		got, _ := io.ReadAll(res.Body)
		res.Body.Close()
		if res.StatusCode != 200 {
			t.Fatalf("dial %d: %d %s", i, res.StatusCode, got)
		}
		if i == 0 {
			first = got
		} else if !bytes.Equal(first, got) {
			t.Fatalf("idempotent response changed: %s != %s", first, got)
		}
	}
	if calls := st.Snapshot(true).Calls; len(calls) != 1 {
		t.Fatalf("idempotent command created %d calls", len(calls))
	}
	now := time.Now().UTC()
	for _, message := range []*domain.Message{
		{ID: "api-unread-1", Version: 1, Conversation: "+861", Direction: domain.Incoming, Number: "+861", Body: "一", Status: "received", Unread: true, CreatedAt: now},
		{ID: "api-unread-2", Version: 1, Conversation: "+862", Direction: domain.Incoming, Number: "+862", Body: "二", Status: "received", Unread: true, CreatedAt: now},
	} {
		if _, err = st.UpsertMessage(message); err != nil {
			t.Fatal(err)
		}
	}
	req, _ = http.NewRequest(http.MethodPost, server.URL+"/api/v1/messages/read-all", bytes.NewReader([]byte(`{}`)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "read-all-message-key")
	req.AddCookie(cookie)
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var readResult struct {
		Updated int `json:"updated"`
	}
	if err = json.NewDecoder(res.Body).Decode(&readResult); err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusOK || readResult.Updated != 2 {
		t.Fatalf("read-all status=%d result=%+v", res.StatusCode, readResult)
	}
}

func TestTemporaryCallGETDoesNotRequireIdempotencyKey(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "temp-api.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	am := auth.New(st.DB(), time.Hour)
	md := modem.New("mock", "", "", "", log)
	svc := app.New(st, md, filter.New(st), log)
	mm := media.New(st, md, filepath.Join(dir, "recordings"), log)
	server := httptest.NewServer(New(st, am, svc, mm, log).Handler())
	defer server.Close()

	call := &domain.Call{ID: "call_temp_status", Direction: domain.Outgoing, Number: "+8613800138000", State: domain.CallActive, StartedAt: time.Now()}
	if _, err = st.UpsertCall(call); err != nil {
		t.Fatal(err)
	}
	token, err := am.CreateActionToken(context.Background(), "temp_session", call.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest(http.MethodGet, server.URL+"/api/v1/temp/call/"+call.ID+"/media/status", nil)
	req.AddCookie(&http.Cookie{Name: "onsim_temp", Value: token})
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(res.Body)
		t.Fatalf("temporary media status GET=%d body=%s", res.StatusCode, body)
	}
}

type testWriter struct{ t *testing.T }

func (w testWriter) Write(p []byte) (int, error) { w.t.Log(string(p)); return len(p), nil }
