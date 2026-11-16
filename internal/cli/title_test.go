package cli

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sun-praise/static-html/internal/session"
)

func TestSendRequiresTitle(t *testing.T) {
	t.Parallel()

	fixtureHTML, err := filepath.Abs(filepath.Join("..", "..", "fixtures", "basic", "index.html"))
	if err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	err = Run([]string{"send", fixtureHTML, "--tag", "test", "--category", "cat", "--project", "proj"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected send to fail without --title")
	}
	if !strings.Contains(err.Error(), "--title is required") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestTitleCommandSetsTitle(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "sessions.db")
	store, err := session.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	sess, err := store.Create("/tmp/index.html")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := Run([]string{"title", sess.ID, "Login Page v2", "--db", dbPath}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "Set title of "+sess.ID+" to Login Page v2") {
		t.Fatalf("unexpected output: %q", stdout.String())
	}

	store, err = session.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	meta, err := store.GetMetadata(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if meta.Title != "Login Page v2" {
		t.Fatalf("expected title to persist, got %q", meta.Title)
	}
}

func TestTitleCommandRejectsEmptyTitle(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "sessions.db")
	store, err := session.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	sess, err := store.Create("/tmp/index.html")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := Run([]string{"title", sess.ID, "", "--db", dbPath}, &stdout, &stderr); err == nil {
		t.Fatal("expected title command to fail with an empty title")
	}
}

func TestTitleCommandRequiresTitleArg(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := Run([]string{"title", "some-id"}, &stdout, &stderr); err == nil {
		t.Fatal("expected title command to fail without a title argument")
	}
}
