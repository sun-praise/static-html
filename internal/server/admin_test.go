package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sun-praise/static-html/internal/session"
)

// adminJSON performs an authenticated admin API request with an optional JSON
// body against the server's full handler chain (auth middleware + routes).
func adminJSON(t *testing.T, handler http.Handler, method, path, bearer string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var reqBody *bytes.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reqBody = bytes.NewReader(raw)
	} else {
		reqBody = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, reqBody)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

// decodeAdminMap parses a JSON object response body.
func decodeAdminMap(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("response is not a JSON object (%d): %s", rec.Code, rec.Body.String())
	}
	return out
}

// provisionAdminUsers creates an admin (root) and a member (member) with one
// API key each, returning both plaintext keys.
func provisionAdminUsers(t *testing.T, store *session.Store) (adminKey, memberKey string) {
	t.Helper()
	if _, err := store.CreateUser("root"); err != nil {
		t.Fatal(err)
	}
	if err := store.SetUserRole("root", session.RoleAdmin); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateUser("member"); err != nil {
		t.Fatal(err)
	}
	root, err := store.FindUserByUsername("root")
	if err != nil {
		t.Fatal(err)
	}
	member, err := store.FindUserByUsername("member")
	if err != nil {
		t.Fatal(err)
	}
	adminKey, _, err = store.IssueAPIKey(root.ID)
	if err != nil {
		t.Fatal(err)
	}
	memberKey, _, err = store.IssueAPIKey(member.ID)
	if err != nil {
		t.Fatal(err)
	}
	return adminKey, memberKey
}

// TestAdminAPI_NoKeyUnauthorized: without a key every admin endpoint answers
// 401 (mutations at the middleware, reads at the handler).
func TestAdminAPI_NoKeyUnauthorized(t *testing.T) {
	t.Parallel()
	srv, _ := newAuthServer(t, true, false)
	handler := srv.httpServer.Handler

	for _, tc := range []struct{ method, path string }{
		{http.MethodGet, "/api/admin/users"},
		{http.MethodGet, "/api/admin/audit"},
		{http.MethodPost, "/api/admin/users"},
		{http.MethodDelete, "/api/admin/keys/whatever"},
	} {
		rec := adminJSON(t, handler, tc.method, tc.path, "", nil)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("%s %s without key: expected 401, got %d (%s)", tc.method, tc.path, rec.Code, rec.Body.String())
		}
	}
}

// TestAdminAPI_MemberForbidden: a member key passes auth but is denied by the
// admin gate on every endpoint, and no mutation lands.
func TestAdminAPI_MemberForbidden(t *testing.T) {
	t.Parallel()
	srv, store := newAuthServer(t, true, false)
	handler := srv.httpServer.Handler
	_, memberKey := provisionAdminUsers(t, store)

	for _, tc := range []struct {
		method, path string
		body         any
	}{
		{http.MethodGet, "/api/admin/users", nil},
		{http.MethodGet, "/api/admin/audit", nil},
		{http.MethodPost, "/api/admin/users", map[string]any{"username": "intruder"}},
		{http.MethodPut, "/api/admin/users/root/role", map[string]any{"role": ""}},
		{http.MethodPost, "/api/admin/users/member/keys", nil},
		{http.MethodDelete, "/api/admin/keys/whatever", nil},
		{http.MethodPost, "/api/admin/audit/prune", map[string]any{"days": 30}},
	} {
		rec := adminJSON(t, handler, tc.method, tc.path, memberKey, tc.body)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("%s %s as member: expected 403, got %d (%s)", tc.method, tc.path, rec.Code, rec.Body.String())
		}
		if !bytes.Contains(rec.Body.Bytes(), []byte("admin role required")) {
			t.Fatalf("%s %s as member: missing denial reason: %s", tc.method, tc.path, rec.Body.String())
		}
	}

	if _, err := store.FindUserByUsername("intruder"); err == nil {
		t.Fatal("member key must not be able to create users")
	}
}

