package server

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sun-praise/static-html/internal/session"
)

func TestNewRequiresStore(t *testing.T) {
	t.Parallel()

	_, err := New("127.0.0.1", 0, nil, "", 0, "")
	if err == nil {
		t.Fatal("expected nil store to be rejected")
	}
}

func TestServerNameOverridesOrigin(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	srv, err := New("127.0.0.1", 0, store, "192.168.2.14", 0, "")
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

	origin := srv.Origin()
	if !strings.HasPrefix(origin, "http://192.168.2.14:") {
		t.Fatalf("expected origin to use server-name 192.168.2.14, got %q", origin)
	}

	origins := srv.Origins()
	if len(origins) != 1 {
		t.Fatalf("expected 1 origin with server-name, got %d", len(origins))
	}
	if !strings.HasPrefix(origins[0], "http://192.168.2.14:") {
		t.Fatalf("expected origins[0] to use server-name 192.168.2.14, got %q", origins[0])
	}
}

func TestServerNameDomain(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	srv, err := New("127.0.0.1", 0, store, "myhost.local", 0, "")
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

	origin := srv.Origin()
	if !strings.HasPrefix(origin, "http://myhost.local:") {
		t.Fatalf("expected origin to use server-name myhost.local, got %q", origin)
	}
}

func TestServerNameEmptyFallsBack(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	srv, err := New("127.0.0.1", 0, store, "", 0, "")
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

	origin := srv.Origin()
	if !strings.HasPrefix(origin, "http://127.0.0.1:") {
		t.Fatalf("expected origin to default to 127.0.0.1, got %q", origin)
	}
}

func TestServerNameValidation(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)

	for _, invalid := range []string{
		"has space",
		"has/slash",
		"has:colon",
		"http://host",
		"https://host",
		"host@evil",
		"host#fragment",
		"host?query=1",
		"host%20name",
		"host\nnewline",
		"host\rtab",
		"host\ttab",
		"..",
		".start",
		"end.",
		"-hyphen",
		"hyphen-",
		"2001:db8::1",
		"::1",
		"fe80::1%eth0",
		"a..b",
		"host..domain.com",
	} {
		_, err := New("127.0.0.1", 0, store, invalid, 0, "")
		if err == nil {
			t.Errorf("expected serverName %q to be rejected", invalid)
		}
	}
}

func TestServerNameUsedInSessionURL(t *testing.T) {
	t.Parallel()

	fixtureHTML, err := filepath.Abs(filepath.Join("..", "..", "fixtures", "basic", "index.html"))
	if err != nil {
		t.Fatal(err)
	}

	store := newTestStore(t)
	srv, err := New("127.0.0.1", 0, store, "192.168.2.14", 0, "")
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

	srv.mu.RLock()
	actualAddr, ok := srv.listener.Addr().(*net.TCPAddr)
	if !ok {
		t.Fatalf("listener address is not TCP: %T", srv.listener.Addr())
	}
	srv.mu.RUnlock()
	actualURL := fmt.Sprintf("http://127.0.0.1:%d", actualAddr.Port)

	createResp, err := http.Post(actualURL+"/api/sessions", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer createResp.Body.Close()

	if createResp.StatusCode != http.StatusCreated {
		respBody, _ := io.ReadAll(createResp.Body)
		t.Fatalf("unexpected status: %d body=%s", createResp.StatusCode, respBody)
	}

	var payload struct {
		URL string `json:"url"`
	}
	if err := json.NewDecoder(createResp.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}

	expectedPrefix := "http://192.168.2.14:"
	if !strings.HasPrefix(payload.URL, expectedPrefix) {
		t.Fatalf("expected session URL to use serverName, got %q", payload.URL)
	}
}

func TestServerNameValidationAcceptsValid(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)

	for _, valid := range []string{
		"192.168.2.14",
		"myhost.local",
		"sub.domain.example.com",
		"host-with-dashes.local",
		"a",
		"localhost",
		"10.0.0.1",
		"255.255.255.255",
		"host123",
	} {
		_, err := New("127.0.0.1", 0, store, valid, 0, "")
		if err != nil {
			t.Errorf("expected serverName %q to be accepted, got error: %v", valid, err)
		}
	}
}


