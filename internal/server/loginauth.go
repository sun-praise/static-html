package server

import (
	"html/template"
	"net/http"
	"strings"

	"github.com/sun-praise/static-html/internal/session"
)

// sessionCookieName is the browser cookie holding the server-side session token.
const sessionCookieName = "sth_session"

// sessionCookieMaxAge matches DefaultLoginSessionTTL (30 days) in seconds, so
// the browser discards the cookie at roughly the same time the server-side
// row expires.
const sessionCookieMaxAge = 30 * 24 * 60 * 60

// loginAuthPageData is the render context for the login and register pages.
// Error is a user-facing message (already localized to a sentence) shown when
// a previous submission failed; empty means no error to display.
type loginAuthPageData struct {
	Title      string
	Heading    string
	Action     string // form target: /login or /register
	SubmitLabel string
	Error      string
	// Next is the validated redirect target passed through from ?next=. It is
	// already sanitized by safeRedirectTarget before reaching the template.
	Next string
	// ShowPasswordField toggles the second password input (confirm) on the
	// register page; login uses a single password field.
	ShowConfirmField bool
	// AlternativeLink/AlternativeLabel render the cross-link between login and
	// register (e.g. "No account? Register" on the login page).
	AlternativeLink  string
	AlternativeLabel string
}

// loginPageTemplate and registerPageTemplate are intentionally separate inline
// templates (no shared layout) to match homePageTemplate's self-contained
// style. CSS is duplicated for the same reason the home page inlines its own.
var loginPageTemplate = template.Must(template.New("login").Parse(`<!doctype html>
<html lang="en">
  <head>
    <meta charset="utf-8">
    <meta name="viewport" content="width=device-width, initial-scale=1">
    <title>{{.Title}}</title>
    <style>
      :root { color-scheme: light; font-family: "IBM Plex Sans", "Segoe UI", sans-serif; }
      body {
        margin: 0; padding: 2rem; min-height: 100vh;
        background: linear-gradient(180deg, #f6f4ee 0%, #ebe7dc 100%);
        color: #171717; display: flex; align-items: center; justify-content: center;
      }
      main {
        width: 100%; max-width: 380px;
        background: rgba(255, 255, 255, 0.9);
        border: 1px solid #d6d1c6; border-radius: 16px; padding: 2rem;
        box-shadow: 0 20px 50px rgba(0, 0, 0, 0.08);
      }
      h1 { margin: 0 0 0.5rem; font-size: 1.4rem; }
      form { display: flex; flex-direction: column; gap: 0.9rem; }
      label { display: flex; flex-direction: column; gap: 0.3rem; font-size: 0.85rem; color: #5a5a5a; }
      input {
        padding: 0.55rem 0.75rem; border: 1px solid #d6d1c6; border-radius: 8px;
        font-size: 0.95rem; background: #fafaf7;
      }
      input:focus { outline: none; border-color: #8a8270; }
      button {
        margin-top: 0.3rem; padding: 0.6rem 1rem; border: none; border-radius: 8px;
        background: #2b2a26; color: #fff; font-size: 0.95rem; cursor: pointer;
      }
      button:hover { background: #171717; }
      .error {
        margin: 0; padding: 0.6rem 0.8rem; border-radius: 8px;
        background: #fdecea; border: 1px solid #f5c6cb; color: #842029; font-size: 0.85rem;
      }
      .alt { margin-top: 1.2rem; text-align: center; font-size: 0.85rem; }
      .alt a { color: #2b2a26; }
    </style>
  </head>
  <body>
    <main>
      <h1>{{.Heading}}</h1>
      {{if .Error}}<p class="error">{{.Error}}</p>{{end}}
      <form method="post" action="{{.Action}}">
        <input type="hidden" name="next" value="{{.Next}}">
        <label>Username
          <input type="text" name="username" autocomplete="username" required autofocus>
        </label>
        <label>Password
          <input type="password" name="password" autocomplete="current-password" required>
        </label>
        {{if .ShowConfirmField}}
        <label>Confirm password
          <input type="password" name="password_confirm" autocomplete="new-password" required>
        </label>
        {{end}}
        <button type="submit">{{.SubmitLabel}}</button>
      </form>
      {{if .AlternativeLink}}<p class="alt"><a href="{{.AlternativeLink}}">{{.AlternativeLabel}}</a></p>{{end}}
    </main>
  </body>
</html>`))

// registerPageTemplate is the same chrome as login; a separate template lets
// future divergence (e.g. a password-strength meter) land without forking
// conditionals inside one template.
var registerPageTemplate = loginPageTemplate

