package session

import (
	"database/sql"
	"path/filepath"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

// TestUsersRoleMigration upgrades a pre-role database (v2.6.0-era schema
// without users.role) and verifies admin data survives the ALTER.
func TestUsersRoleMigration(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "legacy.db")

	// Create the store once, then strip the role column to simulate a
	// database created before the column existed.
	store, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	user, err := store.CreateUser("alice")
	if err != nil {
		t.Fatal(err)
	}
	plaintext, _, err := store.IssueAPIKey(user.ID)
	if err != nil {
		t.Fatal(err)
	}
	store.Close()

	legacy, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := legacy.Exec(`ALTER TABLE users DROP COLUMN role`); err != nil {
		// DROP COLUMN needs SQLite >= 3.35; an older linked sqlite3 fails
		// here for environment reasons, not because the migration is broken.
		t.Skipf("simulate legacy schema failed (needs SQLite >= 3.35): %v", err)
	}
	legacy.Close()

	// Reopen: the migration must add the column back without losing users
	// or keys, and the role round-trips through SetUserRole / IsAdmin.
	reopened, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("reopen with migration: %v", err)
	}
	defer reopened.Close()

	if err := reopened.SetUserRole("alice", RoleAdmin); err != nil {
		t.Fatal(err)
	}
	if uid, ok, err := reopened.VerifyAPIKey(plaintext); err != nil || !ok || uid != user.ID {
		t.Fatalf("key must survive the migration (ok=%v uid=%q err=%v)", ok, uid, err)
	}

	got, err := reopened.FindUserByUsername("alice")
	if err != nil {
		t.Fatal(err)
	}
	if got.Role != RoleAdmin {
		t.Fatalf("expected admin role, got %q", got.Role)
	}
	isAdmin, err := reopened.IsAdmin(got.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !isAdmin {
		t.Fatal("alice should be admin after migration + promote")
	}
}
