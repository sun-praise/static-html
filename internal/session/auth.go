package session

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

// User-facing auth errors.
var (
	ErrUsernameTaken   = errors.New("username already taken")
	ErrUserNotFound    = errors.New("user not found")
	ErrAPIKeyAmbiguous = errors.New("api key prefix matches multiple keys")
)

// User represents a row in the users table.
type User struct {
	ID        string
	Username  string
	CreatedAt time.Time
}

// APIKeyRecord represents a row in the api_keys table. KeyHash holds the
// salted hash of the plaintext key; the plaintext is only ever returned from
// IssueAPIKey at creation time and is never persisted.
type APIKeyRecord struct {
	ID         string
	UserID     string
	KeyHash    string
	Salt       string
	HashAlgo   string
	CreatedAt  time.Time
	RevokedAt  sql.NullTime
	ExpiresAt  sql.NullTime
}

// KeyPrefixLen is the number of leading plaintext characters stored for
// display/matching (e.g. in revoke-key by prefix). The full plaintext is
// never persisted.
const KeyPrefixLen = 12

// hashAlgoSHA256 is the only hash algorithm currently supported.
const hashAlgoSHA256 = "sha256"

// initAuth creates the users and api_keys tables idempotently. Called from
// Store.init via initMetadata's sibling path.
func (s *Store) initAuth() error {
	_, err := s.db.Exec(`
		CREATE TABLE IF NOT EXISTS users (
			id TEXT PRIMARY KEY,
			username TEXT NOT NULL UNIQUE,
			created_at INTEGER NOT NULL
		);

		CREATE TABLE IF NOT EXISTS api_keys (
			id TEXT PRIMARY KEY,
			user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			key_hash TEXT NOT NULL,
			salt TEXT NOT NULL,
			hash_algo TEXT NOT NULL,
			key_prefix TEXT NOT NULL,
			created_at INTEGER NOT NULL,
			revoked_at INTEGER DEFAULT NULL,
			expires_at INTEGER DEFAULT NULL
		);
		CREATE INDEX IF NOT EXISTS idx_api_keys_user_id ON api_keys(user_id);
		CREATE INDEX IF NOT EXISTS idx_api_keys_key_prefix ON api_keys(key_prefix);
	`)
	return err
}

// CreateUser creates a new user with the given username. Returns
// ErrUsernameTaken if the name is already in use.
func (s *Store) CreateUser(username string) (User, error) {
	if strings.TrimSpace(username) == "" {
		return User{}, errors.New("username must not be empty")
	}

	id, err := generateID()
	if err != nil {
		return User{}, err
	}

	now := time.Now().UTC()
	user := User{ID: id, Username: username, CreatedAt: now}

	_, err = s.db.Exec(
		`INSERT INTO users (id, username, created_at) VALUES (?, ?, ?)`,
		user.ID, user.Username, now.UnixNano(),
	)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return User{}, ErrUsernameTaken
		}
		return User{}, err
	}
	return user, nil
}

// ListUsers returns all users ordered by username.
func (s *Store) ListUsers() ([]User, error) {
	rows, err := s.db.Query(`SELECT id, username, created_at FROM users ORDER BY username`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var users []User
	for rows.Next() {
		var (
			u           User
			createdUnix int64
		)
		if err := rows.Scan(&u.ID, &u.Username, &createdUnix); err != nil {
			return nil, err
		}
		u.CreatedAt = time.Unix(0, createdUnix).UTC()
		users = append(users, u)
	}
	return users, rows.Err()
}

// FindUserByUsername returns the user with the given username, or
// ErrUserNotFound.
func (s *Store) FindUserByUsername(username string) (User, error) {
	var (
		u           User
		createdUnix int64
	)
	err := s.db.QueryRow(
		`SELECT id, username, created_at FROM users WHERE username = ?`,
		username,
	).Scan(&u.ID, &u.Username, &createdUnix)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrUserNotFound
	}
	if err != nil {
		return User{}, err
	}
	u.CreatedAt = time.Unix(0, createdUnix).UTC()
	return u, nil
}

// IssueAPIKey generates a new high-entropy API key for the given user,
// persists only its salted hash, and returns the plaintext key (only visible
// at this moment) along with the stored record.
func (s *Store) IssueAPIKey(userID string) (plaintext string, record APIKeyRecord, err error) {
	// Ensure the user exists.
	var exists bool
	if e := s.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM users WHERE id = ?)`, userID).Scan(&exists); e != nil {
		return "", APIKeyRecord{}, e
	}
	if !exists {
		return "", APIKeyRecord{}, ErrUserNotFound
	}

	plaintext, err = generateAPIKey()
	if err != nil {
		return "", APIKeyRecord{}, err
	}

	salt, err := generateSalt()
	if err != nil {
		return "", APIKeyRecord{}, err
	}

	id, err := generateID()
	if err != nil {
		return "", APIKeyRecord{}, err
	}

	now := time.Now().UTC()
	hash := hashAPIKey(hashAlgoSHA256, salt, plaintext)

	record = APIKeyRecord{
		ID:        id,
		UserID:    userID,
		KeyHash:   hash,
		Salt:      salt,
		HashAlgo:  hashAlgoSHA256,
		CreatedAt: now,
	}

	_, err = s.db.Exec(
		`INSERT INTO api_keys (id, user_id, key_hash, salt, hash_algo, key_prefix, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		record.ID, record.UserID, record.KeyHash, record.Salt, record.HashAlgo,
		plaintext[:KeyPrefixLen], now.UnixNano(),
	)
	if err != nil {
		return "", APIKeyRecord{}, err
	}
	return plaintext, record, nil
}

