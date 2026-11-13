package cli

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sun-praise/static-html/internal/session"
)

func newAuditCLIStore(t *testing.T) (string, *session.Store) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "sessions.db")
	store, err := session.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return dbPath, store
}

func TestRunAuditListAndFilters(t *testing.T) {
	t.Parallel()
	dbPath, store := newAuditCLIStore(t)

	now := time.Now().UTC()
	for _, e := range []session.AuditEvent{
		{TSUnix: now.Add(-2 * time.Hour).Unix(), Actor: "alice", Action: session.AuditLogin, Outcome: session.AuditOutcomeOK, IP: "10.0.0.1"},
		{TSUnix: now.Add(-time.Hour).Unix(), Actor: "bob", Action: session.AuditLoginFailed, Outcome: session.AuditOutcomeDenied, IP: "10.0.0.2"},
	} {
		if err := store.RecordAudit(e); err != nil {
			t.Fatal(err)
		}
	}

	var out bytes.Buffer
	if err := runAudit([]string{"--db", dbPath}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), session.AuditLoginFailed) ||
		!strings.Contains(out.String(), "actor=bob") ||
		!strings.Contains(out.String(), "ip=10.0.0.2") {
		t.Fatalf("list output missing fields: %q", out.String())
	}
	// Newest first: bob's event is newer than alice's.
	if strings.Index(out.String(), "actor=bob") > strings.Index(out.String(), "actor=alice") {
		t.Fatalf("events are not newest-first: %q", out.String())
	}

	for _, tc := range []struct {
		name string
		args []string
		want string
		bad  string
	}{
		{"action", []string{"--action", session.AuditLoginFailed}, session.AuditLoginFailed, "actor=alice"},
		{"actor", []string{"--actor", "alice"}, "actor=alice", "actor=bob"},
		{"outcome", []string{"--outcome", "denied"}, session.AuditLoginFailed, "actor=alice"},
		{"limit", []string{"--limit", "1"}, session.AuditLoginFailed, "actor=alice"},
		{"since", []string{"--since", now.Add(-90 * time.Minute).Format("2006-01-02")}, "", ""},
	} {
		out.Reset()
		args := append([]string{"--db", dbPath}, tc.args...)
		if err := runAudit(args, &out); err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if tc.want != "" && !strings.Contains(out.String(), tc.want) {
			t.Errorf("%s: output %q missing %q", tc.name, out.String(), tc.want)
		}
		if tc.bad != "" && strings.Contains(out.String(), tc.bad) {
			t.Errorf("%s: output %q unexpectedly contains %q", tc.name, out.String(), tc.bad)
		}
	}
}

func TestRunAuditPruneAndValidation(t *testing.T) {
	t.Parallel()
	dbPath, store := newAuditCLIStore(t)

	now := time.Now().UTC()
	_ = store.RecordAudit(session.AuditEvent{Action: session.AuditLogin, TSUnix: now.Add(-200 * 24 * time.Hour).Unix()})
	_ = store.RecordAudit(session.AuditEvent{Action: session.AuditLogin, TSUnix: now.Unix()})

	var out bytes.Buffer
	if err := runAudit([]string{"--db", dbPath, "--prune-days", "90"}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "pruned 1 audit event") {
		t.Fatalf("prune output: %q", out.String())
	}
	n, err := store.CountAuditEvents()
	if err != nil || n != 1 {
		t.Fatalf("remaining = %d, err = %v", n, err)
	}

	for _, args := range [][]string{
		{"--db", dbPath, "--prune-days", "0"},
		{"--db", dbPath, "--limit", "0"},
		{"--db", dbPath, "--offset", "-1"},
		{"--db", dbPath, "--since", "not-a-date"},
	} {
		out.Reset()
		if err := runAudit(args, &out); err == nil {
			t.Errorf("expected an error for %v", args)
		}
	}
}

func TestResolveAuditRetentionDays(t *testing.T) {
	// Not parallel: t.Setenv mutates process-wide state.
	t.Setenv("STH_AUDIT_RETENTION_DAYS", "")

	days, err := resolveAuditRetentionDays(map[string]string{})
	if err != nil || days != defaultAuditRetentionDays {
		t.Fatalf("default = %d, err = %v; want %d", days, err, defaultAuditRetentionDays)
	}

	days, err = resolveAuditRetentionDays(map[string]string{"audit-retention-days": "0"})
	if err != nil || days != 0 {
		t.Fatalf("explicit zero = %d, err = %v", days, err)
	}

	if _, err := resolveAuditRetentionDays(map[string]string{"audit-retention-days": "-1"}); err == nil {
		t.Fatal("negative retention should be rejected")
	}
	if _, err := resolveAuditRetentionDays(map[string]string{"audit-retention-days": "abc"}); err == nil {
		t.Fatal("non-numeric retention should be rejected")
	}

	t.Setenv("STH_AUDIT_RETENTION_DAYS", "30")
	days, err = resolveAuditRetentionDays(map[string]string{})
	if err != nil || days != 30 {
		t.Fatalf("env = %d, err = %v; want 30", days, err)
	}
	// Explicit flag wins over env.
	days, err = resolveAuditRetentionDays(map[string]string{"audit-retention-days": "7"})
	if err != nil || days != 7 {
		t.Fatalf("flag override = %d, err = %v; want 7", days, err)
	}
}
