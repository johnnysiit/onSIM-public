package modem

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
)

func TestGenerateAndroidTokenIsStableAndPrivate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gateway", "token")
	if err := GenerateAndroidToken(path); err != nil {
		t.Fatal(err)
	}
	first, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 65 {
		t.Fatalf("token length = %d", len(first))
	}
	if err = GenerateAndroidToken(path); err != nil {
		t.Fatal(err)
	}
	second, _ := os.ReadFile(path)
	if string(first) != string(second) {
		t.Fatal("existing token was unexpectedly replaced")
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("token mode = %o", info.Mode().Perm())
	}
}

func TestAndroidStatusMappingAndAudioQualification(t *testing.T) {
	controller := NewAndroid(AndroidConfig{}, slog.Default()).(*androidController)
	raw, _ := json.Marshal(androidStatus{
		SIMReady: true, Registered: true, Operator: "China Unicom",
		Signal: 3, SignalDBm: -89, Model: "ONEPLUS A5000",
		IMSRegistered: true, VoLTE: true, AudioDownlinkOK: true, AudioUplinkOK: true,
	})
	controller.updateStatus(raw)
	status := controller.ProbeWithoutRequest()
	if status.Provider != "android" || status.Operator != "China Unicom" || !status.IMSRegistered {
		t.Fatalf("unexpected status: %+v", status)
	}
	if !controller.AudioCapable() {
		t.Fatal("qualified duplex audio was not exposed")
	}
}

func TestAndroidCommandFailsClosedWhenDisconnected(t *testing.T) {
	controller := NewAndroid(AndroidConfig{}, slog.Default())
	if err := controller.Dial(context.Background(), "+8613800138000"); err == nil {
		t.Fatal("offline dial unexpectedly succeeded")
	}
}

func TestAndroidHMACDoesNotExposeToken(t *testing.T) {
	token := "test-only-control-token"
	nonce := "00112233445566778899aabbccddeeff"
	got := hmacHex(token, "control:"+nonce)
	const want = "cf10f045bbff1883a3d4c2e307b41112705b026636c992e9d706ca9ec051297e"
	if got != want {
		t.Fatalf("HMAC = %s, want %s", got, want)
	}
	if got == token {
		t.Fatal("wire authenticator exposed the control token")
	}
}

func (a *androidController) ProbeWithoutRequest() Status {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.status
}
