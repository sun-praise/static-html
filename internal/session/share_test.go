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
