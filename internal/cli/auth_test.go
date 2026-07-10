package cli

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

// TestSendAttachesAPIKeyHeader verifies that --api-key results in an
// Authorization: Bearer header on the outbound upload request.
func TestSendAttachesAPIKeyHeader(t *testing.T) {
	t.Parallel()

	fixtureHTML, err := filepath.Abs(filepath.Join("..", "..", "fixtures", "basic", "index.html"))
	if err != nil {
		t.Fatal(err)
	}

	var gotAuth atomic.Value // string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth.Store(r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"url":"http://example/s/x/"}`))
	}))
	defer srv.Close()

	var stdout, stderr strings.Builder
	err = Run([]string{"send", fixtureHTML, "--tag", "t", "--category", "c", "--project", "p", "--server", srv.URL, "--api-key", "sth_testkey123456"}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("send failed: %v (stderr: %s)", err, stderr.String())
	}

	got, _ := gotAuth.Load().(string)
	if got != "Bearer sth_testkey123456" {
		t.Fatalf("expected Authorization header 'Bearer sth_testkey123456', got %q", got)
	}
}

// TestSendAPIKeyFromEnv verifies STH_API_KEY is read when the flag is absent.
// Not parallel: t.Setenv mutates process-wide env.
func TestSendAPIKeyFromEnv(t *testing.T) {
	fixtureHTML, err := filepath.Abs(filepath.Join("..", "..", "fixtures", "basic", "index.html"))
	if err != nil {
		t.Fatal(err)
	}

	var gotAuth atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth.Store(r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"url":"http://example/s/x/"}`))
	}))
	defer srv.Close()

	t.Setenv("STH_API_KEY", "sth_envkey_ABCDEF")

	var stdout, stderr strings.Builder
	err = Run([]string{"send", fixtureHTML, "--tag", "t", "--category", "c", "--project", "p", "--server", srv.URL}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("send failed: %v (stderr: %s)", err, stderr.String())
	}

	got, _ := gotAuth.Load().(string)
	if got != "Bearer sth_envkey_ABCDEF" {
		t.Fatalf("expected env-derived Bearer header, got %q", got)
	}
}

// TestSendFlagOverridesEnvAPIKey verifies --api-key takes precedence over
// STH_API_KEY. Not parallel: t.Setenv mutates process-wide env.
func TestSendFlagOverridesEnvAPIKey(t *testing.T) {
	fixtureHTML, err := filepath.Abs(filepath.Join("..", "..", "fixtures", "basic", "index.html"))
	if err != nil {
		t.Fatal(err)
	}

	var gotAuth atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth.Store(r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"url":"http://example/s/x/"}`))
	}))
	defer srv.Close()

	t.Setenv("STH_API_KEY", "sth_envkey_should_lose")

	var stdout, stderr strings.Builder
	err = Run([]string{"send", fixtureHTML, "--tag", "t", "--category", "c", "--project", "p", "--server", srv.URL, "--api-key", "sth_flag_wins12345"}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("send failed: %v (stderr: %s)", err, stderr.String())
	}

	got, _ := gotAuth.Load().(string)
	if got != "Bearer sth_flag_wins12345" {
		t.Fatalf("expected flag to win, got %q", got)
	}
}

// TestSend401GivesActionableHint verifies a 401 from the server yields a
// message pointing the user at --api-key / STH_API_KEY.
func TestSend401GivesActionableHint(t *testing.T) {
	t.Parallel()

	fixtureHTML, err := filepath.Abs(filepath.Join("..", "..", "fixtures", "basic", "index.html"))
	if err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"unauthorized"}`))
	}))
	defer srv.Close()

	var stdout, stderr strings.Builder
	err = Run([]string{"send", fixtureHTML, "--tag", "t", "--category", "c", "--project", "p", "--server", srv.URL}, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected send to fail on 401")
	}
	if !strings.Contains(err.Error(), "--api-key") || !strings.Contains(err.Error(), "STH_API_KEY") {
		t.Fatalf("expected actionable 401 hint mentioning --api-key and STH_API_KEY, got: %v", err)
	}
}
