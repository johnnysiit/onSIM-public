package store

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"onsim/internal/domain"
)

func TestEmptySnapshotUsesJSONArrays(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "onsim.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	raw, err := json.Marshal(s.Snapshot(false))
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"calls", "messages", "conversations", "rules", "recordings"} {
		want := []byte(`"` + field + `":[]`)
		if !bytes.Contains(raw, want) {
			t.Fatalf("%s is not an empty JSON array: %s", field, raw)
		}
	}
}

func TestMarkAllMessagesReadIsAtomicAndIdempotent(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "onsim.db")
	s, err := Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	messages := []*domain.Message{
		{ID: "incoming-a", Version: 1, Conversation: "+861", Direction: domain.Incoming, Number: "+861", Body: "one", Status: "received", Unread: true, CreatedAt: now},
		{ID: "incoming-b", Version: 1, Conversation: "+862", Direction: domain.Incoming, Number: "+862", Body: "two", Status: "received", Unread: true, CreatedAt: now.Add(time.Second)},
		{ID: "outgoing", Version: 1, Conversation: "+863", Direction: domain.Outgoing, Number: "+863", Body: "sent", Status: "sent", CreatedAt: now},
	}
	for _, message := range messages {
		if _, err = s.UpsertMessage(message); err != nil {
			t.Fatal(err)
		}
	}
	if updated, markErr := s.MarkAllMessagesRead(); markErr != nil || updated != 2 {
		t.Fatalf("first batch updated=%d err=%v", updated, markErr)
	}
	if updated, markErr := s.MarkAllMessagesRead(); markErr != nil || updated != 0 {
		t.Fatalf("idempotent batch updated=%d err=%v", updated, markErr)
	}
	for _, conversation := range s.Snapshot(false).Conversations {
		if conversation.Unread != 0 {
			t.Fatalf("conversation %s unread=%d", conversation.Number, conversation.Unread)
		}
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if s.Message("incoming-a").Unread || s.Message("incoming-b").Unread {
		t.Fatal("read state was not persisted across replay")
	}
}

func TestReplayAndEncryptedSettings(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "onsim.db")
	s, err := Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	settings := domain.Settings{SMS: true, Calls: true, TelegramEnabled: true, TelegramChatID: 42, TelegramToken: "secret-bot-token", ProviderAPIKey: "secret-provider", Country: "CN", AutoBlock: []string{"fraud"}}
	if _, err = s.UpdateSettings(settings); err != nil {
		t.Fatal(err)
	}
	if got := s.Settings().TelegramToken; got != "secret-bot-token" {
		t.Fatalf("hot state token=%q", got)
	}
	s.Close()
	raw, err := os.ReadFile(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte("secret-bot-token")) || bytes.Contains(raw, []byte("secret-provider")) {
		t.Fatal("secret persisted in plaintext")
	}
	s, err = Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if got := s.Settings(); got.TelegramToken != "secret-bot-token" || got.ProviderAPIKey != "secret-provider" {
		t.Fatalf("replay lost secrets: %#v", got)
	}
	if s.Snapshot(true).Settings.TelegramToken != "" {
		t.Fatal("snapshot leaked token")
	}
}
