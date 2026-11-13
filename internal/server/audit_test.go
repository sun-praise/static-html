package server

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sun-praise/static-html/internal/session"
)

func auditAll(t *testing.T, store *session.Store) []session.AuditEvent {
	t.Helper()
	events, err := store.ListAuditEvents(session.AuditFilter{Limit: 1000})
	if err != nil {
		t.Fatalf("list audit events: %v", err)
	}
	return events
}

func auditActionCount(events []session.AuditEvent, action string) int {
	n := 0
	for _, e := range events {
		if e.Action == action {
			n++
		}
	}
	return n
}

func assertNoSecret(t *testing.T, events []session.AuditEvent, secret string) {
	t.Helper()
	for _, e := range events {
		for name, value := range map[string]string{
			"actor": e.Actor, "actor_id": e.ActorID, "target": e.Target,
			"ip": e.IP, "detail": e.Detail, "user_agent": e.UserAgent,
		} {
			if secret != "" && strings.Contains(value, secret) {
				t.Fatalf("audit %s field %s leaked a credential: %q", e.Action, name, value)
			}
		}
	}
}

// TestAuditLoginFlowAndNoSecrets: successful and failed logins are recorded,
// logout resolves the actor from the (about-to-be-revoked) cookie, and no
// audit field ever carries the submitted password or the session token.
func TestAuditLoginFlowAndNoSecrets(t *testing.T) {
	t.Parallel()
	srv, store := newLoginServer(t, true)

	const password = "sup3r-secret-42"
	user, err := store.CreateUser("alice")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetPassword(user.ID, password); err != nil {
		t.Fatal(err)
	}

	fail := postForm(t, srv.httpServer.Handler, "/login",
		"application/x-www-form-urlencoded", "username=alice&password=wrong&next=/")
	if fail.Code != http.StatusOK || !strings.Contains(fail.Body.String(), "Invalid username or password") {
		t.Fatalf("failed login: got %d body=%s", fail.Code, fail.Body.String())
	}

	ok := postForm(t, srv.httpServer.Handler, "/login",
		"application/x-www-form-urlencoded", "username=alice&password="+password+"&next=/")
	if ok.Code != http.StatusFound {
		t.Fatalf("successful login: expected 302, got %d body=%s", ok.Code, ok.Body.String())
	}
	setCookie := ok.Header().Get("Set-Cookie")
	cookieValue := extractCookieValue(setCookie)
	if cookieValue == "" {
		t.Fatalf("no session cookie in %q", setCookie)
	}

	// extractCookieValue returns the complete `sth_session=<token>` pair, so
	// it goes into the Cookie header verbatim.
	req := httptest.NewRequest(http.MethodPost, "/logout", nil)
	req.Header.Set("Cookie", cookieValue)
	req.Header.Set("Accept", "text/html")
	logoutRec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(logoutRec, req)
	if logoutRec.Code != http.StatusFound {
		t.Fatalf("logout: expected 302, got %d", logoutRec.Code)
	}

	events := auditAll(t, store)
	for _, action := range []string{
		session.AuditLogin, session.AuditLoginFailed, session.AuditLogout,
	} {
		if auditActionCount(events, action) != 1 {
			t.Fatalf("expected exactly one %s event, got %d (%+v)", action,
				auditActionCount(events, action), events)
		}
	}
	for _, e := range events {
		if e.Action == session.AuditLogin && (e.Actor != "alice" || e.ActorID != user.ID) {
			t.Fatalf("login actor not resolved: %+v", e)
		}
		if e.Action == session.AuditLoginFailed && e.Outcome != session.AuditOutcomeDenied {
			t.Fatalf("failed login outcome = %q", e.Outcome)
		}
		if e.Action == session.AuditLogout && e.Actor != "alice" {
			t.Fatalf("logout actor = %q, want alice", e.Actor)
		}
		if e.IP == "" {
			t.Fatalf("%s event has no client IP: %+v", e.Action, e)
		}
	}
	assertNoSecret(t, events, password)
	// Assert against the bare token, not the whole `sth_session=<token>` pair:
	// no audit field would ever contain that full pair, so checking it would
	// be a vacuous assertion.
	token := strings.TrimPrefix(cookieValue, sessionCookieName+"=")
	if token == "" || token == cookieValue {
		t.Fatalf("could not extract the bare session token from %q", setCookie)
	}
	assertNoSecret(t, events, token)
	assertNoSecret(t, events, "password=")
}

