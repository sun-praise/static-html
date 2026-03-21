package cli

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sun-praise/static-html/internal/server"
	"github.com/sun-praise/static-html/internal/session"
)

func TestSendPrintsSessionURL(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	srv, err := server.New("127.0.0.1", 0, store)
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = srv.Stop(ctx)
	}()

	fixtureHTML, err := filepath.Abs(filepath.Join("..", "..", "fixtures", "basic", "index.html"))
	if err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := Run([]string{"send", fixtureHTML, "--server", srv.Origin()}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}

	output := stdout.String()
	if !strings.Contains(output, srv.Origin()+"/s/") {
		t.Fatalf("unexpected output: %q", output)
	}
}

func TestSendFailsClearlyWhenServerUnavailable(t *testing.T) {
	t.Parallel()

	fixtureHTML, err := filepath.Abs(filepath.Join("..", "..", "fixtures", "basic", "index.html"))
	if err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	err = Run([]string{"send", fixtureHTML, "--server", "http://127.0.0.1:4399"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected send to fail")
	}

	if !strings.Contains(err.Error(), `Start the server with "sth start" first.`) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func newTestStore(t *testing.T) *session.Store {
	t.Helper()

	store, err := session.NewSQLiteStore(filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("close store: %v", err)
		}
	})

	return store
}
