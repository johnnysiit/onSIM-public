package store

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	_ "github.com/mattn/go-sqlite3"

	"onsim/internal/domain"
)

const schema = `
PRAGMA journal_mode=WAL;
PRAGMA foreign_keys=ON;
PRAGMA synchronous=FULL;
PRAGMA busy_timeout=5000;
CREATE TABLE IF NOT EXISTS schema_version(version INTEGER NOT NULL);
INSERT INTO schema_version(version) SELECT 1 WHERE NOT EXISTS(SELECT 1 FROM schema_version);
CREATE TABLE IF NOT EXISTS events(
 sequence INTEGER PRIMARY KEY AUTOINCREMENT,
 type TEXT NOT NULL,
 entity_id TEXT NOT NULL,
 version INTEGER NOT NULL,
 created_at TEXT NOT NULL,
 payload BLOB NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS events_entity_version ON events(entity_id, version);
CREATE TABLE IF NOT EXISTS users(
 id INTEGER PRIMARY KEY CHECK(id=1),
 password_hash TEXT NOT NULL,
 created_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS sessions(
 token_hash TEXT PRIMARY KEY,
 expires_at TEXT NOT NULL,
 created_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS idempotency(
 key TEXT PRIMARY KEY,
 response_code INTEGER NOT NULL,
 response BLOB NOT NULL,
 expires_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS action_tokens(
 token_hash TEXT PRIMARY KEY,
 kind TEXT NOT NULL,
 entity_id TEXT NOT NULL,
 expires_at TEXT NOT NULL,
 used_at TEXT
);
CREATE TABLE IF NOT EXISTS telegram_state(
 id INTEGER PRIMARY KEY CHECK(id=1),
 update_offset INTEGER NOT NULL DEFAULT 0
);
INSERT OR IGNORE INTO telegram_state(id, update_offset) VALUES(1,0);
CREATE TABLE IF NOT EXISTS telegram_call_messages(
 call_id TEXT PRIMARY KEY,
 chat_id INTEGER NOT NULL,
 message_id INTEGER NOT NULL,
 action_token TEXT NOT NULL,
 updated_at TEXT NOT NULL
);
`

type State struct {
	db *sql.DB
	mu sync.RWMutex

	sequence      int64
	device        domain.DeviceStatus
	activeCallID  string
	calls         map[string]*domain.Call
	messages      map[string]*domain.Message
	conversations map[string]*domain.Conversation
	rules         map[string]*domain.FilterRule
	recordings    map[string]*domain.Recording
	settings      domain.Settings
	subscribers   map[chan domain.Event]struct{}
	secrets       cipher.AEAD
}

func Open(path string) (*State, error) {
	return OpenWithKey(path, filepath.Join(filepath.Dir(path), "master.key"))
}

func OpenWithKey(path, keyPath string) (*State, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite3", path+"?_foreign_keys=on&_busy_timeout=5000")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err = db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate database: %w", err)
	}
	s := &State{
		db:    db,
		calls: map[string]*domain.Call{}, messages: map[string]*domain.Message{},
		conversations: map[string]*domain.Conversation{}, rules: map[string]*domain.FilterRule{},
		recordings: map[string]*domain.Recording{}, subscribers: map[chan domain.Event]struct{}{},
		settings: domain.Settings{SMS: true, Calls: true, Country: "CN", AutoBlock: []string{"fraud", "insurance"}, VoicemailTimeout: 30},
		device:   domain.DeviceStatus{Mode: "starting", Signal: -1, SignalDBm: -1, LastCheckedAt: time.Now()},
	}
	if err = os.MkdirAll(filepath.Dir(keyPath), 0o750); err != nil {
		db.Close()
		return nil, err
	}
	if s.secrets, err = loadSecretCipher(keyPath); err != nil {
		db.Close()
		return nil, fmt.Errorf("load master key: %w", err)
	}
	if err = s.replay(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *State) Close() error { return s.db.Close() }
func (s *State) DB() *sql.DB  { return s.db }

