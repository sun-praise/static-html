package server

import (
	"context"
	"net/http"
	"strings"

	"github.com/sun-praise/static-html/internal/session"
)

// ctxKey is an unexported type to avoid context-key collisions.
type ctxKey int

const userCtxKey ctxKey = iota

// authMiddleware enforces API-key authentication when authEnabled is on.
// When auth is off (the default), it is a no-op pass-through so behavior is
// byte-for-byte identical to the legacy open server.
//
// Path classification:
//   - Preview paths ("/s/...") are gated ONLY by protectPreviews. When
//     protectPreviews is off, previews stay open even under --auth, to
//     preserve the "upload then share the /s/<id>/ link" workflow.
//   - Every other path (home/list, POST/PUT/DELETE session mutations, GET
//     metadata/peers/download) requires a valid Bearer key when authEnabled.
//
// On success the authenticated userID is stored in the request context for
// handlers to read via currentUser.
func (s *Server) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.AuthEnabled() {
			next.ServeHTTP(w, r)
			return
		}

		isPreview := strings.HasPrefix(r.URL.Path, "/s/")
		if isPreview && !s.ProtectPreviews() {
			next.ServeHTTP(w, r)
			return
		}

		userID, ok, err := s.verifyBearer(r)
		if err != nil {
			// Store/query failure, not a bad key — report as 500 so an outage
			// isn't misdiagnosed as invalid credentials.
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if !ok {
			w.Header().Set("WWW-Authenticate", `Bearer realm="sth"`)
			http.Error(w, "Unauthorized: a valid API key is required.", http.StatusUnauthorized)
			return
		}

		ctx := context.WithValue(r.Context(), userCtxKey, userID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
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
