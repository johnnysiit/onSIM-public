package media

import (
	"context"
	"encoding/binary"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"onsim/internal/domain"
	"onsim/internal/store"
)

func TestWaitingToneUsesHongKongDoubleBurstCadence(t *testing.T) {
	pcm := waitingTonePCM()
	if want := 16000 * 4; len(pcm) != want {
		t.Fatalf("waiting tone samples = %d, want %d", len(pcm), want)
	}
	firstSilence := 16000 * 400 / 1000
	if pcm[firstSilence+100] != 0 {
		t.Fatal("first inter-burst pause is not silent")
	}
	secondBurst := 16000 * 600 / 1000
	if pcm[secondBurst+137] == 0 {
		t.Fatal("second ringback burst is silent")
	}
	longPause := 16000
	if pcm[longPause] != 0 {
		t.Fatal("long ringback pause is not silent")
	}
}

func TestVoicemailBeepLength(t *testing.T) {
	if got, want := len(sinePCM(1000, 850*time.Millisecond, .28)), 13600; got != want {
		t.Fatalf("beep samples = %d, want %d", got, want)
	}
}

func TestVoicemailPromptCanBeSynthesized(t *testing.T) {
	pcm, err := voicemailPromptPCM()
	if err != nil {
		t.Fatal(err)
	}
	if len(pcm) < 16000*3 {
		t.Fatalf("voicemail prompt is unexpectedly short: %d samples", len(pcm))
	}
}

