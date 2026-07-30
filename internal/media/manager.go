package media

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/hraban/opus"
	"github.com/pion/webrtc/v4"
	pionmedia "github.com/pion/webrtc/v4/pkg/media"

	"onsim/internal/domain"
	"onsim/internal/modem"
	"onsim/internal/store"
)

type Manager struct {
	state        *store.State
	audio        modem.Controller
	recordings   string
	log          *slog.Logger
	mu           sync.Mutex
	activeCall   string
	cancel       context.CancelFunc
	generation   uint64
	latestNear   []int16
	recorder     *recorder
	webSessions  map[string]*callAudioSession
	mediaTimeout func(string)
}

type Offer struct {
	SDP        string      `json:"sdp"`
	Type       string      `json:"type"`
	MediaState MediaStatus `json:"media,omitempty"`
}

type GreetingInfo struct {
	Custom          bool    `json:"custom"`
	DurationSeconds float64 `json:"durationSeconds"`
}

//go:embed default_voicemail_greeting.wav
var defaultGreetingWAV []byte

func New(state *store.State, audio modem.Controller, recordings string, log *slog.Logger) *Manager {
	return &Manager{state: state, audio: audio, recordings: recordings, log: log, webSessions: map[string]*callAudioSession{}}
}

func (m *Manager) Offer(ctx context.Context, callID string, offer Offer) (Offer, error) {
	call := m.state.Call(callID)
	if call == nil || call.State != domain.CallActive {
		return Offer{}, errors.New("CALL_STATE_CONFLICT")
	}
	if call.MediaOwner != "" && call.MediaOwner != "web" {
		return Offer{}, errors.New("MEDIA_BUSY")
	}
	session, err := m.ensureWebSession(ctx, callID)
	if err != nil {
		return Offer{}, err
	}
	pc, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		return Offer{}, err
	}
	// WebRTC advertises Opus as 48 kHz / 2 channels even when the captured
	// source and encoded payload are mono. Declaring one channel prevents
	// Chrome and standards-compliant Pion offers from matching this track.
	out, err := webrtc.NewTrackLocalStaticSample(webrtc.RTPCodecCapability{
		MimeType: webrtc.MimeTypeOpus, ClockRate: 48000, Channels: 2,
	}, "audio", "onsim")
	if err != nil {
		pc.Close()
		return Offer{}, err
	}
	if _, err = pc.AddTrack(out); err != nil {
		pc.Close()
		return Offer{}, err
	}
	peerCtx, peerCancel := context.WithCancel(context.Background())
	peerID := session.attachPeer(out, peerCancel)
	cleanupPeer := func() {
		peerCancel()
		session.detachPeer(peerID)
		_ = pc.Close()
	}
	pc.OnTrack(func(track *webrtc.TrackRemote, _ *webrtc.RTPReceiver) {
		go func() {
			if bridgeErr := m.browserToSession(peerCtx, track, session, peerID); bridgeErr != nil && peerCtx.Err() == nil {
				m.log.Error("browser uplink bridge stopped", "call_id", callID, "error", bridgeErr)
				cleanupPeer()
			}
		}()
	})
	pc.OnConnectionStateChange(func(st webrtc.PeerConnectionState) {
		m.log.Info("web media peer state", "call_id", callID, "peer_id", peerID, "state", st.String())
		switch st {
		case webrtc.PeerConnectionStateConnected:
			session.markNegotiating(peerID)
		case webrtc.PeerConnectionStateDisconnected:
			session.markRecovering(peerID)
		case webrtc.PeerConnectionStateFailed:
			cleanupPeer()
		}
	})
	go func() {
		select {
		case <-session.ctx.Done():
			cleanupPeer()
		case <-peerCtx.Done():
		}
	}()
	if err = pc.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeOffer, SDP: offer.SDP}); err != nil {
		cleanupPeer()
		return Offer{}, err
	}
	gather := webrtc.GatheringCompletePromise(pc)
	answer, err := pc.CreateAnswer(nil)
	if err != nil {
		cleanupPeer()
		return Offer{}, err
	}
	if err = pc.SetLocalDescription(answer); err != nil {
		cleanupPeer()
		return Offer{}, err
	}
	select {
	case <-gather:
	case <-time.After(10 * time.Second):
		cleanupPeer()
		return Offer{}, errors.New("ICE_TIMEOUT")
	case <-ctx.Done():
		cleanupPeer()
		return Offer{}, ctx.Err()
	}
	local := pc.LocalDescription()
	return Offer{SDP: local.SDP, Type: "answer", MediaState: session.status()}, nil
}

