package modem

import (
	"bufio"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf16"

	"go.bug.st/serial"
)

type Event struct {
	GatewayID      string
	SubscriptionID int
	Type           string
	ClientID       string
	ProviderID     string
	Number         string
	Body           string
	ModemIndex     int
	Reason         string
	Raw            string
}

type Status struct {
	GatewayID           string
	Provider            string
	Transport           string
	SIMReady            bool
	Registered          bool
	VoiceReady          bool
	Operator            string
	AccessTech          string
	Signal              int
	SignalDBm           int
	PhoneNumber         string
	ICCID               string
	IMSI                string
	Manufacturer        string
	Model               string
	IMEI                string
	Firmware            string
	SubVersion          string
	QCN                 string
	VoLTEControl        bool
	ADBState            string
	AndroidVersion      string
	BuildID             string
	SecurityPatch       string
	BasebandVersion     string
	BatteryLevel        int
	BatteryCharging     bool
	SubscriptionID      int
	SIMSlot             int
	IMSRegistered       bool
	VoLTE               bool
	CompanionVersion    string
	ProtocolVersion     int
	AudioDownlinkOK     bool
	AudioUplinkOK       bool
	AudioDownlinkFrames int64
	AudioUplinkFrames   int64
	AudioUplinkBytes    int64
	LastError           string
	LastCheckedAt       time.Time
	Subscriptions       []Subscription
}

type Subscription struct {
	ID          int    `json:"id"`
	SIMSlot     int    `json:"simSlot"`
	DisplayName string `json:"displayName,omitempty"`
	CarrierName string `json:"carrierName,omitempty"`
	PhoneNumber string `json:"phoneNumber,omitempty"`
	IMEI        string `json:"imei,omitempty"`
	Ready       bool   `json:"ready"`
}

type Route struct {
	GatewayID      string `json:"gatewayId,omitempty"`
	SubscriptionID int    `json:"subscriptionId,omitempty"`
}

// RoutedController is implemented by multi-device controllers. The original
// Controller API remains intact for SIM7600 and existing integrations.
type RoutedController interface {
	Controller
	DialRoute(context.Context, Route, string) error
	SendSMSRoute(context.Context, Route, string, string, string) error
	SelectRoute(Route) error
	Statuses(context.Context) []Status
}

type Controller interface {
	Start(context.Context)
	Events() <-chan Event
	Dial(context.Context, string) error
	Answer(context.Context) error
	Hangup(context.Context) error
	DTMF(context.Context, string) error
	SendSMS(context.Context, string, string, string) error
	DeleteSMS(context.Context, int) error
	SetMicMute(context.Context, bool) error
	EnableAudio(context.Context, bool) error
	Healthy() bool
	AudioCapable() bool
	OpenAudio(context.Context) (io.ReadWriteCloser, error)
	Probe(context.Context) Status
}

func New(mode, atPort, audioPort, controlPort string, log *slog.Logger) Controller {
	if mode == "mock" {
		return newMock(log)
	}
	return &ATController{atPath: atPort, audioPath: audioPort, controlPath: controlPort, events: make(chan Event, 128), log: log, lines: make(chan string, 256)}
}

type ATController struct {
	atPath, audioPath, controlPath string
	log                            *slog.Logger
	events                         chan Event
	lines                          chan string
	mu                             sync.RWMutex
	commandMu                      sync.Mutex
	port                           serial.Port
	healthy                        bool
	audio                          bool
	lastClip                       string
}

func (m *ATController) Events() <-chan Event { return m.events }
func (m *ATController) Healthy() bool        { m.mu.RLock(); defer m.mu.RUnlock(); return m.healthy }
func (m *ATController) AudioCapable() bool   { m.mu.RLock(); defer m.mu.RUnlock(); return m.audio }
func (m *ATController) OpenAudio(context.Context) (io.ReadWriteCloser, error) {
	return serial.Open(m.audioPath, &serial.Mode{BaudRate: 921600, DataBits: 8, Parity: serial.NoParity, StopBits: serial.OneStopBit})
}

