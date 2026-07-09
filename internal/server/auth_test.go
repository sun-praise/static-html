package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sun-praise/static-html/internal/session"
)

// newAuthServer builds a server with an in-memory store and the requested auth
// posture, returning the server and its store so tests can provision users/keys
// and inspect sessions.
func newAuthServer(t *testing.T, authEnabled, protectPreviews bool) (*Server, *session.Store) {
	t.Helper()
	store, err := session.NewInMemoryStore()
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	srv, err := New("127.0.0.1", 0, store, "", 0, "")
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	if protectPreviews {
		srv.SetProtectPreviews(true)
	} else if authEnabled {
		srv.SetAuthEnabled(true)
	}
	return srv, store
}

func doReq(t *testing.T, handler http.Handler, method, path, bearer string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

// TestAuthMiddleware_OffIsPassthrough: when auth is disabled, every request
// reaches the handler unchanged (backward compatibility).
func TestAuthMiddleware_OffIsPassthrough(t *testing.T) {
	t.Parallel()
	srv, _ := newAuthServer(t, false, false)

	rec := doReq(t, srv.httpServer.Handler, http.MethodGet, "/", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 with auth off, got %d", rec.Code)
	}
}

// TestAuthMiddleware_MissingCredsRejected: a protected path without a key
// returns 401.
func TestAuthMiddleware_MissingCredsRejected(t *testing.T) {
	t.Parallel()
	srv, _ := newAuthServer(t, true, false)

	rec := doReq(t, srv.httpServer.Handler, http.MethodGet, "/", "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without creds, got %d", rec.Code)
	}
}

// TestAuthMiddleware_InvalidKeyRejected.
func TestAuthMiddleware_InvalidKeyRejected(t *testing.T) {
	t.Parallel()
	srv, _ := newAuthServer(t, true, false)

	rec := doReq(t, srv.httpServer.Handler, http.MethodGet, "/", "sth_definitely_not_real")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for invalid key, got %d", rec.Code)
	}
}

// TestAuthMiddleware_ValidKeyPasses: a valid key reaches the handler.
func TestAuthMiddleware_ValidKeyPasses(t *testing.T) {
	t.Parallel()
	srv, store := newAuthServer(t, true, false)

	user, err := store.CreateUser("alice")
	if err != nil {
		t.Fatal(err)
	}
	key, _, err := store.IssueAPIKey(user.ID)
	if err != nil {
		t.Fatal(err)
	}

	rec := doReq(t, srv.httpServer.Handler, http.MethodGet, "/", key)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 with valid key, got %d (body: %s)", rec.Code, rec.Body.String())
	}
}

// TestAuthMiddleware_RevokedKeyRejected.
func TestAuthMiddleware_RevokedKeyRejected(t *testing.T) {
	t.Parallel()
	srv, store := newAuthServer(t, true, false)

	user, err := store.CreateUser("alice")
	if err != nil {
		t.Fatal(err)
	}
	key, rec, err := store.IssueAPIKey(user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.RevokeAPIKey(rec.ID); err != nil {
		t.Fatal(err)
	}

	r := doReq(t, srv.httpServer.Handler, http.MethodGet, "/", key)
	if r.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for revoked key, got %d", r.Code)
	}
}

// TestAuthMiddleware_PreviewOpenUnlessProtected: with auth on but
// protectPreviews off, /s/<id>/ is reachable without a key. With
// protectPreviews on, it requires a key.
func TestAuthMiddleware_PreviewOpenUnlessProtected(t *testing.T) {
	t.Parallel()

	// Open preview under --auth (default preview posture).
	srv1, store1 := newAuthServer(t, true, false)
	s1, err := store1.Create("index.html")
	if err != nil {
		t.Fatal(err)
	}
	rec := doReq(t, srv1.httpServer.Handler, http.MethodGet, "/s/"+s1.ID+"/", "")
	if rec.Code == http.StatusUnauthorized {
		t.Fatalf("preview should be open under --auth, got 401")
	}

	// Protected preview under --protect-previews.
	srv2, store2 := newAuthServer(t, false, true) // protect implies auth
	s2, err := store2.Create("index.html")
	if err != nil {
		t.Fatal(err)
	}
	noKey := doReq(t, srv2.httpServer.Handler, http.MethodGet, "/s/"+s2.ID+"/", "")
	if noKey.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for preview under --protect-previews without key, got %d", noKey.Code)
	}
	user, err := store2.CreateUser("alice")
	if err != nil {
		t.Fatal(err)
	}
	key, _, err := store2.IssueAPIKey(user.ID)
	if err != nil {
		t.Fatal(err)
	}
	withKey := doReq(t, srv2.httpServer.Handler, http.MethodGet, "/s/"+s2.ID+"/", key)
	if withKey.Code == http.StatusUnauthorized {
		t.Fatalf("preview with valid key should pass, got 401")
	}
}

