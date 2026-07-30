package auth

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"onsim/internal/store"
)

func TestPasswordSessionAndSingleUseToken(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "a.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	m := New(s.DB(), time.Hour)
	ctx := context.Background()
	if err = m.Setup(ctx, "short"); err == nil {
		t.Fatal("short password accepted")
	}
	if err = m.Setup(ctx, "correct horse battery staple"); err != nil {
		t.Fatal(err)
	}
	token, _, err := m.Login(ctx, "correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	if !m.Validate(ctx, token) {
		t.Fatal("session invalid")
	}
	m.Logout(ctx, token)
	if m.Validate(ctx, token) {
		t.Fatal("logout failed")
	}
	action, err := m.CreateActionToken(ctx, "temp_call", "call_1", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if id, err := m.ConsumeActionToken(ctx, action, "temp_call"); err != nil || id != "call_1" {
		t.Fatalf("consume: %s %v", id, err)
	}
	if _, err = m.ConsumeActionToken(ctx, action, "temp_call"); err == nil {
		t.Fatal("token reused")
	}
}
