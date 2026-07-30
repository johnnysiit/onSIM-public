package sipgateway

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"onsim/internal/store"
)

func TestRetrySchedule(t *testing.T) {
	want := []time.Duration{30 * time.Second, 2 * time.Minute, 10 * time.Minute, time.Hour, time.Hour}
	for i, expected := range want {
		if got := retryDelay(i + 1); got != expected {
			t.Fatalf("attempt %d: got %s want %s", i+1, got, expected)
		}
	}
}

func TestCredentialsEncryptedAndGenerated(t *testing.T) {
	dir := t.TempDir()
	state, err := store.Open(filepath.Join(dir, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	target := filepath.Join(dir, "asterisk", "generated.conf")
	gateway := &Gateway{state: state, cfg: Config{Listen: "127.0.0.1:5062", Target: "1001", GeneratedConfig: target}}
	if err = gateway.ensureCredentials(); err != nil {
		t.Fatal(err)
	}
	settings := state.Settings()
	if len(settings.SIPPassword) < 24 {
		t.Fatal("SIP password was not generated")
	}
	if state.Snapshot(false).Settings.SIPPassword != "" {
		t.Fatal("SIP password leaked into snapshot")
	}
	content, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), settings.SIPPassword) || !strings.Contains(string(content), "[1001-auth]") {
		t.Fatal("generated Asterisk config is incomplete")
	}
}
