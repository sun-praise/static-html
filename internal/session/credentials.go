package session

import (
	"database/sql"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// bcryptCost is the bcrypt cost used for human passwords. Unlike API keys
// (which are high-entropy and verified with fast SHA-256 on every request),
// human passwords are low-entropy and MUST be hashed with a slow KDF to resist
// offline brute-force. Cost 10 adds ~60-100ms per verify, acceptable for the
// low-frequency login path.
const bcryptCost = 10

// MinPasswordLength is the minimum acceptable password length at registration.
const MinPasswordLength = 8

// initCredentials creates the user_credentials table idempotently. It is a
// one-to-one, optional companion to users: a user row without a credentials
// row is a legitimate state (a pure API-key user with no password) and cannot
// log in via /login. Splitting password storage into its own table keeps the
// existing users schema (and its API-key-only rows) untouched.
func (s *Store) initCredentials() error {
	_, err := s.db.Exec(`
		CREATE TABLE IF NOT EXISTS user_credentials (
			user_id TEXT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
			password_hash TEXT NOT NULL,
			updated_at INTEGER NOT NULL
		);
	`)
	return err
}

// SetPassword stores a bcrypt hash of password for the given user, creating or
// overwriting the credentials row (upsert). The plaintext is never persisted.
func (s *Store) SetPassword(userID, password string) error {
	if userID == "" {
		return ErrUserNotFound
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcryptCost)
	if err != nil {
		return err
	}
	now := time.Now().UTC().UnixNano()
	_, err = s.db.Exec(
		`INSERT INTO user_credentials (user_id, password_hash, updated_at) VALUES (?, ?, ?)
		 ON CONFLICT(user_id) DO UPDATE SET password_hash = excluded.password_hash, updated_at = excluded.updated_at`,
		userID, string(hash), now,
	)
	return err
}

// VerifyPassword reports whether password matches the stored hash for userID.
// ok is false (with nil err) when the user has no credentials row or the
// password is wrong — callers treat both as a login failure without surfacing
// which condition held, to avoid user enumeration.
func (s *Store) VerifyPassword(userID, password string) (ok bool, err error) {
	var hash string
	err = s.db.QueryRow(
		`SELECT password_hash FROM user_credentials WHERE user_id = ?`,
		userID,
	).Scan(&hash)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) != nil {
		return false, nil
	}
	return true, nil
}
