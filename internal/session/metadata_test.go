package session

import (
	"path/filepath"
	"testing"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()

	dbPath := filepath.Join(t.TempDir(), "sessions.db")
	store, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("store.Close failed: %v", err)
		}
	})
	return store
}

func TestAddAndGetTags(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	session, err := store.Create("/tmp/index.html")
	if err != nil {
		t.Fatal(err)
	}

	if err := store.AddTags(session.ID, "go", "web"); err != nil {
		t.Fatal(err)
	}

	meta, err := store.GetMetadata(session.ID)
	if err != nil {
		t.Fatal(err)
	}

	if len(meta.Tags) != 2 {
		t.Fatalf("expected 2 tags, got %d", len(meta.Tags))
	}

	tagSet := map[string]bool{}
	for _, tag := range meta.Tags {
		tagSet[tag] = true
	}
	if !tagSet["go"] || !tagSet["web"] {
		t.Fatalf("expected tags {go, web}, got %v", meta.Tags)
	}
}

func TestAddDuplicateTags(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	session, err := store.Create("/tmp/index.html")
	if err != nil {
		t.Fatal(err)
	}

	if err := store.AddTags(session.ID, "go", "go"); err != nil {
		t.Fatal(err)
	}

	meta, err := store.GetMetadata(session.ID)
	if err != nil {
		t.Fatal(err)
	}

	if len(meta.Tags) != 1 {
		t.Fatalf("expected 1 tag after duplicate insert, got %d", len(meta.Tags))
	}
}

func TestRemoveTags(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	session, err := store.Create("/tmp/index.html")
	if err != nil {
		t.Fatal(err)
	}

	if err := store.AddTags(session.ID, "go", "web", "html"); err != nil {
		t.Fatal(err)
	}

	if err := store.RemoveTags(session.ID, "web"); err != nil {
		t.Fatal(err)
	}

	meta, err := store.GetMetadata(session.ID)
	if err != nil {
		t.Fatal(err)
	}

	if len(meta.Tags) != 2 {
		t.Fatalf("expected 2 tags after removal, got %d", len(meta.Tags))
	}

	tagSet := map[string]bool{}
	for _, tag := range meta.Tags {
		tagSet[tag] = true
	}
	if !tagSet["go"] || !tagSet["html"] || tagSet["web"] {
		t.Fatalf("unexpected tags after removal: %v", meta.Tags)
	}
}

func TestAddTagsForNonexistentSession(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	err := store.AddTags("nonexistent", "go")
	if err == nil {
		t.Fatal("expected error for nonexistent session")
	}
}

func TestSetAndGetCategory(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	session, err := store.Create("/tmp/index.html")
	if err != nil {
		t.Fatal(err)
	}

	if err := store.SetCategory(session.ID, "tutorial"); err != nil {
		t.Fatal(err)
	}

	meta, err := store.GetMetadata(session.ID)
	if err != nil {
		t.Fatal(err)
	}

	if meta.Category != "tutorial" {
		t.Fatalf("expected category 'tutorial', got %q", meta.Category)
	}
}

func TestSetCategoryOverwrites(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	session, err := store.Create("/tmp/index.html")
	if err != nil {
		t.Fatal(err)
	}

	if err := store.SetCategory(session.ID, "tutorial"); err != nil {
		t.Fatal(err)
	}
	if err := store.SetCategory(session.ID, "docs"); err != nil {
		t.Fatal(err)
	}

	meta, err := store.GetMetadata(session.ID)
	if err != nil {
		t.Fatal(err)
	}

	if meta.Category != "docs" {
		t.Fatalf("expected category 'docs', got %q", meta.Category)
	}
}

func TestClearCategory(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	session, err := store.Create("/tmp/index.html")
	if err != nil {
		t.Fatal(err)
	}

	if err := store.SetCategory(session.ID, "tutorial"); err != nil {
		t.Fatal(err)
	}
	if err := store.ClearCategory(session.ID); err != nil {
		t.Fatal(err)
	}

	meta, err := store.GetMetadata(session.ID)
	if err != nil {
		t.Fatal(err)
	}

	if meta.Category != "" {
		t.Fatalf("expected empty category, got %q", meta.Category)
	}
}

