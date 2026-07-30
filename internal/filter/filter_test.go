package filter

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"onsim/internal/domain"
	"onsim/internal/store"
)

func TestRulePrecedence(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "f.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	now := time.Now()
	_, _ = s.UpsertRule(&domain.FilterRule{ID: "block", Kind: "prefix", Pattern: "+86138", Action: "block", Scope: "both", Enabled: true, CreatedAt: now})
	_, _ = s.UpsertRule(&domain.FilterRule{ID: "allow", Kind: "exact", Pattern: "+8613800000000", Action: "allow", Scope: "both", Enabled: true, CreatedAt: now})
	e := New(s)
	if got := e.Decide(context.Background(), "+8613800000000", "", "call"); got.Action != "allow" {
		t.Fatalf("whitelist did not win: %#v", got)
	}
	if got := e.Decide(context.Background(), "+8613811111111", "", "call"); got.Action != "block" {
		t.Fatalf("prefix did not block: %#v", got)
	}
	normalized, err := Normalize("13800000000", "CN")
	if err != nil || normalized != "+8613800000000" {
		t.Fatalf("normalize=%q err=%v", normalized, err)
	}
}
