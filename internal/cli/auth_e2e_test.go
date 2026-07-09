package cli

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sun-praise/static-html/internal/server"
	"github.com/sun-praise/static-html/internal/session"
)

// authE2EFixture spins up a real sth server (auth on or off) sharing a
// file-backed SQLite DB with the CLI, returning everything the test needs.
type authE2EFixture struct {
	t          *testing.T
	srv        *server.Server
	dbPath     string
	uploadRoot string
	origin     string
}

func newAuthE2EFixture(t *testing.T, authEnabled, protectPreviews bool) *authE2EFixture {
	t.Helper()

	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "sessions.db")
	uploadRoot := filepath.Join(tmp, "uploads")

	// Server-side store. Open, then close so the CLI/server share the same
	// file via separate connections (SQLite WAL allows this).
	bootstrap, err := session.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("bootstrap store: %v", err)
	}
	if err := bootstrap.Close(); err != nil {
		t.Fatalf("close bootstrap store: %v", err)
	}

	srv, err := server.New("127.0.0.1", 0, mustReopen(t, dbPath), "", 0, uploadRoot)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	if protectPreviews {
		srv.SetProtectPreviews(true)
	} else if authEnabled {
		srv.SetAuthEnabled(true)
	}
	if err := srv.Start(); err != nil {
		t.Fatalf("start server: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = srv.Stop(ctx)
	})

	return &authE2EFixture{
		t:          t,
		srv:        srv,
		dbPath:     dbPath,
		uploadRoot: uploadRoot,
		origin:     srv.Origin(),
	}
}