func TestSetAndGetProject(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	session, err := store.Create("/tmp/index.html")
	if err != nil {
		t.Fatal(err)
	}

	if err := store.SetProject(session.ID, "my-site"); err != nil {
		t.Fatal(err)
	}

	meta, err := store.GetMetadata(session.ID)
	if err != nil {
		t.Fatal(err)
	}

	if meta.Project != "my-site" {
		t.Fatalf("expected project 'my-site', got %q", meta.Project)
	}
}

func TestClearProject(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	session, err := store.Create("/tmp/index.html")
	if err != nil {
		t.Fatal(err)
	}

	if err := store.SetProject(session.ID, "my-site"); err != nil {
		t.Fatal(err)
	}
	if err := store.SetProject(session.ID, ""); err != nil {
		t.Fatal(err)
	}

	meta, err := store.GetMetadata(session.ID)
	if err != nil {
		t.Fatal(err)
	}

	if meta.Project != "" {
		t.Fatalf("expected empty project, got %q", meta.Project)
	}
}

func TestGetMetadataForNonexistentSession(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	meta, err := store.GetMetadata("nonexistent")
	if err != nil {
		t.Fatal(err)
	}

	if len(meta.Tags) != 0 || meta.Category != "" || meta.Project != "" {
		t.Fatalf("expected empty metadata for nonexistent session, got %+v", meta)
	}
}

func TestListDocumentsWithNoFilters(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	_, err := store.Create("/tmp/a.html")
	if err != nil {
		t.Fatal(err)
	}
	s2, err := store.Create("/tmp/b.html")
	if err != nil {
		t.Fatal(err)
	}

	docs, err := store.ListDocuments(FilterOptions{})
	if err != nil {
		t.Fatal(err)
	}

	if len(docs) != 2 {
		t.Fatalf("expected 2 documents, got %d", len(docs))
	}

	// Most recent first
	if docs[0].SessionID != s2.ID {
		t.Fatalf("expected most recent session first, got %q", docs[0].SessionID)
	}
}

func TestListDocumentsFilteredByTag(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	s1, err := store.Create("/tmp/a.html")
	if err != nil {
		t.Fatal(err)
	}
	s2, err := store.Create("/tmp/b.html")
	if err != nil {
		t.Fatal(err)
	}

	if err := store.AddTags(s1.ID, "go"); err != nil {
		t.Fatal(err)
	}
	if err := store.AddTags(s2.ID, "web"); err != nil {
		t.Fatal(err)
	}

	docs, err := store.ListDocuments(FilterOptions{Tag: "go"})
	if err != nil {
		t.Fatal(err)
	}

	if len(docs) != 1 {
		t.Fatalf("expected 1 document filtered by tag, got %d", len(docs))
	}
	if docs[0].SessionID != s1.ID {
		t.Fatalf("expected session %q, got %q", s1.ID, docs[0].SessionID)
	}
}

func TestListDocumentsFilteredByCategory(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	s1, err := store.Create("/tmp/a.html")
	if err != nil {
		t.Fatal(err)
	}
	s2, err := store.Create("/tmp/b.html")
	if err != nil {
		t.Fatal(err)
	}

	if err := store.SetCategory(s1.ID, "tutorial"); err != nil {
		t.Fatal(err)
	}
	if err := store.SetCategory(s2.ID, "docs"); err != nil {
		t.Fatal(err)
	}

	docs, err := store.ListDocuments(FilterOptions{Category: "tutorial"})
	if err != nil {
		t.Fatal(err)
	}

	if len(docs) != 1 {
		t.Fatalf("expected 1 document filtered by category, got %d", len(docs))
	}
	if docs[0].SessionID != s1.ID {
		t.Fatalf("expected session %q, got %q", s1.ID, docs[0].SessionID)
	}
}

