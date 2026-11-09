package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/sun-praise/static-html/internal/session"
)

type shareRequest struct {
	User string `json:"user"`
}

type sharesResponse struct {
	SessionID string               `json:"sessionId"`
	Shares    []session.ShareEntry `json:"shares"`
}

// parseShareTargetPath parses "/api/sessions/<id>/shares/<username>" into its
// two segments. ok is false for anything else.
func parseShareTargetPath(urlPath string) (sessionID, username string, ok bool) {
	trimmed := strings.TrimPrefix(urlPath, "/api/sessions/")
	parts := strings.Split(trimmed, "/")
	if len(parts) != 3 || parts[0] == "" || parts[1] != "shares" || parts[2] == "" {
		return "", "", false
	}
	return parts[0], parts[2], true
}

// parseShareTargetPathOK reports whether urlPath is a well-formed
// "/api/sessions/<id>/shares/<username>" (used by the router).
func parseShareTargetPathOK(urlPath string) bool {
	_, _, ok := parseShareTargetPath(urlPath)
	return ok
}

// handleListShares answers GET /api/sessions/{id}/shares with the share list.
func (s *Server) handleListShares(w http.ResponseWriter, r *http.Request) {
	sessionID, ok := extractSessionIDFromMetaPath(r.URL.Path, "/api/sessions/", "/shares")
	if !ok {
		writeJSONError(w, http.StatusBadRequest, "Invalid session ID.")
		return
	}

	if !s.requireOwner(w, r, sessionID) {
		return
	}

	shares, err := s.store.ListSessionShares(sessionID)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, sharesResponse{SessionID: sessionID, Shares: shares})
}

// handleAddShare grants a user (by username) read access to a session via
// PUT /api/sessions/{id}/shares {"user": "bob"}. Owner-only; sharing with the
// owner or an unknown username is rejected.
func (s *Server) handleAddShare(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	sessionID, ok := extractSessionIDFromMetaPath(r.URL.Path, "/api/sessions/", "/shares")
	if !ok {
		writeJSONError(w, http.StatusBadRequest, "Invalid session ID.")
		return
	}

	if !s.requireOwner(w, r, sessionID) {
		return
	}

	var req shareRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "Request body must be valid JSON.")
		return
	}
	req.User = strings.TrimSpace(req.User)
	if req.User == "" {
		writeJSONError(w, http.StatusBadRequest, "user is required.")
		return
	}

	target, err := s.store.FindUserByUsername(req.User)
	if err != nil {
		if errors.Is(err, session.ErrUserNotFound) {
			writeJSONError(w, http.StatusBadRequest, "Unknown user: "+req.User)
			return
		}
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	owner, status, err := s.store.SessionOwner(sessionID)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if status == session.OwnerOwned && owner == target.ID {
		writeJSONError(w, http.StatusBadRequest, "The owner already has access to this session.")
		return
	}

	if err := s.store.ShareSession(sessionID, target.ID); err != nil {
		if errors.Is(err, session.ErrSessionNotFound) {
			writeJSONError(w, http.StatusNotFound, "Session not found.")
			return
		}
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	shares, err := s.store.ListSessionShares(sessionID)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, sharesResponse{SessionID: sessionID, Shares: shares})
}

// handleRemoveShare revokes a user's share via DELETE
// /api/sessions/{id}/shares/{username}. Owner-only.
func (s *Server) handleRemoveShare(w http.ResponseWriter, r *http.Request) {
	sessionID, username, ok := parseShareTargetPath(r.URL.Path)
	if !ok {
		writeJSONError(w, http.StatusBadRequest, "Invalid share path.")
		return
	}

	if !s.requireOwner(w, r, sessionID) {
		return
	}

	target, err := s.store.FindUserByUsername(username)
	if err != nil {
		if errors.Is(err, session.ErrUserNotFound) {
			writeJSONError(w, http.StatusBadRequest, "Unknown user: "+username)
			return
		}
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	if err := s.store.UnshareSession(sessionID, target.ID); err != nil {
		switch {
		case errors.Is(err, session.ErrSessionNotFound):
			writeJSONError(w, http.StatusNotFound, "Session not found.")
		case errors.Is(err, session.ErrNotShared):
			writeJSONError(w, http.StatusNotFound, "User "+username+" is not shared on this session.")
		default:
			writeJSONError(w, http.StatusInternalServerError, err.Error())
		}
		return
	}

	shares, err := s.store.ListSessionShares(sessionID)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, sharesResponse{SessionID: sessionID, Shares: shares})
}

// authorizePreview enforces the --protect-previews access rule for
// /s/<id>/... requests once the middleware has authenticated the caller: an
// owned session is visible to its owner and the users it is shared with;
// legacy unowned sessions stay visible to any authenticated user. It is a
// pass-through when previews are not protected.
func (s *Server) authorizePreview(w http.ResponseWriter, r *http.Request, sessionID string) bool {
	if !s.AuthEnabled() || !s.ProtectPreviews() {
		return true
	}

	uid, ok := currentUser(r)
	if !ok {
		// Unreachable in practice (the middleware rejects unauthenticated
		// preview requests first); fail closed regardless.
		w.Header().Set("WWW-Authenticate", `Bearer realm="sth"`)
		http.Error(w, "Unauthorized: a valid API key is required.", http.StatusUnauthorized)
		return false
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
		return true
	}
	if owner == uid {
		return true
	}

	shared, err := s.store.IsSessionSharedWith(sessionID, uid)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return false
	}
	if !shared {
		http.Error(w, "Forbidden: this session is not shared with you.", http.StatusForbidden)
		return false
	}
	return true
}

// requireViewer is requireOwner's read-only counterpart for endpoints the
// preview drawer calls (metadata, peers, chain): the owner passes, users the
// session is shared with pass, everyone else is rejected. Unowned sessions
// keep the requireOwner behavior (403 under auth).
func (s *Server) requireViewer(w http.ResponseWriter, r *http.Request, sessionID string) bool {
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
	if owner == uid {
		return true
	}

	shared, err := s.store.IsSessionSharedWith(sessionID, uid)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return false
	}
	if !shared {
		http.Error(w, "Forbidden: you do not own this session", http.StatusForbidden)
		return false
	}
	return true
}