func (s *State) replay() error {
	rows, err := s.db.Query(`SELECT sequence,type,entity_id,version,created_at,payload FROM events ORDER BY sequence`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var e domain.Event
		var ts string
		if err := rows.Scan(&e.Sequence, &e.Type, &e.EntityID, &e.Version, &ts, &e.Raw); err != nil {
			return err
		}
		e.Timestamp, _ = time.Parse(time.RFC3339Nano, ts)
		if e.Type == "settings.updated" {
			e.Raw, err = s.decryptSettings(e.Raw)
			if err != nil {
				return fmt.Errorf("decrypt settings: %w", err)
			}
		}
		if err := s.apply(e); err != nil {
			return fmt.Errorf("replay event %d: %w", e.Sequence, err)
		}
		s.sequence = e.Sequence
	}
	return rows.Err()
}

func (s *State) appendLocked(eventType, id string, version int64, payload any) (domain.Event, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return domain.Event{}, err
	}
	now := time.Now().UTC()
	storedRaw := raw
	if eventType == "settings.updated" {
		storedRaw, err = s.encryptSettings(raw)
		if err != nil {
			return domain.Event{}, err
		}
	}
	res, err := s.db.Exec(`INSERT INTO events(type,entity_id,version,created_at,payload) VALUES(?,?,?,?,?)`,
		eventType, id, version, now.Format(time.RFC3339Nano), storedRaw)
	if err != nil {
		return domain.Event{}, err
	}
	seq, _ := res.LastInsertId()
	e := domain.Event{Sequence: seq, Type: eventType, EntityID: id, Version: version, Timestamp: now, Payload: payload, Raw: raw}
	if err := s.apply(e); err != nil {
		return domain.Event{}, err
	}
	s.sequence = seq
	if eventType == "settings.updated" {
		safe := s.settings
		safe.TelegramToken = ""
		safe.ProviderAPIKey = ""
		safe.SIPPassword = ""
		e.Payload = safe
	} else if eventType == "recording.upserted" {
		if recording := s.recordings[id]; recording != nil {
			safe := *recording
			safe.Path = ""
			e.Payload = safe
		}
	}
	for ch := range s.subscribers {
		select {
		case ch <- e:
		default:
		}
	}
	return e, nil
}

func loadSecretCipher(path string) (cipher.AEAD, error) {
	key, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		key = make([]byte, 32)
		if _, err = rand.Read(key); err != nil {
			return nil, err
		}
		if err = os.WriteFile(path, key, 0o600); err != nil {
			return nil, err
		}
	} else if err != nil {
		return nil, err
	}
	if len(key) != 32 {
		return nil, errors.New("master key must be 32 bytes")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func (s *State) seal(value string) string {
	if value == "" {
		return ""
	}
	nonce := make([]byte, s.secrets.NonceSize())
	_, _ = rand.Read(nonce)
	out := s.secrets.Seal(nonce, nonce, []byte(value), nil)
	return "enc:" + base64.RawStdEncoding.EncodeToString(out)
}

func (s *State) open(value string) (string, error) {
	if value == "" || !strings.HasPrefix(value, "enc:") {
		return value, nil
	}
	raw, err := base64.RawStdEncoding.DecodeString(strings.TrimPrefix(value, "enc:"))
	if err != nil {
		return "", err
	}
	n := s.secrets.NonceSize()
	if len(raw) < n {
		return "", errors.New("invalid encrypted value")
	}
	plain, err := s.secrets.Open(nil, raw[:n], raw[n:], nil)
	return string(plain), err
}

func (s *State) encryptSettings(raw []byte) ([]byte, error) {
	var v domain.Settings
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, err
	}
	v.TelegramToken = s.seal(v.TelegramToken)
	v.ProviderAPIKey = s.seal(v.ProviderAPIKey)
	v.SIPPassword = s.seal(v.SIPPassword)
	return json.Marshal(v)
}

func (s *State) decryptSettings(raw []byte) ([]byte, error) {
	var v domain.Settings
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, err
	}
	var err error
	if v.TelegramToken, err = s.open(v.TelegramToken); err != nil {
		return nil, err
	}
	if v.ProviderAPIKey, err = s.open(v.ProviderAPIKey); err != nil {
		return nil, err
	}
	if v.SIPPassword, err = s.open(v.SIPPassword); err != nil {
		return nil, err
	}
	return json.Marshal(v)
}

