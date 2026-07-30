package media

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"sync"
	"time"

	"github.com/hraban/opus"
	"github.com/pion/webrtc/v4"
	pionmedia "github.com/pion/webrtc/v4/pkg/media"

	"onsim/internal/domain"
)

const (
	webFrameSamples  = 320
	webFrameDuration = 20 * time.Millisecond
)

// MediaStatus describes the end-to-end browser media path. Connected is only
// reported after decoded browser PCM has been written to the persistent Android
// audio socket, rather than merely after ICE reaches connected.
type MediaStatus struct {
	State            string     `json:"state"`
	Error            string     `json:"error,omitempty"`
	SessionStartedAt time.Time  `json:"sessionStartedAt"`
	AudioOpenedAt    *time.Time `json:"audioOpenedAt,omitempty"`
	OfferReceivedAt  *time.Time `json:"offerReceivedAt,omitempty"`
	PeerConnectedAt  *time.Time `json:"peerConnectedAt,omitempty"`
	FirstRTPAt       *time.Time `json:"firstRtpAt,omitempty"`
	FirstPCMAt       *time.Time `json:"firstPcmAt,omitempty"`
	LastDisconnectAt *time.Time `json:"lastDisconnectAt,omitempty"`
}

type callAudioSession struct {
	manager  *Manager
	callID   string
	ctx      context.Context
	cancel   context.CancelFunc
	ready    chan struct{}
	done     chan struct{}
	mic      chan []int16
	firstPCM chan struct{}

	mu           sync.Mutex
	port         io.ReadWriteCloser
	startErr     error
	peerID       uint64
	peerCancel   context.CancelFunc
	out          *webrtc.TrackLocalStaticSample
	bridged      bool
	everBridged  bool
	held         bool
	fadeFrames   int
	firstPCMOnce sync.Once
	deadlineGen  uint64
	statusValue  MediaStatus
}

func (m *Manager) ensureWebSession(ctx context.Context, callID string) (*callAudioSession, error) {
	m.mu.Lock()
	if existing := m.webSessions[callID]; existing != nil {
		m.mu.Unlock()
		return existing, existing.waitReady(ctx)
	}
	if m.activeCall != "" && m.activeCall != callID {
		m.mu.Unlock()
		return nil, errors.New("MEDIA_BUSY")
	}
	if m.cancel != nil {
		m.cancel()
	}
	sessionCtx, cancel := context.WithCancel(context.Background())
	session := &callAudioSession{
		manager: m, callID: callID, ctx: sessionCtx, cancel: cancel,
		ready: make(chan struct{}), done: make(chan struct{}),
		mic: make(chan []int16, 100), firstPCM: make(chan struct{}),
		statusValue: MediaStatus{State: "waiting_for_permission", SessionStartedAt: time.Now().UTC()},
	}
	m.generation++
	generation := m.generation
	m.cancel = cancel
	m.activeCall = callID
	m.webSessions[callID] = session
	m.mu.Unlock()
	go session.run(generation)
	return session, session.waitReady(ctx)
}