// handleLoginGet renders the login form, passing through a sanitized ?next=
// so a successful POST can return the user to the page they came from.
func (s *Server) handleLoginGet(w http.ResponseWriter, r *http.Request) {
	data := loginAuthPageData{
		Title:            "Sign in — sth",
		Heading:          "Sign in",
		Action:           "/login",
		SubmitLabel:      "Sign in",
		Next:             safeRedirectTarget(r.URL.Query().Get("next")),
		AlternativeLink:  altLink("/register", s.allowRegistration),
		AlternativeLabel: "No account? Register",
	}
	renderAuthPage(w, loginPageTemplate, data)
}

// handleLoginPost validates credentials and establishes a session cookie.
// On failure it re-renders the form with a generic error (we do not reveal
// whether the username exists, to avoid user enumeration).
func (s *Server) handleLoginPost(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		renderLoginError(w, r, "Could not parse the form. Please try again.")
		return
	}
	username := strings.TrimSpace(r.FormValue("username"))
	password := r.FormValue("password")
	next := safeRedirectTarget(r.FormValue("next"))

	if username == "" || password == "" {
		renderLoginError(w, r, "Username and password are required.")
		return
	}

	user, err := s.store.FindUserByUsername(username)
	if err != nil {
		// Unknown user — treat identically to a wrong password.
		renderLoginError(w, r, "Invalid username or password.")
		return
	}
	ok, err := s.store.VerifyPassword(user.ID, password)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !ok {
		renderLoginError(w, r, "Invalid username or password.")
		return
	}

	token, err := s.store.CreateLoginSession(user.ID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	setSessionCookie(w, token, r)
	http.Redirect(w, r, next, http.StatusFound)
}

// handleRegisterGet renders the registration form, or a "registration closed"
// notice when allowRegistration is off (the page is still reachable so a user
// lands somewhere meaningful rather than a bare 403 on GET).
func (s *Server) handleRegisterGet(w http.ResponseWriter, r *http.Request) {
	if !s.allowRegistration {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`<!doctype html><html lang="en"><head><meta charset="utf-8"><title>Registration closed</title>
<style>body{font-family:"IBM Plex Sans","Segoe UI",sans-serif;background:#f6f4ee;color:#171717;display:flex;align-items:center;justify-content:center;min-height:100vh;margin:0}
main{max-width:380px;background:#fff;border:1px solid #d6d1c6;border-radius:16px;padding:2rem;text-align:center}
a{color:#2b2a26}</style></head><body><main><h1>Registration is closed</h1>
<p>New accounts can only be created by an administrator.</p>
<p><a href="/login">Go to sign in</a></p></main></body></html>`))
		return
	}
	data := loginAuthPageData{
		Title:            "Register — sth",
		Heading:          "Create your account",
		Action:           "/register",
		SubmitLabel:      "Create account",
		Next:             safeRedirectTarget(r.URL.Query().Get("next")),
		ShowConfirmField: true,
		AlternativeLink:  "/login",
		AlternativeLabel: "Already have an account? Sign in",
	}
	renderAuthPage(w, registerPageTemplate, data)
}