// TestAdminAPI_AdminLifecycle walks the whole user/key surface with an admin
// key: list, create (incl. admin flag and conflict), role changes, key issue
// and revoke, plus the error shapes (404 unknown user, 400 bad role).
func TestAdminAPI_AdminLifecycle(t *testing.T) {
	t.Parallel()
	srv, store := newAuthServer(t, true, false)
	handler := srv.httpServer.Handler
	adminKey, _ := provisionAdminUsers(t, store)

	// List: root is admin with exactly one active key.
	rec := adminJSON(t, handler, http.MethodGet, "/api/admin/users", adminKey, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list users: expected 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	users := decodeAdminMap(t, rec)["users"].([]any)
	if len(users) != 2 {
		t.Fatalf("expected 2 users, got %s", rec.Body.String())
	}
	byName := map[string]map[string]any{}
	for _, raw := range users {
		u := raw.(map[string]any)
		byName[u["username"].(string)] = u
	}
	rootUser := byName["root"]
	if rootUser == nil {
		t.Fatalf("root missing from user list: %s", rec.Body.String())
	}
	if rootUser["role"] != session.RoleAdmin {
		t.Fatalf("unexpected root user entry: %v", rootUser)
	}
	if rootUser["userId"] == "" || rootUser["created"] == "" {
		t.Fatalf("root entry must carry userId and created: %v", rootUser)
	}
	if n, _ := rootUser["activeKeys"].(float64); n != 1 {
		t.Fatalf("root should have 1 active key, got %v", rootUser["activeKeys"])
	}

	// Create bob as admin.
	rec = adminJSON(t, handler, http.MethodPost, "/api/admin/users", adminKey, map[string]any{"username": "bob", "admin": true})
	if rec.Code != http.StatusOK {
		t.Fatalf("create bob: expected 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	created := decodeAdminMap(t, rec)
	if created["role"] != session.RoleAdmin {
		t.Fatalf("bob should be admin, got %s", rec.Body.String())
	}
	bob, err := store.FindUserByUsername("bob")
	if err != nil {
		t.Fatalf("bob must exist in the store: %v", err)
	}
	if admin, err := store.IsAdmin(bob.ID); err != nil || !admin {
		t.Fatalf("bob must be admin in the store (admin=%v err=%v)", admin, err)
	}

	// Duplicate username -> 409; empty username -> 400.
	rec = adminJSON(t, handler, http.MethodPost, "/api/admin/users", adminKey, map[string]any{"username": "bob"})
	if rec.Code != http.StatusConflict {
		t.Fatalf("duplicate username: expected 409, got %d (%s)", rec.Code, rec.Body.String())
	}
	rec = adminJSON(t, handler, http.MethodPost, "/api/admin/users", adminKey, map[string]any{"username": "  "})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("empty username: expected 400, got %d (%s)", rec.Code, rec.Body.String())
	}

	// Role changes: unknown user 404, invalid role 400, demote bob to member.
	rec = adminJSON(t, handler, http.MethodPut, "/api/admin/users/ghost/role", adminKey, map[string]any{"role": session.RoleAdmin})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown user role change: expected 404, got %d (%s)", rec.Code, rec.Body.String())
	}
	rec = adminJSON(t, handler, http.MethodPut, "/api/admin/users/bob/role", adminKey, map[string]any{"role": "moderator"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid role: expected 400, got %d (%s)", rec.Code, rec.Body.String())
	}
	rec = adminJSON(t, handler, http.MethodPut, "/api/admin/users/bob/role", adminKey, map[string]any{"role": ""})
	if rec.Code != http.StatusOK {
		t.Fatalf("demote bob: expected 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	if admin, err := store.IsAdmin(bob.ID); err != nil || admin {
		t.Fatalf("bob must be a plain member after demotion (admin=%v err=%v)", admin, err)
	}

	// Issue a key for bob; the plaintext verifies in the store.
	rec = adminJSON(t, handler, http.MethodPost, "/api/admin/users/bob/keys", adminKey, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("issue key: expected 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	issued := decodeAdminMap(t, rec)
	if issued["keyId"] == "" || issued["apiKey"] == "" {
		t.Fatalf("issued key must carry keyId and apiKey: %s", rec.Body.String())
	}
	keyID, _ := issued["keyId"].(string)
	plaintext, _ := issued["apiKey"].(string)
	if uid, ok, err := store.VerifyAPIKey(plaintext); err != nil || !ok || uid != bob.ID {
		t.Fatalf("issued key must verify for bob (uid=%q ok=%v err=%v)", uid, ok, err)
	}

	// Unknown user key issue -> 404.
	rec = adminJSON(t, handler, http.MethodPost, "/api/admin/users/ghost/keys", adminKey, nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("issue key for ghost: expected 404, got %d (%s)", rec.Code, rec.Body.String())
	}

	// Revoke by id, then the key stops verifying; revoking again is 404.
	rec = adminJSON(t, handler, http.MethodDelete, "/api/admin/keys/"+keyID, adminKey, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("revoke key: expected 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	if _, ok, _ := store.VerifyAPIKey(plaintext); ok {
		t.Fatal("revoked key must not verify")
	}
	rec = adminJSON(t, handler, http.MethodDelete, "/api/admin/keys/"+keyID, adminKey, nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("revoke again: expected 404, got %d (%s)", rec.Code, rec.Body.String())
	}
}

// TestAdminAPI_AuditListAndPrune: the audit endpoints list the mutations the
// other admin endpoints recorded and validate their filters; pruning accepts
// only positive day counts.
func TestAdminAPI_AuditListAndPrune(t *testing.T) {
	t.Parallel()
	srv, store := newAuthServer(t, true, false)
	handler := srv.httpServer.Handler
	adminKey, _ := provisionAdminUsers(t, store)

	// Generate events through the API itself.
	adminJSON(t, handler, http.MethodPost, "/api/admin/users", adminKey, map[string]any{"username": "bob", "admin": true})
	rec := adminJSON(t, handler, http.MethodPost, "/api/admin/users/bob/keys", adminKey, nil)
	issued := decodeAdminMap(t, rec)
	keyID, _ := issued["keyId"].(string)
	adminJSON(t, handler, http.MethodDelete, "/api/admin/keys/"+keyID, adminKey, nil)

	// Filter by action=user.add: exactly bob, acted by root.
	rec = adminJSON(t, handler, http.MethodGet, "/api/admin/audit?action=user.add", adminKey, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("audit list: expected 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	events := decodeAdminMap(t, rec)["events"].([]any)
	if len(events) != 1 {
		t.Fatalf("expected 1 user.add event, got %s", rec.Body.String())
	}
	ev := events[0].(map[string]any)
	if ev["action"] != "user.add" || ev["target"] != "bob" || ev["actor"] != "root" {
		t.Fatalf("unexpected audit event: %v", ev)
	}

	// Unfiltered list has all three mutations.
	rec = adminJSON(t, handler, http.MethodGet, "/api/admin/audit", adminKey, nil)
	events = decodeAdminMap(t, rec)["events"].([]any)
	if len(events) != 3 {
		t.Fatalf("expected 3 audit events, got %d", len(events))
	}

	// Bad filters are rejected.
	for _, bad := range []string{
		"/api/admin/audit?limit=zero",
		"/api/admin/audit?limit=0",
		"/api/admin/audit?offset=-1",
		"/api/admin/audit?since=19-01-01",
	} {
		rec = adminJSON(t, handler, http.MethodGet, bad, adminKey, nil)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("audit filter %q: expected 400, got %d (%s)", bad, rec.Code, rec.Body.String())
		}
	}

	// Prune: zero/negative days are rejected; a valid call answers 200.
	rec = adminJSON(t, handler, http.MethodPost, "/api/admin/audit/prune", adminKey, map[string]any{"days": 0})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("prune days=0: expected 400, got %d (%s)", rec.Code, rec.Body.String())
	}
	rec = adminJSON(t, handler, http.MethodPost, "/api/admin/audit/prune", adminKey, map[string]any{"days": 30})
	if rec.Code != http.StatusOK {
		t.Fatalf("prune days=30: expected 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	if _, ok := decodeAdminMap(t, rec)["pruned"]; !ok {
		t.Fatalf("prune response must carry pruned: %s", rec.Body.String())
	}
}