func TestServerBaseURLDefaultPort(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		serverName string
		port       int
		scheme     string
		want       string
	}{
		{"http port 80 omits port", "example.com", 80, "http", "http://example.com"},
		{"https port 443 omits port", "example.com", 443, "https", "https://example.com"},
		{"http port 8080 includes port", "example.com", 8080, "http", "http://example.com:8080"},
		{"https port 8443 includes port", "example.com", 8443, "https", "https://example.com:8443"},
		{"http port 3939 includes port", "192.168.2.14", 3939, "http", "http://192.168.2.14:3939"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := newTestStore(t)
			srv, err := New("127.0.0.1", tt.port, store, tt.serverName, 0, "")
			if err != nil {
				t.Fatal(err)
			}

			r := &http.Request{TLS: nil, Header: http.Header{}}
			if tt.scheme == "https" {
				r.Header.Set("X-Forwarded-Proto", "https")
			}

			got := srv.serverBaseURL(r)
			if got != tt.want {
				t.Errorf("serverBaseURL() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestServerBaseURLFallback(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	srv, err := New("127.0.0.1", 3939, store, "", 0, "")
	if err != nil {
		t.Fatal(err)
	}

	r := &http.Request{TLS: nil, Host: "127.0.0.1:3939", Header: http.Header{}}
	got := srv.serverBaseURL(r)
	if got != "http://127.0.0.1:3939" {
		t.Errorf("serverBaseURL() fallback = %q, want http://127.0.0.1:3939", got)
	}
}

func TestServerBaseURLForwardedProto(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	srv, err := New("127.0.0.1", 443, store, "secure.example.com", 0, "")
	if err != nil {
		t.Fatal(err)
	}

	r := &http.Request{TLS: nil, Host: "secure.example.com", Header: http.Header{}}
	r.Header.Set("X-Forwarded-Proto", "https")

	got := srv.serverBaseURL(r)
	if got != "https://secure.example.com" {
		t.Errorf("serverBaseURL() with X-Forwarded-Proto = %q, want https://secure.example.com", got)
	}
}

func TestServerBaseURLForwardedPort(t *testing.T) {
	t.Parallel()

	// Server listens on 3939 behind nginx on 443. X-Forwarded-Port: 443
	// must make the generated URL drop the port suffix.
	store := newTestStore(t)
	srv, err := New("127.0.0.1", 3939, store, "sth.example.com", 0, "")
	if err != nil {
		t.Fatal(err)
	}

	r := &http.Request{TLS: nil, Host: "sth.example.com", Header: http.Header{}}
	r.Header.Set("X-Forwarded-Proto", "https")
	r.Header.Set("X-Forwarded-Port", "443")

	got := srv.serverBaseURL(r)
	if got != "https://sth.example.com" {
		t.Errorf("serverBaseURL() with X-Forwarded-Port=443 = %q, want https://sth.example.com", got)
	}
}

func TestServerBaseURLServerPortFlagOverridesHeader(t *testing.T) {
	t.Parallel()

	// --server-port 8443 must override both X-Forwarded-Port and listener.
	store := newTestStore(t)
	srv, err := New("127.0.0.1", 3939, store, "sth.example.com", 8443, "")
	if err != nil {
		t.Fatal(err)
	}

	r := &http.Request{TLS: nil, Host: "sth.example.com", Header: http.Header{}}
	r.Header.Set("X-Forwarded-Proto", "https")
	r.Header.Set("X-Forwarded-Port", "443")

	got := srv.serverBaseURL(r)
	if got != "https://sth.example.com:8443" {
		t.Errorf("serverBaseURL() with --server-port=8443 = %q, want https://sth.example.com:8443", got)
	}
}

func TestServerBaseURLNoFlagNoHeaderFallsBackToListener(t *testing.T) {
	t.Parallel()

	// No flag, no header: listener port 3939 wins (current behavior preserved).
	store := newTestStore(t)
	srv, err := New("127.0.0.1", 3939, store, "sth.example.com", 0, "")
	if err != nil {
		t.Fatal(err)
	}

	r := &http.Request{TLS: nil, Host: "sth.example.com", Header: http.Header{}}

	got := srv.serverBaseURL(r)
	if got != "http://sth.example.com:3939" {
		t.Errorf("serverBaseURL() fallback = %q, want http://sth.example.com:3939", got)
	}
}

func TestServerBaseURLInvalidForwardedPortIgnored(t *testing.T) {
	t.Parallel()

	// Non-numeric / out-of-range X-Forwarded-Port must fall back silently.
	store := newTestStore(t)
	srv, err := New("127.0.0.1", 3939, store, "sth.example.com", 0, "")
	if err != nil {
		t.Fatal(err)
	}

	for _, raw := range []string{"abc", "0", "-1", "70000", ""} {
		r := &http.Request{TLS: nil, Host: "sth.example.com", Header: http.Header{}}
		if raw != "" {
			r.Header.Set("X-Forwarded-Port", raw)
		}

		got := srv.serverBaseURL(r)
		if got != "http://sth.example.com:3939" {
			t.Errorf("serverBaseURL() with X-Forwarded-Port=%q = %q, want fallback http://sth.example.com:3939", raw, got)
		}
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
	srv, err := New("127.0.0.1", 0, store, "", 0, "")
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
	srv, err := New("127.0.0.1", 0, store, "", 0, "")
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
	srv, err := New("127.0.0.1", 0, store, "", 0, "")
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

// TestUploadRootPlacesSessionUnderConfiguredDir verifies that when New() is
// given a non-empty uploadRoot, uploaded session snapshots land under that
// directory (not the platform default). This is the contract container
// deployments rely on to persist uploads across restarts.
func TestUploadRootPlacesSessionUnderConfiguredDir(t *testing.T) {
	t.Parallel()

	uploadRoot := t.TempDir()

	store := newTestStore(t)
	srv, err := New("127.0.0.1", 0, store, "", 0, uploadRoot)
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

	// Build a minimal archive with a single index.html.
	body := &bytes.Buffer{}
	formWriter := multipart.NewWriter(body)
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
	fileWriter, err := archiveWriter.Create("index.html")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(fileWriter, "<!doctype html><h1>root-test</h1>"); err != nil {
		t.Fatal(err)
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
		URL string `json:"url"`
	}
	if err := json.NewDecoder(createResp.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}

	// The upload root must now contain exactly one session-* directory.
	entries, err := os.ReadDir(uploadRoot)
	if err != nil {
		t.Fatal(err)
	}
	var sessionDir string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() && strings.HasPrefix(name, "session-") {
			if sessionDir != "" {
				t.Fatalf("expected exactly one session dir under upload root, found multiple (%q and %q)", sessionDir, name)
			}
			sessionDir = name
		}
	}
	if sessionDir == "" {
		t.Fatalf("expected a session-* directory under upload root %q, found none (entries=%v)", uploadRoot, entries)
	}

	// The uploaded index.html must live inside that session dir.
	servedEntry := filepath.Join(uploadRoot, sessionDir, "index.html")
	if _, err := os.Stat(servedEntry); err != nil {
		t.Fatalf("expected uploaded entry at %q: %v", servedEntry, err)
	}

	// And the preview URL must serve it.
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
		t.Fatalf("unexpected preview status: %d", htmlResp.StatusCode)
	}
	if !bytes.Contains(htmlBytes, []byte("root-test")) {
		t.Fatalf("preview html missing expected content: %s", string(htmlBytes))
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
	srv, err := New("127.0.0.1", 0, store, "", 0, "")
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
	srv, err := New("127.0.0.1", 0, store, "", 0, "")
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
	srv, err := New("127.0.0.1", 0, store, "", 0, "")
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
	srv, err := New("127.0.0.1", 0, store, "", 0, "")
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
	srv, err := New("127.0.0.1", 0, store, "", 0, "")
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
	srv, err := New("127.0.0.1", 0, store, "", 0, "")
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
	srv, err := New("127.0.0.1", 0, store, "", 0, "")
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
	srv, err := New("127.0.0.1", 0, store, "", 0, "")
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
		{
			name:      "fragment with special characters encoded safely",
			rawURL:    "/?q=test#sec%20tion",
			removeKey: "q",
			want:      "/#sec%20tion",
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

func TestBuildPageURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		rawURL string
		page   int
		want   string
	}{
		{
			name:   "basic page",
			rawURL: "/",
			page:   2,
			want:   "/?page=2",
		},
		{
			name:   "preserves existing params",
			rawURL: "/?q=test",
			page:   3,
			want:   "/?page=3&q=test",
		},
		{
			name:   "preserves fragment",
			rawURL: "/?q=test#section",
			page:   2,
			want:   "/?page=2&q=test#section",
		},
		{
			name:   "fragment only no query",
			rawURL: "/#section",
			page:   1,
			want:   "/?page=1#section",
		},
		{
			name:   "replaces existing page param",
			rawURL: "/?page=1&q=test",
			page:   5,
			want:   "/?page=5&q=test",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			u, err := url.Parse(tt.rawURL)
			if err != nil {
				t.Fatal(err)
			}
			got := buildPageURL(u, tt.page)
			if got != tt.want {
				t.Errorf("buildPageURL(%q, %d) = %q, want %q", tt.rawURL, tt.page, got, tt.want)
			}
		})
	}
}

// createSessionWithMetadata inserts a session with the given category/project
// directly into the store so peers tests can control metadata precisely.
func createSessionWithMetadata(t *testing.T, store *session.Store, entryFile, category, project string) string {
	t.Helper()

	s, err := store.Create(entryFile)
	if err != nil {
		t.Fatal(err)
	}
	if category != "" {
		if err := store.SetCategory(s.ID, category); err != nil {
			t.Fatal(err)
		}
	}
	if project != "" {
		if err := store.SetProject(s.ID, project); err != nil {
			t.Fatal(err)
		}
	}
	return s.ID
}

func TestGetPeersSuccess(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	s1 := createSessionWithMetadata(t, store, "/tmp/alpha.html", "tutorial", "my-site")
	s2 := createSessionWithMetadata(t, store, "/tmp/beta.html", "tutorial", "my-site")
	s3 := createSessionWithMetadata(t, store, "/tmp/gamma.html", "docs", "my-site")

	srv, err := New("127.0.0.1", 0, store, "", 0, "")
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

	resp, err := http.Get(srv.Origin() + "/api/sessions/" + s1 + "/peers")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected status 200, got %d body=%s", resp.StatusCode, body)
	}

	var result session.PeersResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}

	if result.Current.SessionID != s1 {
		t.Fatalf("expected current %q, got %q", s1, result.Current.SessionID)
	}
	if result.Current.Category != "tutorial" || result.Current.Project != "my-site" {
		t.Fatalf("unexpected current metadata: %+v", result.Current)
	}

	if len(result.ByCategory) != 1 || result.ByCategory[0].SessionID != s2 {
		t.Fatalf("expected byCategory=[%s], got %+v", s2, result.ByCategory)
	}

	catIDs := map[string]bool{}
	for _, p := range result.ByProject {
		catIDs[p.SessionID] = true
	}
	if len(result.ByProject) != 2 || !catIDs[s2] || !catIDs[s3] {
		t.Fatalf("expected byProject to contain %q and %q, got %+v", s2, s3, result.ByProject)
	}
}

func TestGetPeersEmpty(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	s1 := createSessionWithMetadata(t, store, "/tmp/alpha.html", "tutorial", "my-site")
	// A second session with completely different metadata.
	createSessionWithMetadata(t, store, "/tmp/beta.html", "docs", "other-app")

	srv, err := New("127.0.0.1", 0, store, "", 0, "")
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

	resp, err := http.Get(srv.Origin() + "/api/sessions/" + s1 + "/peers")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected status 200, got %d", resp.StatusCode)
	}

	var result session.PeersResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}

	if len(result.ByCategory) != 0 || len(result.ByProject) != 0 {
		t.Fatalf("expected empty peers, got %+v", result)
	}
}

