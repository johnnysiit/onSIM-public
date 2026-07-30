package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/argon2"
)

type Manager struct {
	db  *sql.DB
	ttl time.Duration
}

func New(db *sql.DB, ttl time.Duration) *Manager { return &Manager{db: db, ttl: ttl} }

func (m *Manager) Initialized(ctx context.Context) bool {
	var n int
	return m.db.QueryRowContext(ctx, `SELECT count(*) FROM users`).Scan(&n) == nil && n > 0
}

func (m *Manager) Setup(ctx context.Context, password string) error {
	if len(password) < 10 {
		return errors.New("PASSWORD_TOO_SHORT")
	}
	if m.Initialized(ctx) {
		return errors.New("ALREADY_INITIALIZED")
	}
	hash, err := hashPassword(password)
	if err != nil {
		return err
	}
	_, err = m.db.ExecContext(ctx, `INSERT INTO users(id,password_hash,created_at) VALUES(1,?,?)`,
		hash, time.Now().UTC().Format(time.RFC3339Nano))
	return err
}

func (m *Manager) Login(ctx context.Context, password string) (string, time.Time, error) {
	var hash string
	if err := m.db.QueryRowContext(ctx, `SELECT password_hash FROM users WHERE id=1`).Scan(&hash); err != nil {
		return "", time.Time{}, errors.New("INVALID_CREDENTIALS")
	}
	if !verifyPassword(password, hash) {
		return "", time.Time{}, errors.New("INVALID_CREDENTIALS")
	}
	raw := randomToken(32)
	expires := time.Now().UTC().Add(m.ttl)
	_, err := m.db.ExecContext(ctx, `INSERT INTO sessions(token_hash,expires_at,created_at) VALUES(?,?,?)`,
		tokenHash(raw), expires.Format(time.RFC3339Nano), time.Now().UTC().Format(time.RFC3339Nano))
	return raw, expires, err
}

func (m *Manager) Validate(ctx context.Context, raw string) bool {
	if raw == "" {
		return false
	}
	var expiry string
	if err := m.db.QueryRowContext(ctx, `SELECT expires_at FROM sessions WHERE token_hash=?`, tokenHash(raw)).Scan(&expiry); err != nil {
		return false
	}
	t, err := time.Parse(time.RFC3339Nano, expiry)
	if err != nil || time.Now().After(t) {
		_, _ = m.db.ExecContext(ctx, `DELETE FROM sessions WHERE token_hash=?`, tokenHash(raw))
		return false
	}
	return true
}

func (m *Manager) Logout(ctx context.Context, raw string) {
	_, _ = m.db.ExecContext(ctx, `DELETE FROM sessions WHERE token_hash=?`, tokenHash(raw))
}

func (m *Manager) CreateActionToken(ctx context.Context, kind, entityID string, ttl time.Duration) (string, error) {
	raw := randomToken(32)
	_, err := m.db.ExecContext(ctx, `INSERT INTO action_tokens(token_hash,kind,entity_id,expires_at) VALUES(?,?,?,?)`,
		tokenHash(raw), kind, entityID, time.Now().UTC().Add(ttl).Format(time.RFC3339Nano))
	return raw, err
}

func (m *Manager) ConsumeActionToken(ctx context.Context, raw, kind string) (string, error) {
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	var entity, expiry string
	var used sql.NullString
	if err = tx.QueryRowContext(ctx, `SELECT entity_id,expires_at,used_at FROM action_tokens WHERE token_hash=? AND kind=?`,
		tokenHash(raw), kind).Scan(&entity, &expiry, &used); err != nil {
		return "", errors.New("INVALID_TOKEN")
	}
	exp, _ := time.Parse(time.RFC3339Nano, expiry)
	if used.Valid || time.Now().After(exp) {
		return "", errors.New("TOKEN_EXPIRED")
	}
	if _, err = tx.ExecContext(ctx, `UPDATE action_tokens SET used_at=? WHERE token_hash=? AND used_at IS NULL`,
		time.Now().UTC().Format(time.RFC3339Nano), tokenHash(raw)); err != nil {
		return "", err
	}
	return entity, tx.Commit()
}

func (m *Manager) ValidateActionToken(ctx context.Context, raw, kind string) (string, bool) {
	var entity, expiry string
	var used sql.NullString
	if err := m.db.QueryRowContext(ctx, `SELECT entity_id,expires_at,used_at FROM action_tokens WHERE token_hash=? AND kind=?`,
		tokenHash(raw), kind).Scan(&entity, &expiry, &used); err != nil {
		return "", false
	}
	exp, err := time.Parse(time.RFC3339Nano, expiry)
	return entity, err == nil && !used.Valid && time.Now().Before(exp)
}

func hashPassword(password string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	const memory, iterations, parallelism, keyLen = 64 * 1024, 3, 2, 32
	key := argon2.IDKey([]byte(password), salt, iterations, memory, parallelism, keyLen)
	return fmt.Sprintf("$argon2id$v=19$m=%d,t=%d,p=%d$%s$%s", memory, iterations, parallelism,
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key)), nil
}

func verifyPassword(password, encoded string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 {
		return false
	}
	var memory uint32
	var iterations uint32
	var parallelism uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &iterations, &parallelism); err != nil {
		return false
	}
	salt, err1 := base64.RawStdEncoding.DecodeString(parts[4])
	want, err2 := base64.RawStdEncoding.DecodeString(parts[5])
	if err1 != nil || err2 != nil {
		return false
	}
	got := argon2.IDKey([]byte(password), salt, iterations, memory, parallelism, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1
}

func randomToken(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

func tokenHash(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func ParseChatID(v string) (int64, error) { return strconv.ParseInt(strings.TrimSpace(v), 10, 64) }