func (s *State) apply(e domain.Event) error {
	raw := e.Raw
	if len(raw) == 0 {
		raw, _ = json.Marshal(e.Payload)
	}
	switch e.Type {
	case "device.updated":
		return json.Unmarshal(raw, &s.device)
	case "call.upserted":
		var v domain.Call
		if err := json.Unmarshal(raw, &v); err != nil {
			return err
		}
		s.calls[v.ID] = &v
		if v.State == domain.CallIncoming || v.State == domain.CallDialing || v.State == domain.CallAlerting || v.State == domain.CallActive || v.State == domain.CallEnding {
			s.activeCallID = v.ID
		} else if s.activeCallID == v.ID {
			s.activeCallID = ""
		}
	case "message.upserted":
		var v domain.Message
		if err := json.Unmarshal(raw, &v); err != nil {
			return err
		}
		s.messages[v.ID] = &v
		s.rebuildConversation(v.Number)
	case "rule.upserted":
		var v domain.FilterRule
		if err := json.Unmarshal(raw, &v); err != nil {
			return err
		}
		s.rules[v.ID] = &v
	case "rule.deleted":
		delete(s.rules, e.EntityID)
	case "recording.upserted":
		var stored struct {
			domain.Recording
			StoragePath string `json:"storagePath"`
		}
		if err := json.Unmarshal(raw, &stored); err != nil {
			return err
		}
		v := stored.Recording
		v.Path = stored.StoragePath
		s.recordings[v.ID] = &v
	case "recording.deleted":
		delete(s.recordings, e.EntityID)
	case "settings.updated":
		return json.Unmarshal(raw, &s.settings)
	default:
		return fmt.Errorf("unknown event type %q", e.Type)
	}
	return nil
}

func (s *State) Snapshot(initialized bool) domain.Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := domain.Snapshot{
		Sequence:      s.sequence,
		Initialized:   initialized,
		Device:        s.device,
		Calls:         make([]*domain.Call, 0, len(s.calls)),
		Messages:      make([]*domain.Message, 0, len(s.messages)),
		Conversations: make([]*domain.Conversation, 0, len(s.conversations)),
		Rules:         make([]*domain.FilterRule, 0, len(s.rules)),
		Recordings:    make([]*domain.Recording, 0, len(s.recordings)),
		Settings:      s.settings,
	}
	if c := s.calls[s.activeCallID]; c != nil {
		v := *c
		out.ActiveCall = &v
	}
	for _, v := range s.calls {
		x := *v
		out.Calls = append(out.Calls, &x)
	}
	for _, v := range s.messages {
		x := *v
		out.Messages = append(out.Messages, &x)
	}
	for _, v := range s.conversations {
		x := *v
		out.Conversations = append(out.Conversations, &x)
	}
	for _, v := range s.rules {
		x := *v
		out.Rules = append(out.Rules, &x)
	}
	for _, v := range s.recordings {
		x := *v
		x.Path = ""
		out.Recordings = append(out.Recordings, &x)
	}
	sort.Slice(out.Calls, func(i, j int) bool { return out.Calls[i].StartedAt.After(out.Calls[j].StartedAt) })
	sort.Slice(out.Messages, func(i, j int) bool { return out.Messages[i].CreatedAt.Before(out.Messages[j].CreatedAt) })
	sort.Slice(out.Conversations, func(i, j int) bool { return out.Conversations[i].LastAt.After(out.Conversations[j].LastAt) })
	sort.Slice(out.Rules, func(i, j int) bool { return out.Rules[i].CreatedAt.After(out.Rules[j].CreatedAt) })
	sort.Slice(out.Recordings, func(i, j int) bool { return out.Recordings[i].CreatedAt.After(out.Recordings[j].CreatedAt) })
	out.Settings.TelegramToken = ""
	out.Settings.ProviderAPIKey = ""
	out.Settings.SIPPassword = ""
	if out.Settings.VoicemailTimeout == 0 {
		out.Settings.VoicemailTimeout = 30
	}
	return out
}