func TestGetPeersNotFound(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)

	srv, err := New("127.0.0.1", 0, store, "", 0, "")
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

	resp, err := http.Get(srv.Origin() + "/api/sessions/nonexistent/peers")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected status 404, got %d", resp.StatusCode)
	}
}

// createChainedSession is the chain-aware analogue of createSessionWithMetadata:
// it creates a session, attaches metadata, then links it into the chain for
// (project, entryFile basename). Returns the session id.
func createChainedSession(t *testing.T, store *session.Store, entryFile, category, project string, tags ...string) string {
	t.Helper()

	s, err := store.CreateUploaded(entryFile, entryFile)
	if err != nil {
		t.Fatalf("CreateUploaded(%q): %v", entryFile, err)
	}
	if len(tags) > 0 {
		if err := store.AddTags(s.ID, tags...); err != nil {
			t.Fatalf("AddTags: %v", err)
		}
	}
	if category != "" {
		if err := store.SetCategory(s.ID, category); err != nil {
			t.Fatalf("SetCategory: %v", err)
		}
	}
	if project != "" {
		if err := store.SetProject(s.ID, project); err != nil {
			t.Fatalf("SetProject: %v", err)
		}
	}
	if _, _, err := store.LinkToChain(s.ID, project, entryFile, ""); err != nil {
		t.Fatalf("LinkToChain: %v", err)
	}
	return s.ID
}

