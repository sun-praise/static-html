package session

import (
	"testing"
)

func TestSearchDocumentsWithSpecialChars(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)

	s1, err := store.Create("/tmp/test.html")
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.Create("/tmp/other.html")
	if err != nil {
		t.Fatal(err)
	}

	if err := store.AddTags(s1.ID, "100%"); err != nil {
		t.Fatal(err)
	}

	// Searching for literal "%" should match only the tag "100%", not all docs.
	docs, err := store.SearchDocuments("%", FilterOptions{})
	if err != nil {
		t.Fatal(err)
	}

	if len(docs) != 1 {
		t.Fatalf("expected 1 result for literal '%%' search, got %d", len(docs))
	}
	if docs[0].SessionID != s1.ID {
		t.Fatalf("expected session %q, got %q", s1.ID, docs[0].SessionID)
	}
}

func TestSearchDocumentsWithUnderscore(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)

	s1, err := store.Create("/tmp/a_b.html")
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.Create("/tmp/axb.html")
	if err != nil {
		t.Fatal(err)
	}

	// Searching for literal "a_b" should match only a_b.html, not axb.html.
	docs, err := store.SearchDocuments("a_b", FilterOptions{})
	if err != nil {
		t.Fatal(err)
	}

	if len(docs) != 1 {
		t.Fatalf("expected 1 result for literal 'a_b' search, got %d", len(docs))
	}
	if docs[0].SessionID != s1.ID {
		t.Fatalf("expected session %q (%s), got %q (%s)", s1.ID, "/tmp/a_b.html", docs[0].SessionID, docs[0].Name)
	}
}

func TestSearchDocumentsWithBackslash(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)

	s1, err := store.Create("/tmp/test.html")
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.Create("/tmp/other.html")
	if err != nil {
		t.Fatal(err)
	}

	if err := store.AddTags(s1.ID, `win\path`); err != nil {
		t.Fatal(err)
	}

	// Searching for literal "\" should match only the tag "win\path".
	docs, err := store.SearchDocuments(`\`, FilterOptions{})
	if err != nil {
		t.Fatal(err)
	}

	if len(docs) != 1 {
		t.Fatalf("expected 1 result for literal '\\' search, got %d", len(docs))
	}
	if docs[0].SessionID != s1.ID {
		t.Fatalf("expected session %q, got %q", s1.ID, docs[0].SessionID)
	}
}
