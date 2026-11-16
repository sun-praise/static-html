package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path"
	"path/filepath"
	"strings"

	"github.com/sun-praise/static-html/internal/session"
)

type shareRequest struct {
	User string `json:"user"`
	// Path, when non-empty, scopes the grant to a single HTML page inside
	// the session (slash-separated, relative). Empty grants the whole
	// session.
	Path string `json:"path"`
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

	sharePath := session.SharePathAll
	if req.Path != "" {
		var err error
		sharePath, err = s.validSharePage(sessionID, req.Path)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, err.Error())
			return
		}
	}

	if err := s.store.ShareSessionPage(sessionID, target.ID, sharePath); err != nil {
		if errors.Is(err, session.ErrSessionNotFound) {
			writeJSONError(w, http.StatusNotFound, "Session not found.")
			return
		}
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	scope := "session"
	if sharePath != session.SharePathAll {
		scope = "page"
	}
	s.recordAudit(r, auditShareGrant, sessionID, auditOK,
		"user="+req.User+" scope="+scope+" path="+sharePath, "")

	shares, err := s.store.ListSessionShares(sessionID)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, sharesResponse{SessionID: sessionID, Shares: shares})
}

// validSharePage normalizes and validates a page path for a NEW page-scoped
// grant: a relative path inside the session root that names an HTML file
// which currently exists. Returns the normalized slash path.
func (s *Server) validSharePage(sessionID, rawPath string) (string, error) {
	normalized, err := session.NormalizeSharePagePath(rawPath)
	if err != nil {
		return "", err
	}
	sess, found, err := s.store.Get(sessionID)
	if err != nil {
		return "", err
	}
	if !found {
		return "", errors.New("session not found")
	}
	target := filepath.Clean(filepath.Join(sess.StoredRootDir, filepath.FromSlash(normalized)))
	if !IsSubpath(sess.StoredRootDir, target) {
		return "", errors.New("page path must be relative and inside the session")
	}
	if err := ensureFile(target); err != nil {
		return "", fmt.Errorf("page %q does not exist in this session", normalized)
	}
	return normalized, nil
}

// handleRemoveShare revokes a user's share via DELETE
// /api/sessions/{id}/shares/{username}[?path=<page>]. Owner-only. Without
// ?path the whole-session grant is revoked; with it, that page grant.
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

	unsharePath := session.SharePathAll
	pageScoped := false
	if raw := r.URL.Query().Get("path"); raw != "" {
		// Revocation only normalizes: the grant must stay removable even when
		// the page no longer exists on disk, otherwise a re-upload could
		// strand a live grant with no way to revoke it short of the whole
		// session.
		unsharePath, err = session.NormalizeSharePagePath(raw)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, err.Error())
			return
		}
		pageScoped = true
	}

	if err := s.store.UnshareSessionPage(sessionID, target.ID, unsharePath); err != nil {
		switch {
		case errors.Is(err, session.ErrSessionNotFound):
			writeJSONError(w, http.StatusNotFound, "Session not found.")
		case errors.Is(err, session.ErrNotShared):
			if pageScoped {
				writeJSONError(w, http.StatusNotFound, "User "+username+" has no share on page "+unsharePath+".")
				return
			}
			writeJSONError(w, http.StatusNotFound, "User "+username+" is not shared on this session.")
		default:
			writeJSONError(w, http.StatusInternalServerError, err.Error())
		}
		return
	}
	revokeScope := "session"
	if pageScoped {
		revokeScope = "page"
	}
	s.recordAudit(r, auditShareRevoke, sessionID, auditOK,
		"user="+username+" scope="+revokeScope+" path="+unsharePath, "")

	shares, err := s.store.ListSessionShares(sessionID)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, sharesResponse{SessionID: sessionID, Shares: shares})
}

// renderAssetExts is the whitelist of non-HTML extensions a page-scoped grant
// may read: the plumbing a browser needs to render the granted page (styles,
// scripts, images, fonts, media). Everything else — JSON payloads, text
// dumps, PDFs, archives, .env files — is session content rather than render
// plumbing, so it requires a whole-session grant.
var renderAssetExts = map[string]bool{
	".css":   true,
	".js":    true,
	".mjs":   true,
	".map":   true,
	".png":   true,
	".jpg":   true,
	".jpeg":  true,
	".gif":   true,
	".svg":   true,
	".webp":  true,
	".avif":  true,
	".bmp":   true,
	".ico":   true,
	".woff":  true,
	".woff2": true,
	".ttf":   true,
	".otf":   true,
	".eot":   true,
	".mp4":   true,
	".webm":  true,
	".ogg":   true,
	".mp3":   true,
	".wav":   true,
	".wasm":  true,
	".vtt":   true,
	// webmanifest is fetched by the browser for installable/offline pages.
	".webmanifest": true,
}

// isRenderAsset reports whether assetPath names a browser render asset a
// page-scoped grant may read.
func isRenderAsset(assetPath string) bool {
	return renderAssetExts[strings.ToLower(path.Ext(strings.TrimSpace(assetPath)))]
}