// BeginWaitingMedia starts Hong Kong-style double-burst ringback once the
// cellular call is active. The tone and browser microphone share one persistent
// Android PCM socket, so acquiring browser media never tears down Telephony Tx.
func (m *Manager) BeginWaitingMedia(callID string) {
	go func() {
		session, err := m.ensureWebSession(context.Background(), callID)
		if err != nil {
			m.log.Warn("waiting media unavailable", "call_id", callID, "error", err)
			return
		}
		if call := m.state.Call(callID); call != nil && !call.Held {
			session.startAnswerDeadline(15 * time.Second)
		}
	}()
}

func (m *Manager) SetHeld(callID string, held bool) {
	m.mu.Lock()
	session := m.webSessions[callID]
	m.mu.Unlock()
	if session != nil {
		session.setHeld(held)
		return
	}
	if held {
		go func() {
			session, err := m.ensureWebSession(context.Background(), callID)
			if err == nil {
				session.setHeld(true)
			}
		}()
	}
}

func (m *Manager) StopWaitingMedia(callID string) {
	m.stopWebSession(callID)
}

func (m *Manager) stopWebSession(callID string) {
	m.mu.Lock()
	session := m.webSessions[callID]
	m.mu.Unlock()
	if session != nil {
		session.cancel()
	}
}

func (m *Manager) SetMediaTimeoutHandler(handler func(string)) {
	m.mu.Lock()
	m.mediaTimeout = handler
	m.mu.Unlock()
}

func (m *Manager) MediaStatus(callID string) MediaStatus {
	m.mu.Lock()
	session := m.webSessions[callID]
	m.mu.Unlock()
	if session == nil {
		return MediaStatus{State: "unavailable", Error: "MEDIA_SESSION_NOT_FOUND"}
	}
	return session.status()
}

func (m *Manager) selectCallRoute(callID string) error {
	call := m.state.Call(callID)
	if call == nil {
		return errors.New("CALL_NOT_FOUND")
	}
	if routed, ok := m.audio.(modem.RoutedController); ok {
		return routed.SelectRoute(modem.Route{GatewayID: call.GatewayID, SubscriptionID: call.SubscriptionID})
	}
	return nil
}

// StartVoicemail owns the call media, plays the announcement and beep, then
// records only the caller until the remote party hangs up.
func (m *Manager) StartVoicemail(callID string) {
	m.stopWebSessionAndWait(callID)
	go func() {
		if err := m.runVoicemail(callID); err != nil && !errors.Is(err, context.Canceled) {
			m.log.Error("voicemail media stopped", "call_id", callID, "error", err)
		}
	}()
}

func (m *Manager) stopWebSessionAndWait(callID string) {
	m.mu.Lock()
	session := m.webSessions[callID]
	m.mu.Unlock()
	if session == nil {
		return
	}
	session.cancel()
	select {
	case <-session.done:
	case <-time.After(2 * time.Second):
		m.log.Warn("timed out closing web media before voicemail", "call_id", callID)
	}
}

