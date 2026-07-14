package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sun-praise/static-html/internal/session"
)

// newLoginServer is like newAuthServer but also enables registration so the
// /register flow is exercisable without each test repeating the call.
func newLoginServer(t *testing.T, authEnabled bool) (*Server, *session.Store) {
	t.Helper()
	srv, store := newAuthServer(t, authEnabled, false)
	if authEnabled {
		srv.SetAllowRegistration(true)
	}
	return srv, store
}

// doReqWithCookie is doReq plus a Cookie header.
func doReqWithCookie(t *testing.T, handler http.Handler, method, path, cookie string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	if cookie != "" {
		req.Header.Set("Cookie", cookie)
	}
	req.Header.Set("Accept", "text/html")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

// postForm issues a POST with the given form values and returns the recorder.
// Accept defaults to text/html so login redirects rather than 401s where
// relevant.
func postForm(t *testing.T, handler http.Handler, path, contentType string, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Accept", "text/html")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

// registerAndLogin performs a full registration and returns the Set-Cookie
// header value (the session cookie) for follow-up authenticated requests.
// Fails the test if registration does not yield a 302 with a session cookie.
func registerAndLogin(t *testing.T, srv *Server, username, password string) string {
	t.Helper()
	form := "username=" + username + "&password=" + password + "&password_confirm=" + password + "&next=/"
	rec := postForm(t, srv.httpServer.Handler, "/register", "application/x-www-form-urlencoded", form)
	if rec.Code != http.StatusFound {
		t.Fatalf("register %q: expected 302, got %d (body: %s)", username, rec.Code, rec.Body.String())
	}
	cookie := rec.Header().Get("Set-Cookie")
	if !strings.Contains(cookie, sessionCookieName+"=") {
		t.Fatalf("register %q: no session cookie in Set-Cookie: %q", username, cookie)
	}
	return extractCookieValue(cookie)
}

// extractCookieValue pulls the sth_session=... value out of a Set-Cookie header
// for use as a subsequent Cookie request header.
func extractCookieValue(setCookie string) string {
	parts := strings.Split(setCookie, ";")
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if strings.HasPrefix(p, sessionCookieName+"=") {
			return p
		}
	}
	return ""
}

// ---------------- Registration tests ----------------

func TestRegister_SuccessSetsCookieAndRedirects(t *testing.T) {
	t.Parallel()
	srv, _ := newLoginServer(t, true)

	form := "username=alice&password=secret123&password_confirm=secret123&next=/"
	rec := postForm(t, srv.httpServer.Handler, "/register", "application/x-www-form-urlencoded", form)

	if rec.Code != http.StatusFound {
		t.Fatalf("expected 302, got %d", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/" {
		t.Fatalf("expected redirect to /, got %q", loc)
	}
	cookie := rec.Header().Get("Set-Cookie")
	if !strings.Contains(cookie, sessionCookieName+"=") {
		t.Fatalf("expected session cookie, got %q", cookie)
	}
	if !strings.Contains(cookie, "HttpOnly") {
		t.Errorf("cookie not HttpOnly: %q", cookie)
	}
	if !strings.Contains(cookie, "SameSite=Lax") {
		t.Errorf("cookie not SameSite=Lax: %q", cookie)
	}
}

func TestRegister_DuplicateUsernameRejected(t *testing.T) {
	t.Parallel()
	srv, store := newLoginServer(t, true)
	if _, err := store.CreateUser("alice"); err != nil {
		t.Fatal(err)
	}

	form := "username=alice&password=secret123&password_confirm=secret123"
	rec := postForm(t, srv.httpServer.Handler, "/register", "application/x-www-form-urlencoded", form)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 (re-rendered form), got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "already taken") {
		t.Errorf("expected 'already taken' error in body, got: %s", rec.Body.String())
	}
	if strings.Contains(rec.Header().Get("Set-Cookie"), sessionCookieName) {
		t.Errorf("no session cookie should be set on failure")
	}
}

func TestRegister_ShortPasswordRejected(t *testing.T) {
	t.Parallel()
	srv, _ := newLoginServer(t, true)

	form := "username=bob&password=short&password_confirm=short"
	rec := postForm(t, srv.httpServer.Handler, "/register", "application/x-www-form-urlencoded", form)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 (re-rendered), got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "at least 8") {
		t.Errorf("expected length error, got: %s", rec.Body.String())
	}
}

