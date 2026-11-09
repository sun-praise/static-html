package server

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sun-praise/static-html/internal/session"
)

// newSharingFixture provisions a protectPreviews server with alice owning a
// session backed by a real file, plus keys for alice/bob/carol.
func newSharingFixture(t *testing.T) (srv *Server, store *session.Store, sessionID, aliceKey, bobKey, carolKey string) {
	t.Helper()

	dir := t.TempDir()
	entry := filepath.Join(dir, "index.html")
	if err := os.WriteFile(entry, []byte("<!doctype html><p>hello</p>"), 0o644); err != nil {
		t.Fatal(err)
	}

	srv, store = newAuthServer(t, false, true) // protectPreviews implies auth

	alice, err := store.CreateUser("alice")
	if err != nil {
		t.Fatal(err)
	}
	bob, err := store.CreateUser("bob")
	if err != nil {
		t.Fatal(err)
	}
	carol, err := store.CreateUser("carol")
	if err != nil {
		t.Fatal(err)
	}

	aliceKey, _, err = store.IssueAPIKey(alice.ID)
	if err != nil {
		t.Fatal(err)
	}
	bobKey, _, err = store.IssueAPIKey(bob.ID)
	if err != nil {
		t.Fatal(err)
	}
	carolKey, _, err = store.IssueAPIKey(carol.ID)
	if err != nil {
		t.Fatal(err)
	}

	sess, err := store.CreateUploaded("index.html", entry)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetSessionOwner(sess.ID, alice.ID); err != nil {
		t.Fatal(err)
	}

	return srv, store, sess.ID, aliceKey, bobKey, carolKey
}

