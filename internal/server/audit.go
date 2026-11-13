package server

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/sun-praise/static-html/internal/session"
)

// Short aliases so handler files can record events without importing the
// session package solely for the constants.
const (
	auditLogin         = session.AuditLogin
	auditLoginFailed   = session.AuditLoginFailed
	auditLogout        = session.AuditLogout
	auditAuthRejected  = session.AuditAuthRejected
	auditShareGrant    = session.AuditShareGrant
	auditShareRevoke   = session.AuditShareRevoke
	auditPreviewAllow  = session.AuditPreviewAllow
	auditPreviewDeny   = session.AuditPreviewDeny
	auditSessionCreate = session.AuditSessionCreate
	auditSessionDelete = session.AuditSessionDelete
	auditOK            = session.AuditOutcomeOK
	auditDenied        = session.AuditOutcomeDenied
)

// Anonymous clients must not be able to fill audit_events (and saturate the
// single SQLite connection) with a flood of 401s. Rejections are coalesced per
// proxy-observed peer + path inside one window. The bucket deliberately uses
// RemoteAddr (which the client cannot control) rather than X-Forwarded-For:
// an attacker rotating XFF would otherwise get a fresh bucket per request.
// The map is dropped at window rollover and capped, so at most
// auditRejectMaxKeys rejection rows can be written per window.
const (
	auditRejectWindowSeconds = 60
	auditRejectMaxKeys       = 4096
)

// clientIP returns the best-effort source address for an audit row. Behind
// the one-hop Caddy reverse proxy, RemoteAddr is always 127.0.0.1 and Caddy
// puts the real client in X-Forwarded-For. The first XFF entry is used as-is:
// it CAN be spoofed by a client that sends its own XFF header, so audit IP
// values are evidence, not proof — correlation with the reverse proxy's log
// is required for incident response. Never use this value for authorization.
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if first := strings.TrimSpace(strings.Split(xff, ",")[0]); first != "" {
			return first
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil {
		return host
	}
	return r.RemoteAddr
}

// peerHost returns the proxy-observed peer address without the ephemeral
// port. Behind Caddy this is the loopback address, which is exactly what we
// want for throttling: stable, and not influenced by client-supplied headers.
func peerHost(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil {
		return host
	}
	return r.RemoteAddr
}

// auditIdentity resolves the actor for an event. When the request is
// authenticated the username snapshot comes from the DB; otherwise fallback
// is used. A process-local cache keeps repeated page views from adding a
// second DB round-trip on top of the authorization query. Usernames are
// immutable in this deployment, and caching the first observed value is
// exactly the snapshot semantics the audit log wants.
func (s *Server) auditIdentity(r *http.Request, fallback string) (actor, actorID string) {
	uid, ok := currentUser(r)
	if !ok {
		return fallback, ""
	}
	if cached, ok := s.auditUsernameCache.Load(uid); ok {
		return cached.(string), uid
	}
	user, err := s.store.FindUserByID(uid)
	if err != nil {
		return "", uid
	}
	s.auditUsernameCache.Store(uid, user.Username)
	return user.Username, uid
}

// recordAudit appends a security event using the request's identity. Audit is
// strictly best-effort: a broken sink must never turn a successful login or a
// legitimate preview into a 500. Failures are written to stderr (the container
// log) so operators can still see that auditing is degraded.
func (s *Server) recordAudit(r *http.Request, action, target, outcome, detail, actorOverride string) {
	actor, actorID := s.auditIdentity(r, actorOverride)
	s.recordAuditAs(r, action, target, outcome, detail, actor, actorID)
}

// recordAuditAs records an event with an explicitly resolved identity. Login
// and logout know who acted even though authMiddleware has not (or no longer
// has) put a user in the request context.
func (s *Server) recordAuditAs(r *http.Request, action, target, outcome, detail, actor, actorID string) {
	e := session.AuditEvent{
		Actor:     actor,
		ActorID:   actorID,
		Action:    action,
		Target:    target,
		Outcome:   outcome,
		IP:        clientIP(r),
		UserAgent: r.UserAgent(),
		Detail:    detail,
	}
	if err := s.store.RecordAudit(e); err != nil {
		fmt.Fprintf(os.Stderr, "audit: failed to record %s: %v\n", action, err)
	}
}

// allowAuditReject coalesces repeated anonymous rejections. Returns true when
// this (peer, path) pair has not been recorded in the current window. When the
// cap is reached, further new keys are dropped instead of clearing the map —
// clearing would let an attacker reset the budget by spraying distinct paths.
func (s *Server) allowAuditReject(peer, path string) bool {
	window := time.Now().Unix() / auditRejectWindowSeconds
	key := peer + "|" + path

	s.auditRejectMu.Lock()
	defer s.auditRejectMu.Unlock()
	if s.auditRejectWindow != window {
		s.auditRejectSeen = make(map[string]struct{})
		s.auditRejectWindow = window
	}
	if _, seen := s.auditRejectSeen[key]; seen {
		return false
	}
	if len(s.auditRejectSeen) >= auditRejectMaxKeys {
		return false
	}
	s.auditRejectSeen[key] = struct{}{}
	return true
}

// auditRejected records a credential rejection. Detail carries only method,
// path and a short reason — never the query string, Authorization header or
// cookie, all of which may hold live credentials.
func (s *Server) auditRejected(r *http.Request, reason string) {
	// Throttle on the peer the proxy actually connected from, NOT on the
	// client-controlled X-Forwarded-For value. The XFF-derived IP is still
	// stored as evidence in the audit row by recordAudit.
	if !s.allowAuditReject(peerHost(r), r.URL.Path) {
		return
	}
	detail := r.Method + " " + r.URL.Path
	if reason != "" {
		detail += " reason=" + reason
	}
	s.recordAudit(r, auditAuthRejected, r.URL.Path, auditDenied, detail, "")
}

// auditPageRequest reports whether a preview request should produce an audit
// row. Only HTML pages (and the session root, which serves the entry page)
// are recorded: a single page load pulls in css/js/images/fonts, and writing
// one row per asset would bury the useful events. WebSocket upgrades are not
// preview page requests and are excluded by the caller.
func auditPageRequest(assetPath string) bool {
	if strings.TrimSpace(assetPath) == "" {
		return true
	}
	return IsHTMLFile(assetPath)
}

// SetAuditRetentionDays configures the periodic prune. It must be called
// before Start. Zero disables automatic pruning entirely.
func (s *Server) SetAuditRetentionDays(days int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if days < 0 {
		days = 0
	}
	s.auditRetentionDays = days
}

// startAuditPrune runs one prune immediately and then every 24h, so a
// long-lived instance does not grow audit_events without bound. done is
// closed on return so Stop can wait for the goroutine before the store is
// closed by the caller.
func (s *Server) startAuditPrune(days int, stop <-chan struct{}, done chan<- struct{}) {
	defer close(done)
	if days <= 0 {
		return
	}
	prune := func() {
		cutoff := time.Now().UTC().Add(-time.Duration(days) * 24 * time.Hour)
		if _, err := s.store.PruneAuditEvents(cutoff); err != nil {
			fmt.Fprintf(os.Stderr, "audit: prune failed: %v\n", err)
		}
	}
	prune()

	ticker := time.NewTicker(24 * time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			prune()
		}
	}
}