func (s *State) Settings() domain.Settings {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := s.settings
	if out.VoicemailTimeout == 0 {
		out.VoicemailTimeout = 30
	}
	return out
}

func (s *State) ActiveCall() *domain.Call {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if c := s.calls[s.activeCallID]; c != nil {
		v := *c
		return &v
	}
	return nil
}

func (s *State) Call(id string) *domain.Call {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if c := s.calls[id]; c != nil {
		v := *c
		return &v
	}
	return nil
}

func (s *State) Message(id string) *domain.Message {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if m := s.messages[id]; m != nil {
		v := *m
		return &v
	}
	return nil
}

func (s *State) Recording(id string) *domain.Recording {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if r := s.recordings[id]; r != nil {
		v := *r
		return &v
	}
	return nil
}

func (s *State) Rules() []*domain.FilterRule {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*domain.FilterRule, 0, len(s.rules))
	for _, r := range s.rules {
		v := *r
		out = append(out, &v)
	}
	return out
}

func (s *State) UpsertCall(v *domain.Call) (domain.Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if old := s.calls[v.ID]; old != nil && v.Version != old.Version+1 {
		return domain.Event{}, errors.New("CALL_STATE_CONFLICT")
	}
	if v.Version == 0 {
		v.Version = 1
	}
	return s.appendLocked("call.upserted", v.ID, v.Version, v)
}

func (s *State) UpsertMessage(v *domain.Message) (domain.Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if old := s.messages[v.ID]; old != nil && v.Version != old.Version+1 {
		return domain.Event{}, errors.New("MESSAGE_STATE_CONFLICT")
	}
	if v.Version == 0 {
		v.Version = 1
	}
	return s.appendLocked("message.upserted", v.ID, v.Version, v)
}

// MarkAllMessagesRead persists the complete batch before exposing any of it
// in memory. This keeps conversation badges and the event stream consistent
// if SQLite rejects one of the updates.
func (s *State) MarkAllMessagesRead() (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	updates := make([]*domain.Message, 0)
	for _, existing := range s.messages {
		if !existing.Unread || existing.Deleted || existing.Direction != domain.Incoming {
			continue
		}
		message := *existing
		message.Version++
		message.Unread = false
		updates = append(updates, &message)
	}
	if len(updates) == 0 {
		return 0, nil
	}
	sort.Slice(updates, func(i, j int) bool { return updates[i].ID < updates[j].ID })
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	events := make([]domain.Event, 0, len(updates))
	for _, message := range updates {
		raw, marshalErr := json.Marshal(message)
		if marshalErr != nil {
			_ = tx.Rollback()
			return 0, marshalErr
		}
		now := time.Now().UTC()
		res, insertErr := tx.Exec(`INSERT INTO events(type,entity_id,version,created_at,payload) VALUES(?,?,?,?,?)`,
			"message.upserted", message.ID, message.Version, now.Format(time.RFC3339Nano), raw)
		if insertErr != nil {
			_ = tx.Rollback()
			return 0, insertErr
		}
		sequence, _ := res.LastInsertId()
		events = append(events, domain.Event{
			Sequence: sequence, Type: "message.upserted", EntityID: message.ID,
			Version: message.Version, Timestamp: now, Payload: message, Raw: raw,
		})
	}
	if err = tx.Commit(); err != nil {
		return 0, err
	}
	for _, event := range events {
		if err = s.apply(event); err != nil {
			return 0, err
		}
		s.sequence = event.Sequence
		for subscriber := range s.subscribers {
			select {
			case subscriber <- event:
			default:
			}
		}
	}
	return len(events), nil
}

func (s *State) UpsertRule(v *domain.FilterRule) (domain.Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	version := int64(1)
	if old := s.rules[v.ID]; old != nil {
		version = old.CreatedAt.UnixNano() + 1
	}
	return s.appendLocked("rule.upserted", v.ID, version, v)
}