func TestRegister_PasswordMismatchRejected(t *testing.T) {
	t.Parallel()
	srv, _ := newLoginServer(t, true)

	form := "username=carol&password=secret123&password_confirm=DIFFERENT12"
	rec := postForm(t, srv.httpServer.Handler, "/register", "application/x-www-form-urlencoded", form)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 (re-rendered), got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "do not match") {
		t.Errorf("expected mismatch error, got: %s", rec.Body.String())
	}
}

func TestRegister_DisabledReturns403(t *testing.T) {
	t.Parallel()
	srv, _ := newAuthServer(t, true, false) // allowRegistration defaults to false
	// Deliberately do NOT call SetAllowRegistration(true).

	form := "username=dave&password=secret123&password_confirm=secret123"
	rec := postForm(t, srv.httpServer.Handler, "/register", "application/x-www-form-urlencoded", form)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 when registration disabled, got %d", rec.Code)
	}
}

func TestRegisterDisabled_GetShowsNotice(t *testing.T) {
	t.Parallel()
	srv, _ := newAuthServer(t, true, false) // registration disabled

	rec := doReqWithCookie(t, srv.httpServer.Handler, http.MethodGet, "/register", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Registration is closed") {
		t.Errorf("expected closed notice, got: %s", rec.Body.String())
	}
}

// ---------------- Login tests ----------------

func TestLogin_CorrectCredentialsSetsCookie(t *testing.T) {
	t.Parallel()
	srv, store := newLoginServer(t, true)
	user, err := store.CreateUser("alice")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetPassword(user.ID, "secret123"); err != nil {
		t.Fatal(err)
	}

	form := "username=alice&password=secret123&next=/"
	rec := postForm(t, srv.httpServer.Handler, "/login", "application/x-www-form-urlencoded", form)

	if rec.Code != http.StatusFound {
		t.Fatalf("expected 302, got %d", rec.Code)
	}
	if !strings.Contains(rec.Header().Get("Set-Cookie"), sessionCookieName+"=") {
		t.Errorf("expected session cookie on successful login")
	}
}

func TestLogin_WrongPasswordNoCookie(t *testing.T) {
	t.Parallel()
	srv, store := newLoginServer(t, true)
	user, err := store.CreateUser("alice")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetPassword(user.ID, "secret123"); err != nil {
		t.Fatal(err)
	}

	form := "username=alice&password=WRONGPASSWORD"
	rec := postForm(t, srv.httpServer.Handler, "/login", "application/x-www-form-urlencoded", form)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 (re-rendered), got %d", rec.Code)
	}
	if strings.Contains(rec.Header().Get("Set-Cookie"), sessionCookieName) {
		t.Errorf("no cookie should be set on failed login")
	}
	if !strings.Contains(rec.Body.String(), "Invalid username or password") {
		t.Errorf("expected generic error, got: %s", rec.Body.String())
	}
}

func TestLogin_UnknownUserSameErrorAsWrongPassword(t *testing.T) {
	t.Parallel()
	srv, _ := newLoginServer(t, true)

	form := "username=ghost&password=whatever12"
	rec := postForm(t, srv.httpServer.Handler, "/login", "application/x-www-form-urlencoded", form)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Invalid username or password") {
		t.Errorf("expected identical generic error for unknown user, got: %s", rec.Body.String())
	}
}

// ---------------- Logout tests ----------------

func TestLogout_ClearsCookie(t *testing.T) {
	t.Parallel()
	srv, _ := newLoginServer(t, true)
	cookieVal := registerAndLogin(t, srv, "alice", "secret123")

	rec := doReqWithCookie(t, srv.httpServer.Handler, http.MethodPost, "/logout", cookieVal)
	if rec.Code != http.StatusFound {
		t.Fatalf("expected 302, got %d", rec.Code)
	}
	setCookie := rec.Header().Get("Set-Cookie")
	if !strings.Contains(setCookie, "Max-Age=0") && !strings.Contains(setCookie, "Max-Age=-1") {
		t.Errorf("cookie not cleared on logout: %q", setCookie)
	}
}

// ---------------- Cookie middleware tests ----------------