// TestProtectPreviews_ImpliesAuth: setting protectPreviews forces auth on.
func TestProtectPreviews_ImpliesAuth(t *testing.T) {
	t.Parallel()
	srv, _ := newAuthServer(t, false, true)

	if !srv.AuthEnabled() {
		t.Fatal("protectPreviews should imply authEnabled")
	}
	if !srv.ProtectPreviews() {
		t.Fatal("protectPreviews should be on")
	}
}

// TestRequireOwner_AllowsOwnerDeniesOther: authenticated owner passes; a
// different authenticated user gets 403; auth-off passes through.
func TestRequireOwner(t *testing.T) {
	t.Parallel()
	srv, store := newAuthServer(t, true, false)

	alice, err := store.CreateUser("alice")
	if err != nil {
		t.Fatal(err)
	}
	bob, err := store.CreateUser("bob")
	if err != nil {
		t.Fatal(err)
	}
	aliceKey, _, err := store.IssueAPIKey(alice.ID)
	if err != nil {
		t.Fatal(err)
	}
	bobKey, _, err := store.IssueAPIKey(bob.ID)
	if err != nil {
		t.Fatal(err)
	}

	// Alice owns a session.
	owned, err := store.Create("index.html")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetSessionOwner(owned.ID, alice.ID); err != nil {
		t.Fatal(err)
	}

	// Alice can access metadata (a read endpoint, owner-scoped by handler).
	recAlice := doReq(t, srv.httpServer.Handler, http.MethodGet, "/api/sessions/"+owned.ID+"/metadata", aliceKey)
	if recAlice.Code == http.StatusForbidden {
		t.Fatalf("owner should not be forbidden, got 403 (body: %s)", recAlice.Body.String())
	}

	// Bob is forbidden on the same session's metadata.
	recBob := doReq(t, srv.httpServer.Handler, http.MethodGet, "/api/sessions/"+owned.ID+"/metadata", bobKey)
	if recBob.Code != http.StatusForbidden {
		t.Fatalf("non-owner should be forbidden, got %d", recBob.Code)
	}
}

// TestRequireOwner_AuthOffPermissive: with auth disabled, requireOwner is a
// no-op (returns true), preserving legacy behavior.
func TestRequireOwner_AuthOffPermissive(t *testing.T) {
	t.Parallel()
	srv, store := newAuthServer(t, false, false)

	owned, err := store.Create("index.html")
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/sessions/"+owned.ID+"/metadata", nil)
	rec := httptest.NewRecorder()
	if !srv.requireOwner(rec, req, owned.ID) {
		t.Fatal("requireOwner should pass through when auth is off")
	}
}

// TestRequireOwner_MissingSessionIs404Not403: under auth, a session that does
// not exist must return 404 (not 403), so a client can distinguish "not found"
// from "exists but not yours".
func TestRequireOwner_MissingSessionIs404Not403(t *testing.T) {
	t.Parallel()
	srv, store := newAuthServer(t, true, false)

	user, err := store.CreateUser("alice")
	if err != nil {
		t.Fatal(err)
	}
	key, _, err := store.IssueAPIKey(user.ID)
	if err != nil {
		t.Fatal(err)
	}

	rec := doReq(t, srv.httpServer.Handler, http.MethodGet, "/api/sessions/does-not-exist/metadata", key)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for missing session under auth, got %d", rec.Code)
	}
}