func TestGetChainSuccess(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	s1 := createChainedSession(t, store, "/work/index.html", "c1", "proj", "a", "b")
	s2 := createChainedSession(t, store, "/work/index.html", "c2", "proj", "a", "c")
	s3 := createChainedSession(t, store, "/work/index.html", "c2", "proj", "a", "d", "e")

	srv, err := New("127.0.0.1", 0, store, "", 0, "")
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

	resp, err := http.Get(srv.Origin() + "/api/sessions/" + s3 + "/chain")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200, got %d body=%s", resp.StatusCode, body)
	}

	var result chainResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}

	if result.Chain.Project != "proj" || result.Chain.EntryFile != "index.html" {
		t.Fatalf("unexpected chain identity: %+v", result.Chain)
	}
	if result.Chain.VersionNum != 3 {
		t.Fatalf("expected 3 versions; got %d", result.Chain.VersionNum)
	}
	if len(result.Versions) != 3 {
		t.Fatalf("expected 3 version rows; got %d", len(result.Versions))
	}
	// Ordered ascending by version_no.
	want := []string{s1, s2, s3}
	for i, v := range result.Versions {
		if v.SessionID != want[i] {
			t.Fatalf("versions[%d] = %q, want %q", i, v.SessionID, want[i])
		}
		if v.VersionNo != i+1 {
			t.Fatalf("versions[%d].VersionNo = %d, want %d", i, v.VersionNo, i+1)
		}
	}
	if !result.Versions[2].Current {
		t.Fatalf("expected requested session %q to be marked current", s3)
	}
	if result.Current.SessionID != s3 {
		t.Fatalf("current mismatch: got %q want %q", result.Current.SessionID, s3)
	}

	if len(result.MetadataDiff) != 3 {
		t.Fatalf("expected 3 diff rows; got %d", len(result.MetadataDiff))
	}
	// v2 vs v1 should show category c1->c2.
	v2diff := result.MetadataDiff[1]
	if v2diff.CategoryOld != "c1" || v2diff.CategoryNew != "c2" {
		t.Fatalf("v2 category transition wrong: %+v", v2diff)
	}
}

