package session

import (
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

	reopened, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := reopened.Close(); err != nil {
			t.Fatal(err)
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

	recentAfterReopen, err := reopened.ListRecent(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(recentAfterReopen) != 1 {
		t.Fatalf("expected 1 session after reopen, got %d", len(recentAfterReopen))
	}
}