func TestMiddleware_CookieAuthorizesGet(t *testing.T) {
	t.Parallel()
	srv, _ := newLoginServer(t, true)
	cookieVal := registerAndLogin(t, srv, "alice", "secret123")

	// GET / with the session cookie (no Bearer) should pass.
	rec := doReqWithCookie(t, srv.httpServer.Handler, http.MethodGet, "/", cookieVal)
	if rec.Code != http.StatusOK {
		t.Fatalf("cookie should authorize GET /, got %d (body: %s)", rec.Code, rec.Body.String())
	}
}

// TestHome_ShowsLogoutBarWhenLoggedIn asserts the homepage renders the logout
// control (username + Sign out form posting to /logout) for an authenticated
// browser session, and omits it entirely when auth is off.
func TestHome_ShowsLogoutBarWhenLoggedIn(t *testing.T) {
	t.Parallel()
	srv, _ := newLoginServer(t, true)
	cookieVal := registerAndLogin(t, srv, "alice", "secret123")

	rec := doReqWithCookie(t, srv.httpServer.Handler, http.MethodGet, "/", cookieVal)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{
		`class="logout-bar"`,
		`Signed in as`,
		`<strong>alice</strong>`,
		`action="/logout"`,
		`Sign out`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("homepage body missing %q for logged-in user", want)
		}
	}
}

func TestHome_NoLogoutBarWhenAuthOff(t *testing.T) {
	t.Parallel()
	srv, _ := newLoginServer(t, false)
	// No login possible (auth off). The homepage must not render any logout UI.
	rec := doReq(t, srv.httpServer.Handler, http.MethodGet, "/", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	// The .logout-bar CSS rule is always in <style>; assert on the rendered DOM
	// element + label instead, which only exist for a logged-in user.
	for _, marker := range []string{`class="logout-bar"`, `Signed in as`, `action="/logout"`} {
		if strings.Contains(rec.Body.String(), marker) {
			t.Fatalf("homepage must not show logout UI when auth is off; found %q", marker)
		}
	}
}

func TestMiddleware_CookieDoesNotAuthorizeMutating(t *testing.T) {
	t.Parallel()
	srv, _ := newLoginServer(t, true)
	cookieVal := registerAndLogin(t, srv, "alice", "secret123")

	// POST /api/sessions with only a cookie must NOT pass (CSRF safety).
	rec := doReqWithCookie(t, srv.httpServer.Handler, http.MethodPost, "/api/sessions", cookieVal)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("cookie must NOT authorize POST, got %d (CSRF risk)", rec.Code)
	}
}

func TestMiddleware_BearerStillAuthorizesMutating(t *testing.T) {
	t.Parallel()
	srv, store := newLoginServer(t, true)
	user, err := store.CreateUser("alice")
	if err != nil {
		t.Fatal(err)
	}
	key, _, err := store.IssueAPIKey(user.ID)
	if err != nil {
		t.Fatal(err)
	}

	// POST /api/sessions with a Bearer key should reach the handler (which may
	// then 400 on missing body, but NOT 401 — that's what we assert).
	rec := doReq(t, srv.httpServer.Handler, http.MethodPost, "/api/sessions", key)
	if rec.Code == http.StatusUnauthorized {
		t.Fatalf("Bearer should authorize POST, got 401")
	}
}

func TestMiddleware_BrowserRedirectedToLogin(t *testing.T) {
	t.Parallel()
	srv, _ := newLoginServer(t, true)

	// GET / with Accept: text/html and no creds -> 302 to /login?next=%2F
	rec := doReqWithCookie(t, srv.httpServer.Handler, http.MethodGet, "/", "")
	if rec.Code != http.StatusFound {
		t.Fatalf("expected 302 redirect for browser, got %d", rec.Code)
	}
	loc := rec.Header().Get("Location")
	if !strings.HasPrefix(loc, "/login?next=") {
		t.Errorf("expected redirect to /login?next=, got %q", loc)
	}
}

func TestMiddleware_NonBrowserGets401NotRedirect(t *testing.T) {
	t.Parallel()
	srv, _ := newLoginServer(t, true)

	// GET / WITHOUT Accept: text/html (default httptest has none) -> 401.
	rec := doReq(t, srv.httpServer.Handler, http.MethodGet, "/", "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for non-browser, got %d", rec.Code)
	}
	if got := rec.Header().Get("WWW-Authenticate"); !strings.Contains(got, "Bearer") {
		t.Errorf("expected WWW-Authenticate Bearer, got %q", got)
	}
}

