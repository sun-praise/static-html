package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/sun-praise/static-html/internal/session"
)

func newTitleTestServer(t *testing.T, store *session.Store) *Server {
	t.Helper()

	srv, err := New("127.0.0.1", 0, store, "", 0, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = srv.Stop(ctx)
	})
	return srv
}

func TestHomeDisplaysBoundTitle(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	titled, err := store.Create("/tmp/alpha.html")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetTitle(titled.ID, "登录页 v2"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Create("/tmp/beta.html"); err != nil {
		t.Fatal(err)
	}

	srv := newTitleTestServer(t, store)

	resp, err := http.Get(srv.Origin() + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unexpected status: %d", resp.StatusCode)
	}
	if !bytes.Contains(body, []byte("登录页 v2")) {
		t.Fatalf("expected home page to show bound title, got: %s", string(body))
	}
	if bytes.Contains(body, []byte("alpha.html")) {
		t.Fatalf("titled session should display title, not file name: %s", string(body))
	}
	if !bytes.Contains(body, []byte("beta.html")) {
		t.Fatalf("legacy session should fall back to file name, got: %s", string(body))
	}
}

func TestCreateSessionWithTitleAndRename(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	srv := newTitleTestServer(t, store)

	fixtureHTML, err := filepath.Abs(filepath.Join("..", "..", "fixtures", "basic", "index.html"))
	if err != nil {
		t.Fatal(err)
	}

	body, err := json.Marshal(map[string]any{
		"filePath": fixtureHTML,
		"title":    "API Titled",
		"tags":     []string{"test"},
		"category": "test-cat",
		"project":  "test-proj",
	})
	if err != nil {
		t.Fatal(err)
	}

	resp, err := http.Post(srv.Origin()+"/api/sessions", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		respBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("unexpected create status %d body=%s", resp.StatusCode, respBody)
	}

	var payload struct {
		SessionID string `json:"sessionId"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	sessionID := payload.SessionID

	metaResp, err := http.Get(srv.Origin() + "/api/sessions/" + sessionID + "/metadata")
	if err != nil {
		t.Fatal(err)
	}
	defer metaResp.Body.Close()

	metaBody, err := io.ReadAll(metaResp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(metaBody, []byte("API Titled")) {
		t.Fatalf("expected metadata to include title, got: %s", string(metaBody))
	}

	renameBody, err := json.Marshal(map[string]string{"title": "Renamed Title"})
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPut, srv.Origin()+"/api/sessions/"+sessionID+"/title", bytes.NewReader(renameBody))
	if err != nil {
		t.Fatal(err)
	}
	renameResp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer renameResp.Body.Close()

	if renameResp.StatusCode != http.StatusOK {
		renameResult, _ := io.ReadAll(renameResp.Body)
		t.Fatalf("expected rename status 200, got %d body=%s", renameResp.StatusCode, renameResult)
	}

	homeResp, err := http.Get(srv.Origin() + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer homeResp.Body.Close()

	homeBody, err := io.ReadAll(homeResp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(homeBody, []byte("Renamed Title")) {
		t.Fatalf("expected home page to show renamed title, got: %s", string(homeBody))
	}
	if bytes.Contains(homeBody, []byte("API Titled")) {
		t.Fatalf("old title should be replaced, got: %s", string(homeBody))
	}
}

func TestSetTitleEndpointRejectsEmptyTitle(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	srv := newTitleTestServer(t, store)

	fixtureHTML, err := filepath.Abs(filepath.Join("..", "..", "fixtures", "basic", "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	sessionID := createTestSession(t, srv.Origin(), fixtureHTML)

	emptyBody, err := json.Marshal(map[string]string{"title": "   "})
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPut, srv.Origin()+"/api/sessions/"+sessionID+"/title", bytes.NewReader(emptyBody))
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		result, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected status 400, got %d body=%s", resp.StatusCode, result)
	}
}

func TestSetTitleEndpointNotFound(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	srv := newTitleTestServer(t, store)

	renameBody, err := json.Marshal(map[string]string{"title": "Whatever"})
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPut, srv.Origin()+"/api/sessions/nonexistent/title", bytes.NewReader(renameBody))
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		result, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected status 404, got %d body=%s", resp.StatusCode, result)
	}
}
