package server

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/sun-praise/static-html/internal/session"
)

// TestHandleMe covers GET /api/me: the identity shape for admin and member
// keys, rejection without credentials, and the auth-disabled posture.
func TestHandleMe(t *testing.T) {
	srv, store := newAuthServer(t, true, false)
	handler := srv.authMiddleware(srv.routes())

	admin, err := store.CreateUser("root")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetUserRole("root", session.RoleAdmin); err != nil {
		t.Fatal(err)
	}
	member, err := store.CreateUser("member")
	if err != nil {
		t.Fatal(err)
	}
	adminKey, _, err := store.IssueAPIKey(admin.ID)
	if err != nil {
		t.Fatal(err)
	}
	memberKey, _, err := store.IssueAPIKey(member.ID)
	if err != nil {
		t.Fatal(err)
	}

	// Admin key: identity with role/isAdmin.
	rec := doReq(t, handler, "GET", "/api/me", adminKey)
	if rec.Code != http.StatusOK {
		t.Fatalf("admin key: expected 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["username"] != "root" || body["role"] != session.RoleAdmin || body["isAdmin"] != true {
		t.Fatalf("unexpected identity: %v", body)
	}

	// Member key: same shape, isAdmin false.
	rec = doReq(t, handler, "GET", "/api/me", memberKey)
	if rec.Code != http.StatusOK {
		t.Fatalf("member key: expected 200, got %d", rec.Code)
	}
	body = nil
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["username"] != "member" || body["isAdmin"] != false {
		t.Fatalf("unexpected member identity: %v", body)
	}

	// No credentials: 401 (not the login redirect — API clients only).
	rec = doReq(t, handler, "GET", "/api/me", "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("no credentials: expected 401, got %d", rec.Code)
	}

	// Auth disabled: no identity exists to report.
	srv2, _ := newAuthServer(t, false, false)
	rec = doReq(t, srv2.authMiddleware(srv2.routes()), "GET", "/api/me", "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("auth disabled: expected 401, got %d", rec.Code)
	}
}