func (m *Manager) runVoicemail(callID string) error {
	if err := m.selectCallRoute(callID); err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.mu.Lock()
	if m.cancel != nil {
		m.cancel()
	}
	m.generation++
	generation := m.generation
	m.cancel = cancel
	m.activeCall = callID
	m.mu.Unlock()
	defer m.release(callID, generation)
	defer func() {
		m.mu.Lock()
		hasRecording := m.recorder != nil && m.recorder.callID == callID
		m.mu.Unlock()
		if hasRecording {
			_, _ = m.StopRecording(callID)
		}
	}()
	if err := m.audio.EnableAudio(ctx, true); err != nil {
		return err
	}
	port, err := m.audio.OpenAudio(ctx)
	if err != nil {
		return err
	}
	defer port.Close()
	recordReady := make(chan struct{})
	readDone := make(chan error, 1)
	go func() {
		raw := make([]byte, 640)
		for {
			if _, readErr := io.ReadFull(port, raw); readErr != nil {
				readDone <- readErr
				return
			}
			select {
			case <-recordReady:
				pcm := make([]int16, len(raw)/2)
				for i := range pcm {
					pcm[i] = int16(binary.LittleEndian.Uint16(raw[i*2:]))
				}
				m.writeRecording(pcm)
			default:
			}
		}
	}()
	prompt, err := m.voicemailPromptPCM()
	if err != nil {
		return err
	}
	if err = writePCMRealtime(ctx, port, prompt); err != nil {
		return err
	}
	if err = writePCMRealtime(ctx, port, sinePCM(1000, 850*time.Millisecond, .28)); err != nil {
		return err
	}
	m.mu.Lock()
	m.latestNear = nil
	m.mu.Unlock()
	if _, err = m.startRecording(callID, "voicemail"); err != nil {
		return err
	}
	close(recordReady)
	select {
	case <-ctx.Done():
		return ctx.Err()
	case err = <-readDone:
		return err
	}
}

func waitingTonePCM() []int16 {
	out := make([]int16, 0, 16000*4)
	out = append(out, dualTonePCM(440, 480, 400*time.Millisecond, .16)...)
	out = append(out, make([]int16, 16000*200/1000)...)
	out = append(out, dualTonePCM(440, 480, 400*time.Millisecond, .16)...)
	out = append(out, make([]int16, 16000*3)...)
	return out
}

func dualTonePCM(first, second float64, duration time.Duration, amplitude float64) []int16 {
	count := int(float64(16000) * duration.Seconds())
	out := make([]int16, count)
	for i := range out {
		envelope := 1.0
		if i < 160 {
			envelope = float64(i) / 160
		} else if count-i < 160 {
			envelope = float64(count-i) / 160
		}
		a := math.Sin(2 * math.Pi * first * float64(i) / 16000)
		b := math.Sin(2 * math.Pi * second * float64(i) / 16000)
		out[i] = int16((a + b) * .5 * amplitude * envelope * 32767)
	}
	return out
}

func sinePCM(frequency float64, duration time.Duration, amplitude float64) []int16 {
	count := int(float64(16000) * duration.Seconds())
	out := make([]int16, count)
	for i := range out {
		envelope := 1.0
		if i < 160 {
			envelope = float64(i) / 160
		} else if count-i < 160 {
			envelope = float64(count-i) / 160
		}
		out[i] = int16(math.Sin(2*math.Pi*frequency*float64(i)/16000) * amplitude * envelope * 32767)
	}
	return out
}