func TestGetChainSingleVersion(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	// Create a session but DO NOT link it; the endpoint should still return
	// a synthesized single-version view.
	s, err := store.CreateUploaded("/work/once.html", "/work/once.html")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetProject(s.ID, "solo"); err != nil {
		t.Fatal(err)
	}

	srv, err := New("127.0.0.1", 0, store, "", 0, "")
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

	resp, err := http.Get(srv.Origin() + "/api/sessions/" + s.ID + "/chain")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200, got %d body=%s", resp.StatusCode, body)
	}

	var result chainResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if len(result.Versions) != 1 {
		t.Fatalf("expected synthesized 1-version view; got %d", len(result.Versions))
	}
	if !result.Versions[0].Current {
		t.Fatal("sole version should be flagged current")
	}
	if result.Versions[0].Project != "solo" {
		t.Fatalf("synthesized version should carry metadata; project=%q", result.Versions[0].Project)
	}
	// No chain row => no diffs.
	if len(result.MetadataDiff) != 0 {
		t.Fatalf("expected no diffs for unlinked session; got %d", len(result.MetadataDiff))
	}
}

func TestGetChainNotFound(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	srv, err := New("127.0.0.1", 0, store, "", 0, "")
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

	resp, err := http.Get(srv.Origin() + "/api/sessions/nonexistent/chain")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", resp.StatusCode)
	}
}