// handleRegisterPost creates a user with a password and immediately logs them
// in. Returns 403 when registration is disabled (defense-in-depth: the GET
// already shows a notice, but the POST must also refuse).
func (s *Server) handleRegisterPost(w http.ResponseWriter, r *http.Request) {
	if !s.allowRegistration {
		http.Error(w, "Registration is disabled on this server.", http.StatusForbidden)
		return
	}
	if err := r.ParseForm(); err != nil {
		renderRegisterError(w, r, "Could not parse the form. Please try again.")
		return
	}
	username := strings.TrimSpace(r.FormValue("username"))
	password := r.FormValue("password")
	confirm := r.FormValue("password_confirm")
	next := safeRedirectTarget(r.FormValue("next"))

	if username == "" {
		renderRegisterError(w, r, "Username is required.")
		return
	}
	if len(password) < session.MinPasswordLength {
		renderRegisterError(w, r, "Password must be at least 8 characters.")
		return
	}
	if password != confirm {
		renderRegisterError(w, r, "Passwords do not match.")
		return
	}

	user, err := s.store.CreateUser(username)
	if err != nil {
		if err == session.ErrUsernameTaken {
			renderRegisterError(w, r, "That username is already taken.")
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := s.store.SetPassword(user.ID, password); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	token, err := s.store.CreateLoginSession(user.ID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	setSessionCookie(w, token, r)
	http.Redirect(w, r, next, http.StatusFound)
}

// handleLogout deletes the server-side session and clears the cookie. It is a
// POST so a cross-site GET cannot log a user out (minor, but consistent with
// treating login state as mutable via mutating verbs only).
func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(sessionCookieName); err == nil {
		_ = s.store.DeleteLoginSession(cookie.Value)
	}
	clearSessionCookie(w, r)
	http.Redirect(w, r, "/login", http.StatusFound)
}

// renderAuthPage writes a 200 HTML response from an auth-page template,
// falling back to a plain error string if template execution fails.
func renderAuthPage(w http.ResponseWriter, tmpl *template.Template, data loginAuthPageData) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := tmpl.Execute(w, data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// renderLoginError re-renders the login form preserving the next target.
func renderLoginError(w http.ResponseWriter, r *http.Request, message string) {
	data := loginAuthPageData{
		Title:            "Sign in — sth",
		Heading:          "Sign in",
		Action:           "/login",
		SubmitLabel:      "Sign in",
		Error:            message,
		Next:             safeRedirectTarget(r.URL.Query().Get("next")),
		AlternativeLink:  altLink("/register", true),
		AlternativeLabel: "No account? Register",
	}
	// On a POST the query string carries no ?next; pull it from the submitted
	// form value instead so the redirect target survives the re-render.
	if data.Next == "" {
		_ = r.ParseForm()
		data.Next = safeRedirectTarget(r.FormValue("next"))
	}
	renderAuthPage(w, loginPageTemplate, data)
}

// renderRegisterError re-renders the register form preserving the next target.
func renderRegisterError(w http.ResponseWriter, r *http.Request, message string) {
	_ = r.ParseForm()
	data := loginAuthPageData{
		Title:            "Register — sth",
		Heading:          "Create your account",
		Action:           "/register",
		SubmitLabel:      "Create account",
		Error:            message,
		Next:             safeRedirectTarget(r.FormValue("next")),
		ShowConfirmField: true,
		AlternativeLink:  "/login",
		AlternativeLabel: "Already have an account? Sign in",
	}
	renderAuthPage(w, registerPageTemplate, data)
}

// setSessionCookie issues the session cookie with hardened attributes. Secure
// is set only when the request arrived over HTTPS (via r.TLS or
// X-Forwarded-Proto) so plain-HTTP local dev still works; the trade-off (no
// transport protection on HTTP) is the same as for API keys over HTTP.
func setSessionCookie(w http.ResponseWriter, token string, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/",
		MaxAge:   sessionCookieMaxAge,
		HttpOnly: true,
		Secure:   determineScheme(r) == "https",
		SameSite: http.SameSiteLaxMode,
	})
}

// clearSessionCookie overwrites the cookie with an immediately-expiring value.
func clearSessionCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   determineScheme(r) == "https",
		SameSite: http.SameSiteLaxMode,
	})
}

// verifySessionCookie reads the cookie and resolves it to a userID. ok is
// false for missing cookie, unknown/expired token, or a store error — callers
// fall back to other auth paths or 401.
func (s *Server) verifySessionCookie(r *http.Request) (userID string, ok bool, err error) {
	cookie, cErr := r.Cookie(sessionCookieName)
	if cErr != nil {
		return "", false, nil
	}
	uid, ok, err := s.store.VerifyLoginSession(cookie.Value)
	if err != nil || !ok {
		return "", false, err
	}
	return uid, true, nil
}

// acceptsHTML reports whether the client prefers an HTML response. We use it
// to decide between 302-to-/login (browsers) and 401+WWW-Authenticate (API
// clients like curl) for unauthenticated GET requests.
func acceptsHTML(r *http.Request) bool {
	accept := r.Header.Get("Accept")
	for _, part := range strings.Split(accept, ",") {
		entry := strings.TrimSpace(part)
		// Match "text/html" and "text/html;...", but not e.g. "application/xhtml".
		if entry == "text/html" || strings.HasPrefix(entry, "text/html;") {
			return true
		}
	}
	return false
}

// safeRedirectTarget sanitizes a user-supplied ?next= value so it can only
// redirect within this site. Empty or unsafe values collapse to "/".
// Rules: must start with a single "/" (so not "//host" which browsers treat as
// protocol-relative absolute), and must not start with "/\" (a backslash that
// some browsers normalize). Scheme-prefixed values are rejected by the
// leading-"/" rule.
func safeRedirectTarget(raw string) string {
	if raw == "" {
		return "/"
	}
	if !strings.HasPrefix(raw, "/") {
		return "/"
	}
	if strings.HasPrefix(raw, "//") || strings.HasPrefix(raw, "/\\") {
		return "/"
	}
	return raw
}

// altLink returns the cross-link between login and register, hidden when
// registration is disabled (the login page then offers no register link).
func altLink(href string, allowRegistration bool) string {
	if !allowRegistration {
		return ""
	}
	return href
}
