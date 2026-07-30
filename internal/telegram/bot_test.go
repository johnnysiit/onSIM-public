package telegram

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"onsim/internal/app"
	"onsim/internal/auth"
	"onsim/internal/domain"
	"onsim/internal/filter"
	"onsim/internal/modem"
	"onsim/internal/store"
)

func TestStartHelpAndCallConversation(t *testing.T) {
	var mu sync.Mutex
	var requests []map[string]any
	var nextMessageID int64
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		_ = json.NewDecoder(r.Body).Decode(&payload)
		payload["_method"] = strings.TrimPrefix(r.URL.Path, "/bottest/")
		mu.Lock()
		requests = append(requests, payload)
		nextMessageID++
		id := nextMessageID
		mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": map[string]any{"message_id": id}})
	}))
	defer api.Close()

	state, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	settings := state.Settings()
	settings.TelegramEnabled, settings.TelegramToken, settings.TelegramChatID = true, "test", 42
	if _, err = state.UpdateSettings(settings); err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	controller := modem.New("mock", "", "", "", log)
	service := app.New(state, controller, filter.New(state), log)
	bot := New(state, service, auth.New(state.DB(), time.Hour), "https://onsim.local", log)
	bot.apiBase = api.URL + "/bot"

	bot.handleUpdate(context.Background(), settings, update{Message: &message{Chat: chat{ID: 42}, Text: "/start"}})
	bot.handleUpdate(context.Background(), settings, update{Message: &message{Chat: chat{ID: 42}, Text: "/help"}})
	bot.handleUpdate(context.Background(), settings, update{Message: &message{Chat: chat{ID: 42}, Text: "/call"}})

	bot.mu.Lock()
	if len(bot.replies) != 1 {
		t.Fatalf("/call did not create a force-reply prompt: %#v", bot.replies)
	}
	var replyMessageID int64
	for id := range bot.replies {
		replyMessageID = id
	}
	bot.mu.Unlock()
	bot.handleUpdate(context.Background(), settings, update{Message: &message{
		Chat: chat{ID: 42}, Text: "13800138000",
		ReplyTo: &message{MessageID: replyMessageID},
	}})
	bot.mu.Lock()
	if len(bot.pending) != 1 {
		t.Fatalf("number reply did not create confirmation: %#v", bot.pending)
	}
	var confirmationID string
	for id := range bot.pending {
		confirmationID = id
	}
	bot.mu.Unlock()
	bot.handleUpdate(context.Background(), settings, update{Callback: &callback{
		ID: "callback-1", Data: "confirm:" + confirmationID,
		Message: message{Chat: chat{ID: 42}},
	}})
	if call := state.ActiveCall(); call == nil || call.Number != "+8613800138000" || call.Direction != domain.Outgoing {
		t.Fatalf("confirmed Telegram call was not dialed: %#v", call)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(requests) < 6 {
		t.Fatalf("too few Telegram API requests: %d", len(requests))
	}
	if markup, ok := requests[0]["reply_markup"].(map[string]any); !ok || markup["keyboard"] == nil {
		t.Fatalf("/start missing persistent preset keyboard: %#v", requests[0])
	}
	foundHelp, foundPrompt, foundConfirm, foundCallLink := false, false, false, false
	for _, request := range requests {
		text, _ := request["text"].(string)
		foundHelp = foundHelp || strings.Contains(text, "/call [号码]")
		foundPrompt = foundPrompt || strings.Contains(text, "请输入要拨打")
		foundConfirm = foundConfirm || strings.Contains(text, "请确认拨打")
		if strings.Contains(text, "打开通话页面") {
			markup, _ := request["reply_markup"].(map[string]any)
			raw, _ := json.Marshal(markup)
			foundCallLink = strings.Contains(string(raw), "https://onsim.local/t/call/")
		}
	}
	if !foundHelp || !foundPrompt || !foundConfirm || !foundCallLink {
		t.Fatalf("missing Telegram workflow responses: help=%v prompt=%v confirm=%v callLink=%v", foundHelp, foundPrompt, foundConfirm, foundCallLink)
	}
}