// authorizePreview enforces the --protect-previews access rule for
// /s/<id>/... requests once the middleware has authenticated the caller:
//   - the owner and legacy unowned sessions see everything;
//   - a whole-session grant sees every page and asset;
//   - a page grant (path) sees exactly that HTML page plus the session's
//     render assets (whitelist in renderAssetExts); sibling HTML pages stay
//     403, and non-render files (JSON, dumps, PDFs, archives) need a
//     whole-session grant.
//
// The /s/<id>/ root serves the entry file, so it is treated as a request for
// the entry page. anyGrantSuffices relaxes page-vs-asset discrimination (the
// live-reload websocket uses it). It is a pass-through when previews are not
// protected.
func (s *Server) authorizePreview(w http.ResponseWriter, r *http.Request, sess session.Session, assetPath string, anyGrantSuffices bool) bool {
	if !s.AuthEnabled() || !s.ProtectPreviews() {
		return true
	}

	// Audit HTML page views only. A single page load also fetches css/js/
	// images, and the live-reload websocket is a long-lived connection —
	// recording those would bury the events that matter.
	auditPreview := func(allowed bool, reason string) {
		if anyGrantSuffices || !auditPageRequest(assetPath) {
			return
		}
		outcome, action := auditDenied, auditPreviewDeny
		if allowed {
			outcome, action = auditOK, auditPreviewAllow
		}
		detail := "page=" + sharePathFor(sess, assetPath) + " reason=" + reason
		s.recordAudit(r, action, sess.ID, outcome, detail, "")
	}

	uid, ok := currentUser(r)
	if !ok {
		// Unreachable in practice (the middleware rejects unauthenticated
		// preview requests first); fail closed regardless.
		w.Header().Set("WWW-Authenticate", `Bearer realm="sth"`)
		auditPreview(false, "no_credentials")
		http.Error(w, "Unauthorized: a valid API key is required.", http.StatusUnauthorized)
		return false
	}

	owner, status, err := s.store.SessionOwner(sess.ID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return false
	}
	switch status {
	case session.OwnerMissing:
		http.NotFound(w, r)
		return false
	case session.OwnerUnowned:
		auditPreview(true, "legacy_unowned")
		return true
	}
	if owner == uid {
		auditPreview(true, "owner")
		return true
	}

	var allowed bool
	switch {
	case anyGrantSuffices:
		allowed, err = s.store.HasAnyShare(sess.ID, uid)
	case isHTMLRequestPath(sess, assetPath):
		allowed, err = s.store.HasShareOn(sess.ID, uid, sharePathFor(sess, assetPath))
	case isRenderAsset(assetPath):
		allowed, err = s.store.HasAnyShare(sess.ID, uid)
	default:
		// Not a page and not render plumbing (JSON payloads, dumps, PDFs,
		// archives, .env): only a whole-session grant covers it.
		allowed, err = s.store.IsSessionSharedWith(sess.ID, uid)
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return false
	}
	if !allowed {
		auditPreview(false, "no_share")
		http.Error(w, "Forbidden: this page is not shared with you.", http.StatusForbidden)
		return false
	}
	auditPreview(true, "shared")
	return true
}

// isHTMLRequestPath reports whether the requested path names an HTML page
// (the root path counts: it serves the session's entry file).
func isHTMLRequestPath(sess session.Session, assetPath string) bool {
	if strings.TrimSpace(assetPath) == "" {
		return true
	}
	return IsHTMLFile(assetPath)
}

// sharePathFor maps a preview request path to the normalized page path used
// by share grants. The root path maps to the entry file's path.
func sharePathFor(sess session.Session, assetPath string) string {
	trimmed := strings.TrimSpace(assetPath)
	if trimmed == "" {
		if rel, err := filepath.Rel(sess.StoredRootDir, sess.StoredEntryFile); err == nil {
			return path.Clean(filepath.ToSlash(rel))
		}
		return ""
	}
	return path.Clean(trimmed)
}

// requireViewer is requireOwner's read-only counterpart for endpoints the
// preview drawer calls (metadata, peers, chain, diff): the owner passes, users
// the session is shared with pass, everyone else is rejected. Unowned sessions
// keep the requireOwner behavior (403 under auth).
func (s *Server) requireViewer(w http.ResponseWriter, r *http.Request, sessionID string) bool {
	return s.requireViewerGrant(w, r, sessionID, true)
}

// requireViewerSessionScope is requireViewer tightened to whole-session
// grants. A page-scoped sharee may read the granted page, but not the
// session-wide document history: the diff endpoint returns the full text of
// two versions, which is outside what a page grant covers.
func (s *Server) requireViewerSessionScope(w http.ResponseWriter, r *http.Request, sessionID string) bool {
	return s.requireViewerGrant(w, r, sessionID, false)
}

// requireViewerGrant implements the two viewer checks above; anyShare selects
// whether a page-scoped grant counts as a share.
func (s *Server) requireViewerGrant(w http.ResponseWriter, r *http.Request, sessionID string, anyShare bool) bool {
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

	var shared bool
	if anyShare {
		shared, err = s.store.HasAnyShare(sessionID, uid)
	} else {
		shared, err = s.store.IsSessionSharedWith(sessionID, uid)
	}
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