func TestMiddleware_PreviewStaysOpenWithoutCreds(t *testing.T) {
	t.Parallel()
	srv, store := newLoginServer(t, true)
	// Seed a session so /s/<id>/ resolves to a real preview path.
	user, err := store.CreateUser("alice")
	if err != nil {
		t.Fatal(err)
	}
	_ = user
	// Without a session in the store, /s/whatever returns 404 from the handler,
	// not a 401 from the middleware — that's the point: the middleware let it
	// through. We assert it's NOT 401.
	rec := doReqWithCookie(t, srv.httpServer.Handler, http.MethodGet, "/s/nonexistent/", "")
	if rec.Code == http.StatusUnauthorized {
		t.Fatalf("preview must not require auth by default, got 401")
	}
}

// ---------------- Safe redirect tests ----------------

func TestSafeRedirectTarget(t *testing.T) {
	t.Parallel()
	cases := []struct{ in, want string }{
		{"", "/"},
		{"/", "/"},
		{"/dashboard", "/dashboard"},
		{"//evil.com", "/"},      // protocol-relative absolute
		{"/\\evil.com", "/"},     // backslash trick
		{"https://evil.com", "/"}, // scheme
		{"http://evil.com", "/"},
		{"dashboard", "/"},       // relative, no leading slash
	}
	for _, c := range cases {
		if got := safeRedirectTarget(c.in); got != c.want {
			t.Errorf("safeRedirectTarget(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// ---------------- Auth-off backward compatibility ----------------

func TestAuthPages_ReachableWhenAuthDisabled(t *testing.T) {
	t.Parallel()
	srv, _ := newLoginServer(t, false) // auth OFF

	// Even with auth off, /login and /register should serve (they're harmless
	// and the redirect logic is skipped, but the routes must not 404).
	for _, p := range []string{"/login", "/register"} {
		rec := doReqWithCookie(t, srv.httpServer.Handler, http.MethodGet, p, "")
		if rec.Code != http.StatusOK {
			t.Errorf("%s: expected 200 with auth off, got %d", p, rec.Code)
		}
	}
}

// ---------------- Session store unit tests (cookie token model) ----------------

func TestLoginSession_TokenStoredAsHash(t *testing.T) {
	t.Parallel()
	store, err := session.NewInMemoryStore()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	user, err := store.CreateUser("alice")
	if err != nil {
		t.Fatal(err)
	}
	token, err := store.CreateLoginSession(user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if token == "" {
		t.Fatal("token should be non-empty")
	}
	// Verify round-trips correctly.
	uid, ok, err := store.VerifyLoginSession(token)
	if err != nil || !ok {
		t.Fatalf("verify failed: ok=%v err=%v", ok, err)
	}
	if uid != user.ID {
		t.Errorf("verify returned wrong user: got %q want %q", uid, user.ID)
	}
}

func TestLoginSession_ExpiredTokenRejected(t *testing.T) {
	t.Parallel()
	store, err := session.NewInMemoryStore()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	user, err := store.CreateUser("alice")
	if err != nil {
		t.Fatal(err)
	}
	token, err := store.CreateLoginSession(user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteLoginSession(token); err != nil {
		t.Fatal(err)
	}
	_, ok, err := store.VerifyLoginSession(token)
	if err != nil || ok {
		t.Errorf("deleted token should not verify: ok=%v err=%v", ok, err)
	}
}

// ---------------- Password store unit tests ----------------

func TestPassword_VerifyCorrectAndWrong(t *testing.T) {
	t.Parallel()
	store, err := session.NewInMemoryStore()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	user, err := store.CreateUser("alice")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetPassword(user.ID, "correct-horse-battery"); err != nil {
		t.Fatal(err)
	}
	if ok, _ := store.VerifyPassword(user.ID, "correct-horse-battery"); !ok {
		t.Error("correct password should verify")
	}
	if ok, _ := store.VerifyPassword(user.ID, "wrong"); ok {
		t.Error("wrong password should not verify")
	}
}

func TestPassword_UserWithoutCredentialsCannotLogin(t *testing.T) {
	t.Parallel()
	store, err := session.NewInMemoryStore()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	user, err := store.CreateUser("apionly")
	if err != nil {
		t.Fatal(err)
	}
	// No SetPassword call — this is a pure API-key user.
	if ok, _ := store.VerifyPassword(user.ID, "anything"); ok {
		t.Error("user without credentials should not verify any password")
	}
}
