package server

import (
	"context"
	"net/http"
	"net/url"
	"strings"

	"github.com/sun-praise/static-html/internal/session"
)

// ctxKey is an unexported type to avoid context-key collisions.
type ctxKey int

const userCtxKey ctxKey = iota

// authMiddleware enforces authentication when authEnabled is on.
// When auth is off (the default), it is a no-op pass-through so behavior is
// byte-for-byte identical to the legacy open server.
//
// Path/method classification (auth enabled):
//   - Auth pages ("/login", "/register", "/logout") are always open so an
//     unauthenticated user can reach the login form.
//   - Preview paths ("/s/...") are gated ONLY by protectPreviews. When
//     protectPreviews is off, previews stay open even under --auth, to
//     preserve the "upload then share the /s/<id>/ link" workflow.
//   - Mutating methods (POST/PUT/DELETE) accept ONLY a Bearer API key. The
//     session cookie is deliberately ignored here to make CSRF structurally
//     impossible: a cross-site request cannot set a custom Authorization
//     header, so it can never satisfy this check. Browser users browse with
//     the cookie; writes still go through CLI (sth send --api-key).
//   - Read methods (GET/HEAD) on other paths accept EITHER a valid session
//     cookie OR a Bearer key, so both browsers and API clients work. When
//     neither is present and the client wants HTML (a browser), we redirect
//     to /login?next=<path> instead of returning a bare 401.
//
// On success the authenticated userID is stored in the request context for
// handlers to read via currentUser.
func (s *Server) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.AuthEnabled() {
			next.ServeHTTP(w, r)
			return
		}

		// Auth pages are always reachable; they handle their own logic.
		if isAuthPage(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}

		isPreview := strings.HasPrefix(r.URL.Path, "/s/")
		if isPreview && !s.ProtectPreviews() {
			next.ServeHTTP(w, r)
			return
		}

		// Mutating requests: Bearer only (CSRF-safe). The session cookie is
		// intentionally NOT consulted, so a stolen/leaked cookie cannot be
		// used to perform writes via a cross-site request.
		if isMutatingMethod(r.Method) {
			s.requireBearer(w, r, next)
			return
		}

		// Read requests: try cookie first, then Bearer. Either is sufficient.
		uid, ok, err := s.verifySessionCookie(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if !ok {
			uid, ok, err = s.verifyBearer(r)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
		}
		if !ok {
			// Browser clients get redirected to the login page; API clients
			// (curl, sth CLI) get the original 401 + WWW-Authenticate.
			if acceptsHTML(r) {
				target := "/login?next=" + url.QueryEscape(r.URL.Path)
				http.Redirect(w, r, target, http.StatusFound)
				return
			}
			w.Header().Set("WWW-Authenticate", `Bearer realm="sth"`)
			http.Error(w, "Unauthorized: a valid API key is required.", http.StatusUnauthorized)
			return
		}

		ctx := context.WithValue(r.Context(), userCtxKey, uid)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// requireBearer handles the mutating-method path: accept only a valid Bearer
// key and inject the user, otherwise 401. Extracted so the read-path branch
// stays readable.
func (s *Server) requireBearer(w http.ResponseWriter, r *http.Request, next http.Handler) {
	uid, ok, err := s.verifyBearer(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !ok {
		w.Header().Set("WWW-Authenticate", `Bearer realm="sth"`)
		http.Error(w, "Unauthorized: a valid API key is required.", http.StatusUnauthorized)
		return
	}
	ctx := context.WithValue(r.Context(), userCtxKey, uid)
	next.ServeHTTP(w, r.WithContext(ctx))
}

// isAuthPage reports whether path is one of the browser auth pages that must
// stay reachable without credentials (so a logged-out user can sign in).
func isAuthPage(path string) bool {
	return path == "/login" || path == "/register" || path == "/logout"
}

// isMutatingMethod reports whether the HTTP method can change server state and
// therefore must use the CSRF-safe Bearer path only.
func isMutatingMethod(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch:
		return true
	}
	return false
}

// verifyBearer extracts the Bearer token from the Authorization header and
// verifies it against the store. Returns the owning userID on success.
// The error is non-nil only for store/query failures (distinct from an invalid
// key, which is reported as ok=false with a nil error) so callers can return
// 500 for outages and 401 only for genuinely bad credentials.
func (s *Server) verifyBearer(r *http.Request) (userID string, ok bool, err error) {
	header := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if !strings.HasPrefix(header, prefix) {
		return "", false, nil
	}
	token := strings.TrimSpace(strings.TrimPrefix(header, prefix))
	if token == "" {
		return "", false, nil
	}
	uid, ok, err := s.store.VerifyAPIKey(token)
	if err != nil {
		return "", false, err
	}
	if !ok {
		return "", false, nil
	}
	return uid, true, nil
}

// currentUser returns the authenticated userID from the request context, if
// any. ok is false when auth is disabled or no credential was provided.
func currentUser(r *http.Request) (userID string, ok bool) {
	v := r.Context().Value(userCtxKey)
	uid, isStr := v.(string)
	if !isStr || uid == "" {
		return "", false
	}
	return uid, true
}

// assignOwnerIfNeeded stamps the authenticated user as the owner of a newly
// created session. It is a no-op when auth is disabled (no user in context),
// preserving the legacy unowned-session behavior. Returns an error if the
// owner stamp fails so create handlers can fail loudly instead of silently
// leaking an unowned session under auth.
func (s *Server) assignOwnerIfNeeded(r *http.Request, sessionID string) error {
	uid, ok := currentUser(r)
	if !ok {
		return nil
	}
	return s.store.SetSessionOwner(sessionID, uid)
}

// requireOwner checks that the authenticated user owns sessionID. It writes a
// response and returns false when the check fails:
//   - 404 when the session does not exist (distinct from ownership denial)
//   - 403 when the session exists but has no owner (auth-on) or a different
//     owner
//
// It returns true when ownership is confirmed OR when auth is disabled (no
// user in context) — callers gate the whole flow on authEnabled via the
// middleware, so a missing user here means auth-off, which is permissive by
// design for backward compatibility.
func (s *Server) requireOwner(w http.ResponseWriter, r *http.Request, sessionID string) bool {
	uid, ok := currentUser(r)
	if !ok {
		// Auth disabled: no ownership enforcement.
		return true
	}
	owner, status, err := s.store.SessionOwner(sessionID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return false
	}
	switch status {
	case session.OwnerMissing:
		http.NotFound(w, r)
		return false
	case session.OwnerUnowned:
		http.Error(w, "Forbidden: session has no owner in this deployment", http.StatusForbidden)
		return false
	}
	if owner != uid {
		http.Error(w, "Forbidden: you do not own this session", http.StatusForbidden)
		return false
	}
	return true
}