func (s *State) DeleteRule(id string) (domain.Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.rules[id] == nil {
		return domain.Event{}, sql.ErrNoRows
	}
	return s.appendLocked("rule.deleted", id, time.Now().UnixNano(), map[string]string{"id": id})
}

func (s *State) UpdateSettings(v domain.Settings) (domain.Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if v.Country == "" {
		v.Country = "CN"
	}
	if len(v.AutoBlock) == 0 {
		v.AutoBlock = []string{"fraud", "insurance"}
	}
	return s.appendLocked("settings.updated", "settings", s.sequence+1, v)
}

func (s *State) UpdateDevice(v domain.DeviceStatus) (domain.Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v.LastCheckedAt = time.Now().UTC()
	if sameDevice(s.device, v) {
		s.device.LastCheckedAt = v.LastCheckedAt
		return domain.Event{Sequence: s.sequence, Type: "device.unchanged", EntityID: "device", Timestamp: v.LastCheckedAt}, nil
	}
	return s.appendLocked("device.updated", "device", s.sequence+1, v)
}

func sameDevice(a, b domain.DeviceStatus) bool {
	return a.Mode == b.Mode && a.ATPort == b.ATPort && a.AudioPort == b.AudioPort && a.ATConnected == b.ATConnected &&
		a.AudioCapable == b.AudioCapable && a.SIMReady == b.SIMReady && a.Registered == b.Registered && a.VoiceReady == b.VoiceReady && a.Operator == b.Operator &&
		a.AccessTech == b.AccessTech && a.Signal == b.Signal && a.SignalDBm == b.SignalDBm &&
		a.TelegramOK == b.TelegramOK && a.SIPStatus == b.SIPStatus && a.SIPPending == b.SIPPending &&
		math.Abs(a.DiskUsedPct-b.DiskUsedPct) < .01 && slices.Equal(a.Degraded, b.Degraded)
}

func (s *State) UpdateDiskUsage(pct float64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if math.Abs(s.device.DiskUsedPct-pct) < 1 && s.device.DiskUsedPct != 0 {
		return nil
	}
	s.device.DiskUsedPct = pct
	s.device.LastCheckedAt = time.Now().UTC()
	_, err := s.appendLocked("device.updated", "device", s.sequence+1, s.device)
	return err
}

func (s *State) UpsertRecording(v *domain.Recording) (domain.Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	stored := struct {
		*domain.Recording
		StoragePath string `json:"storagePath"`
	}{Recording: v, StoragePath: v.Path}
	return s.appendLocked("recording.upserted", v.ID, time.Now().UnixNano(), stored)
}

func (s *State) DeleteRecording(id string) (domain.Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.recordings[id] == nil {
		return domain.Event{}, errors.New("NOT_FOUND")
	}
	return s.appendLocked("recording.deleted", id, time.Now().UnixNano(), map[string]string{"id": id})
}

func (s *State) Subscribe() (<-chan domain.Event, func()) {
	ch := make(chan domain.Event, 64)
	s.mu.Lock()
	s.subscribers[ch] = struct{}{}
	s.mu.Unlock()
	cancel := func() {
		s.mu.Lock()
		if _, ok := s.subscribers[ch]; ok {
			delete(s.subscribers, ch)
			close(ch)
		}
		s.mu.Unlock()
	}
	return ch, cancel
}

func (s *State) rebuildConversation(number string) {
	c := &domain.Conversation{ID: number, Number: number}
	for _, m := range s.messages {
		if m.Number != number || m.Deleted {
			continue
		}
		if m.CreatedAt.After(c.LastAt) {
			c.LastAt = m.CreatedAt
			c.LastBody = m.Body
			c.Filtered = m.Filtered
		}
		if m.Unread {
			c.Unread++
		}
	}
	s.conversations[number] = c
}

func (s *State) IntegrityCheck(ctx context.Context) error {
	var result string
	if err := s.db.QueryRowContext(ctx, `PRAGMA integrity_check`).Scan(&result); err != nil {
		return err
	}
	if result != "ok" {
		return errors.New(result)
	}
	return nil
}