func TestListDocumentsFilteredByProject(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	s1, err := store.Create("/tmp/a.html")
	if err != nil {
		t.Fatal(err)
	}
	s2, err := store.Create("/tmp/b.html")
	if err != nil {
		t.Fatal(err)
	}

	if err := store.SetProject(s1.ID, "alpha"); err != nil {
		t.Fatal(err)
	}
	if err := store.SetProject(s2.ID, "beta"); err != nil {
		t.Fatal(err)
	}

	docs, err := store.ListDocuments(FilterOptions{Project: "alpha"})
	if err != nil {
		t.Fatal(err)
	}

	if len(docs) != 1 {
		t.Fatalf("expected 1 document filtered by project, got %d", len(docs))
	}
	if docs[0].SessionID != s1.ID {
		t.Fatalf("expected session %q, got %q", s1.ID, docs[0].SessionID)
	}
}

func TestListDocumentsIncludesMetadata(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	s, err := store.Create("/tmp/my-page.html")
	if err != nil {
		t.Fatal(err)
	}

	if err := store.AddTags(s.ID, "go", "web"); err != nil {
		t.Fatal(err)
	}
	if err := store.SetCategory(s.ID, "tutorial"); err != nil {
		t.Fatal(err)
	}
	if err := store.SetProject(s.ID, "my-site"); err != nil {
		t.Fatal(err)
	}

	docs, err := store.ListDocuments(FilterOptions{})
	if err != nil {
		t.Fatal(err)
	}

	if len(docs) != 1 {
		t.Fatalf("expected 1 document, got %d", len(docs))
	}

	doc := docs[0]
	if len(doc.Tags) != 2 {
		t.Fatalf("expected 2 tags, got %d", len(doc.Tags))
	}
	if doc.Category != "tutorial" {
		t.Fatalf("expected category 'tutorial', got %q", doc.Category)
	}
	if doc.Project != "my-site" {
		t.Fatalf("expected project 'my-site', got %q", doc.Project)
	}
}

func TestSearchDocumentsByFileName(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	s1, err := store.Create("/tmp/my-report.html")
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.Create("/tmp/other.html")
	if err != nil {
		t.Fatal(err)
	}

	docs, err := store.SearchDocuments("report")
	if err != nil {
		t.Fatal(err)
	}

	if len(docs) != 1 {
		t.Fatalf("expected 1 search result, got %d", len(docs))
	}
	if docs[0].SessionID != s1.ID {
		t.Fatalf("expected session %q, got %q", s1.ID, docs[0].SessionID)
	}
}

func TestSearchDocumentsByTag(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	s1, err := store.Create("/tmp/a.html")
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.Create("/tmp/b.html")
	if err != nil {
		t.Fatal(err)
	}

	if err := store.AddTags(s1.ID, "important"); err != nil {
		t.Fatal(err)
	}

	docs, err := store.SearchDocuments("important")
	if err != nil {
		t.Fatal(err)
	}

	if len(docs) != 1 {
		t.Fatalf("expected 1 search result, got %d", len(docs))
	}
	if docs[0].SessionID != s1.ID {
		t.Fatalf("expected session %q, got %q", s1.ID, docs[0].SessionID)
	}
}

func TestSearchDocumentsByCategory(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	s1, err := store.Create("/tmp/a.html")
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.Create("/tmp/b.html")
	if err != nil {
		t.Fatal(err)
	}

	if err := store.SetCategory(s1.ID, "tutorial"); err != nil {
		t.Fatal(err)
	}

	docs, err := store.SearchDocuments("tuto")
	if err != nil {
		t.Fatal(err)
	}

	if len(docs) != 1 {
		t.Fatalf("expected 1 search result, got %d", len(docs))
	}
	if docs[0].SessionID != s1.ID {
		t.Fatalf("expected session %q, got %q", s1.ID, docs[0].SessionID)
	}
}

func TestSearchDocumentsByProject(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	s1, err := store.Create("/tmp/a.html")
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.Create("/tmp/b.html")
	if err != nil {
		t.Fatal(err)
	}

	if err := store.SetProject(s1.ID, "alpha-project"); err != nil {
		t.Fatal(err)
	}

	docs, err := store.SearchDocuments("alpha")
	if err != nil {
		t.Fatal(err)
	}

	if len(docs) != 1 {
		t.Fatalf("expected 1 search result, got %d", len(docs))
	}
	if docs[0].SessionID != s1.ID {
		t.Fatalf("expected session %q, got %q", s1.ID, docs[0].SessionID)
	}
}