func mustReopen(t *testing.T, dbPath string) *session.Store {
	t.Helper()
	store, err := session.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

// cliUser runs `sth user <sub> ...` against the fixture's DB and returns
// trimmed stdout (panics the test on error).
func (f *authE2EFixture) cliUser(args ...string) string {
	f.t.Helper()
	full := append([]string{"user"}, args...)
	full = append(full, "--db", f.dbPath)
	var stdout, stderr bytes.Buffer
	if err := Run(full, &stdout, &stderr); err != nil {
		f.t.Fatalf("sth %v: %v (stderr: %s)", full, err, stderr.String())
	}
	return stdout.String()
}

// cliSend runs `sth send` and returns (stdout, err).
func (f *authE2EFixture) cliSend(file, apiKey string) (string, error) {
	f.t.Helper()
	args := []string{"send", file, "--tag", "t", "--category", "c", "--project", "p", "--server", f.origin}
	if apiKey != "" {
		args = append(args, "--api-key", apiKey)
	}
	var stdout, stderr bytes.Buffer
	err := Run(args, &stdout, &stderr)
	return stdout.String(), err
}

// httpDo issues a request with an optional Bearer key and returns status +
// body.
func (f *authE2EFixture) httpDo(method, path, bearer string) (int, string) {
	f.t.Helper()
	req, err := http.NewRequest(method, f.origin+path, nil)
	if err != nil {
		f.t.Fatalf("new request: %v", err)
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		f.t.Fatalf("do request %s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body)
}

// parseSendURL extracts the "/s/<id>/" URL printed by sth send.
func parseSendURL(t *testing.T, out string) string {
	t.Helper()
	idx := strings.Index(out, "/s/")
	if idx < 0 {
		t.Fatalf("send output missing /s/ URL: %q", out)
	}
	rest := out[idx:]
	// trim trailing whitespace/newline
	return strings.TrimSpace(rest)
}

// TestE2E_AuthFullFlow exercises the whole stack end-to-end:
//   - auth off: send works without a key
//   - auth on: send without key is rejected; with key works
//   - owner isolation: alice cannot read bob's session over HTTP
//   - preview stays open under --auth
func TestE2E_AuthFullFlow(t *testing.T) {
	t.Parallel()

	fixtureHTML, err := filepath.Abs(filepath.Join("..", "..", "fixtures", "basic", "index.html"))
	if err != nil {
		t.Fatal(err)
	}

	// --- Stage 1: auth OFF, legacy behavior preserved ---
	fx := newAuthE2EFixture(t, false, false)

	out, err := fx.cliSend(fixtureHTML, "")
	if err != nil {
		t.Fatalf("auth-off send failed: %v", err)
	}
	anonURL := parseSendURL(t, out)

	// Preview reachable without auth.
	if code, _ := fx.httpDo(http.MethodGet, anonURL, ""); code != http.StatusOK {
		t.Fatalf("auth-off preview: expected 200, got %d", code)
	}
	fx.t.Log("stage 1 (auth off) OK")

	// --- Stage 2: auth ON ---
	fx2 := newAuthE2EFixture(t, true, false)

	// Create alice + key via the real CLI.
	fx2.cliUser("add", "alice")
	issueOut := fx2.cliUser("issue-key", "alice")
	aliceKey := extractKey(t, issueOut)

	// Send WITHOUT a key → the CLI surfaces a 401 hint.
	_, err = fx2.cliSend(fixtureHTML, "")
	if err == nil {
		t.Fatal("expected send without key to fail under auth")
	}
	if !strings.Contains(err.Error(), "--api-key") {
		t.Fatalf("expected 401 hint mentioning --api-key, got: %v", err)
	}

	// Send WITH alice's key → succeeds.
	out, err = fx2.cliSend(fixtureHTML, aliceKey)
	if err != nil {
		t.Fatalf("send with alice key failed: %v", err)
	}
	aliceSessionURL := parseSendURL(t, out)

	// Alice can fetch her own metadata over HTTP.
	if code, _ := fx2.httpDo(http.MethodGet, sessionMetaPath(aliceSessionURL), aliceKey); code == http.StatusUnauthorized || code == http.StatusForbidden {
		t.Fatalf("alice should access her own metadata, got %d", code)
	}
	fx2.t.Log("stage 2 (auth on, alice) OK")

	// --- Stage 3: owner isolation ---
	// Create bob + key; bob cannot access alice's session.
	fx2.cliUser("add", "bob")
	bobKey := extractKey(t, fx2.cliUser("issue-key", "bob"))

	aliceID := sessionIDFromURL(t, aliceSessionURL)

	code, _ := fx2.httpDo(http.MethodGet, "/api/sessions/"+aliceID+"/metadata", bobKey)
	if code != http.StatusForbidden {
		t.Fatalf("bob reading alice's metadata: expected 403, got %d", code)
	}
	// Bob cannot delete alice's session (DELETE /api/sessions/<id>).
	code, _ = fx2.httpDo(http.MethodDelete, "/api/sessions/"+aliceID, bobKey)
	if code != http.StatusForbidden {
		t.Fatalf("bob deleting alice's session: expected 403, got %d", code)
	}
	// Alice can still delete her own (proves it was ownership, not a 404).
	code, _ = fx2.httpDo(http.MethodDelete, "/api/sessions/"+aliceID, aliceKey)
	if code != http.StatusOK {
		t.Fatalf("alice deleting own session: expected 200, got %d", code)
	}
	fx2.t.Log("stage 3 (owner isolation) OK")
}

// TestE2E_PreviewProtection: under --protect-previews, the preview endpoint
// requires a key (and any valid key works, not just the owner's).
func TestE2E_PreviewProtection(t *testing.T) {
	t.Parallel()

	fixtureHTML, err := filepath.Abs(filepath.Join("..", "..", "fixtures", "basic", "index.html"))
	if err != nil {
		t.Fatal(err)
	}

	fx := newAuthE2EFixture(t, false, true) // protect implies auth
	fx.cliUser("add", "alice")
	aliceKey := extractKey(t, fx.cliUser("issue-key", "alice"))

	out, err := fx.cliSend(fixtureHTML, aliceKey)
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	previewURL := parseSendURL(t, out)

	// Preview without a key → 401.
	if code, _ := fx.httpDo(http.MethodGet, previewURL, ""); code != http.StatusUnauthorized {
		t.Fatalf("protected preview without key: expected 401, got %d", code)
	}

	// Preview with a valid key → passes (API-key-only, no owner gate).
	if code, _ := fx.httpDo(http.MethodGet, previewURL, aliceKey); code == http.StatusUnauthorized {
		t.Fatalf("protected preview with valid key: expected to pass, got 401")
	}

	// A different user's key also passes (preview is not owner-scoped).
	fx.cliUser("add", "carol")
	carolKey := extractKey(t, fx.cliUser("issue-key", "carol"))
	if code, _ := fx.httpDo(http.MethodGet, previewURL, carolKey); code == http.StatusUnauthorized {
		t.Fatalf("protected preview with carol's key: expected to pass, got 401")
	}
}

// TestE2E_UserRevokeKey: a revoked key immediately stops working.
func TestE2E_UserRevokeKey(t *testing.T) {
	t.Parallel()

	fx := newAuthE2EFixture(t, true, false)
	fx.cliUser("add", "alice")
	issueOut := fx.cliUser("issue-key", "alice")
	aliceKey := extractKey(t, issueOut)

	// Key works.
	if code, _ := fx.httpDo(http.MethodGet, "/", aliceKey); code != http.StatusOK {
		t.Fatalf("alice key before revoke: expected 200, got %d", code)
	}

	// Revoke via prefix (first chars of the key).
	prefix := aliceKey[:len("sth_")+8]
	fx.cliUser("revoke-key", prefix)

	// Key now rejected.
	if code, _ := fx.httpDo(http.MethodGet, "/", aliceKey); code != http.StatusUnauthorized {
		t.Fatalf("alice key after revoke: expected 401, got %d", code)
	}
}

// TestE2E_HomePageOwnerScoping: alice only sees her own sessions on the home
// page, never bob's.
func TestE2E_HomePageOwnerScoping(t *testing.T) {
	t.Parallel()

	fixtureHTML, err := filepath.Abs(filepath.Join("..", "..", "fixtures", "basic", "index.html"))
	if err != nil {
		t.Fatal(err)
	}

	fx := newAuthE2EFixture(t, true, false)
	fx.cliUser("add", "alice")
	fx.cliUser("add", "bob")
	aliceKey := extractKey(t, fx.cliUser("issue-key", "alice"))
	bobKey := extractKey(t, fx.cliUser("issue-key", "bob"))

	aliceOut, err := fx.cliSend(fixtureHTML, aliceKey)
	if err != nil {
		t.Fatalf("alice send: %v", err)
	}
	bobOut, err := fx.cliSend(fixtureHTML, bobKey)
	if err != nil {
		t.Fatalf("bob send: %v", err)
	}
	aliceID := sessionIDFromURL(t, aliceOut)
	bobID := sessionIDFromURL(t, bobOut)

	// Alice's home page contains her session id but not bob's.
	_, aliceHome := fx.httpDo(http.MethodGet, "/", aliceKey)
	if !strings.Contains(aliceHome, aliceID) {
		t.Fatalf("alice home missing her own session %s", aliceID)
	}
	if strings.Contains(aliceHome, bobID) {
		t.Fatalf("alice home must not show bob's session %s", bobID)
	}
}

// extractKey pulls the "sth_..." key from `sth user issue-key` stdout, which
// prints the key on its own indented line.
func extractKey(t *testing.T, stdout string) string {
	t.Helper()
	for _, line := range strings.Split(stdout, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "sth_") {
			return line
		}
	}
	t.Fatalf("no API key found in issue-key output: %q", stdout)
	return ""
}

// sessionMetaPath turns a "/s/<id>/" URL into "/api/sessions/<id>/metadata".
func sessionMetaPath(sessionURL string) string {
	id := strings.TrimPrefix(sessionURL, "/s/")
	id = strings.TrimSuffix(id, "/")
	return "/api/sessions/" + id + "/metadata"
}

func sessionIDFromURL(t *testing.T, out string) string {
	t.Helper()
	url := parseSendURL(t, out)
	id := strings.TrimPrefix(url, "/s/")
	id = strings.TrimSuffix(id, "/")
	if id == "" {
		t.Fatalf("empty session id from %q", out)
	}
	return id
}