func TestPersistentUplinkSwitchesToBrowserPCMWithoutReopeningPort(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	manager := &Manager{log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	session := &callAudioSession{
		manager: manager, callID: "call_test", ctx: ctx, cancel: cancel,
		mic: make(chan []int16, 20), firstPCM: make(chan struct{}),
		statusValue: MediaStatus{State: "negotiating", SessionStartedAt: time.Now()},
		peerID:      1,
	}
	writer := &frameWriter{frames: make(chan []byte, 32)}
	done := make(chan error, 1)
	go func() { done <- session.writeUplink(writer) }()
	select {
	case <-writer.frames: // The far end hears waiting tone before browser RTP.
	case <-time.After(time.Second):
		t.Fatal("waiting tone was not written")
	}
	mic := make([]int16, webFrameSamples)
	for i := range mic {
		mic[i] = 10000
	}
	for i := 0; i < 8; i++ {
		if !session.enqueueMic(1, mic) {
			t.Fatal("browser PCM was rejected")
		}
	}
	select {
	case <-session.firstPCM:
	case <-time.After(time.Second):
		t.Fatal("browser PCM never reached the persistent writer")
	}
	if got := session.status().State; got != "bridged" {
		t.Fatalf("media state = %q, want bridged", got)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("persistent writer did not stop")
	}
}

func TestHeldPersistentSessionIgnoresBrowserPCMAndResumesWithFade(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	manager := &Manager{log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	session := &callAudioSession{
		manager: manager, callID: "call_held", ctx: ctx, cancel: cancel,
		mic: make(chan []int16, 20), firstPCM: make(chan struct{}),
		statusValue: MediaStatus{State: "bridged", SessionStartedAt: time.Now()},
		peerID:      1, bridged: true, everBridged: true, held: true,
	}
	writer := &frameWriter{frames: make(chan []byte, 32)}
	done := make(chan error, 1)
	go func() { done <- session.writeUplink(writer) }()
	mic := make([]int16, webFrameSamples)
	for i := range mic {
		mic[i] = 12000
	}
	if !session.enqueueMic(1, mic) {
		t.Fatal("held session rejected its current peer")
	}
	heldFrame := <-writer.frames
	if got := int16(binary.LittleEndian.Uint16(heldFrame[200:202])); got == 12000 {
		t.Fatal("held session leaked browser microphone PCM")
	}
	session.setHeld(false)
	if !session.enqueueMic(1, mic) {
		t.Fatal("resumed session rejected microphone PCM")
	}
	select {
	case <-writer.frames:
	case <-time.After(time.Second):
		t.Fatal("resumed session did not write PCM")
	}
	cancel()
	<-done
}

func TestCustomGreetingIsNormalizedAndReset(t *testing.T) {
	dir := t.TempDir()
	manager := &Manager{recordings: filepath.Join(dir, "recordings"), log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	pcm := make([]int16, 16000)
	for i := range pcm {
		pcm[i] = int16(i % 1000)
	}
	info, err := manager.SaveGreeting(encodeWAV(pcm))
	if err != nil {
		t.Fatal(err)
	}
	if !info.Custom || info.DurationSeconds < .99 || !manager.Greeting().Custom {
		t.Fatalf("unexpected greeting metadata: %#v", info)
	}
	raw, err := os.ReadFile(manager.greetingPath())
	if err != nil {
		t.Fatal(err)
	}
	normalized, duration, err := parseWAV(raw)
	if err != nil || len(normalized) != 16000 || duration != time.Second {
		t.Fatalf("normalized greeting invalid: samples=%d duration=%v err=%v", len(normalized), duration, err)
	}
	if err = manager.ResetGreeting(); err != nil || manager.Greeting().Custom {
		t.Fatalf("reset greeting failed: %v", err)
	}
	defaultAudio, err := manager.GreetingAudio()
	if err != nil {
		t.Fatal(err)
	}
	defaultPCM, defaultDuration, err := parseWAV(defaultAudio)
	if err != nil || len(defaultPCM) == 0 || defaultDuration < 10*time.Second {
		t.Fatalf("invalid embedded default greeting: samples=%d duration=%v err=%v", len(defaultPCM), defaultDuration, err)
	}
}

func TestDeleteRecordingRemovesOriginalCacheAndState(t *testing.T) {
	dir := t.TempDir()
	state, err := store.Open(filepath.Join(dir, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	original := filepath.Join(dir, "message.opus")
	if err = os.WriteFile(original, []byte("opus"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(original+".wav", encodeWAV(make([]int16, 320)), 0o640); err != nil {
		t.Fatal(err)
	}
	rec := &domain.Recording{ID: "rec_delete", CallID: "call_delete", Path: original, FileName: "message.opus", CreatedAt: time.Now(), Kind: "voicemail"}
	if _, err = state.UpsertRecording(rec); err != nil {
		t.Fatal(err)
	}
	manager := &Manager{state: state, recordings: filepath.Join(dir, "recordings"), log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	if err = manager.DeleteRecording(rec.ID); err != nil {
		t.Fatal(err)
	}
	if state.Recording(rec.ID) != nil {
		t.Fatal("recording remains in state after deletion")
	}
	for _, file := range []string{original, original + ".wav"} {
		if _, statErr := os.Stat(file); !os.IsNotExist(statErr) {
			t.Fatalf("%s still exists after deletion: %v", file, statErr)
		}
	}
}

func TestAnswerMediaDeadlineHangsUpWhenNoBrowserPCMArrives(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "media.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	call := &domain.Call{
		ID: "call_timeout", Version: 1, Direction: domain.Incoming,
		State: domain.CallActive, MediaOwner: "web", StartedAt: time.Now(),
	}
	if _, err = st.UpsertCall(call); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	timedOut := make(chan string, 1)
	manager := &Manager{
		state: st, log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		mediaTimeout: func(id string) { timedOut <- id },
	}
	session := &callAudioSession{
		manager: manager, callID: call.ID, ctx: ctx, cancel: cancel,
		firstPCM:    make(chan struct{}),
		statusValue: MediaStatus{State: "waiting_for_permission", SessionStartedAt: time.Now()},
	}
	session.startAnswerDeadline(25 * time.Millisecond)
	select {
	case id := <-timedOut:
		if id != call.ID {
			t.Fatalf("timed out call = %q", id)
		}
	case <-time.After(time.Second):
		t.Fatal("missing media timeout callback")
	}
	if status := session.status(); status.State != "timeout" || status.Error != "WEB_MEDIA_TIMEOUT" {
		t.Fatalf("unexpected timeout status: %+v", status)
	}
}

type frameWriter struct {
	mu     sync.Mutex
	frames chan []byte
}

func (w *frameWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	frame := append([]byte(nil), p...)
	w.mu.Unlock()
	select {
	case w.frames <- frame:
	default:
	}
	return len(p), nil
}
