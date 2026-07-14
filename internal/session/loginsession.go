package session

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"time"
)

// DefaultLoginSessionTTL is how long a browser session cookie remains valid
// after login. Sessions can be revoked earlier via /logout or DB deletion.
const DefaultLoginSessionTTL = 30 * 24 * time.Hour

// ErrLoginSessionInvalid is returned by VerifyLoginSession when the presented
// token does not match a live (non-expired) session. Callers treat this as
// "no session" rather than an error to log.
var ErrLoginSessionInvalid = errors.New("login session token is invalid or expired")

// sessionTokenPrefix mirrors the API key prefix style so tokens are visibly
// identifiable as sth session tokens (and distinct from API keys) in logs.
const sessionTokenPrefix = "sths_"

// initLoginSessions creates the login_sessions table idempotently. Only the
// token hash is persisted; the plaintext token returned by CreateLoginSession
// lives only in the caller's stack and the Set-Cookie response, mirroring the
// API-key security model.
//
// Lookup is O(1) on token_hash as the primary key. Unlike API keys, session
// tokens are NOT salted before hashing: the token itself carries 256 bits of
// entropy (32 random bytes), so brute-forcing the SHA-256 preimage is
// infeasible regardless of salt, and dropping the salt enables a direct
// single-column PK lookup. A DB leak therefore still does NOT hand the
// attacker live cookies — they would need to invert SHA-256 of a 256-bit
// random value. (API keys use a salt because they are matched by a stored
// KeyPrefixLen prefix scan; session tokens have no such prefix path.)
func (s *Store) initLoginSessions() error {
	_, err := s.db.Exec(`
		CREATE TABLE IF NOT EXISTS login_sessions (
			token_hash TEXT PRIMARY KEY,
			user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			created_at INTEGER NOT NULL,
			expires_at INTEGER NOT NULL
		);
		CREATE INDEX IF NOT EXISTS idx_login_sessions_user_id ON login_sessions(user_id);
		CREATE INDEX IF NOT EXISTS idx_login_sessions_expires ON login_sessions(expires_at);
	`)
	return err
}

// CreateLoginSession issues a new server-side session for userID and returns
// the plaintext token to place in the Set-Cookie response. The DB stores only
// SHA-256(token) so a DB leak does not hand the attacker live cookies.
func (s *Store) CreateLoginSession(userID string) (token string, err error) {
	if userID == "" {
		return "", ErrUserNotFound
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	token = sessionTokenPrefix + base64.RawURLEncoding.EncodeToString(raw)
	now := time.Now().UTC()
	expires := now.Add(DefaultLoginSessionTTL)
	_, err = s.db.Exec(
		`INSERT INTO login_sessions (token_hash, user_id, created_at, expires_at) VALUES (?, ?, ?, ?)`,
		hashSessionToken(token), userID, now.UnixNano(), expires.UnixNano(),
	)
	if err != nil {
		return "", err
	}
	return token, nil
}

// VerifyLoginSession looks up the session by its hash and returns the owning
// userID when the token matches a non-expired row. Expired rows are treated as
// invalid and opportunistically cleaned up.
func (s *Store) VerifyLoginSession(token string) (userID string, ok bool, err error) {
	if token == "" {
		return "", false, nil
	}
	hash := hashSessionToken(token)
	var (
		uid       string
		expiresNs int64
	)
	err = s.db.QueryRow(
		`SELECT user_id, expires_at FROM login_sessions WHERE token_hash = ?`,
		hash,
	).Scan(&uid, &expiresNs)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	if time.Now().UTC().UnixNano() > expiresNs {
		// Best-effort cleanup of the expired row; ignore error.
		_, _ = s.db.Exec(`DELETE FROM login_sessions WHERE token_hash = ?`, hash)
		return "", false, nil
	}
	return uid, true, nil
}

// DeleteLoginSession invalidates a session (logout). It is safe to call with an
// unknown/already-deleted token — it simply affects zero rows.
func (s *Store) DeleteLoginSession(token string) error {
	if token == "" {
		return nil
	}
	_, err := s.db.Exec(
		`DELETE FROM login_sessions WHERE token_hash = ?`,
		hashSessionToken(token),
	)
	return err
}

// hashSessionToken computes the stored hash of a session token. The 256-bit
// random token is the security boundary; SHA-256 over it makes the stored
// value a non-reversible handle so a DB dump cannot be replayed as cookies.
func hashSessionToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
