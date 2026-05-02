package session

import (
	"database/sql"
	"path/filepath"
	"testing"
)

func TestStorePersistsSessionsAcrossReopen(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "sessions.db")
	entryFile := filepath.Join(t.TempDir(), "preview", "index.html")

	store, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if store != nil {
			if err := store.Close(); err != nil {
				t.Errorf("close store: %v", err)
			}
		}
	})

	created, err := store.Create(entryFile)
	if err != nil {
		t.Fatal(err)
	}

	recentBeforeClose, err := store.ListRecent(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(recentBeforeClose) != 1 {
		t.Fatalf("expected 1 session before close, got %d", len(recentBeforeClose))
	}

	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store = nil

	reopened, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := reopened.Close(); err != nil {
			t.Errorf("close reopened store: %v", err)
		}
	}()

	found, ok, err := reopened.Get(created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected persisted session to exist after reopening store")
	}
	if found.EntryFile != created.EntryFile {
		t.Fatalf("expected entry file %q, got %q", created.EntryFile, found.EntryFile)
	}
	if found.RootDir != created.RootDir {
		t.Fatalf("expected root dir %q, got %q", created.RootDir, found.RootDir)
	}
	if found.StoredEntryFile != created.StoredEntryFile {
		t.Fatalf("expected stored entry file %q, got %q", created.StoredEntryFile, found.StoredEntryFile)
	}
	if found.StoredRootDir != created.StoredRootDir {
		t.Fatalf("expected stored root dir %q, got %q", created.StoredRootDir, found.StoredRootDir)
	}

	recentAfterReopen, err := reopened.ListRecent(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(recentAfterReopen) != 1 {
		t.Fatalf("expected 1 session after reopen, got %d", len(recentAfterReopen))
	}
}

func TestStoreMigratesLegacyRows(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "sessions.db")
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatal(err)
	}

	_, err = db.Exec(`
		CREATE TABLE sessions (
			session_id TEXT PRIMARY KEY,
			entry_file TEXT NOT NULL,
			root_dir TEXT NOT NULL,
			created_at_unix INTEGER NOT NULL
		);
		INSERT INTO sessions (session_id, entry_file, root_dir, created_at_unix)
		VALUES ('legacy', '/tmp/index.html', '/tmp', 1);
	`)
	if err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	store, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := store.Close(); err != nil {
			t.Errorf("close store: %v", err)
		}
	}()

	found, ok, err := store.Get("legacy")
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected migrated legacy session")
	}
	if found.StoredEntryFile != found.EntryFile {
		t.Fatalf("expected stored entry file to fall back to entry file, got %q", found.StoredEntryFile)
	}
	if found.StoredRootDir != found.RootDir {
		t.Fatalf("expected stored root dir to fall back to root dir, got %q", found.StoredRootDir)
	}
}

func TestSoftDelete(t *testing.T) {
	t.Parallel()

	store, err := NewInMemoryStore()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})

	sess, err := store.Create("/tmp/index.html")
	if err != nil {
		t.Fatal(err)
	}

	if err := store.SoftDelete(sess.ID); err != nil {
		t.Fatalf("SoftDelete returned error: %v", err)
	}

	got, found, err := store.Get(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatal("expected session to still exist in DB after soft delete")
	}
	if got.ID != sess.ID {
		t.Fatalf("expected session ID %q, got %q", sess.ID, got.ID)
	}
}

func TestSoftDeleteNonExistent(t *testing.T) {
	t.Parallel()

	store, err := NewInMemoryStore()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})

	err = store.SoftDelete("nonexistent")
	if err == nil {
		t.Fatal("expected error for non-existent session")
	}
	if err != ErrSessionNotFound {
		t.Fatalf("expected ErrSessionNotFound, got %v", err)
	}
}

func TestSoftDeleteIdempotent(t *testing.T) {
	t.Parallel()

	store, err := NewInMemoryStore()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})

	sess, err := store.Create("/tmp/index.html")
	if err != nil {
		t.Fatal(err)
	}

	if err := store.SoftDelete(sess.ID); err != nil {
		t.Fatalf("first SoftDelete returned error: %v", err)
	}
	if err := store.SoftDelete(sess.ID); err != nil {
		t.Fatalf("second SoftDelete returned error: %v", err)
	}
}

func TestSoftDeleteMigration(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "sessions.db")
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatal(err)
	}

	_, err = db.Exec(`
		CREATE TABLE sessions (
			session_id TEXT PRIMARY KEY,
			entry_file TEXT NOT NULL,
			root_dir TEXT NOT NULL,
			created_at_unix INTEGER NOT NULL
		);
		INSERT INTO sessions (session_id, entry_file, root_dir, created_at_unix)
		VALUES ('legacy', '/tmp/index.html', '/tmp', 1);
	`)
	if err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	store, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = store.Close()
	}()

	if err := store.SoftDelete("legacy"); err != nil {
		t.Fatalf("SoftDelete on migrated DB returned error: %v", err)
	}
}
