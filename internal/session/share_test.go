package session

import (
	"testing"
)

func newShareFixture(t *testing.T) (*Store, User, User, User, Session) {
	t.Helper()
	store, err := NewInMemoryStore()
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	alice, err := store.CreateUser("alice")
	if err != nil {
		t.Fatal(err)
	}
	bob, err := store.CreateUser("bob")
	if err != nil {
		t.Fatal(err)
	}
	carol, err := store.CreateUser("carol")
	if err != nil {
		t.Fatal(err)
	}

	sess, err := store.Create("index.html")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetSessionOwner(sess.ID, alice.ID); err != nil {
		t.Fatal(err)
	}
	return store, alice, bob, carol, sess
}

func TestShareSession_GrantAndQuery(t *testing.T) {
	t.Parallel()
	store, _, bob, carol, sess := newShareFixture(t)

	if err := store.ShareSession(sess.ID, bob.ID); err != nil {
		t.Fatalf("share: %v", err)
	}

	shared, err := store.IsSessionSharedWith(sess.ID, bob.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !shared {
		t.Fatal("bob should be shared after ShareSession")
	}

	notShared, err := store.IsSessionSharedWith(sess.ID, carol.ID)
	if err != nil {
		t.Fatal(err)
	}
	if notShared {
		t.Fatal("carol must not be shared")
	}
}

func TestShareSession_Idempotent(t *testing.T) {
	t.Parallel()
	store, _, bob, _, sess := newShareFixture(t)

	if err := store.ShareSession(sess.ID, bob.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.ShareSession(sess.ID, bob.ID); err != nil {
		t.Fatalf("re-share should be a no-op, got %v", err)
	}

	shares, err := store.ListSessionShares(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(shares) != 1 {
		t.Fatalf("expected 1 share after re-share, got %d", len(shares))
	}
}

func TestShareSession_MissingSession(t *testing.T) {
	t.Parallel()
	store, _, bob, _, _ := newShareFixture(t)

	err := store.ShareSession("nope", bob.ID)
	if err != ErrSessionNotFound {
		t.Fatalf("expected ErrSessionNotFound, got %v", err)
	}
}

func TestShareSessionPage_ScopedGrant(t *testing.T) {
	t.Parallel()
	store, alice, bob, _, sess := newShareFixture(t)

	if err := store.ShareSessionPage(sess.ID, bob.ID, "report.html"); err != nil {
		t.Fatalf("share page: %v", err)
	}

	// The exact page is covered.
	if ok, err := store.HasShareOn(sess.ID, bob.ID, "report.html"); err != nil || !ok {
		t.Fatalf("report.html should be covered (ok=%v err=%v)", ok, err)
	}
	// Sibling pages are not.
	if ok, err := store.HasShareOn(sess.ID, bob.ID, "index.html"); err != nil || ok {
		t.Fatalf("index.html must not be covered (ok=%v err=%v)", ok, err)
	}
	// Whole-session access is not implied.
	if ok, err := store.IsSessionSharedWith(sess.ID, bob.ID); err != nil || ok {
		t.Fatalf("page grant must not imply whole-session (ok=%v err=%v)", ok, err)
	}
	// But it counts as "some share" for assets/drawer.
	if ok, err := store.HasAnyShare(sess.ID, bob.ID); err != nil || !ok {
		t.Fatalf("HasAnyShare should see the page grant (ok=%v err=%v)", ok, err)
	}
	// Alice (the owner) has no grants at all.
	if ok, err := store.HasAnyShare(sess.ID, alice.ID); err != nil || ok {
		t.Fatalf("owner must not need a grant (ok=%v err=%v)", ok, err)
	}

	// Whole-session and page grants coexist as distinct rows.
	if err := store.ShareSession(sess.ID, bob.ID); err != nil {
		t.Fatal(err)
	}
	shares, err := store.ListSessionShares(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(shares) != 2 {
		t.Fatalf("expected whole+page rows, got %d: %+v", len(shares), shares)
	}
	scopes := map[string]bool{}
	for _, s := range shares {
		if s.Username != "bob" {
			t.Fatalf("unexpected share user %+v", s)
		}
		scopes[s.Path] = true
		if s.Path == "" && s.Scope != ShareScopeSession {
			t.Fatalf("empty path should carry scope session, got %q", s.Scope)
		}
		if s.Path == "report.html" && s.Scope != ShareScopePage {
			t.Fatalf("page path should carry scope page, got %q", s.Scope)
		}
	}
	if !scopes[""] || !scopes["report.html"] {
		t.Fatalf("expected both scopes, got %v", scopes)
	}

	// Revoking the page grant keeps the whole-session grant.
	if err := store.UnshareSessionPage(sess.ID, bob.ID, "report.html"); err != nil {
		t.Fatal(err)
	}
	if ok, _ := store.IsSessionSharedWith(sess.ID, bob.ID); !ok {
		t.Fatal("whole-session grant must survive page revoke")
	}
	if err := store.UnshareSessionPage(sess.ID, bob.ID, "report.html"); err != ErrNotShared {
		t.Fatalf("repeat page revoke: expected ErrNotShared, got %v", err)
	}
}

func TestListSharedWithUser_PageScope(t *testing.T) {
	t.Parallel()
	store, _, bob, _, sess := newShareFixture(t)

	if err := store.ShareSessionPage(sess.ID, bob.ID, "report.html"); err != nil {
		t.Fatal(err)
	}

	docs, err := store.ListSharedWithUser(bob.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 1 {
		t.Fatalf("expected 1 shared doc, got %d", len(docs))
	}
	if docs[0].Path != "report.html" {
		t.Fatalf("expected path report.html, got %q", docs[0].Path)
	}
	if docs[0].OwnerUsername != "alice" {
		t.Fatalf("expected owner alice, got %q", docs[0].OwnerUsername)
	}
}

func TestListSessionShares_ResolvesUsername(t *testing.T) {
	t.Parallel()
	store, alice, bob, _, sess := newShareFixture(t)

	if err := store.ShareSession(sess.ID, bob.ID); err != nil {
		t.Fatal(err)
	}

	shares, err := store.ListSessionShares(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(shares) != 1 {
		t.Fatalf("expected 1 share, got %d", len(shares))
	}
	if shares[0].Username != "bob" {
		t.Fatalf("expected username bob, got %q", shares[0].Username)
	}
	if shares[0].UserID != bob.ID {
		t.Fatalf("expected user id %s, got %q", bob.ID, shares[0].UserID)
	}
	if shares[0].CreatedAt == "" {
		t.Fatal("expected createdAt to be set")
	}
	if shares[0].UserID == alice.ID {
		t.Fatal("owner must not appear in the share list")
	}
}

func TestUnshareSession(t *testing.T) {
	t.Parallel()
	store, _, bob, _, sess := newShareFixture(t)

	if err := store.ShareSession(sess.ID, bob.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.UnshareSession(sess.ID, bob.ID); err != nil {
		t.Fatalf("unshare: %v", err)
	}

	shared, err := store.IsSessionSharedWith(sess.ID, bob.ID)
	if err != nil {
		t.Fatal(err)
	}
	if shared {
		t.Fatal("bob should no longer be shared")
	}

	err = store.UnshareSession(sess.ID, bob.ID)
	if err != ErrNotShared {
		t.Fatalf("expected ErrNotShared on repeat unshare, got %v", err)
	}

	err = store.UnshareSession("nope", bob.ID)
	if err != ErrSessionNotFound {
		t.Fatalf("expected ErrSessionNotFound, got %v", err)
	}
}

func TestListSharedWithUser(t *testing.T) {
	t.Parallel()
	store, alice, bob, carol, sess := newShareFixture(t)

	// A second session owned by carol and shared with bob.
	other, err := store.Create("report.html")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetSessionOwner(other.ID, carol.ID); err != nil {
		t.Fatal(err)
	}

	if err := store.ShareSession(sess.ID, bob.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.ShareSession(other.ID, bob.ID); err != nil {
		t.Fatal(err)
	}
	// Carol shared with alice; must not appear for bob.
	third, err := store.Create("notes.html")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetSessionOwner(third.ID, carol.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.ShareSession(third.ID, alice.ID); err != nil {
		t.Fatal(err)
	}

	docs, err := store.ListSharedWithUser(bob.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 2 {
		t.Fatalf("expected 2 shared docs for bob, got %d", len(docs))
	}
	for _, doc := range docs {
		if doc.OwnerUsername == "" {
			t.Fatal("owner username must be resolved")
		}
		if doc.OwnerUsername == bob.Username {
			t.Fatal("own sessions must not appear in the shared list")
		}
	}

	// Soft-deleting a shared session hides it from the list.
	if err := store.SoftDelete(other.ID); err != nil {
		t.Fatal(err)
	}
	docs, err = store.ListSharedWithUser(bob.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 1 {
		t.Fatalf("expected 1 shared doc after soft delete, got %d", len(docs))
	}
	if docs[0].SessionID != sess.ID {
		t.Fatalf("expected the surviving session %s, got %s", sess.ID, docs[0].SessionID)
	}

	// Alice sees exactly her one incoming share.
	aliceDocs, err := store.ListSharedWithUser(alice.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(aliceDocs) != 1 || aliceDocs[0].SessionID != third.ID {
		t.Fatalf("expected alice to see only the third session, got %+v", aliceDocs)
	}
}