func writePCMRealtime(ctx context.Context, writer io.Writer, pcm []int16) error {
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for offset := 0; offset < len(pcm); offset += 320 {
		end := offset + 320
		if end > len(pcm) {
			end = len(pcm)
		}
		if _, err := writer.Write(int16Bytes(pcm[offset:end])); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
	return nil
}

func (m *Manager) voicemailPromptPCM() ([]int16, error) {
	if raw, err := os.ReadFile(m.greetingPath()); err == nil {
		if pcm, _, parseErr := parseWAV(raw); parseErr == nil && len(pcm) > 0 {
			return pcm, nil
		}
		m.log.Warn("custom voicemail greeting invalid; using default")
	}
	return defaultVoicemailPromptPCM()
}

func defaultVoicemailPromptPCM() ([]int16, error) {
	pcm, _, err := parseWAV(defaultGreetingWAV)
	return pcm, err
}

// voicemailPromptPCM is kept for deterministic default-prompt tests.
func voicemailPromptPCM() ([]int16, error) { return defaultVoicemailPromptPCM() }

func parseWAV(raw []byte) ([]int16, time.Duration, error) {
	if len(raw) < 12 || string(raw[:4]) != "RIFF" || string(raw[8:12]) != "WAVE" {
		return nil, 0, errors.New("VOICEMAIL_PROMPT_INVALID")
	}
	rate, channels, bits, format := 0, 0, 0, 0
	var data []byte
	for at := 12; at+8 <= len(raw); {
		size := int(binary.LittleEndian.Uint32(raw[at+4 : at+8]))
		start, end := at+8, at+8+size
		if end > len(raw) {
			// espeak-ng uses 0x7ffff000 as an unknown streaming data size.
			if string(raw[at:at+4]) != "data" || size < 0x7fff0000 {
				return nil, 0, errors.New("VOICEMAIL_PROMPT_INVALID")
			}
			end = len(raw)
		}
		switch string(raw[at : at+4]) {
		case "fmt ":
			if size < 16 {
				return nil, 0, errors.New("VOICEMAIL_PROMPT_INVALID")
			}
			format = int(binary.LittleEndian.Uint16(raw[start : start+2]))
			channels = int(binary.LittleEndian.Uint16(raw[start+2 : start+4]))
			rate = int(binary.LittleEndian.Uint32(raw[start+4 : start+8]))
			bits = int(binary.LittleEndian.Uint16(raw[start+14 : start+16]))
		case "data":
			data = raw[start:end]
		}
		if end == len(raw) {
			break
		}
		at = end + size%2
	}
	if format != 1 || rate < 8000 || rate > 192000 || channels < 1 || channels > 8 || bits != 16 || len(data) < 2*channels {
		return nil, 0, errors.New("VOICEMAIL_PROMPT_INVALID")
	}
	source := make([]int16, len(data)/2/channels)
	for i := range source {
		var total int32
		for channel := 0; channel < channels; channel++ {
			total += int32(int16(binary.LittleEndian.Uint16(data[(i*channels+channel)*2:])))
		}
		source[i] = int16(total / int32(channels))
	}
	duration := time.Duration(float64(len(source)) / float64(rate) * float64(time.Second))
	if duration > 30*time.Second {
		return nil, duration, errors.New("VOICEMAIL_GREETING_TOO_LONG")
	}
	if rate == 16000 {
		return source, duration, nil
	}
	target := make([]int16, len(source)*16000/rate)
	for i := range target {
		position := float64(i) * float64(rate) / 16000
		left := int(position)
		if left >= len(source)-1 {
			target[i] = source[len(source)-1]
			continue
		}
		fraction := position - float64(left)
		target[i] = int16(float64(source[left])*(1-fraction) + float64(source[left+1])*fraction)
	}
	return target, duration, nil
}

func encodeWAV(pcm []int16) []byte {
	raw := make([]byte, 44+len(pcm)*2)
	copy(raw[:4], "RIFF")
	binary.LittleEndian.PutUint32(raw[4:8], uint32(len(raw)-8))
	copy(raw[8:12], "WAVE")
	copy(raw[12:16], "fmt ")
	binary.LittleEndian.PutUint32(raw[16:20], 16)
	binary.LittleEndian.PutUint16(raw[20:22], 1)
	binary.LittleEndian.PutUint16(raw[22:24], 1)
	binary.LittleEndian.PutUint32(raw[24:28], 16000)
	binary.LittleEndian.PutUint32(raw[28:32], 32000)
	binary.LittleEndian.PutUint16(raw[32:34], 2)
	binary.LittleEndian.PutUint16(raw[34:36], 16)
	copy(raw[36:40], "data")
	binary.LittleEndian.PutUint32(raw[40:44], uint32(len(pcm)*2))
	for i, sample := range pcm {
		binary.LittleEndian.PutUint16(raw[44+i*2:], uint16(sample))
	}
	return raw
}

func (m *Manager) greetingPath() string {
	return filepath.Join(filepath.Dir(m.recordings), "voicemail-greeting.wav")
}

func (m *Manager) SaveGreeting(raw []byte) (GreetingInfo, error) {
	if len(raw) > 10<<20 {
		return GreetingInfo{}, errors.New("VOICEMAIL_GREETING_TOO_LARGE")
	}
	pcm, duration, err := parseWAV(raw)
	if err != nil {
		return GreetingInfo{}, err
	}
	if err = os.MkdirAll(filepath.Dir(m.greetingPath()), 0o750); err != nil {
		return GreetingInfo{}, err
	}
	temp := m.greetingPath() + ".tmp"
	if err = os.WriteFile(temp, encodeWAV(pcm), 0o640); err != nil {
		return GreetingInfo{}, err
	}
	if err = os.Rename(temp, m.greetingPath()); err != nil {
		_ = os.Remove(temp)
		return GreetingInfo{}, err
	}
	return GreetingInfo{Custom: true, DurationSeconds: duration.Seconds()}, nil
}

func (m *Manager) Greeting() GreetingInfo {
	raw, err := os.ReadFile(m.greetingPath())
	if err == nil {
		_, duration, parseErr := parseWAV(raw)
		if parseErr == nil {
			return GreetingInfo{Custom: true, DurationSeconds: duration.Seconds()}
		}
	}
	_, duration, parseErr := parseWAV(defaultGreetingWAV)
	if parseErr != nil {
		return GreetingInfo{}
	}
	return GreetingInfo{Custom: false, DurationSeconds: duration.Seconds()}
}

func (m *Manager) ResetGreeting() error {
	err := os.Remove(m.greetingPath())
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func (m *Manager) GreetingPath() (string, error) {
	if !m.Greeting().Custom {
		return "", errors.New("NOT_FOUND")
	}
	return m.greetingPath(), nil
}

func (m *Manager) GreetingAudio() ([]byte, error) {
	if raw, err := os.ReadFile(m.greetingPath()); err == nil {
		if _, _, parseErr := parseWAV(raw); parseErr == nil {
			return raw, nil
		}
	}
	if _, _, err := parseWAV(defaultGreetingWAV); err != nil {
		return nil, errors.New("VOICEMAIL_PROMPT_UNAVAILABLE")
	}
	return append([]byte(nil), defaultGreetingWAV...), nil
}

func (m *Manager) PlaybackPath(id string) (string, error) {
	rec := m.state.Recording(id)
	if rec == nil {
		return "", errors.New("NOT_FOUND")
	}
	original := m.recordingPath(rec)
	wav := original + ".wav"
	if info, err := os.Stat(wav); err == nil && info.Size() > 44 {
		return wav, nil
	}
	temp := original + ".tmp.wav"
	_ = os.Remove(temp)
	cmd := exec.Command("opusdec", "--quiet", original, temp)
	if output, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("RECORDING_CONVERSION_FAILED: %s", strings.TrimSpace(string(output)))
	}
	if err := os.Rename(temp, wav); err != nil {
		_ = os.Remove(temp)
		return "", err
	}
	return wav, nil
}

func (m *Manager) DeleteRecording(id string) error {
	rec := m.state.Recording(id)
	if rec == nil {
		return errors.New("NOT_FOUND")
	}
	original := m.recordingPath(rec)
	if err := os.Remove(original); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.Remove(original + ".wav"); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	_, err := m.state.DeleteRecording(id)
	return err
}

func (m *Manager) OriginalPath(id string) (string, error) {
	rec := m.state.Recording(id)
	if rec == nil {
		return "", errors.New("NOT_FOUND")
	}
	return m.recordingPath(rec), nil
}

func (m *Manager) recordingPath(rec *domain.Recording) string {
	if rec.Path != "" {
		return rec.Path
	}
	// Recordings created by older releases did not persist Path. FileName is
	// generated by onSIM and contains no directory component.
	return filepath.Join(m.recordings, filepath.Base(rec.FileName))
}

// BridgePCM connects an 8 kHz signed 16-bit SIP PCM stream to the modem's
// 16 kHz signed 16-bit USB PCM stream. A call has exactly one media owner.
func (m *Manager) BridgePCM(ctx context.Context, callID, owner string, remote io.Reader, remoteOut io.Writer) error {
	call := m.state.Call(callID)
	if call == nil || call.State != domain.CallActive || call.MediaOwner != owner {
		return errors.New("CALL_STATE_CONFLICT")
	}
	m.mu.Lock()
	if m.activeCall != "" && m.activeCall != callID {
		m.mu.Unlock()
		return errors.New("MEDIA_BUSY")
	}
	if m.cancel != nil {
		m.cancel()
	}
	mediaCtx, cancel := context.WithCancel(ctx)
	m.generation++
	generation := m.generation
	m.cancel = cancel
	m.activeCall = callID
	m.mu.Unlock()

	// See Offer: claim and enable the device synchronously before opening its
	// PCM socket, otherwise fast-answering IVRs race call.active handling.
	if err := m.audio.EnableAudio(ctx, true); err != nil {
		cancel()
		m.release(callID, generation)
		return errors.New("MEDIA_UNAVAILABLE")
	}
	port, err := m.audio.OpenAudio(ctx)
	if err != nil {
		cancel()
		m.release(callID, generation)
		return errors.New("MEDIA_UNAVAILABLE")
	}
	defer func() {
		cancel()
		_ = port.Close()
		m.release(callID, generation)
	}()

	errs := make(chan error, 2)
	go func() { errs <- m.sipToModem(mediaCtx, remote, port) }()
	go func() { errs <- m.modemToSIP(mediaCtx, port, remoteOut) }()
	select {
	case <-mediaCtx.Done():
		return mediaCtx.Err()
	case err = <-errs:
		return err
	}
}

func (m *Manager) sipToModem(ctx context.Context, remote io.Reader, port io.Writer) error {
	pcm8 := make([]byte, 320)
	for {
		n, err := remote.Read(pcm8)
		if err != nil {
			return err
		}
		if n < 2 {
			continue
		}
		n -= n % 2
		samples := make([]int16, n/2)
		raw16 := make([]byte, n*2)
		for i := range samples {
			v := int16(binary.LittleEndian.Uint16(pcm8[i*2:]))
			samples[i] = v
			binary.LittleEndian.PutUint16(raw16[i*4:], uint16(v))
			binary.LittleEndian.PutUint16(raw16[i*4+2:], uint16(v))
		}
		if _, err = port.Write(raw16); err != nil {
			return err
		}
		m.mu.Lock()
		m.latestNear = append(m.latestNear[:0], samples...)
		m.mu.Unlock()
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
	}
}

func (m *Manager) modemToSIP(ctx context.Context, port io.Reader, remote io.Writer) error {
	raw16 := make([]byte, 640)
	raw8 := make([]byte, 320)
	far := make([]int16, 320)
	for {
		if _, err := io.ReadFull(port, raw16); err != nil {
			return err
		}
		for i := 0; i < 160; i++ {
			a := int16(binary.LittleEndian.Uint16(raw16[i*4:]))
			b := int16(binary.LittleEndian.Uint16(raw16[i*4+2:]))
			v := int16((int32(a) + int32(b)) / 2)
			binary.LittleEndian.PutUint16(raw8[i*2:], uint16(v))
			far[i] = v
		}
		if _, err := remote.Write(raw8); err != nil {
			return err
		}
		m.writeRecording(far[:160])
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
	}
}

func (m *Manager) browserToModem(ctx context.Context, track *webrtc.TrackRemote, port io.Writer) error {
	decoder, err := opus.NewDecoder(48000, 1)
	if err != nil {
		return err
	}
	pcm48 := make([]int16, 5760)
	for {
		pkt, _, err := track.ReadRTP()
		if err != nil {
			return err
		}
		n, err := decoder.Decode(pkt.Payload, pcm48)
		if err != nil {
			continue
		}
		pcm16 := make([]int16, n/3)
		for i := range pcm16 {
			pcm16[i] = pcm48[i*3]
		}
		raw := int16Bytes(pcm16)
		if _, err = port.Write(raw); err != nil {
			return err
		}
		m.mu.Lock()
		m.latestNear = append(m.latestNear[:0], pcm16...)
		m.mu.Unlock()
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
	}
}

func (m *Manager) modemToBrowser(ctx context.Context, port io.Reader, out *webrtc.TrackLocalStaticSample) error {
	encoder, err := opus.NewEncoder(16000, 1, opus.AppVoIP)
	if err != nil {
		return err
	}
	raw := make([]byte, 640)
	pcm := make([]int16, 320)
	encoded := make([]byte, 4000)
	for {
		if _, err = io.ReadFull(port, raw); err != nil {
			return err
		}
		for i := range pcm {
			pcm[i] = int16(binary.LittleEndian.Uint16(raw[i*2:]))
		}
		n, err := encoder.Encode(pcm, encoded)
		if err == nil {
			if err = out.WriteSample(pionmedia.Sample{Data: append([]byte(nil), encoded[:n]...), Duration: 20 * time.Millisecond}); err != nil {
				return err
			}
		} else {
			return err
		}
		m.writeRecording(pcm)
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
	}
}

func (m *Manager) StartRecording(callID string) (*domain.Recording, error) {
	return m.startRecording(callID, "call")
}

func (m *Manager) startRecording(callID, kind string) (*domain.Recording, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.state.Snapshot(false).Device.DiskUsedPct >= 95 {
		return nil, errors.New("STORAGE_FULL")
	}
	if m.activeCall != callID {
		return nil, errors.New("MEDIA_UNAVAILABLE")
	}
	if m.recorder != nil {
		return nil, errors.New("RECORDING_ACTIVE")
	}
	if err := os.MkdirAll(m.recordings, 0o750); err != nil {
		return nil, err
	}
	id := domain.NewID("rec")
	name := id + ".opus"
	target := filepath.Join(m.recordings, name)
	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, "opusenc", "--quiet", "--raw", "--raw-rate", "16000", "--raw-chan", "1", "-", target)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		return nil, err
	}
	if err = cmd.Start(); err != nil {
		cancel()
		return nil, errors.New("OPUSENC_UNAVAILABLE")
	}
	m.recorder = &recorder{id: id, callID: callID, name: name, path: target, started: time.Now().UTC(), cmd: cmd, stdin: stdin, cancel: cancel, kind: kind}
	call := m.state.Call(callID)
	if call != nil {
		call.Version++
		call.Recording = true
		call.RecordingID = id
		_, _ = m.state.UpsertCall(call)
	}
	return &domain.Recording{ID: id, CallID: callID, FileName: name, CreatedAt: m.recorder.started, Kind: kind}, nil
}

