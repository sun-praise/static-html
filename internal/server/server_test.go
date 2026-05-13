package server

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
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

	body, err := json.Marshal(map[string]any{
		"filePath": absoluteFixture,
		"tags":     []string{"test"},
		"category": "tutorial",
		"project":  "my-site",
	})
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

	body, err := json.Marshal(map[string]any{
		"filePath": fixtureHTML,
		"tags":     []string{"test"},
		"category": "test",
		"project":  "test",
	})
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

func TestCreateUploadedSessionAndServeAssets(t *testing.T) {
	t.Parallel()

	rootDir := t.TempDir()
	entryFile := filepath.Join(rootDir, "index.html")
	assetFile := filepath.Join(rootDir, "assets", "style.css")
	if err := os.MkdirAll(filepath.Dir(assetFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(entryFile, []byte("<!doctype html><link rel=\"stylesheet\" href=\"assets/style.css\"><h1>Uploaded Preview</h1>"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(assetFile, []byte("body{background:#f5f5f5}"), 0o644); err != nil {
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

	body := &bytes.Buffer{}
	formWriter := multipart.NewWriter(body)
	if err := formWriter.WriteField("entryFile", "/remote/work/index.html"); err != nil {
		t.Fatal(err)
	}
	if err := formWriter.WriteField("entryPath", "index.html"); err != nil {
		t.Fatal(err)
	}
	if err := formWriter.WriteField("category", "uploaded"); err != nil {
		t.Fatal(err)
	}
	if err := formWriter.WriteField("project", "test-proj"); err != nil {
		t.Fatal(err)
	}
	if err := formWriter.WriteField("tags", "upload"); err != nil {
		t.Fatal(err)
	}
	archivePart, err := formWriter.CreateFormFile("archive", "site.zip")
	if err != nil {
		t.Fatal(err)
	}
	archiveWriter := zip.NewWriter(archivePart)
	for _, item := range []struct {
		name    string
		content string
	}{
		{name: "index.html", content: "<!doctype html><link rel=\"stylesheet\" href=\"assets/style.css\"><h1>Uploaded Preview</h1>"},
		{name: "assets/style.css", content: "body{background:#f5f5f5}"},
	} {
		fileWriter, err := archiveWriter.Create(item.name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(fileWriter, item.content); err != nil {
			t.Fatal(err)
		}
	}
	if err := archiveWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := formWriter.Close(); err != nil {
		t.Fatal(err)
	}

	createResp, err := http.Post(srv.Origin()+"/api/sessions", formWriter.FormDataContentType(), body)
	if err != nil {
		t.Fatal(err)
	}
	defer createResp.Body.Close()

	if createResp.StatusCode != http.StatusCreated {
		responseBody, _ := io.ReadAll(createResp.Body)
		t.Fatalf("unexpected create status: %d body=%s", createResp.StatusCode, responseBody)
	}

	var payload struct {
		URL       string `json:"url"`
		EntryFile string `json:"entryFile"`
	}
	if err := json.NewDecoder(createResp.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if payload.EntryFile != "index.html" {
		t.Fatalf("unexpected entryFile: %q", payload.EntryFile)
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
	if !bytes.Contains(htmlBytes, []byte("Uploaded Preview")) {
		t.Fatalf("preview html missing expected content: %s", string(htmlBytes))
	}

	cssURL, err := url.JoinPath(payload.URL, "assets/style.css")
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

func createTestSession(t *testing.T, srvURL, filePath string) string {
	t.Helper()

	body, err := json.Marshal(map[string]any{
		"filePath": filePath,
		"tags":     []string{"test"},
		"category": "test-cat",
		"project":  "test-proj",
	})
	if err != nil {
		t.Fatal(err)
	}

	resp, err := http.Post(srvURL+"/api/sessions", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		respBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("createTestSession: unexpected status %d body=%s", resp.StatusCode, respBody)
	}

	var payload struct {
		SessionID string `json:"sessionId"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	return payload.SessionID
}

func TestDeleteSessionSuccess(t *testing.T) {
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

	sessionID := createTestSession(t, srv.Origin(), fixtureHTML)

	req, err := http.NewRequest(http.MethodDelete, srv.Origin()+"/api/sessions/"+sessionID, nil)
	if err != nil {
		t.Fatal(err)
	}

	deleteResp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer deleteResp.Body.Close()

	if deleteResp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(deleteResp.Body)
		t.Fatalf("expected 200, got %d body=%s", deleteResp.StatusCode, respBody)
	}

	var result map[string]string
	if err := json.NewDecoder(deleteResp.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if result["status"] != "deleted" {
		t.Fatalf("expected status 'deleted', got %q", result["status"])
	}
}

func TestDeleteSessionNotFound(t *testing.T) {
	t.Parallel()

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

	req, err := http.NewRequest(http.MethodDelete, srv.Origin()+"/api/sessions/nonexistent", nil)
	if err != nil {
		t.Fatal(err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", resp.StatusCode)
	}
}

func TestSearchByFileContent(t *testing.T) {
	t.Parallel()

	rootDir := t.TempDir()
	entryFile := filepath.Join(rootDir, "index.html")
	if err := os.WriteFile(entryFile, []byte("<html><body><h1>ralphplus project</h1></body></html>"), 0o644); err != nil {
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

	createTestSession(t, srv.Origin(), entryFile)

	searchResp, err := http.Get(srv.Origin() + "/?q=ralph")
	if err != nil {
		t.Fatal(err)
	}
	defer searchResp.Body.Close()

	searchBody, err := io.ReadAll(searchResp.Body)
	if err != nil {
		t.Fatal(err)
	}

	if searchResp.StatusCode != http.StatusOK {
		t.Fatalf("unexpected search status: %d", searchResp.StatusCode)
	}

	if !bytes.Contains(searchBody, []byte("index.html")) {
		t.Fatalf("expected search to find file by content, got: %s", string(searchBody))
	}
}

func TestSearchNoResults(t *testing.T) {
	t.Parallel()

	rootDir := t.TempDir()
	entryFile := filepath.Join(rootDir, "index.html")
	if err := os.WriteFile(entryFile, []byte("<html><body><h1>hello world</h1></body></html>"), 0o644); err != nil {
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

	createTestSession(t, srv.Origin(), entryFile)

	searchResp, err := http.Get(srv.Origin() + "/?q=zzznonexistent")
	if err != nil {
		t.Fatal(err)
	}
	defer searchResp.Body.Close()

	if searchResp.StatusCode != http.StatusOK {
		t.Fatalf("unexpected search status: %d", searchResp.StatusCode)
	}

	searchBody, err := io.ReadAll(searchResp.Body)
	if err != nil {
		t.Fatal(err)
	}

	if bytes.Contains(searchBody, []byte("index.html")) {
		t.Fatal("expected no results for non-matching query")
	}
}

func TestSearchContentNoDuplicate(t *testing.T) {
	t.Parallel()

	rootDir := t.TempDir()
	// File name contains "ralph", and content also contains "ralph"
	entryFile := filepath.Join(rootDir, "ralph-page.html")
	if err := os.WriteFile(entryFile, []byte("<html><body><h1>ralph is here</h1></body></html>"), 0o644); err != nil {
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

	createTestSession(t, srv.Origin(), entryFile)

	searchResp, err := http.Get(srv.Origin() + "/?q=ralph")
	if err != nil {
		t.Fatal(err)
	}
	defer searchResp.Body.Close()

	searchBody, err := io.ReadAll(searchResp.Body)
	if err != nil {
		t.Fatal(err)
	}

	if searchResp.StatusCode != http.StatusOK {
		t.Fatalf("unexpected search status: %d", searchResp.StatusCode)
	}

	// Count occurrences of the session card — should appear exactly once
	count := bytes.Count(searchBody, []byte("ralph-page.html"))
	if count != 1 {
		t.Fatalf("expected exactly 1 occurrence of ralph-page.html, got %d", count)
	}
}

func TestDeleteSessionIdempotent(t *testing.T) {
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

	sessionID := createTestSession(t, srv.Origin(), fixtureHTML)

	for i := range 2 {
		req, err := http.NewRequest(http.MethodDelete, srv.Origin()+"/api/sessions/"+sessionID, nil)
		if err != nil {
			t.Fatal(err)
		}

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Fatalf("delete %d: expected 200, got %d", i+1, resp.StatusCode)
		}
	}
}

func TestDownloadSession(t *testing.T) {
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

	sessionID := createTestSession(t, srv.Origin(), fixtureHTML)

	downloadURL := srv.Origin() + "/api/sessions/" + sessionID + "/download"
	resp, err := http.Get(downloadURL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("unexpected status: %d body=%s", resp.StatusCode, body)
	}

	contentType := resp.Header.Get("Content-Type")
	if contentType != "application/zip" {
		t.Fatalf("unexpected content-type: %q", contentType)
	}

	zipBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}

	zipReader, err := zip.NewReader(bytes.NewReader(zipBytes), int64(len(zipBytes)))
	if err != nil {
		t.Fatal(err)
	}

	found := false
	for _, f := range zipReader.File {
		if f.Name == "index.html" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("zip does not contain index.html, files: %v", zipReader.File)
	}
}

func TestDownloadSessionNotFound(t *testing.T) {
	t.Parallel()

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

	resp, err := http.Get(srv.Origin() + "/api/sessions/nonexistent/download")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", resp.StatusCode)
	}
}

func TestBuildClearURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		rawURL    string
		removeKey string
		want      string
	}{
		{
			name:      "no query no fragment",
			rawURL:    "/",
			removeKey: "q",
			want:      "/",
		},
		{
			name:      "remove only param leaves root",
			rawURL:    "/?q=test",
			removeKey: "q",
			want:      "/",
		},
		{
			name:      "remove one param keeps others",
			rawURL:    "/?q=test&tag=foo",
			removeKey: "q",
			want:      "/?tag=foo",
		},
		{
			name:      "fragment preserved with no remaining params",
			rawURL:    "/?q=test#section",
			removeKey: "q",
			want:      "/#section",
		},
		{
			name:      "fragment preserved with remaining params",
			rawURL:    "/?q=test&tag=foo#section",
			removeKey: "q",
			want:      "/?tag=foo#section",
		},
		{
			name:      "fragment preserved when key not present",
			rawURL:    "/?tag=foo#section",
			removeKey: "q",
			want:      "/?tag=foo#section",
		},
		{
			name:      "fragment only no query params",
			rawURL:    "/#section",
			removeKey: "q",
			want:      "/#section",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			u, err := url.Parse(tt.rawURL)
			if err != nil {
				t.Fatal(err)
			}
			got := buildClearURL(u, tt.removeKey)
			if got != tt.want {
				t.Errorf("buildClearURL(%q, %q) = %q, want %q", tt.rawURL, tt.removeKey, got, tt.want)
			}
		})
	}
}