func (m *ATController) Probe(ctx context.Context) Status {
	out := Status{Provider: "sim7600", Transport: "usb-serial", Signal: -1, SignalDBm: -1, LastCheckedAt: time.Now().UTC()}
	if r, e := m.command(ctx, "AT+CPIN?", 5*time.Second); e == nil {
		out.SIMReady = strings.Contains(r, "READY")
	}
	reg, _ := m.command(ctx, "AT+CEREG?", 5*time.Second)
	out.Registered = registeredResponse(reg)
	if reg, e := m.command(ctx, "AT+CREG?", 5*time.Second); e == nil {
		out.VoiceReady = registeredResponse(reg)
	}
	if r, e := m.command(ctx, "AT+COPS?", 5*time.Second); e == nil {
		out.Operator = quoted(r)
		out.AccessTech = accessTechnology(r)
	}
	if r, e := m.command(ctx, "AT+CSQ", 5*time.Second); e == nil {
		if p := strings.Index(r, ":"); p >= 0 {
			fields := strings.Split(strings.TrimSpace(r[p+1:]), ",")
			if len(fields) > 0 {
				out.Signal, _ = strconv.Atoi(fields[0])
				if out.Signal >= 0 && out.Signal <= 31 {
					out.SignalDBm = -113 + 2*out.Signal
				}
			}
		}
	}
	if r, e := m.command(ctx, "AT+CNUM", 5*time.Second); e == nil {
		out.PhoneNumber = quotedAt(r, 1)
	}
	if r, e := m.command(ctx, "AT+ICCID", 5*time.Second); e == nil {
		out.ICCID = valueAfterColon(r)
	}
	if r, e := m.command(ctx, "AT+CIMI", 5*time.Second); e == nil {
		out.IMSI = firstValueLine(r)
	}
	if r, e := m.command(ctx, "AT+CGMI", 5*time.Second); e == nil {
		out.Manufacturer = firstValueLine(r)
	}
	if r, e := m.command(ctx, "AT+CGMM", 5*time.Second); e == nil {
		out.Model = firstValueLine(r)
	}
	if r, e := m.command(ctx, "AT+CGSN", 5*time.Second); e == nil {
		out.IMEI = firstValueLine(r)
	}
	if r, e := m.command(ctx, "AT+CGMR", 5*time.Second); e == nil {
		out.Firmware = valueAfterColon(r)
	}
	if r, e := m.command(ctx, "AT+CSUB", 5*time.Second); e == nil {
		out.SubVersion = subVersion(r)
	}
	if r, e := m.command(ctx, "AT+CQCNV", 5*time.Second); e == nil {
		out.QCN = quoted(r)
	}
	if _, e := m.command(ctx, "AT+VOLTESETTING?", 5*time.Second); e == nil {
		out.VoLTEControl = true
	}
	return out
}

func (m *ATController) Start(ctx context.Context) {
	go m.connectLoop(ctx)
}

func (m *ATController) connectLoop(ctx context.Context) {
	delay := time.Second
	for ctx.Err() == nil {
		if err := m.connect(ctx); err != nil {
			m.setStatus(false, false)
			m.emit(Event{Type: "device.offline", Reason: err.Error()})
			m.log.Warn("modem unavailable", "error", err, "retry", delay)
			select {
			case <-time.After(delay):
			case <-ctx.Done():
				return
			}
			if delay < 30*time.Second {
				delay *= 2
			}
			continue
		}
		delay = time.Second
	}
}

