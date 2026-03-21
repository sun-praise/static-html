package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"testing"
	"time"

	"github.com/sun-praise/static-html/internal/session"
)

func TestNewRequiresStore(t *testing.T) {
	t.Parallel()

	_, err := New("127.0.0.1", 0, nil)
	if err == nil {
		t.Fatal("expected nil store to be rejected")
	}
}

func TestCreateSessionAndServeAssets(t *testing.T) {
	t.Parallel()

	fixtureHTML := filepath.Join("..", "..", "fixtures", "basic", "index.html")
	absoluteFixture, err := filepath.Abs(fixtureHTML)
	if err != nil {
		t.Fatal(err)
	}

	store := newTestStore(t)
	srv, err := New("127.0.0.1", 0, store)
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

	body, err := json.Marshal(map[string]string{"filePath": absoluteFixture})
	if err != nil {
		t.Fatal(err)
	}

	createResp, err := http.Post(srv.Origin()+"/api/sessions", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer createResp.Body.Close()

	if createResp.StatusCode != http.StatusCreated {
		responseBody, _ := io.ReadAll(createResp.Body)
		t.Fatalf("unexpected create status: %d body=%s", createResp.StatusCode, responseBody)
	}

	var payload struct {
		URL string `json:"url"`
	}
	if err := json.NewDecoder(createResp.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}

	htmlResp, err := http.Get(payload.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer htmlResp.Body.Close()

	htmlBytes, err := io.ReadAll(htmlResp.Body)
	if err != nil {
		t.Fatal(err)
	}

	if htmlResp.StatusCode != http.StatusOK {
		t.Fatalf("unexpected html status: %d", htmlResp.StatusCode)
	}

	if !bytes.Contains(htmlBytes, []byte("Static HTML Preview")) {
		t.Fatalf("preview html missing expected content: %s", string(htmlBytes))
	}

	cssURL, err := url.JoinPath(payload.URL, "style.css")
	if err != nil {
		t.Fatal(err)
	}

	cssResp, err := http.Get(cssURL)
	if err != nil {
		t.Fatal(err)
	}
	defer cssResp.Body.Close()

	cssBytes, err := io.ReadAll(cssResp.Body)
	if err != nil {
		t.Fatal(err)
	}

	if cssResp.StatusCode != http.StatusOK {
		t.Fatalf("unexpected css status: %d", cssResp.StatusCode)
	}

	if !bytes.Contains(cssBytes, []byte("background")) {
		t.Fatalf("css missing expected content: %s", string(cssBytes))
	}
}

func TestTraversalIsRejected(t *testing.T) {
	t.Parallel()

	fixtureHTML, err := filepath.Abs(filepath.Join("..", "..", "fixtures", "basic", "index.html"))
	if err != nil {
		t.Fatal(err)
	}

	store := newTestStore(t)
	srv, err := New("127.0.0.1", 0, store)
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

	body, err := json.Marshal(map[string]string{"filePath": fixtureHTML})
	if err != nil {
		t.Fatal(err)
	}

	resp, err := http.Post(srv.Origin()+"/api/sessions", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	var payload struct {
		URL string `json:"url"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}

	traversalResp, err := http.Get(payload.URL + "..%2Fgo.mod")
	if err != nil {
		t.Fatal(err)
	}
	defer traversalResp.Body.Close()

	if traversalResp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", traversalResp.StatusCode)
	}
}

func newTestStore(t *testing.T) *session.Store {
	t.Helper()

	dbPath := filepath.Join(t.TempDir(), "sessions.db")
	store, err := session.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("store.Close failed: %v", err)
		}
	})

	return store
}
