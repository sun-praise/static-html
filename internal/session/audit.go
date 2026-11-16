package session

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Audit event names. The dotted namespace keeps the CLI filter useful
// (--action auth. matches every auth event is intentionally NOT supported;
// the filter is exact to avoid accidentally widening a query).
const (
	AuditLogin         = "auth.login"
	AuditLoginFailed   = "auth.login_failed"
	AuditLogout        = "auth.logout"
	AuditAuthRejected  = "auth.rejected"
	AuditShareGrant    = "share.grant"
	AuditShareRevoke   = "share.revoke"
	AuditPreviewAllow  = "preview.allow"
	AuditPreviewDeny   = "preview.deny"
	AuditSessionCreate = "session.create"
	AuditSessionDelete = "session.delete"
)

// Audit outcomes.
const (
	AuditOutcomeOK     = "ok"
	AuditOutcomeDenied = "denied"
)

// Field limits keep a hostile client from growing the audit table without
// bound by sending a giant User-Agent or username. Truncation is by rune so a
// multibyte username never produces invalid UTF-8 in the DB.
const (
	auditMaxActor   = 128
	auditMaxActorID = 64
	auditMaxAction  = 64
	auditMaxTarget  = 512
	auditMaxOutcome = 16
	auditMaxIP      = 64
	auditMaxUA      = 512
	auditMaxDetail  = 512
)

// AuditEvent is one append-only security event. Actor/ActorID are snapshots
// of the identity at the time of the event: deleting a user must not rewrite
// history. Detail is short free-form context and MUST NEVER contain a
// password, API key, session cookie, request body or full query string.
type AuditEvent struct {
	ID        int64
	TSUnix    int64
	Actor     string
	ActorID   string
	Action    string
	Target    string
	Outcome   string
	IP        string
	UserAgent string
	Detail    string
}

// AuditFilter narrows ListAuditEvents. Limit <= 0 falls back to
// defaultAuditLimit; hard-capped by maxAuditLimit.
type AuditFilter struct {
	Action  string
	Actor   string
	Outcome string
	Since   time.Time
	Limit   int
	Offset  int
}

const (
	defaultAuditLimit = 100
	maxAuditLimit     = 1000
)

func auditTruncate(s string, max int) string {
	s = strings.TrimSpace(s)
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max])
}

// initAudit creates the append-only audit table. It is intentionally a
// separate table with no foreign keys: an audit row must survive deletion of
// the user or session it describes.
func (s *Store) initAudit() error {
	_, err := s.db.Exec(`
		CREATE TABLE IF NOT EXISTS audit_events (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			ts_unix INTEGER NOT NULL,
			actor TEXT NOT NULL DEFAULT '',
			actor_id TEXT NOT NULL DEFAULT '',
			action TEXT NOT NULL,
			target TEXT NOT NULL DEFAULT '',
			outcome TEXT NOT NULL,
			ip TEXT NOT NULL DEFAULT '',
			user_agent TEXT NOT NULL DEFAULT '',
			detail TEXT NOT NULL DEFAULT ''
		);
		CREATE INDEX IF NOT EXISTS idx_audit_events_ts ON audit_events(ts_unix DESC, id DESC);
		CREATE INDEX IF NOT EXISTS idx_audit_events_actor ON audit_events(actor);
		CREATE INDEX IF NOT EXISTS idx_audit_events_action ON audit_events(action);
	`)
	return err
}

// RecordAudit appends one event. Callers must treat the returned error as
// non-fatal for the request (see server.recordAudit), but should surface it
// on stderr so a broken audit sink is visible to operators.
func (s *Store) RecordAudit(e AuditEvent) error {
	if e.Action == "" {
		return errors.New("audit: action is required")
	}
	ts := e.TSUnix
	if ts <= 0 {
		ts = time.Now().UTC().Unix()
	}
	outcome := e.Outcome
	if outcome == "" {
		outcome = AuditOutcomeOK
	}
	_, err := s.db.Exec(
		`INSERT INTO audit_events
			(ts_unix, actor, actor_id, action, target, outcome, ip, user_agent, detail)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		ts,
		auditTruncate(e.Actor, auditMaxActor),
		auditTruncate(e.ActorID, auditMaxActorID),
		auditTruncate(e.Action, auditMaxAction),
		auditTruncate(e.Target, auditMaxTarget),
		auditTruncate(outcome, auditMaxOutcome),
		auditTruncate(e.IP, auditMaxIP),
		auditTruncate(e.UserAgent, auditMaxUA),
		auditTruncate(e.Detail, auditMaxDetail),
	)
	if err != nil {
		return fmt.Errorf("audit: record event: %w", err)
	}
	return nil
}

// ListAuditEvents returns the newest matching events first.
func (s *Store) ListAuditEvents(f AuditFilter) ([]AuditEvent, error) {
	limit := f.Limit
	if limit <= 0 {
		limit = defaultAuditLimit
	}
	if limit > maxAuditLimit {
		limit = maxAuditLimit
	}
	if f.Offset < 0 {
		f.Offset = 0
	}

	var where []string
	var args []any
	if a := strings.TrimSpace(f.Action); a != "" {
		where = append(where, "action = ?")
		args = append(args, a)
	}
	if a := strings.TrimSpace(f.Actor); a != "" {
		where = append(where, "actor = ?")
		args = append(args, a)
	}
	if o := strings.TrimSpace(f.Outcome); o != "" {
		where = append(where, "outcome = ?")
		args = append(args, o)
	}
	if !f.Since.IsZero() {
		where = append(where, "ts_unix >= ?")
		args = append(args, f.Since.Unix())
	}

	query := `SELECT id, ts_unix, actor, actor_id, action, target, outcome, ip, user_agent, detail
		FROM audit_events`
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	query += " ORDER BY ts_unix DESC, id DESC LIMIT ? OFFSET ?"
	args = append(args, limit, f.Offset)

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("audit: list events: %w", err)
	}
	defer rows.Close()

	events := []AuditEvent{}
	for rows.Next() {
		var e AuditEvent
		if err := rows.Scan(&e.ID, &e.TSUnix, &e.Actor, &e.ActorID, &e.Action,
			&e.Target, &e.Outcome, &e.IP, &e.UserAgent, &e.Detail); err != nil {
			return nil, fmt.Errorf("audit: scan event: %w", err)
		}
		events = append(events, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("audit: iterate events: %w", err)
	}
	return events, nil
}

// PruneAuditEvents deletes events strictly older than before and returns the
// number of removed rows. A retention policy is applied at server start and
// periodically afterwards; the CLI can also invoke it directly.
func (s *Store) PruneAuditEvents(before time.Time) (int64, error) {
	res, err := s.db.Exec(`DELETE FROM audit_events WHERE ts_unix < ?`, before.Unix())
	if err != nil {
		return 0, fmt.Errorf("audit: prune events: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("audit: prune rows affected: %w", err)
	}
	return n, nil
}

// CountAuditEvents is a small helper used by tests and the CLI prune output.
func (s *Store) CountAuditEvents() (int64, error) {
	var n int64
	err := s.db.QueryRow(`SELECT COUNT(*) FROM audit_events`).Scan(&n)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("audit: count events: %w", err)
	}
	return n, nil
}
