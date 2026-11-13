package session

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func newAuditStore(t *testing.T) *Store {
	t.Helper()
	store, err := NewInMemoryStore()
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

// TestAuditRecordListFilter pins the query contract the CLI relies on:
// newest first, exact-match filters, limit/offset and since.
func TestAuditRecordListFilter(t *testing.T) {
	t.Parallel()
	store := newAuditStore(t)

	now := time.Now().UTC()
	for _, e := range []AuditEvent{
		{TSUnix: now.Add(-30 * time.Second).Unix(), Actor: "alice", Action: AuditLogin, Outcome: AuditOutcomeOK, IP: "10.0.0.1", Target: "alice"},
		{TSUnix: now.Add(-20 * time.Second).Unix(), Actor: "bob", Action: AuditLoginFailed, Outcome: AuditOutcomeDenied, IP: "10.0.0.2"},
		{TSUnix: now.Add(-10 * time.Second).Unix(), Actor: "alice", Action: AuditShareGrant, Outcome: AuditOutcomeOK, Target: "sess-1"},
	} {
		if err := store.RecordAudit(e); err != nil {
			t.Fatal(err)
		}
	}

	all, err := store.ListAuditEvents(AuditFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 {
		t.Fatalf("expected 3 events, got %d", len(all))
	}
	if all[0].Action != AuditShareGrant || all[2].Action != AuditLogin {
		t.Fatalf("events are not newest-first: %+v", all)
	}
	if all[0].ID <= all[1].ID || all[1].ID <= all[2].ID {
		t.Fatalf("newest-first order should also descend by id: %+v", all)
	}

	cases := []struct {
		name   string
		filter AuditFilter
		want   int
	}{
		{"action", AuditFilter{Action: AuditLogin}, 1},
		{"actor", AuditFilter{Actor: "alice"}, 2},
		{"outcome", AuditFilter{Outcome: AuditOutcomeDenied}, 1},
		{"actor+action", AuditFilter{Actor: "alice", Action: AuditShareGrant}, 1},
		{"since", AuditFilter{Since: now.Add(-15 * time.Second)}, 1},
		{"limit", AuditFilter{Limit: 2}, 2},
		{"offset", AuditFilter{Offset: 2}, 1},
		{"unknown action", AuditFilter{Action: "nope"}, 0},
	}
	for _, tc := range cases {
		got, err := store.ListAuditEvents(tc.filter)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if len(got) != tc.want {
			t.Errorf("%s: expected %d events, got %d", tc.name, tc.want, len(got))
		}
	}

	n, err := store.CountAuditEvents()
	if err != nil || n != 3 {
		t.Fatalf("count = %d, err = %v", n, err)
	}
}

// TestAuditTruncatesFields: a hostile client must not be able to bloat the
// audit table with a giant User-Agent or username, and multibyte values must
// stay valid UTF-8 after truncation.
func TestAuditTruncatesFields(t *testing.T) {
	t.Parallel()
	store := newAuditStore(t)

	err := store.RecordAudit(AuditEvent{
		Action:    AuditAuthRejected,
		Actor:     strings.Repeat("甲", 300),
		ActorID:   strings.Repeat("i", 200),
		Target:    strings.Repeat("t", 900),
		IP:        strings.Repeat("1", 200),
		UserAgent: strings.Repeat("U", 900),
		Detail:    strings.Repeat("d", 900),
	})
	if err != nil {
		t.Fatal(err)
	}
	events, err := store.ListAuditEvents(AuditFilter{})
	if err != nil {
		t.Fatal(err)
	}
	e := events[0]
	checks := []struct {
		name string
		got  string
		max  int
	}{
		{"actor", e.Actor, auditMaxActor},
		{"actor_id", e.ActorID, auditMaxActorID},
		{"target", e.Target, auditMaxTarget},
		{"ip", e.IP, auditMaxIP},
		{"ua", e.UserAgent, auditMaxUA},
		{"detail", e.Detail, auditMaxDetail},
	}
	for _, c := range checks {
		if utf8.RuneCountInString(c.got) != c.max {
			t.Errorf("%s rune length = %d, want %d", c.name, utf8.RuneCountInString(c.got), c.max)
		}
		if !utf8.ValidString(c.got) {
			t.Errorf("%s is not valid UTF-8 after truncation", c.name)
		}
	}
}

func TestAuditPruneAndValidation(t *testing.T) {
	t.Parallel()
	store := newAuditStore(t)

	if err := store.RecordAudit(AuditEvent{}); err == nil {
		t.Fatal("expected an error when action is empty")
	}

	now := time.Now().UTC()
	_ = store.RecordAudit(AuditEvent{Action: AuditLogin, TSUnix: now.Add(-200 * 24 * time.Hour).Unix()})
	_ = store.RecordAudit(AuditEvent{Action: AuditLogin, TSUnix: now.Add(-100 * 24 * time.Hour).Unix()})
	_ = store.RecordAudit(AuditEvent{Action: AuditLogin, TSUnix: now.Unix()})

	removed, err := store.PruneAuditEvents(now.Add(-90 * 24 * time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if removed != 2 {
		t.Fatalf("expected 2 pruned rows, got %d", removed)
	}
	n, err := store.CountAuditEvents()
	if err != nil || n != 1 {
		t.Fatalf("remaining = %d, err = %v", n, err)
	}
}

// TestAuditSurvivesSubjectDeletion: audit rows are snapshots with no foreign
// keys, so removing a user must not erase its history.
func TestAuditSurvivesSubjectDeletion(t *testing.T) {
	t.Parallel()
	store := newAuditStore(t)

	user, err := store.CreateUser("ephemeral")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.RecordAudit(AuditEvent{Action: AuditLogin, Actor: user.Username, ActorID: user.ID}); err != nil {
		t.Fatal(err)
	}
	// The store does not expose DeleteUser; exercise the cascade directly.
	if _, err := store.db.Exec(`DELETE FROM users WHERE id = ?`, user.ID); err != nil {
		t.Fatal(err)
	}
	events, err := store.ListAuditEvents(AuditFilter{Action: AuditLogin})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Actor != "ephemeral" {
		t.Fatalf("audit history was lost: %+v", events)
	}
}
