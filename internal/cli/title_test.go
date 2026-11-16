package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
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

func TestTitleCommandRemoteServer(t *testing.T) {
	t.Parallel()

	var gotPath string
	var gotAuth string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read request body: %v", err)
		}
		if err := json.Unmarshal(body, &gotBody); err != nil {
			t.Errorf("decode request body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"sessionId":"abc123","tags":[],"category":"","project":"","title":"Renamed"}`)
	}))
	defer srv.Close()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := Run([]string{"title", "abc123", "Renamed", "--server", srv.URL, "--api-key", "sth_key123"}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}

	if gotPath != "/api/sessions/abc123/title" {
		t.Fatalf("unexpected request path: %q", gotPath)
	}
	if gotBody["title"] != "Renamed" {
		t.Fatalf("unexpected request body: %#v", gotBody)
	}
	if gotAuth != "Bearer sth_key123" {
		t.Fatalf("expected API key to be attached, got %q", gotAuth)
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

func TestTitleCommandRejectsTooLongTitle(t *testing.T) {
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

	long := strings.Repeat("长", session.MaxTitleRunes+1)
	if err := store.SetTitle(sess.ID, long); err == nil {
		t.Fatal("expected over-long title to be rejected")
	}
	if err := store.SetTitle(sess.ID, strings.Repeat("长", session.MaxTitleRunes)); err != nil {
		t.Fatalf("expected title at the cap to be accepted, got %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
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