func doBodyReq(t *testing.T, handler http.Handler, method, path, bearer, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

// doBodyReqWithCookie is doBodyReq plus a session cookie and optional Origin.
func doBodyReqWithCookie(t *testing.T, handler http.Handler, method, path, cookie, origin, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Cookie", cookie)
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

// TestSharesAPI_CookieAuthAndOrigin: the browser share panel authenticates
// with the login cookie; a cross-origin Origin header on a cookie-authed
// mutation is rejected; no credentials is still 401.
func TestSharesAPI_CookieAuthAndOrigin(t *testing.T) {
	t.Parallel()
	srv, store, sid, _, _, _ := newSharingFixture(t)
	srv.SetAllowRegistration(true)

	// The owner registers through the browser and receives a session cookie.
	ownerCookie := registerAndLogin(t, srv, "dave", "correct horse pw")
	dave, err := store.FindUserByUsername("dave")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetSessionOwner(sid, dave.ID); err != nil {
		t.Fatal(err)
	}

	// The share target also exists as a browser user.
	erinCookie := registerAndLogin(t, srv, "erin", "battery staple 7")

	base := "/api/sessions/" + sid + "/shares"

	// No credentials → 401.
	if rec := doBodyReq(t, srv.httpServer.Handler, http.MethodPut, base, "", `{"user":"erin"}`); rec.Code != http.StatusUnauthorized {
		t.Fatalf("no creds: expected 401, got %d", rec.Code)
	}

	// Cross-origin cookie-authed mutation → 403.
	cross := doBodyReqWithCookie(t, srv.httpServer.Handler, http.MethodPut, base, ownerCookie, "https://evil.example", `{"user":"erin"}`)
	if cross.Code != http.StatusForbidden {
		t.Fatalf("cross-origin cookie PUT: expected 403, got %d", cross.Code)
	}

	// Same-origin cookie-authed share grant works.
	same := doBodyReqWithCookie(t, srv.httpServer.Handler, http.MethodPut, base, ownerCookie, "http://"+httptest.NewRequest(http.MethodGet, "/", nil).Host, `{"user":"erin"}`)
	if same.Code != http.StatusOK {
		t.Fatalf("same-origin cookie PUT: expected 200, got %d (body: %s)", same.Code, same.Body.String())
	}

	// The shared user, logged in via the browser, opens the preview with her
	// cookie.
	rec := doReqWithCookie(t, srv.httpServer.Handler, http.MethodGet, "/s/"+sid+"/", erinCookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("shared user preview via cookie: expected 200, got %d", rec.Code)
	}

	// Cookie-authed removal works too.
	del := doBodyReqWithCookie(t, srv.httpServer.Handler, http.MethodDelete, base+"/erin", ownerCookie, "", "")
	if del.Code != http.StatusOK {
		t.Fatalf("cookie DELETE share: expected 200, got %d", del.Code)
	}
	after := doReqWithCookie(t, srv.httpServer.Handler, http.MethodGet, "/s/"+sid+"/", erinCookie)
	if after.Code != http.StatusForbidden {
		t.Fatalf("shared user after revoke via cookie: expected 403, got %d", after.Code)
	}
}

// TestSharesAPI_MutationsOutsideSharePathsStillBearerOnly: the cookie
// carve-out must not leak into other mutating endpoints (e.g. session delete).
func TestSharesAPI_MutationsOutsideSharePathsStillBearerOnly(t *testing.T) {
	t.Parallel()
	srv, store, sid, _, _, _ := newSharingFixture(t)
	srv.SetAllowRegistration(true)

	cookie := registerAndLogin(t, srv, "mallory", "some password 9")
	mallory, err := store.FindUserByUsername("mallory")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetSessionOwner(sid, mallory.ID); err != nil {
		t.Fatal(err)
	}

	// Owner's own cookie cannot DELETE the session: cookie is not accepted
	// outside share management.
	if rec := doReqWithCookie(t, srv.httpServer.Handler, http.MethodDelete, "/api/sessions/"+sid, cookie); rec.Code != http.StatusUnauthorized {
		t.Fatalf("cookie DELETE session: expected 401, got %d", rec.Code)
	}
	// Tag mutation likewise stays Bearer-only.
	if rec := doBodyReqWithCookie(t, srv.httpServer.Handler, http.MethodPut, "/api/sessions/"+sid+"/tags", cookie, "", `{"tags":["x"]}`); rec.Code != http.StatusUnauthorized {
		t.Fatalf("cookie PUT tags: expected 401, got %d", rec.Code)
	}
}

// TestPreviewSharing_AccessMatrix: under --protect-previews an owned session
// is visible to its owner and shared users only; anonymous requests are 401;
// revoking the share cuts access back off.
func TestPreviewSharing_AccessMatrix(t *testing.T) {
	t.Parallel()
	srv, store, sid, aliceKey, bobKey, carolKey := newSharingFixture(t)
	preview := "/s/" + sid + "/"

	anonymous := doReq(t, srv.httpServer.Handler, http.MethodGet, preview, "")
	if anonymous.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous: expected 401, got %d", anonymous.Code)
	}

	owner := doReq(t, srv.httpServer.Handler, http.MethodGet, preview, aliceKey)
	if owner.Code != http.StatusOK {
		t.Fatalf("owner: expected 200, got %d (body: %s)", owner.Code, owner.Body.String())
	}

	carolBefore := doReq(t, srv.httpServer.Handler, http.MethodGet, preview, carolKey)
	if carolBefore.Code != http.StatusForbidden {
		t.Fatalf("non-shared carol: expected 403, got %d", carolBefore.Code)
	}

	bobUser, err := store.FindUserByUsername("bob")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.ShareSession(sid, bobUser.ID); err != nil {
		t.Fatal(err)
	}

	bob := doReq(t, srv.httpServer.Handler, http.MethodGet, preview, bobKey)
	if bob.Code != http.StatusOK {
		t.Fatalf("shared bob: expected 200, got %d", bob.Code)
	}

	// Asset paths inside the session are gated the same way.
	asset := doReq(t, srv.httpServer.Handler, http.MethodGet, preview+"missing.css", carolKey)
	if asset.Code != http.StatusForbidden {
		t.Fatalf("non-shared asset fetch: expected 403, got %d", asset.Code)
	}

	if err := store.UnshareSession(sid, bobUser.ID); err != nil {
		t.Fatal(err)
	}
	bobAfter := doReq(t, srv.httpServer.Handler, http.MethodGet, preview, bobKey)
	if bobAfter.Code != http.StatusForbidden {
		t.Fatalf("bob after unshare: expected 403, got %d", bobAfter.Code)
	}
}

// TestPreviewSharing_UnownedLegacySessionStaysOpen: sessions without an owner
// (created before auth existed) remain visible to any authenticated user.
func TestPreviewSharing_UnownedLegacySessionStaysOpen(t *testing.T) {
	t.Parallel()
	srv, store, _, _, bobKey, _ := newSharingFixture(t)

	sess, err := store.CreateUploaded("legacy.html", "whatever.html")
	if err != nil {
		t.Fatal(err)
	}

	rec := doReq(t, srv.httpServer.Handler, http.MethodGet, "/s/"+sess.ID+"/", bobKey)
	if rec.Code == http.StatusForbidden || rec.Code == http.StatusUnauthorized {
		t.Fatalf("legacy unowned session should stay viewable by any user, got %d", rec.Code)
	}
}

// TestPreviewSharing_OpenWhenUnprotected: without --protect-previews the
// preview stays open (legacy share-the-link workflow).
func TestPreviewSharing_OpenWhenUnprotected(t *testing.T) {
	t.Parallel()
	srv, store, _, _, _, _ := newSharingFixture(t)
	srv.SetProtectPreviews(false)
	srv.SetAuthEnabled(false)

	sess, err := store.CreateUploaded("open.html", "missing.html")
	if err != nil {
		t.Fatal(err)
	}

	rec := doReq(t, srv.httpServer.Handler, http.MethodGet, "/s/"+sess.ID+"/", "")
	if rec.Code == http.StatusUnauthorized {
		t.Fatal("preview must stay open when previews are unprotected, got 401")
	}
}

// TestSharesAPI_Management: the shares endpoints are owner-only; adding an
// unknown user or the owner is rejected; removal is 404 when not shared.
func TestSharesAPI_Management(t *testing.T) {
	t.Parallel()
	srv, _, sid, aliceKey, bobKey, _ := newSharingFixture(t)
	base := "/api/sessions/" + sid + "/shares"

	// Non-owner cannot manage shares.
	if rec := doBodyReq(t, srv.httpServer.Handler, http.MethodPut, base, bobKey, `{"user":"bob"}`); rec.Code != http.StatusForbidden {
		t.Fatalf("bob PUT shares: expected 403, got %d", rec.Code)
	}
	if rec := doReq(t, srv.httpServer.Handler, http.MethodGet, base, bobKey); rec.Code != http.StatusForbidden {
		t.Fatalf("bob GET shares: expected 403, got %d", rec.Code)
	}

	// Owner shares with bob.
	added := doBodyReq(t, srv.httpServer.Handler, http.MethodPut, base, aliceKey, `{"user":"bob"}`)
	if added.Code != http.StatusOK {
		t.Fatalf("owner PUT share: expected 200, got %d (body: %s)", added.Code, added.Body.String())
	}
	if !strings.Contains(added.Body.String(), `"username":"bob"`) {
		t.Fatalf("response should list bob, got %s", added.Body.String())
	}

	// Idempotent re-share.
	if rec := doBodyReq(t, srv.httpServer.Handler, http.MethodPut, base, aliceKey, `{"user":"bob"}`); rec.Code != http.StatusOK {
		t.Fatalf("re-share: expected 200, got %d", rec.Code)
	}

	// Unknown user is a client error.
	if rec := doBodyReq(t, srv.httpServer.Handler, http.MethodPut, base, aliceKey, `{"user":"ghost"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown user: expected 400, got %d", rec.Code)
	}

	// Sharing with the owner is rejected.
	if rec := doBodyReq(t, srv.httpServer.Handler, http.MethodPut, base, aliceKey, `{"user":"alice"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("self-share: expected 400, got %d", rec.Code)
	}

	// Empty user is rejected.
	if rec := doBodyReq(t, srv.httpServer.Handler, http.MethodPut, base, aliceKey, `{"user":""}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("empty user: expected 400, got %d", rec.Code)
	}

	// Owner lists shares.
	listed := doReq(t, srv.httpServer.Handler, http.MethodGet, base, aliceKey)
	if listed.Code != http.StatusOK || !strings.Contains(listed.Body.String(), `"username":"bob"`) {
		t.Fatalf("GET shares: expected 200 with bob, got %d %s", listed.Code, listed.Body.String())
	}

	// Owner removes bob's share.
	removed := doReq(t, srv.httpServer.Handler, http.MethodDelete, base+"/bob", aliceKey)
	if removed.Code != http.StatusOK {
		t.Fatalf("DELETE share: expected 200, got %d (body: %s)", removed.Code, removed.Body.String())
	}

	// Removing a share that does not exist is 404.
	if rec := doReq(t, srv.httpServer.Handler, http.MethodDelete, base+"/bob", aliceKey); rec.Code != http.StatusNotFound {
		t.Fatalf("DELETE missing share: expected 404, got %d", rec.Code)
	}

	// Unknown username on delete is a client error.
	if rec := doReq(t, srv.httpServer.Handler, http.MethodDelete, base+"/ghost", aliceKey); rec.Code != http.StatusBadRequest {
		t.Fatalf("DELETE unknown user: expected 400, got %d", rec.Code)
	}
}

// TestSharesAPI_MissingSession: share management on a nonexistent session
// answers 404, not 403.
func TestSharesAPI_MissingSession(t *testing.T) {
	t.Parallel()
	srv, _, _, aliceKey, _, _ := newSharingFixture(t)

	base := "/api/sessions/does-not-exist/shares"
	if rec := doReq(t, srv.httpServer.Handler, http.MethodGet, base, aliceKey); rec.Code != http.StatusNotFound {
		t.Fatalf("GET on missing session: expected 404, got %d", rec.Code)
	}
	if rec := doBodyReq(t, srv.httpServer.Handler, http.MethodPut, base, aliceKey, `{"user":"bob"}`); rec.Code != http.StatusNotFound {
		t.Fatalf("PUT on missing session: expected 404, got %d", rec.Code)
	}
}

// TestSharedViewer_ReadEndpoints: a shared user can read the metadata and
// peers the preview drawer needs, while an unshared user cannot.
func TestSharedViewer_ReadEndpoints(t *testing.T) {
	t.Parallel()
	srv, store, sid, _, bobKey, carolKey := newSharingFixture(t)

	bobUser, err := store.FindUserByUsername("bob")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.ShareSession(sid, bobUser.ID); err != nil {
		t.Fatal(err)
	}

	if rec := doReq(t, srv.httpServer.Handler, http.MethodGet, "/api/sessions/"+sid+"/metadata", bobKey); rec.Code != http.StatusOK {
		t.Fatalf("shared viewer metadata: expected 200, got %d (body: %s)", rec.Code, rec.Body.String())
	}
	if rec := doReq(t, srv.httpServer.Handler, http.MethodGet, "/api/sessions/"+sid+"/peers", bobKey); rec.Code != http.StatusOK {
		t.Fatalf("shared viewer peers: expected 200, got %d", rec.Code)
	}
	if rec := doReq(t, srv.httpServer.Handler, http.MethodGet, "/api/sessions/"+sid+"/metadata", carolKey); rec.Code != http.StatusForbidden {
		t.Fatalf("unshared metadata: expected 403, got %d", rec.Code)
	}

	// Mutating metadata endpoints stay owner-only.
	if rec := doBodyReq(t, srv.httpServer.Handler, http.MethodPut, "/api/sessions/"+sid+"/tags", bobKey, `{"tags":["x"]}`); rec.Code != http.StatusForbidden {
		t.Fatalf("shared viewer tags mutation: expected 403, got %d", rec.Code)
	}
	if rec := doReq(t, srv.httpServer.Handler, http.MethodDelete, "/api/sessions/"+sid, bobKey); rec.Code != http.StatusForbidden {
		t.Fatalf("shared viewer delete: expected 403, got %d", rec.Code)
	}
	if rec := doReq(t, srv.httpServer.Handler, http.MethodGet, "/api/sessions/"+sid+"/download", bobKey); rec.Code != http.StatusForbidden {
		t.Fatalf("shared viewer download: expected 403, got %d", rec.Code)
	}
}

// TestHomePage_SharedSection: the shared user's home page lists the session
// under "Shared with you" with the owner's username.
func TestHomePage_SharedSection(t *testing.T) {
	t.Parallel()
	srv, store, sid, _, bobKey, _ := newSharingFixture(t)

	bobUser, err := store.FindUserByUsername("bob")
	if err != nil {
		t.Fatal(err)
	}

	before := doReq(t, srv.httpServer.Handler, http.MethodGet, "/", bobKey)
	if strings.Contains(before.Body.String(), "Shared with you") {
		t.Fatal("shared section must be absent before any share")
	}

	if err := store.ShareSession(sid, bobUser.ID); err != nil {
		t.Fatal(err)
	}

	after := doReq(t, srv.httpServer.Handler, http.MethodGet, "/", bobKey)
	if !strings.Contains(after.Body.String(), "Shared with you") {
		t.Fatal("shared section missing after share")
	}
	if !strings.Contains(after.Body.String(), "/s/"+sid+"/") {
		t.Fatal("shared section should link the session preview")
	}
	if !strings.Contains(after.Body.String(), "by alice") {
		t.Fatal("shared section should credit the owner")
	}
}