func (m *ATController) connect(ctx context.Context) error {
	if _, err := os.Stat(m.atPath); err != nil {
		return err
	}
	p, err := serial.Open(m.atPath, &serial.Mode{BaudRate: 115200, DataBits: 8, Parity: serial.NoParity, StopBits: serial.OneStopBit})
	if err != nil {
		return err
	}
	m.mu.Lock()
	m.port = p
	m.mu.Unlock()
	defer func() {
		_ = p.Close()
		m.mu.Lock()
		if m.port == p {
			m.port = nil
		}
		m.mu.Unlock()
	}()
	go m.readLoop(ctx, p)
	// If a previous SMS submission was interrupted while the modem was waiting
	// at its un-terminated "> " prompt, ESC returns it to command mode.
	_, _ = p.Write([]byte{0x1b})
	time.Sleep(100 * time.Millisecond)
	if _, err = m.command(ctx, "AT", 3*time.Second); err != nil {
		return err
	}
	_, _ = m.command(ctx, "ATE0", 3*time.Second)
	_, _ = m.command(ctx, "AT+CMEE=2", 3*time.Second)
	_, _ = m.command(ctx, `AT+CLIP=1`, 3*time.Second)
	if _, volteErr := m.command(ctx, "AT+VOLTESETTING?", 3*time.Second); volteErr == nil {
		_, _ = m.command(ctx, "AT+VOLTESETTING=1", 3*time.Second)
	}
	_, _ = m.command(ctx, `AT+CMGF=1`, 3*time.Second)
	_, _ = m.command(ctx, `AT+CSMS=1`, 3*time.Second)
	_, _ = m.command(ctx, `AT+CSCS="UCS2"`, 3*time.Second)
	// These are the model-specific parameters published by Waveshare for the
	// SIM7600CE-CNSE Chinese SMS flow. Prefer the circuit-switched bearer so
	// LTE SMS can use SGs and the modem can fall back to 3G/2G. GPRS-preferred
	// mode selects the working data bearer even when the operator does not
	// provide SMS over that bearer, which leaves CMGS waiting until timeout.
	_, _ = m.command(ctx, `AT+CSMP=17,167,2,25`, 3*time.Second)
	_, _ = m.command(ctx, `AT+CGSMS=3`, 3*time.Second)
	_, _ = m.command(ctx, `AT+CPMS="SM","SM","SM"`, 5*time.Second)
	_, _ = m.command(ctx, `AT+CNMI=2,1,0,0,0`, 3*time.Second)
	audio := m.probeAudio(ctx)
	m.setStatus(true, audio)
	m.emit(Event{Type: "device.online"})
	go m.healthLoop(ctx)
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		select {
		case <-tick.C:
			if !m.Healthy() {
				return errors.New("modem connection lost")
			}
		case <-ctx.Done():
			_ = p.Close()
			return ctx.Err()
		}
	}
}

func (m *ATController) probeAudio(ctx context.Context) bool {
	if _, err := os.Stat(m.audioPath); err != nil {
		return false
	}
	// The PCM command service becomes ready several seconds after the primary
	// AT port on SIM7600. Treating the first ERROR/timeout as permanent made a
	// healthy USB PCM interface appear unavailable after a modem reset.
	for attempt := 0; attempt < 6; attempt++ {
		if r, err := m.command(ctx, "AT+CPCMREG=?", 5*time.Second); err == nil && strings.Contains(r, "+CPCMREG") {
			_, _ = m.command(ctx, "AT+CPCMFRM=1", 3*time.Second)
			return true
		}
		select {
		case <-time.After(2 * time.Second):
		case <-ctx.Done():
			return false
		}
	}
	return false
}

func (m *ATController) readLoop(ctx context.Context, p io.Reader) {
	reader := bufio.NewReaderSize(p, 64*1024)
	var pending strings.Builder
	dispatch := func(line string) bool {
		line = strings.TrimSpace(line)
		if line == "" {
			return true
		}
		m.handleURC(ctx, line)
		select {
		case m.lines <- line:
			return true
		case <-ctx.Done():
			return false
		}
	}
	for {
		b, err := reader.ReadByte()
		if err != nil {
			break
		}
		if b == '>' && strings.TrimSpace(pending.String()) == "" {
			pending.Reset()
			if !dispatch(">") {
				return
			}
			continue
		}
		if b == '\r' || b == '\n' {
			if !dispatch(pending.String()) {
				return
			}
			pending.Reset()
			continue
		}
		if pending.Len() < 64*1024 {
			pending.WriteByte(b)
		} else {
			pending.Reset()
		}
	}
	m.setStatus(false, false)
	m.emit(Event{Type: "device.offline", Reason: "serial port closed"})
}

