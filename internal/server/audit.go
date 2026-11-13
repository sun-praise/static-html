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

// auditIdentity resolves the actor for an event. When the request is
// authenticated the username snapshot comes from the DB; otherwise fallback
// is used (e.g. the username typed into a failed login form).
func (s *Server) auditIdentity(r *http.Request, fallback string) (actor, actorID string) {
	uid, ok := currentUser(r)
	if !ok {
		return fallback, ""
	}
	actorID = uid
	if user, err := s.store.FindUserByID(uid); err == nil {
		return user.Username, actorID
	}
	return "", actorID
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

// auditRejected records a credential rejection. Detail carries only method,
// path and a short reason — never the query string, Authorization header or
// cookie, all of which may hold live credentials.
func (s *Server) auditRejected(r *http.Request, reason string) {
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
// long-lived instance does not grow audit_events without bound.
func (s *Server) startAuditPrune(days int, stop <-chan struct{}) {
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