// VerifyAPIKey looks up the key by recomputing its hash. Returns the owning
// userID when the key is valid, not revoked, and not expired. ok is false for
// missing/invalid/expired/revoked keys without an error (unless the query
// itself fails).
func (s *Store) VerifyAPIKey(plaintext string) (userID string, ok bool, err error) {
	if len(plaintext) < KeyPrefixLen {
		return "", false, nil
	}
	prefix := plaintext[:KeyPrefixLen]

	rows, err := s.db.Query(
		`SELECT user_id, key_hash, salt, hash_algo, revoked_at, expires_at
		 FROM api_keys WHERE key_prefix = ?`,
		prefix,
	)
	if err != nil {
		return "", false, err
	}
	defer rows.Close()

	for rows.Next() {
		var (
			uid        string
			keyHash    string
			salt       string
			hashAlgo   string
			revokedUnix sql.NullInt64
			expiresUnix sql.NullInt64
		)
		if err := rows.Scan(&uid, &keyHash, &salt, &hashAlgo, &revokedUnix, &expiresUnix); err != nil {
			return "", false, err
		}
		if revokedUnix.Valid {
			continue // revoked
		}
		if expiresUnix.Valid && time.Now().UTC().UnixNano() > expiresUnix.Int64 {
			continue // expired
		}
		if hashAPIKey(hashAlgo, salt, plaintext) == keyHash {
			return uid, true, nil
		}
	}
	return "", false, rows.Err()
}

// RevokeAPIKey revokes the key matching the given id or unique plaintext
// prefix. If multiple non-revoked keys match the prefix, it fails closed with
// ErrAPIKeyAmbiguous.
func (s *Store) RevokeAPIKey(idOrPrefix string) error {
	// Try exact id first.
	res, err := s.db.Exec(
		`UPDATE api_keys SET revoked_at = ? WHERE id = ? AND revoked_at IS NULL`,
		time.Now().UTC().UnixNano(), idOrPrefix,
	)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n > 0 {
		return nil
	}

	// Fall back to prefix match.
	var count int
	if e := s.db.QueryRow(
		`SELECT COUNT(*) FROM api_keys WHERE key_prefix LIKE ? AND revoked_at IS NULL`,
		idOrPrefix+"%",
	).Scan(&count); e != nil {
		return e
	}
	if count == 0 {
		return ErrAPIKeyNotFound
	}
	if count > 1 {
		return ErrAPIKeyAmbiguous
	}

	_, err = s.db.Exec(
		`UPDATE api_keys SET revoked_at = ? WHERE key_prefix LIKE ? AND revoked_at IS NULL`,
		time.Now().UTC().UnixNano(), idOrPrefix+"%",
	)
	return err
}

// ErrAPIKeyNotFound is returned when no key matches a revoke request.
var ErrAPIKeyNotFound = errors.New("api key not found")

// CountAPIKeysByUser returns the number of active (non-revoked) keys for a
// user, for display in `sth user list`.
func (s *Store) CountAPIKeysByUser(userID string) (int, error) {
	var count int
	err := s.db.QueryRow(
		`SELECT COUNT(*) FROM api_keys WHERE user_id = ? AND revoked_at IS NULL`,
		userID,
	).Scan(&count)
	return count, err
}

// SetSessionOwner records the owning user for a session. Empty userID clears
// ownership (sets to NULL).
func (s *Store) SetSessionOwner(sessionID, userID string) error {
	if userID == "" {
		_, err := s.db.Exec(`UPDATE sessions SET user_id = NULL WHERE session_id = ?`, sessionID)
		return err
	}
	_, err := s.db.Exec(`UPDATE sessions SET user_id = ? WHERE session_id = ?`, userID, sessionID)
	return err
}

// SessionOwner returns the userID owning a session and whether one is set.
func (s *Store) SessionOwner(sessionID string) (userID string, ok bool, err error) {
	var uid sql.NullString
	if e := s.db.QueryRow(`SELECT user_id FROM sessions WHERE session_id = ?`, sessionID).Scan(&uid); e != nil {
		if errors.Is(e, sql.ErrNoRows) {
			return "", false, nil
		}
		return "", false, e
	}
	if !uid.Valid || uid.String == "" {
		return "", false, nil
	}
	return uid.String, true, nil
}

// generateAPIKey returns a new random key with the "sth_" prefix followed by
// 32 random bytes encoded as base64url (high entropy, URL-safe).
func generateAPIKey() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return "sth_" + base64.RawURLEncoding.EncodeToString(buf), nil
}

func generateSalt() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

// hashAPIKey computes the salted hash of the plaintext key for the given
// algorithm. Currently only sha256 is supported.
func hashAPIKey(algo, salt, plaintext string) string {
	switch algo {
	case hashAlgoSHA256:
		sum := sha256.Sum256([]byte(salt + plaintext))
		return hex.EncodeToString(sum[:])
	default:
		// Unsupported algorithm: return a value that can never match, so the
		// key is effectively unusable without erroring the whole query.
		return fmt.Sprintf("unsupported:%s", algo)
	}
}
