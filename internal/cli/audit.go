package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/sun-praise/static-html/internal/session"
)

// defaultAuditRetentionDays bounds the growth of audit_events on a
// long-running instance. A value of 0 disables automatic pruning.
const defaultAuditRetentionDays = 90

// resolveAuditRetentionDays reads --audit-retention-days, falling back to
// STH_AUDIT_RETENTION_DAYS and then the default. Negative values are rejected
// rather than silently interpreted as "keep forever".
func resolveAuditRetentionDays(flags map[string]string) (int, error) {
	raw, ok := flags["audit-retention-days"]
	if !ok {
		raw = strings.TrimSpace(os.Getenv("STH_AUDIT_RETENTION_DAYS"))
		if raw == "" {
			return defaultAuditRetentionDays, nil
		}
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 {
		return 0, errors.New("audit-retention-days must be a non-negative integer")
	}
	return n, nil
}

// runAudit is a local, read-only query over the audit table. There is
// deliberately no HTTP endpoint for it: the server has no admin role, and
// exposing the log to any authenticated user would leak everyone's activity.
// Operators read it on the host (or via `docker exec ... html-server audit
// --db /data/sessions.db`).
func runAudit(args []string, stdout io.Writer) error {
	flags, _, err := parseArgs(args)
	if err != nil {
		return err
	}

	store, err := openStore(flags)
	if err != nil {
		return err
	}
	defer store.Close()

	if raw, ok := flags["prune-days"]; ok {
		days, err := strconv.Atoi(raw)
		if err != nil || days < 1 {
			return errors.New("prune-days must be a positive integer")
		}
		cutoff := time.Now().UTC().Add(-time.Duration(days) * 24 * time.Hour)
		removed, err := store.PruneAuditEvents(cutoff)
		if err != nil {
			return err
		}
		fmt.Fprintf(stdout, "pruned %d audit event(s) older than %s\n",
			removed, cutoff.Format("2006-01-02"))
		return nil
	}

	filter := session.AuditFilter{
		Action:  flags["action"],
		Actor:   flags["actor"],
		Outcome: flags["outcome"],
	}
	if raw, ok := flags["limit"]; ok {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 {
			return errors.New("limit must be a positive integer")
		}
		filter.Limit = n
	}
	if raw, ok := flags["offset"]; ok {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 0 {
			return errors.New("offset must be a non-negative integer")
		}
		filter.Offset = n
	}
	if raw, ok := flags["since"]; ok {
		t, err := time.Parse("2006-01-02", raw)
		if err != nil {
			return errors.New("since must be YYYY-MM-DD")
		}
		filter.Since = t.UTC()
	}

	events, err := store.ListAuditEvents(filter)
	if err != nil {
		return err
	}
	if len(events) == 0 {
		fmt.Fprintln(stdout, "No audit events.")
		return nil
	}
	for _, e := range events {
		ts := time.Unix(e.TSUnix, 0).Local().Format("2006-01-02 15:04:05")
		fmt.Fprintf(stdout, "%s  %-18s %-7s actor=%s target=%s ip=%s detail=%s ua=%s\n",
			ts, e.Action, e.Outcome, e.Actor, e.Target, e.IP, e.Detail, e.UserAgent)
	}
	return nil
}