func (m *Manager) StopRecording(callID string) (*domain.Recording, error) {
	m.mu.Lock()
	r := m.recorder
	if r == nil || r.callID != callID {
		m.mu.Unlock()
		return nil, errors.New("RECORDING_NOT_ACTIVE")
	}
	m.recorder = nil
	m.mu.Unlock()
	_ = r.stdin.Close()
	err := r.cmd.Wait()
	r.cancel()
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(r.path)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(r.path)
	if err != nil {
		return nil, err
	}
	h := sha256.New()
	_, _ = io.Copy(h, f)
	f.Close()
	rec := &domain.Recording{ID: r.id, CallID: r.callID, Path: r.path, FileName: r.name, Duration: int64(time.Since(r.started).Seconds()), Size: info.Size(), SHA256: hex.EncodeToString(h.Sum(nil)), CreatedAt: r.started, Kind: r.kind}
	_, err = m.state.UpsertRecording(rec)
	if call := m.state.Call(callID); call != nil {
		call.Version++
		call.Recording = false
		_, _ = m.state.UpsertCall(call)
	}
	return rec, err
}

func (m *Manager) EndCall(callID string) {
	m.StopWaitingMedia(callID)
	m.mu.Lock()
	cancel := m.cancel
	hasRecording := m.recorder != nil && m.recorder.callID == callID
	m.mu.Unlock()
	if hasRecording {
		_, _ = m.StopRecording(callID)
	}
	if cancel != nil {
		cancel()
	}
}

func (m *Manager) writeRecording(far []int16) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.recorder == nil {
		return
	}
	mixed := make([]int16, len(far))
	for i, v := range far {
		near := int16(0)
		if i < len(m.latestNear) {
			near = m.latestNear[i]
		}
		sum := int32(v) + int32(near)
		if sum > 32767 {
			sum = 32767
		}
		if sum < -32768 {
			sum = -32768
		}
		mixed[i] = int16(sum / 2)
	}
	_, _ = m.recorder.stdin.Write(int16Bytes(mixed))
}
func (m *Manager) release(callID string, generation uint64) {
	m.mu.Lock()
	if m.activeCall == callID && m.generation == generation {
		m.activeCall = ""
		m.cancel = nil
	}
	m.mu.Unlock()
}
func int16Bytes(p []int16) []byte {
	b := make([]byte, len(p)*2)
	for i, v := range p {
		binary.LittleEndian.PutUint16(b[i*2:], uint16(v))
	}
	return b
}

type recorder struct {
	id, callID, name, path string
	kind                   string
	started                time.Time
	cmd                    *exec.Cmd
	stdin                  io.WriteCloser
	cancel                 context.CancelFunc
}