func (s *callAudioSession) waitReady(ctx context.Context) error {
	select {
	case <-s.ready:
		s.mu.Lock()
		err := s.startErr
		s.mu.Unlock()
		if err != nil {
			return errors.New("MEDIA_UNAVAILABLE")
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *callAudioSession) run(generation uint64) {
	defer close(s.done)
	defer func() {
		s.manager.mu.Lock()
		if s.manager.webSessions[s.callID] == s {
			delete(s.manager.webSessions, s.callID)
		}
		s.manager.mu.Unlock()
		s.manager.release(s.callID, generation)
	}()
	if err := s.waitForActiveCall(); err != nil {
		s.failStart(err)
		return
	}
	if err := s.manager.selectCallRoute(s.callID); err != nil {
		s.failStart(err)
		return
	}
	if err := s.manager.audio.EnableAudio(s.ctx, true); err != nil {
		s.failStart(err)
		return
	}
	port, err := s.manager.audio.OpenAudio(s.ctx)
	if err != nil {
		s.failStart(err)
		return
	}
	now := time.Now().UTC()
	s.mu.Lock()
	s.port = port
	s.statusValue.AudioOpenedAt = &now
	s.mu.Unlock()
	close(s.ready)
	go func() {
		<-s.ctx.Done()
		_ = port.Close()
	}()

	errs := make(chan error, 2)
	go func() { errs <- s.writeUplink(port) }()
	go func() { errs <- s.readDownlink(port) }()
	select {
	case <-s.ctx.Done():
	case err = <-errs:
		if err != nil && s.ctx.Err() == nil {
			s.manager.log.Error("persistent Android audio session stopped", "call_id", s.callID, "error", err)
			s.setError("ANDROID_AUDIO_DISCONNECTED")
		}
		s.cancel()
	}
	_ = port.Close()
}

func (s *callAudioSession) waitForActiveCall() error {
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		call := s.manager.state.Call(s.callID)
		if call == nil || call.State == domain.CallEnded || call.State == domain.CallFailed {
			return errors.New("CALL_STATE_CONFLICT")
		}
		if call.State == domain.CallActive {
			return nil
		}
		select {
		case <-s.ctx.Done():
			return s.ctx.Err()
		case <-ticker.C:
		}
	}
}

func (s *callAudioSession) failStart(err error) {
	s.mu.Lock()
	s.startErr = err
	s.statusValue.State = "unavailable"
	s.statusValue.Error = err.Error()
	s.mu.Unlock()
	close(s.ready)
}

func (s *callAudioSession) attachPeer(out *webrtc.TrackLocalStaticSample, cancel context.CancelFunc) uint64 {
	now := time.Now().UTC()
	s.mu.Lock()
	previous := s.peerCancel
	s.peerID++
	id := s.peerID
	s.peerCancel = cancel
	s.out = out
	s.bridged = false
	s.fadeFrames = 0
	s.statusValue.State = "negotiating"
	s.statusValue.Error = ""
	s.statusValue.OfferReceivedAt = &now
	s.mu.Unlock()
	if previous != nil {
		previous()
	}
	s.drainMic()
	return id
}

func (s *callAudioSession) detachPeer(id uint64) {
	s.mu.Lock()
	if s.peerID == id {
		s.out = nil
		s.peerCancel = nil
		s.bridged = false
		if s.statusValue.State != "timeout" {
			s.statusValue.State = "recovering"
		}
	}
	s.mu.Unlock()
	s.drainMic()
}

func (s *callAudioSession) markNegotiating(id uint64) {
	now := time.Now().UTC()
	s.mu.Lock()
	if s.peerID == id {
		s.statusValue.PeerConnectedAt = &now
		if !s.bridged {
			s.statusValue.State = "negotiating"
		}
	}
	s.mu.Unlock()
}

func (s *callAudioSession) markRecovering(id uint64) {
	now := time.Now().UTC()
	s.mu.Lock()
	if s.peerID == id {
		s.bridged = false
		s.fadeFrames = 0
		s.statusValue.State = "recovering"
		s.statusValue.LastDisconnectAt = &now
	}
	s.mu.Unlock()
	s.drainMic()
}

func (s *callAudioSession) enqueueMic(id uint64, frame []int16) bool {
	now := time.Now().UTC()
	s.mu.Lock()
	if s.peerID != id {
		s.mu.Unlock()
		return false
	}
	if s.held {
		s.mu.Unlock()
		return true
	}
	first := !s.bridged
	s.mu.Unlock()
	copyFrame := append([]int16(nil), frame...)
	select {
	case s.mic <- copyFrame:
	default:
		select {
		case <-s.mic:
		default:
		}
		select {
		case s.mic <- copyFrame:
		default:
		}
	}
	if first {
		s.mu.Lock()
		if s.peerID != id {
			s.mu.Unlock()
			return false
		}
		s.bridged = true
		s.fadeFrames = 5
		s.statusValue.FirstRTPAt = &now
		offerAt := s.statusValue.OfferReceivedAt
		s.mu.Unlock()
		s.manager.log.Info("first browser microphone frame received",
			"call_id", s.callID, "peer_id", id,
			"offer_to_rtp_ms", elapsedMillis(offerAt, now))
	}
	return true
}

func (s *callAudioSession) writeUplink(port io.Writer) error {
	ticker := time.NewTicker(webFrameDuration)
	defer ticker.Stop()
	tone := waitingTonePCM()
	toneOffset := 0
	firstPCMLogged := false
	for {
		select {
		case <-s.ctx.Done():
			return s.ctx.Err()
		case <-ticker.C:
		}
		toneFrame := make([]int16, webFrameSamples)
		for i := range toneFrame {
			toneFrame[i] = tone[toneOffset]
			toneOffset = (toneOffset + 1) % len(tone)
		}
		s.mu.Lock()
		held := s.held
		bridged := s.bridged && !held
		fade := s.fadeFrames
		if fade > 0 {
			s.fadeFrames--
		}
		s.mu.Unlock()
		frame := toneFrame
		gotMic := false
		if bridged {
			frame = make([]int16, webFrameSamples)
			select {
			case mic := <-s.mic:
				copy(frame, mic)
				gotMic = true
			default:
			}
			if gotMic && fade > 0 {
				micWeight := float64(6-fade) / 5
				for i := range frame {
					frame[i] = clampPCM(float64(frame[i])*micWeight + float64(toneFrame[i])*(1-micWeight))
				}
			}
		}
		if _, err := port.Write(int16Bytes(frame)); err != nil {
			return err
		}
		if bridged && gotMic {
			now := time.Now().UTC()
			s.mu.Lock()
			firstBridge := !s.everBridged
			s.everBridged = true
			s.statusValue.State = "bridged"
			if s.statusValue.FirstPCMAt == nil {
				s.statusValue.FirstPCMAt = &now
			}
			firstRTP := s.statusValue.FirstRTPAt
			s.mu.Unlock()
			if firstBridge && !firstPCMLogged {
				firstPCMLogged = true
				s.firstPCMOnce.Do(func() { close(s.firstPCM) })
				s.manager.log.Info("first browser PCM written to Telephony Tx",
					"call_id", s.callID, "rtp_to_pcm_ms", elapsedMillis(firstRTP, now))
			}
		}
	}
}

func (s *callAudioSession) readDownlink(port io.Reader) error {
	encoder, err := opus.NewEncoder(16000, 1, opus.AppVoIP)
	if err != nil {
		return err
	}
	raw := make([]byte, webFrameSamples*2)
	pcm := make([]int16, webFrameSamples)
	encoded := make([]byte, 4000)
	for {
		if _, err = io.ReadFull(port, raw); err != nil {
			return err
		}
		for i := range pcm {
			pcm[i] = int16(binary.LittleEndian.Uint16(raw[i*2:]))
		}
		s.mu.Lock()
		out := s.out
		peerID := s.peerID
		held := s.held
		s.mu.Unlock()
		if out != nil && !held {
			n, encodeErr := encoder.Encode(pcm, encoded)
			if encodeErr != nil {
				return encodeErr
			}
			if writeErr := out.WriteSample(pionmedia.Sample{
				Data: append([]byte(nil), encoded[:n]...), Duration: webFrameDuration,
			}); writeErr != nil {
				s.manager.log.Warn("browser downlink track unavailable",
					"call_id", s.callID, "peer_id", peerID, "error", writeErr)
				s.markRecovering(peerID)
			}
		}
		if !held {
			s.manager.writeRecording(pcm)
		}
		select {
		case <-s.ctx.Done():
			return s.ctx.Err()
		default:
		}
	}
}

func (s *callAudioSession) startAnswerDeadline(timeout time.Duration) {
	s.mu.Lock()
	s.deadlineGen++
	generation := s.deadlineGen
	s.mu.Unlock()
	go func() {
		timer := time.NewTimer(timeout)
		defer timer.Stop()
		select {
		case <-s.firstPCM:
			return
		case <-s.ctx.Done():
			return
		case <-timer.C:
		}
		call := s.manager.state.Call(s.callID)
		if call == nil || call.State != domain.CallActive || call.MediaOwner != "web" || call.Held {
			return
		}
		s.mu.Lock()
		if generation != s.deadlineGen || s.everBridged {
			s.mu.Unlock()
			return
		}
		s.statusValue.State = "timeout"
		s.statusValue.Error = "WEB_MEDIA_TIMEOUT"
		s.mu.Unlock()
		s.manager.log.Warn("browser media timed out after answer", "call_id", s.callID, "timeout", timeout)
		s.manager.mu.Lock()
		handler := s.manager.mediaTimeout
		s.manager.mu.Unlock()
		if handler != nil {
			handler(s.callID)
		}
	}()
}

func (s *callAudioSession) setHeld(held bool) {
	s.mu.Lock()
	s.held = held
	if held {
		s.deadlineGen++
		s.fadeFrames = 0
	} else if s.bridged {
		s.fadeFrames = 5
	}
	s.mu.Unlock()
	if held {
		s.drainMic()
	}
}

func (s *callAudioSession) status() MediaStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.statusValue
}

