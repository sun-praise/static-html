package cli

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sun-praise/static-html/internal/session"
)

// newShareCLIFixture creates a temp database with alice/bob users and one
// alice-owned session, returning the DB path and session ID.
func newShareCLIFixture(t *testing.T) (dbPath, sessionID string) {
	t.Helper()

	dbPath = filepath.Join(t.TempDir(), "test.db")
	store, err := session.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	alice, err := store.CreateUser("alice")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateUser("bob"); err != nil {
		t.Fatal(err)
	}

	sess, err := store.Create("index.html")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetSessionOwner(sess.ID, alice.ID); err != nil {
		t.Fatal(err)
	}
	return dbPath, sess.ID
}

func TestRunShare_LocalDB(t *testing.T) {
	t.Parallel()
	dbPath, sid := newShareCLIFixture(t)

	var out bytes.Buffer
	if err := runShare([]string{sid, "bob", "--db", dbPath}, &out); err != nil {
		t.Fatalf("runShare: %v", err)
	}
	if !strings.Contains(out.String(), "bob") {
		t.Fatalf("expected bob in output, got %q", out.String())
	}

	store, err := session.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	bob, err := store.FindUserByUsername("bob")
	if err != nil {
		t.Fatal(err)
	}
	if shared, err := store.IsSessionSharedWith(sid, bob.ID); err != nil || !shared {
		t.Fatalf("expected share to persist (shared=%v err=%v)", shared, err)
	}
}

func TestRunShare_OwnerSelfShareRejected(t *testing.T) {
	t.Parallel()
	dbPath, sid := newShareCLIFixture(t)

	var out bytes.Buffer
	err := runShare([]string{sid, "alice", "--db", dbPath}, &out)
	if err == nil {
		t.Fatal("sharing with the owner should fail")
	}
	if !strings.Contains(err.Error(), "owner") {
		t.Fatalf("error should mention the owner, got %q", err.Error())
	}
}

func TestRunUnshare_LocalDB(t *testing.T) {
	t.Parallel()
	dbPath, sid := newShareCLIFixture(t)

	var out bytes.Buffer
	if err := runShare([]string{sid, "bob", "--db", dbPath}, &out); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := runUnshare([]string{sid, "bob", "--db", dbPath}, &out); err != nil {
		t.Fatalf("runUnshare: %v", err)
	}

	err := runUnshare([]string{sid, "bob", "--db", dbPath}, &out)
	if err == nil || !strings.Contains(err.Error(), "not shared") {
		t.Fatalf("repeat unshare should fail with not-shared, got %v", err)
	}
}

func TestRunShare_PageScope(t *testing.T) {
	t.Parallel()
	dbPath, sid := newShareCLIFixture(t)

	var out bytes.Buffer
	if err := runShare([]string{sid, "bob", "--page", "report.html", "--db", dbPath}, &out); err != nil {
		t.Fatalf("runShare --page: %v", err)
	}
	if !strings.Contains(out.String(), "page: report.html") {
		t.Fatalf("expected page scope in output, got %q", out.String())
	}

	store, err := session.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	bob, err := store.FindUserByUsername("bob")
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := store.HasShareOn(sid, bob.ID, "report.html"); err != nil || !ok {
		t.Fatalf("page grant missing (ok=%v err=%v)", ok, err)
	}

	out.Reset()
	if err := runUnshare([]string{sid, "bob", "--page", "report.html", "--db", dbPath}, &out); err != nil {
		t.Fatalf("runUnshare --page: %v", err)
	}
	if ok, _ := store.HasShareOn(sid, bob.ID, "report.html"); ok {
		t.Fatal("page grant should be revoked")
	}
}

func TestRunShares_LocalDB(t *testing.T) {
	t.Parallel()
	dbPath, sid := newShareCLIFixture(t)

	var out bytes.Buffer
	if err := runShare([]string{sid, "bob", "--db", dbPath}, &out); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := runShares([]string{sid, "--db", dbPath}, &out); err != nil {
		t.Fatalf("runShares: %v", err)
	}
	if !strings.Contains(out.String(), "bob") {
		t.Fatalf("expected bob in listing, got %q", out.String())
	}
}

func TestRunShare_UsageErrors(t *testing.T) {
	t.Parallel()
	dbPath, _ := newShareCLIFixture(t)

	if err := runShare([]string{"--db", dbPath}, &bytes.Buffer{}); err == nil {
		t.Fatal("missing args should error")
	}
	if err := runUnshare([]string{"only-one", "--db", dbPath}, &bytes.Buffer{}); err == nil {
		t.Fatal("missing username should error")
	}
	if err := runShares([]string{"--db", dbPath}, &bytes.Buffer{}); err == nil {
		t.Fatal("missing session should error")
	}
}

// TestRunShare_RemoteServer: the remote path sends the Bearer key and prints
// the returned share list.
func TestRunShare_RemoteServer(t *testing.T) {
	t.Parallel()
	_, sid := newShareCLIFixture(t)

	var gotAuth, gotBody, gotMethod string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotMethod = r.Method
		buf := new(bytes.Buffer)
		_, _ = buf.ReadFrom(r.Body)
		gotBody = buf.String()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"sessionId":"` + sid + `","shares":[{"userId":"u1","username":"bob","createdAt":"2026-01-01T00:00:00Z"}]}`))
	}))
	defer server.Close()

	var out bytes.Buffer
	if err := runShare([]string{sid, "bob", "--server", server.URL, "--api-key", "sth_test"}, &out); err != nil {
		t.Fatalf("runShare remote: %v", err)
	}

	if gotAuth != "Bearer sth_test" {
		t.Fatalf("expected Bearer key on request, got %q", gotAuth)
	}
	if gotMethod != http.MethodPut {
		t.Fatalf("expected PUT, got %s", gotMethod)
	}
	if !strings.Contains(gotBody, `"user":"bob"`) {
		t.Fatalf("expected user in body, got %q", gotBody)
	}
	if !strings.Contains(out.String(), "bob") {
		t.Fatalf("expected share list in output, got %q", out.String())
	}
}

// TestRunShare_PagePathNormalizedLocalDB: the local (--db) path normalizes
// --page the same way the server does, so a grant written by the CLI matches
// the path the preview comparer uses instead of storing a row that can never
// match. Malformed paths are rejected instead of silently stored.
func TestRunShare_PagePathNormalizedLocalDB(t *testing.T) {
	t.Parallel()
	dbPath, sid := newShareCLIFixture(t)

	var out bytes.Buffer
	if err := runShare([]string{sid, "bob", "--page", "./report.html", "--db", dbPath}, &out); err != nil {
		t.Fatalf("runShare --page ./report.html: %v", err)
	}
	if !strings.Contains(out.String(), "page: report.html") {
		t.Fatalf("output should show the normalized path, got %q", out.String())
	}

	store, err := session.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	bob, err := store.FindUserByUsername("bob")
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := store.HasShareOn(sid, bob.ID, "report.html"); err != nil || !ok {
		t.Fatalf("normalized page grant missing (ok=%v err=%v)", ok, err)
	}

	for _, bad := range []string{"../escape.html", "/abs/report.html", "style.css"} {
		if err := runShare([]string{sid, "bob", "--page", bad, "--db", dbPath}, &out); err == nil {
			t.Fatalf("expected --page %q to be rejected", bad)
		}
	}
}
