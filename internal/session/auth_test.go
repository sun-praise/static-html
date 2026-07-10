package session

import (
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"
)

func newAuthTestStore(t *testing.T) *Store {
	t.Helper()
	store, err := NewInMemoryStore()
	if err != nil {
		t.Fatalf("create in-memory store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func TestCreateUser_UsernameUnique(t *testing.T) {
	t.Parallel()
	store := newAuthTestStore(t)

	if _, err := store.CreateUser("alice"); err != nil {
		t.Fatalf("create alice: %v", err)
	}
	_, err := store.CreateUser("alice")
	if err == nil {
		t.Fatal("expected error creating duplicate user, got nil")
	}
	if !errors.Is(err, ErrUsernameTaken) {
		t.Fatalf("expected ErrUsernameTaken, got %v", err)
	}
}

func TestCreateUser_EmptyName(t *testing.T) {
	t.Parallel()
	store := newAuthTestStore(t)
	if _, err := store.CreateUser("   "); err == nil {
		t.Fatal("expected error for empty username")
	}
}

func TestListUsers(t *testing.T) {
	t.Parallel()
	store := newAuthTestStore(t)
	if _, err := store.CreateUser("bob"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateUser("alice"); err != nil {
		t.Fatal(err)
	}
	users, err := store.ListUsers()
	if err != nil {
		t.Fatal(err)
	}
	if len(users) != 2 {
		t.Fatalf("expected 2 users, got %d", len(users))
	}
	if users[0].Username != "alice" {
		t.Fatalf("expected ordered first user alice, got %q", users[0].Username)
	}
}

func TestIssueAPIKey_PlaintextNotStored(t *testing.T) {
	t.Parallel()
	store := newAuthTestStore(t)
	user, err := store.CreateUser("alice")
	if err != nil {
		t.Fatal(err)
	}

	plaintext, _, err := store.IssueAPIKey(user.ID)
	if err != nil {
		t.Fatalf("issue key: %v", err)
	}
	if !strings.HasPrefix(plaintext, "sth_") {
		t.Fatalf("expected key to have sth_ prefix, got %q", plaintext)
	}

	// The plaintext must never be persisted. Inspect the raw table.
	rows, err := store.db.Query(`SELECT key_hash, salt, key_prefix FROM api_keys`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var hash, salt, prefix string
		if err := rows.Scan(&hash, &salt, &prefix); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(hash, plaintext) || strings.Contains(salt, plaintext) {
			t.Fatal("plaintext key found persisted in hash or salt")
		}
		if strings.HasPrefix(plaintext, prefix) == false {
			t.Fatalf("stored prefix %q is not a prefix of plaintext", prefix)
		}
	}
}

func TestVerifyAPIKey_ValidAndRevoked(t *testing.T) {
	t.Parallel()
	store := newAuthTestStore(t)
	user, err := store.CreateUser("alice")
	if err != nil {
		t.Fatal(err)
	}

	plaintext, rec, err := store.IssueAPIKey(user.ID)
	if err != nil {
		t.Fatal(err)
	}

	uid, ok, err := store.VerifyAPIKey(plaintext)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if !ok || uid != user.ID {
		t.Fatalf("expected valid key for %q, got ok=%v uid=%q", user.ID, ok, uid)
	}

	if err := store.RevokeAPIKey(rec.ID); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	_, ok, _ = store.VerifyAPIKey(plaintext)
	if ok {
		t.Fatal("expected revoked key to fail verification")
	}
}

func TestVerifyAPIKey_Garbage(t *testing.T) {
	t.Parallel()
	store := newAuthTestStore(t)
	_, ok, err := store.VerifyAPIKey("sth_notarealkey")
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("expected fake key to not verify")
	}
}

// insertAPIKeyRow inserts a raw api_keys row with a controlled key_prefix so
// tests can deterministically reproduce prefix collisions (IssueAPIKey uses
// random prefixes that never collide in practice).
func insertAPIKeyRow(t *testing.T, store *Store, id, userID, prefix string) {
	t.Helper()
	_, err := store.db.Exec(
		`INSERT INTO api_keys (id, user_id, key_hash, salt, hash_algo, key_prefix, created_at)
		 VALUES (?, ?, 'deadbeef', 'salt', 'sha256', ?, ?)`,
		id, userID, prefix, time.Now().UTC().UnixNano(),
	)
	if err != nil {
		t.Fatalf("insert api_keys row: %v", err)
	}
}

func TestRevokeAPIKey_AmbiguousPrefixFailsClosed(t *testing.T) {
	t.Parallel()
	store := newAuthTestStore(t)
	user, err := store.CreateUser("alice")
	if err != nil {
		t.Fatal(err)
	}
	// Two active keys sharing the same prefix (>= KeyPrefixLen) → ambiguous.
	sharedPrefix := "sth_collideXYZ"
	insertAPIKeyRow(t, store, "k1", user.ID, sharedPrefix)
	insertAPIKeyRow(t, store, "k2", user.ID, sharedPrefix)

	err = store.RevokeAPIKey("sth_collideX") // 13 chars, >= KeyPrefixLen, matches both
	if !errors.Is(err, ErrAPIKeyAmbiguous) {
		t.Fatalf("expected ErrAPIKeyAmbiguous, got %v", err)
	}
	// Neither key should have been revoked (fail closed).
	for _, id := range []string{"k1", "k2"} {
		var revoked sql.NullInt64
		if e := store.db.QueryRow(`SELECT revoked_at FROM api_keys WHERE id = ?`, id).Scan(&revoked); e != nil {
			t.Fatal(e)
		}
		if revoked.Valid {
			t.Fatalf("key %s was revoked despite ambiguity", id)
		}
	}
}

func TestRevokeAPIKey_PrefixAndNotFound(t *testing.T) {
	t.Parallel()
	store := newAuthTestStore(t)
	user, err := store.CreateUser("alice")
	if err != nil {
		t.Fatal(err)
	}

	plaintext, rec, err := store.IssueAPIKey(user.ID)
	if err != nil {
		t.Fatal(err)
	}

	// Revoke by a unique prefix of the issued key.
	prefix := plaintext[:KeyPrefixLen]
	if err := store.RevokeAPIKey(prefix); err != nil {
		t.Fatalf("revoke by prefix: %v", err)
	}
	// Second revoke of the same prefix → already revoked → not found.
	if err := store.RevokeAPIKey(prefix); err == nil {
		t.Fatal("expected error revoking already-revoked prefix")
	}
	// Re-revoke by exact id is also not-found now.
	if err := store.RevokeAPIKey(rec.ID); err == nil {
		t.Fatal("expected error re-revoking by exact id")
	}
	// Garbage → not found.
	if err := store.RevokeAPIKey("definitely-not-a-key"); err == nil {
		t.Fatal("expected error for unknown key")
	}
}

func TestRevokeAPIKey_EscapesLikeWildcards(t *testing.T) {
	t.Parallel()
	store := newAuthTestStore(t)
	user, err := store.CreateUser("alice")
	if err != nil {
		t.Fatal(err)
	}
	// A key whose prefix contains a LIKE wildcard ('%'). Revoke must match it
	// literally, not as a wildcard. Prefix length >= KeyPrefixLen to clear the
	// minimum-length guard.
	wildPrefix := "sth_%_weirdXY"
	insertAPIKeyRow(t, store, "k1", user.ID, wildPrefix)
	if err := store.RevokeAPIKey("sth_%_weirdX"); err != nil {
		t.Fatalf("revoke literal-prefix-with-wildcard: %v", err)
	}
}

func TestRevokeAPIKey_TooShortFailsClosed(t *testing.T) {
	t.Parallel()
	store := newAuthTestStore(t)
	user, err := store.CreateUser("alice")
	if err != nil {
		t.Fatal(err)
	}
	// An active key exists, but a short/empty prefix must not match it.
	insertAPIKeyRow(t, store, "k1", user.ID, "sth_realtoken12")
	for _, bad := range []string{"", "sth", "sth_real"} {
		if err := store.RevokeAPIKey(bad); err == nil {
			t.Fatalf("expected error for too-short prefix %q, got nil", bad)
		}
	}
	// The key must remain unrevoked.
	var revoked sql.NullInt64
	if e := store.db.QueryRow(`SELECT revoked_at FROM api_keys WHERE id = ?`, "k1").Scan(&revoked); e != nil {
		t.Fatal(e)
	}
	if revoked.Valid {
		t.Fatal("key was revoked despite short-prefix guard")
	}
}