func (s *callAudioSession) setError(code string) {
	s.mu.Lock()
	s.statusValue.State = "unavailable"
	s.statusValue.Error = code
	s.mu.Unlock()
}

func (s *callAudioSession) drainMic() {
	for {
		select {
		case <-s.mic:
		default:
			return
		}
	}
}

func (m *Manager) browserToSession(ctx context.Context, track *webrtc.TrackRemote, session *callAudioSession, peerID uint64) error {
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
		for offset := 0; offset < len(pcm16); offset += webFrameSamples {
			frame := make([]int16, webFrameSamples)
			end := offset + webFrameSamples
			if end > len(pcm16) {
				end = len(pcm16)
			}
			copy(frame, pcm16[offset:end])
			if !session.enqueueMic(peerID, frame) {
				return context.Canceled
			}
		}
		session.managerLatestNear(pcm16)
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
	}
}

func (s *callAudioSession) managerLatestNear(pcm []int16) {
	s.manager.mu.Lock()
	s.manager.latestNear = append(s.manager.latestNear[:0], pcm...)
	s.manager.mu.Unlock()
}

func elapsedMillis(start *time.Time, end time.Time) int64 {
	if start == nil {
		return 0
	}
	return end.Sub(*start).Milliseconds()
}

func clampPCM(value float64) int16 {
	if value > 32767 {
		return 32767
	}
	if value < -32768 {
		return -32768
	}
	return int16(value)
}
