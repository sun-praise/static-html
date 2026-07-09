package server

import (
	"context"
	"net/http"
	"strings"
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

		userID, ok := s.verifyBearer(r)
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
func (s *Server) verifyBearer(r *http.Request) (string, bool) {
	header := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if !strings.HasPrefix(header, prefix) {
		return "", false
	}
	token := strings.TrimSpace(strings.TrimPrefix(header, prefix))
	if token == "" {
		return "", false
	}
	uid, ok, err := s.store.VerifyAPIKey(token)
	if err != nil || !ok {
		return "", false
	}
	return uid, true
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
// preserving the legacy unowned-session behavior.
func (s *Server) assignOwnerIfNeeded(r *http.Request, sessionID string) {
	uid, ok := currentUser(r)
	if !ok {
		return
	}
	_ = s.store.SetSessionOwner(sessionID, uid)
}

// requireOwner checks that the authenticated user owns sessionID. It writes a
// 403 response and returns false when the user is authenticated but does not
// own the session. It returns true when ownership is confirmed OR when auth is
// disabled (no user in context) — callers gate the whole flow on authEnabled
// via the middleware, so a missing user here means auth-off, which is
// permissive by design for backward compatibility.
func (s *Server) requireOwner(w http.ResponseWriter, r *http.Request, sessionID string) bool {
	uid, ok := currentUser(r)
	if !ok {
		// Auth disabled: no ownership enforcement.
		return true
	}
	owner, hasOwner, err := s.store.SessionOwner(sessionID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return false
	}
	// Sessions created while auth was off have no owner; in auth mode treat
	// them as unowned (not accessible to any authenticated user).
	if !hasOwner || owner == "" {
		http.Error(w, "Forbidden: session has no owner in this deployment.", http.StatusForbidden)
		return false
	}
	if owner != uid {
		http.Error(w, "Forbidden: you do not own this session.", http.StatusForbidden)
		return false
	}
	return true
}