func (m *ATController) handleURC(ctx context.Context, line string) {
	switch {
	case strings.HasPrefix(line, "+CLIP:"):
		if q := quoted(line); q != "" {
			m.lastClip = normalizeDecoded(q)
			m.emit(Event{Type: "call.incoming", Number: m.lastClip, Raw: line})
		}
	case line == "RING" || strings.HasPrefix(line, "+CRING:"):
		m.emit(Event{Type: "call.incoming", Number: m.lastClip, Raw: line})
	case strings.Contains(line, "VOICE CALL: BEGIN"):
		m.emit(Event{Type: "call.active", Number: m.lastClip, Raw: line})
	case strings.Contains(line, "VOICE CALL: END") || line == "NO CARRIER":
		m.emit(Event{Type: "call.ended", Raw: line})
	case strings.HasPrefix(line, "+CMTI:"):
		parts := strings.Split(line, ",")
		if len(parts) == 2 {
			idx, _ := strconv.Atoi(strings.TrimSpace(parts[1]))
			go m.readSMS(ctx, idx)
		}
	}
}

func (m *ATController) healthLoop(ctx context.Context) {
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			if _, err := m.command(ctx, "AT", 5*time.Second); err != nil {
				m.setStatus(false, false)
				m.mu.Lock()
				if m.port != nil {
					_ = m.port.Close()
				}
				m.mu.Unlock()
				return
			}
		case <-ctx.Done():
			return
		}
	}
}