// TestAuditSharePreviewAndSessionEvents covers the full security-relevant
// chain on one shared session, and pins that render-asset requests do NOT
// produce preview audit rows.
func TestAuditSharePreviewAndSessionEvents(t *testing.T) {
	t.Parallel()
	srv, store, sid, aliceKey, bobKey, _ := newSharingFixture(t)
	h := srv.httpServer.Handler
	page := "/s/" + sid + "/index.html"
	sharesAPI := "/api/sessions/" + sid + "/shares"

	// Stranger opens the page -> 403 and preview.deny.
	if rec := doReq(t, h, http.MethodGet, page, bobKey); rec.Code != http.StatusForbidden {
		t.Fatalf("stranger preview: expected 403, got %d", rec.Code)
	}
	// Grant just this page.
	if rec := doBodyReq(t, h, http.MethodPut, sharesAPI, aliceKey,
		`{"user":"bob","path":"index.html"}`); rec.Code != http.StatusOK {
		t.Fatalf("grant: expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	// Now the same request is allowed. http.ServeFile canonicalizes a direct
	// hit on index.html with a 301, which still proves authorization passed.
	if rec := doReq(t, h, http.MethodGet, page, bobKey); rec.Code != http.StatusOK &&
		rec.Code != http.StatusMovedPermanently {
		t.Fatalf("shared preview: expected 200/301, got %d", rec.Code)
	}

	// A css asset is render plumbing and must not create a preview event.
	before := len(auditAll(t, store))
	doReq(t, h, http.MethodGet, "/s/"+sid+"/style.css", bobKey)
	if after := len(auditAll(t, store)); after != before {
		t.Fatalf("css request added %d audit row(s)", after-before)
	}

	// Revoke, then delete the session.
	if rec := doReq(t, h, http.MethodDelete, sharesAPI+"/bob?path=index.html", aliceKey); rec.Code != http.StatusOK {
		t.Fatalf("revoke: expected 200, got %d", rec.Code)
	}
	if rec := doReq(t, h, http.MethodDelete, "/api/sessions/"+sid, aliceKey); rec.Code != http.StatusOK {
		t.Fatalf("delete: expected 200, got %d", rec.Code)
	}

	// Create a fresh session over the API.
	entry := filepath.Join(t.TempDir(), "page.html")
	if err := os.WriteFile(entry, []byte("<!doctype html><p>audit</p>"), 0o644); err != nil {
		t.Fatal(err)
	}
	body := fmt.Sprintf(`{"filePath":%q,"tags":["t"],"category":"c","project":"p"}`, entry)
	if rec := doBodyReq(t, h, http.MethodPost, "/api/sessions", aliceKey, body); rec.Code != http.StatusCreated {
		t.Fatalf("create: expected 201, got %d body=%s", rec.Code, rec.Body.String())
	}

	events := auditAll(t, store)
	for _, action := range []string{
		session.AuditShareGrant, session.AuditShareRevoke,
		session.AuditPreviewAllow, session.AuditPreviewDeny,
		session.AuditSessionCreate, session.AuditSessionDelete,
	} {
		if auditActionCount(events, action) == 0 {
			t.Fatalf("missing %s event in %+v", action, events)
		}
	}
	for _, e := range events {
		if e.Action == session.AuditShareGrant && (e.Actor != "alice" || e.Target != sid) {
			t.Fatalf("grant actor/target wrong: %+v", e)
		}
		if e.Action == session.AuditPreviewDeny && (e.Actor != "bob" || e.Outcome != session.AuditOutcomeDenied) {
			t.Fatalf("deny actor/outcome wrong: %+v", e)
		}
	}
	assertNoSecret(t, events, aliceKey)
	assertNoSecret(t, events, bobKey)
}

// TestAuditRejectedRequests: an unauthenticated API request is recorded, and
// the query string is never copied into the audit detail (it may carry a key).
func TestAuditRejectedRequests(t *testing.T) {
	t.Parallel()
	srv, store, sid, _, _, _ := newSharingFixture(t)
	h := srv.httpServer.Handler

	if rec := doReq(t, h, http.MethodGet,
		"/api/sessions/"+sid+"/shares?api_key=leakme", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
	events := auditAll(t, store)
	if auditActionCount(events, session.AuditAuthRejected) != 1 {
		t.Fatalf("expected one auth.rejected event: %+v", events)
	}
	for _, e := range events {
		if e.Action == session.AuditAuthRejected {
			if e.Actor != "" || e.Outcome != session.AuditOutcomeDenied {
				t.Fatalf("rejected event wrong: %+v", e)
			}
			if strings.Contains(e.Detail, "leakme") || strings.Contains(e.Target, "?") {
				t.Fatalf("query string leaked into audit row: %+v", e)
			}
		}
	}
	assertNoSecret(t, events, "leakme")
}

// TestAuditRetentionPrunesOnStart: the background prune loop must run once at
// startup (not only after 24h), and Start/Stop must not deadlock or leave the
// stop channel in a state that breaks a second lifecycle.
func TestAuditRetentionPrunesOnStart(t *testing.T) {
	srv, store := newAuthServer(t, false, false)
	srv.SetAuditRetentionDays(90)

	now := time.Now().UTC()
	_ = store.RecordAudit(session.AuditEvent{
		Action: session.AuditLogin, TSUnix: now.Add(-200 * 24 * time.Hour).Unix(),
	})
	_ = store.RecordAudit(session.AuditEvent{Action: session.AuditLogin, TSUnix: now.Unix()})

	stopServer := func() {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := srv.Stop(ctx); err != nil {
			t.Fatalf("stop: %v", err)
		}
	}
	if err := srv.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer stopServer()

	deadline := time.Now().Add(2 * time.Second)
	for {
		n, err := store.CountAuditEvents()
		if err != nil {
			t.Fatal(err)
		}
		if n == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("expected the stale event to be pruned, still %d rows", n)
		}
		time.Sleep(10 * time.Millisecond)
	}

	// A second Start/Stop cycle exercises nil-ing out the stop/done channels.
	stopServer()
	srv.mu.RLock()
	stop, done := srv.auditPruneStop, srv.auditPruneDone
	srv.mu.RUnlock()
	if stop != nil || done != nil {
		t.Fatal("prune lifecycle channels were not cleared on Stop")
	}
	if err := srv.Start(); err != nil {
		t.Fatalf("restart: %v", err)
	}
}

// TestAuditFailedLoginNeverStoresSubmittedValues: people sometimes paste a
// password into the username field. A failed-login row must contain only a
// fixed reason plus source metadata, never anything the client submitted.
func TestAuditFailedLoginNeverStoresSubmittedValues(t *testing.T) {
	t.Parallel()
	srv, store := newLoginServer(t, true)

	const secret = "PastedP@ssw0rd-into-username"
	form := "username=" + url.QueryEscape(secret) + "&password=x&next=/"
	if rec := postForm(t, srv.httpServer.Handler, "/login",
		"application/x-www-form-urlencoded", form); rec.Code != http.StatusOK {
		t.Fatalf("expected the login form to re-render, got %d", rec.Code)
	}

	events := auditAll(t, store)
	if auditActionCount(events, session.AuditLoginFailed) != 1 {
		t.Fatalf("expected one failed-login event: %+v", events)
	}
	assertNoSecret(t, events, secret)
	for _, e := range events {
		if e.Action == session.AuditLoginFailed {
			if e.Actor != "" || e.Target != "" || e.Detail != "invalid_credentials" {
				t.Fatalf("failed-login row carries submitted values: %+v", e)
			}
		}
	}
}

// TestAuditAnonymousLogoutWritesNothing: /logout without a valid session is a
// no-op and must never be turned into an unbounded audit write primitive.
func TestAuditAnonymousLogoutWritesNothing(t *testing.T) {
	t.Parallel()
	srv, store := newLoginServer(t, true)

	for i := 0; i < 5; i++ {
		postForm(t, srv.httpServer.Handler, "/logout",
			"application/x-www-form-urlencoded", "")
	}
	if n := auditActionCount(auditAll(t, store), session.AuditLogout); n != 0 {
		t.Fatalf("anonymous logout produced %d audit rows", n)
	}
}

// TestAuditRejectedIsCoalesced: a 401 flood from one client writes a single
// row per (ip, path) per window, while a different path still gets its own row.
func TestAuditRejectedIsCoalesced(t *testing.T) {
	t.Parallel()
	srv, store, sid, _, _, _ := newSharingFixture(t)
	h := srv.httpServer.Handler

	for i := 0; i < 6; i++ {
		doReq(t, h, http.MethodGet, "/api/sessions/"+sid+"/shares", "")
	}
	doReq(t, h, http.MethodGet, "/api/sessions/"+sid+"/metadata", "")

	events := auditAll(t, store)
	if n := auditActionCount(events, session.AuditAuthRejected); n != 2 {
		t.Fatalf("expected 2 coalesced rejection rows (one per path), got %d: %+v", n, events)
	}
}

// TestAuditRejectedCoalescingIgnoresSpoofedXFF: rotating a client-controlled
// X-Forwarded-For header must not buy a fresh throttle bucket per request.
// The XFF value is still stored as evidence in the row that IS written.
func TestAuditRejectedCoalescingIgnoresSpoofedXFF(t *testing.T) {
	t.Parallel()
	srv, store, sid, _, _, _ := newSharingFixture(t)
	h := srv.httpServer.Handler
	path := "/api/sessions/" + sid + "/shares"

	for i := 0; i < 8; i++ {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("X-Forwarded-For", fmt.Sprintf("203.0.113.%d", i))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("request %d: expected 401, got %d", i, rec.Code)
		}
	}

	events := auditAll(t, store)
	if n := auditActionCount(events, session.AuditAuthRejected); n != 1 {
		t.Fatalf("rotating XFF bypassed coalescing, wrote %d rows: %+v", n, events)
	}
	for _, e := range events {
		if e.Action == session.AuditAuthRejected && e.IP != "203.0.113.0" {
			t.Fatalf("XFF evidence not preserved in the audit row: %+v", e)
		}
	}
}