// TestGetChainSoftDeletedRequestReturns404 is the HTTP-level regression guard
// for the CodeRabbit finding: requesting the chain of a soft-deleted session
// must return 404, not a fabricated "current" version synthesized from the
// remaining live chain members.
func TestGetChainSoftDeletedRequestReturns404(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	target := createChainedSession(t, store, "/work/index.html", "c1", "proj", "a")
	createChainedSession(t, store, "/work/index.html", "c1", "proj", "a")
	createChainedSession(t, store, "/work/index.html", "c1", "proj", "a")

	// Sanity: the chain is multi-version before the delete.
	_, versions, err := store.GetChainOfSession(target)
	if err != nil {
		t.Fatalf("pre-delete GetChainOfSession: %v", err)
	}
	if len(versions) != 3 {
		t.Fatalf("expected 3 versions pre-delete; got %d", len(versions))
	}

	if err := store.SoftDelete(target); err != nil {
		t.Fatalf("SoftDelete: %v", err)
	}

	srv, err := New("127.0.0.1", 0, store, "", 0, "")
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

	resp, err := http.Get(srv.Origin() + "/api/sessions/" + target + "/chain")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 404 for soft-deleted session, got %d body=%s", resp.StatusCode, body)
	}
}

// createChainedSessionWithFile is createChainedSession but the entry HTML is a
// real on-disk file, so the diff endpoint can read actual content. The file
// is written under a fresh temp dir; the session's StoredEntryFile points at it.
func createChainedSessionWithFile(t *testing.T, store *session.Store, htmlContent, project, category string, tags ...string) string {
	t.Helper()

	dir := t.TempDir()
	path := filepath.Join(dir, "index.html")
	if err := os.WriteFile(path, []byte(htmlContent), 0o644); err != nil {
		t.Fatalf("write index.html: %v", err)
	}
	s, err := store.CreateUploaded("index.html", path)
	if err != nil {
		t.Fatalf("CreateUploaded: %v", err)
	}
	if len(tags) > 0 {
		if err := store.AddTags(s.ID, tags...); err != nil {
			t.Fatalf("AddTags: %v", err)
		}
	}
	if category != "" {
		if err := store.SetCategory(s.ID, category); err != nil {
			t.Fatalf("SetCategory: %v", err)
		}
	}
	if project != "" {
		if err := store.SetProject(s.ID, project); err != nil {
			t.Fatalf("SetProject: %v", err)
		}
	}
	if _, _, err := store.LinkToChain(s.ID, project, "index.html", ""); err != nil {
		t.Fatalf("LinkToChain: %v", err)
	}
	return s.ID
}