func TestSearchDocumentsEmptyQuery(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	_, err := store.SearchDocuments("")
	if err == nil {
		t.Fatal("expected error for empty search query")
	}
}

func TestSearchDocumentsNoResults(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	_, err := store.Create("/tmp/index.html")
	if err != nil {
		t.Fatal(err)
	}

	docs, err := store.SearchDocuments("nonexistent")
	if err != nil {
		t.Fatal(err)
	}

	if len(docs) != 0 {
		t.Fatalf("expected 0 search results, got %d", len(docs))
	}
}

func TestMetadataPersistsAcrossReopen(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "sessions.db")
	store, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}

	s, err := store.Create("/tmp/index.html")
	if err != nil {
		t.Fatal(err)
	}

	if err := store.AddTags(s.ID, "go", "web"); err != nil {
		t.Fatal(err)
	}
	if err := store.SetCategory(s.ID, "tutorial"); err != nil {
		t.Fatal(err)
	}
	if err := store.SetProject(s.ID, "my-site"); err != nil {
		t.Fatal(err)
	}

	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()

	meta, err := reopened.GetMetadata(s.ID)
	if err != nil {
		t.Fatal(err)
	}

	if len(meta.Tags) != 2 {
		t.Fatalf("expected 2 tags after reopen, got %d", len(meta.Tags))
	}
	if meta.Category != "tutorial" {
		t.Fatalf("expected category 'tutorial' after reopen, got %q", meta.Category)
	}
	if meta.Project != "my-site" {
		t.Fatalf("expected project 'my-site' after reopen, got %q", meta.Project)
	}
}

func TestListDocumentsWithMultipleFilters(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	s1, err := store.Create("/tmp/a.html")
	if err != nil {
		t.Fatal(err)
	}
	s2, err := store.Create("/tmp/b.html")
	if err != nil {
		t.Fatal(err)
	}

	if err := store.AddTags(s1.ID, "go"); err != nil {
		t.Fatal(err)
	}
	if err := store.SetCategory(s1.ID, "tutorial"); err != nil {
		t.Fatal(err)
	}
	if err := store.AddTags(s2.ID, "go"); err != nil {
		t.Fatal(err)
	}
	if err := store.SetCategory(s2.ID, "docs"); err != nil {
		t.Fatal(err)
	}

	// Filter by tag=go AND category=tutorial should return only s1
	docs, err := store.ListDocuments(FilterOptions{Tag: "go", Category: "tutorial"})
	if err != nil {
		t.Fatal(err)
	}

	if len(docs) != 1 {
		t.Fatalf("expected 1 document with combined filters, got %d", len(docs))
	}
	if docs[0].SessionID != s1.ID {
		t.Fatalf("expected session %q, got %q", s1.ID, docs[0].SessionID)
	}
}

func TestCascadingDeleteOnSessionRemoval(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	s, err := store.Create("/tmp/index.html")
	if err != nil {
		t.Fatal(err)
	}

	if err := store.AddTags(s.ID, "go"); err != nil {
		t.Fatal(err)
	}
	if err := store.SetCategory(s.ID, "tutorial"); err != nil {
		t.Fatal(err)
	}
	if err := store.SetProject(s.ID, "my-site"); err != nil {
		t.Fatal(err)
	}

	// Manually delete the session to test cascade
	_, err = store.db.Exec(`DELETE FROM sessions WHERE session_id = ?`, s.ID)
	if err != nil {
		t.Fatal(err)
	}

	meta, err := store.GetMetadata(s.ID)
	if err != nil {
		t.Fatal(err)
	}

	if len(meta.Tags) != 0 || meta.Category != "" || meta.Project != "" {
		t.Fatalf("expected metadata to be cascade-deleted, got tags=%v category=%q project=%q", meta.Tags, meta.Category, meta.Project)
	}
}