func (m *ATController) command(ctx context.Context, command string, timeout time.Duration) (string, error) {
	m.commandMu.Lock()
	defer m.commandMu.Unlock()
	m.mu.RLock()
	p := m.port
	m.mu.RUnlock()
	if p == nil {
		return "", errors.New("MODEM_OFFLINE")
	}
	for len(m.lines) > 0 {
		<-m.lines
	}
	if _, err := p.Write([]byte(command + "\r")); err != nil {
		return "", err
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	var out []string
	for {
		select {
		case line := <-m.lines:
			if line == command {
				continue
			}
			if line == "OK" {
				return strings.Join(out, "\n"), nil
			}
			if line == "ERROR" || strings.HasPrefix(line, "+CME ERROR") || strings.HasPrefix(line, "+CMS ERROR") {
				return strings.Join(out, "\n"), errors.New(line)
			}
			out = append(out, line)
		case <-timer.C:
			return strings.Join(out, "\n"), errors.New("MODEM_TIMEOUT")
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
}

func (m *ATController) Dial(ctx context.Context, number string) error {
	_, err := m.command(ctx, "ATD"+digits(number)+";", 2*time.Minute)
	return err
}
func (m *ATController) Answer(ctx context.Context) error {
	_, err := m.command(ctx, "ATA", 2*time.Minute)
	return err
}
func (m *ATController) Hangup(ctx context.Context) error {
	_, err := m.command(ctx, "AT+CHUP", 2*time.Minute)
	return err
}
func (m *ATController) DTMF(ctx context.Context, key string) error {
	if len(key) != 1 || !strings.Contains("0123456789*#ABCD", key) {
		return errors.New("INVALID_DTMF")
	}
	_, err := m.command(ctx, "AT+VTS="+key, 5*time.Second)
	return err
}
func (m *ATController) SetMicMute(ctx context.Context, muted bool) error {
	v := 0
	if muted {
		v = 1
	}
	_, err := m.command(ctx, fmt.Sprintf("AT+CMUT=%d", v), 5*time.Second)
	return err
}
func (m *ATController) EnableAudio(ctx context.Context, enabled bool) error {
	v := "0,1"
	if enabled {
		v = "1"
	}
	_, err := m.command(ctx, "AT+CPCMREG="+v, 5*time.Second)
	return err
}
func (m *ATController) SendSMS(ctx context.Context, _, number, body string) error {
	// SIM7600 text mode with UCS2 is substantially more reliable for Chinese text than
	// terminal-locale conversions. Long messages are split on rune boundaries.
	chunks := splitRunes(body, 67)
	for _, chunk := range chunks {
		if err := m.sendUCS2(ctx, number, chunk); err != nil {
			return err
		}
	}
	return nil
}
func (m *ATController) sendUCS2(ctx context.Context, number, body string) error {
	m.commandMu.Lock()
	defer m.commandMu.Unlock()
	m.mu.RLock()
	p := m.port
	m.mu.RUnlock()
	if p == nil {
		return errors.New("MODEM_OFFLINE")
	}
	for len(m.lines) > 0 {
		<-m.lines
	}
	cmd := fmt.Sprintf("AT+CMGS=\"%s\"\r", encodeUCS2(number))
	if _, err := p.Write([]byte(cmd)); err != nil {
		return err
	}
	promptTimer := time.NewTimer(10 * time.Second)
	for {
		select {
		case line := <-m.lines:
			if strings.Contains(line, ">") {
				if !promptTimer.Stop() {
					select {
					case <-promptTimer.C:
					default:
					}
				}
				if _, err := p.Write(append([]byte(encodeUCS2(body)), 0x1a)); err != nil {
					return err
				}
				goto result
			}
			if strings.Contains(line, "ERROR") {
				promptTimer.Stop()
				return errors.New(line)
			}
		case <-promptTimer.C:
			_, _ = p.Write([]byte{0x1b})
			return errors.New("SMS_PROMPT_TIMEOUT")
		case <-ctx.Done():
			promptTimer.Stop()
			_, _ = p.Write([]byte{0x1b})
			return ctx.Err()
		}
	}
result:
	resultTimer := time.NewTimer(2 * time.Minute)
	defer resultTimer.Stop()
	for {
		select {
		case line := <-m.lines:
			if line == "OK" {
				return nil
			}
			if strings.Contains(line, "ERROR") {
				return errors.New(line)
			}
		case <-resultTimer.C:
			_, _ = p.Write([]byte{0x1b})
			m.hardReset("SMS submission timed out")
			return errors.New("SMS_SEND_TIMEOUT")
		case <-ctx.Done():
			_, _ = p.Write([]byte{0x1b})
			return ctx.Err()
		}
	}
}

// hardReset uses the module's independent modem/control USB function. A failed
// network SMS submission can leave the primary AT function inside CMGS even
// after ESC; resetting through ttyUSB3 is then the only unattended recovery.
func (m *ATController) hardReset(reason string) {
	if m.controlPath == "" {
		return
	}
	p, err := serial.Open(m.controlPath, &serial.Mode{BaudRate: 115200, DataBits: 8, Parity: serial.NoParity, StopBits: serial.OneStopBit})
	if err != nil {
		m.log.Error("open modem recovery port", "error", err, "reason", reason)
		return
	}
	if _, err = p.Write([]byte("AT+CFUN=1,1\r")); err != nil {
		m.log.Error("reset modem through recovery port", "error", err, "reason", reason)
		_ = p.Close()
		return
	}
	_ = p.Close()
	m.log.Warn("reset modem through recovery port", "reason", reason)
	m.setStatus(false, false)
	m.mu.Lock()
	if m.port != nil {
		_ = m.port.Close()
	}
	m.mu.Unlock()
}
func (m *ATController) DeleteSMS(ctx context.Context, index int) error {
	if index <= 0 {
		return nil
	}
	_, err := m.command(ctx, fmt.Sprintf("AT+CMGD=%d", index), 10*time.Second)
	return err
}

func (m *ATController) readSMS(ctx context.Context, idx int) {
	out, err := m.command(ctx, fmt.Sprintf("AT+CMGR=%d", idx), 15*time.Second)
	if err != nil {
		m.emit(Event{Type: "sms.error", ModemIndex: idx, Reason: err.Error()})
		return
	}
	lines := strings.Split(out, "\n")
	if len(lines) < 2 {
		return
	}
	number := quoted(lines[0])
	body := lines[len(lines)-1]
	m.emit(Event{Type: "sms.received", Number: normalizeDecoded(number), Body: normalizeDecoded(body), ModemIndex: idx})
}

func (m *ATController) setStatus(healthy, audio bool) {
	m.mu.Lock()
	m.healthy, m.audio = healthy, audio
	m.mu.Unlock()
}
func (m *ATController) emit(e Event) {
	select {
	case m.events <- e:
	default:
		m.log.Error("modem event overflow", "event", e.Type)
	}
}
func quoted(s string) string {
	start := strings.IndexByte(s, '"')
	if start < 0 {
		return ""
	}
	end := strings.IndexByte(s[start+1:], '"')
	if end < 0 {
		return ""
	}
	return s[start+1 : start+1+end]
}
func quotedAt(s string, index int) string {
	for i := 0; i <= index; i++ {
		start := strings.IndexByte(s, '"')
		if start < 0 {
			return ""
		}
		s = s[start+1:]
		end := strings.IndexByte(s, '"')
		if end < 0 {
			return ""
		}
		if i == index {
			return s[:end]
		}
		s = s[end+1:]
	}
	return ""
}
func valueAfterColon(s string) string {
	if p := strings.IndexByte(s, ':'); p >= 0 {
		s = s[p+1:]
	}
	return strings.Trim(strings.TrimSpace(strings.Split(s, "\n")[0]), `"`)
}
func firstValueLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "AT+") {
			return strings.Trim(line, `"`)
		}
	}
	return ""
}
func subVersion(s string) string {
	var values []string
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "+CSUB:"))
		if line != "" {
			values = append(values, line)
		}
	}
	return strings.Join(values, " · ")
}
func accessTechnology(s string) string {
	line := strings.TrimSpace(strings.Split(s, "\n")[0])
	fields := strings.Split(line, ",")
	if len(fields) < 4 {
		return ""
	}
	code, err := strconv.Atoi(strings.TrimSpace(fields[len(fields)-1]))
	if err != nil {
		return ""
	}
	switch code {
	case 0:
		return "GSM"
	case 1:
		return "GSM Compact"
	case 2:
		return "UTRAN"
	case 3:
		return "GSM/EDGE"
	case 4:
		return "UTRAN/HSDPA"
	case 5:
		return "UTRAN/HSUPA"
	case 6:
		return "UTRAN/HSPA"
	case 7:
		return "LTE"
	case 8:
		return "EC-GSM-IoT"
	case 9:
		return "NB-IoT"
	default:
		return "Unknown"
	}
}
func registeredResponse(s string) bool {
	return strings.Contains(s, ",1") || strings.Contains(s, ",5")
}
func digits(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' || r == '+' || r == '*' || r == '#' {
			b.WriteRune(r)
		}
	}
	return b.String()
}
func encodeUCS2(s string) string {
	units := utf16.Encode([]rune(s))
	b := make([]byte, len(units)*2)
	for i, u := range units {
		b[i*2] = byte(u >> 8)
		b[i*2+1] = byte(u)
	}
	return strings.ToUpper(hex.EncodeToString(b))
}
func normalizeDecoded(s string) string {
	if len(s)%4 != 0 {
		return s
	}
	b, err := hex.DecodeString(s)
	if err != nil {
		return s
	}
	units := make([]uint16, len(b)/2)
	for i := range units {
		units[i] = uint16(b[2*i])<<8 | uint16(b[2*i+1])
	}
	decoded := string(utf16.Decode(units))
	for _, r := range decoded {
		if r < 0x20 && r != '\n' && r != '\r' {
			return s
		}
	}
	return decoded
}
func splitRunes(s string, n int) []string {
	r := []rune(s)
	if len(r) == 0 {
		return []string{""}
	}
	out := []string{}
	for len(r) > 0 {
		size := n
		if len(r) < size {
			size = len(r)
		}
		out = append(out, string(r[:size]))
		r = r[size:]
	}
	return out
}