func TestGetDiffSuccess(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	// v1 and v2 share (project, entry file); v2 adds one line.
	createChainedSessionWithFile(t, store, "<html>\n<body>\n<h1>v1</h1>\n</body>\n</html>\n", "proj", "cat")
	v2 := createChainedSessionWithFile(t, store, "<html>\n<body>\n<h1>v1</h1>\n<p>v2 line</p>\n</body>\n</html>\n", "proj", "cat")

	srv, err := New("127.0.0.1", 0, store, "", 0, "")
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

	resp, err := http.Get(srv.Origin() + "/api/sessions/" + v2 + "/diff?from=1&to=2")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200, got %d body=%s", resp.StatusCode, body)
	}

	var result diffResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if result.FromVersion != 1 || result.ToVersion != 2 {
		t.Fatalf("version mismatch: from=%d to=%d", result.FromVersion, result.ToVersion)
	}
	if result.Summary.Added != 1 || result.Summary.Removed != 0 {
		t.Fatalf("expected +1 −0; got %+v", result.Summary)
	}
	if len(result.Lines) == 0 {
		t.Fatal("expected non-empty lines")
	}
	// The added line must carry the v2 paragraph text.
	var added diffLine
	for _, l := range result.Lines {
		if l.Kind == "add" {
			added = l
		}
	}
	if added.Text != "<p>v2 line</p>" {
		t.Fatalf("added line text = %q, want %q", added.Text, "<p>v2 line</p>")
	}
	if added.OldNo != 0 || added.NewNo == 0 {
		t.Fatalf("added line line numbers wrong: old=%d new=%d", added.OldNo, added.NewNo)
	}
}

func TestGetDiffMissingParams(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	v1 := createChainedSessionWithFile(t, store, "<p>x</p>\n", "proj", "cat")

	srv, err := New("127.0.0.1", 0, store, "", 0, "")
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

	// Missing 'to'.
	resp, err := http.Get(srv.Origin() + "/api/sessions/" + v1 + "/diff?from=1")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for missing 'to', got %d", resp.StatusCode)
	}

	// Non-numeric.
	resp, err = http.Get(srv.Origin() + "/api/sessions/" + v1 + "/diff?from=abc&to=1")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for non-numeric from, got %d", resp.StatusCode)
	}
}

func TestGetDiffVersionOutOfRange(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	v1 := createChainedSessionWithFile(t, store, "<p>x</p>\n", "proj", "cat")
	createChainedSessionWithFile(t, store, "<p>y</p>\n", "proj", "cat")

	srv, err := New("127.0.0.1", 0, store, "", 0, "")
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

	// Version 99 doesn't exist in this 2-version chain.
	resp, err := http.Get(srv.Origin() + "/api/sessions/" + v1 + "/diff?from=1&to=99")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for out-of-range version, got %d", resp.StatusCode)
	}
}

func TestGetDiffSameVersionRejected(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	v1 := createChainedSessionWithFile(t, store, "<p>x</p>\n", "proj", "cat")

	srv, err := New("127.0.0.1", 0, store, "", 0, "")
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

	resp, err := http.Get(srv.Origin() + "/api/sessions/" + v1 + "/diff?from=1&to=1")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for from==to, got %d", resp.StatusCode)
	}
}

func TestGetDiffNotFound(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	srv, err := New("127.0.0.1", 0, store, "", 0, "")
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

	resp, err := http.Get(srv.Origin() + "/api/sessions/nonexistent/diff?from=1&to=2")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", resp.StatusCode)
	}
}
