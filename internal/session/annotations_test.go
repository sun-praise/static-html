package session

import (
	"errors"
	"testing"
	"time"
)

func mustCreateSession(t *testing.T, store *Store) Session {
	t.Helper()
	s, err := store.Create("/tmp/index.html")
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func sampleAnnotation(kind string) Annotation {
	return Annotation{
		Kind:   kind,
		Quote:  "预览链接只是临时快照",
		Prefix: "反馈集中在两件事上：一是",
		Suffix: "；二是看的人只能被动阅读",
		Tag:    "需求",
	}
}

func TestAddAndListAnnotations(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	s := mustCreateSession(t, store)

	created, err := store.AddAnnotation(s.ID, "alice", sampleAnnotation(AnnotationKindUnderline))
	if err != nil {
		t.Fatal(err)
	}
	if created.ID == "" {
		t.Fatal("expected generated annotation id")
	}
	if created.CreatedAt == "" {
		t.Fatal("expected created timestamp")
	}
	// A note carries the reader's thought alongside the quote.
	created, err = store.AddAnnotation(s.ID, "alice", Annotation{
		Kind:    AnnotationKindNote,
		Quote:   "标注只能截图，成本高",
		Comment: "痛点不是高亮，是想法无处可写",
	})
	if err != nil {
		t.Fatal(err)
	}

	list, err := store.ListAnnotations(s.ID, "alice")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("expected 2 annotations, got %d", len(list))
	}
	if list[0].Quote != "预览链接只是临时快照" {
		t.Fatalf("unexpected first quote: %q", list[0].Quote)
	}
	// Guard against nanoseconds being formatted as seconds (regression).
	ts, err := time.Parse(time.RFC3339, list[0].CreatedAt)
	if err != nil {
		t.Fatalf("CreatedAt not RFC3339: %v", err)
	}
	if ts.Year() < 2026 {
		t.Fatalf("CreatedAt year too small: %s", list[0].CreatedAt)
	}
	if list[1].Comment != "痛点不是高亮，是想法无处可写" {
		t.Fatalf("unexpected comment: %q", list[1].Comment)
	}
}

func TestAnnotationsAreOwnerScoped(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	s := mustCreateSession(t, store)

	if _, err := store.AddAnnotation(s.ID, "alice", sampleAnnotation(AnnotationKindUnderline)); err != nil {
		t.Fatal(err)
	}

	// Bob sees nothing.
	list, err := store.ListAnnotations(s.ID, "bob")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 0 {
		t.Fatalf("expected 0 annotations for bob, got %d", len(list))
	}

	// Bob cannot delete alice's annotation.
	if err := store.DeleteAnnotation(s.ID, "bob", "whatever"); !errors.Is(err, ErrAnnotationNotFound) {
		t.Fatalf("expected ErrAnnotationNotFound, got %v", err)
	}

	// Alice can delete her own.
	aliceList, err := store.ListAnnotations(s.ID, "alice")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteAnnotation(s.ID, "alice", aliceList[0].ID); err != nil {
		t.Fatal(err)
	}
	aliceList, err = store.ListAnnotations(s.ID, "alice")
	if err != nil {
		t.Fatal(err)
	}
	if len(aliceList) != 0 {
		t.Fatalf("expected 0 annotations after delete, got %d", len(aliceList))
	}
}

func TestAnnotationValidation(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	s := mustCreateSession(t, store)

	// Invalid kind.
	if _, err := store.AddAnnotation(s.ID, "", sampleAnnotation("highlight")); err == nil {
		t.Fatal("expected error for invalid kind")
	}

	// Empty quote.
	if _, err := store.AddAnnotation(s.ID, "", Annotation{Kind: AnnotationKindUnderline, Quote: "  "}); err == nil {
		t.Fatal("expected error for empty quote")
	}

	// Note without comment.
	if _, err := store.AddAnnotation(s.ID, "", Annotation{Kind: AnnotationKindNote, Quote: "q"}); err == nil {
		t.Fatal("expected error for note without comment")
	}

	// Overlong quote is clamped, not rejected.
	a := sampleAnnotation(AnnotationKindUnderline)
	a.Quote = makeLongString(maxAnnotationQuote + 100)
	saved, err := store.AddAnnotation(s.ID, "", a)
	if err != nil {
		t.Fatal(err)
	}
	if got := len([]rune(saved.Quote)); got > maxAnnotationQuote {
		t.Fatalf("quote not clamped: %d runes", got)
	}
}

func TestAnnotationsSurviveSoftDelete(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	s := mustCreateSession(t, store)

	created, err := store.AddAnnotation(s.ID, "alice", sampleAnnotation(AnnotationKindUnderline))
	if err != nil {
		t.Fatal(err)
	}

	// Sessions are soft-deleted (deleted_at marker) and their preview page
	// 404s afterwards — annotation listing follows suit with
	// ErrSessionNotFound. The rows themselves survive any hard delete.
	if err := store.SoftDelete(s.ID); err != nil {
		t.Fatal(err)
	}

	if _, err := store.ListAnnotations(s.ID, "alice"); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("expected ErrSessionNotFound after soft delete, got %v", err)
	}
	// The stored rows survive; hard deletion (none today) would cascade.
	var n int
	if err := store.db.QueryRow(
		`SELECT COUNT(*) FROM session_annotations WHERE annotation_id = ?`, created.ID,
	).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("expected annotation row to survive soft delete, got %d", n)
	}
}

func TestAnnotationsCrossVersionVisibility(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	s1 := mustCreateSession(t, store)
	s2 := mustCreateSession(t, store)

	// Both sessions join the same chain (same project + entry file).
	if _, _, err := store.LinkToChain(s1.ID, "proj", "demo.html", ""); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.LinkToChain(s2.ID, "proj", "demo.html", ""); err != nil {
		t.Fatal(err)
	}

	if _, err := store.AddAnnotation(s1.ID, "alice", sampleAnnotation(AnnotationKindUnderline)); err != nil {
		t.Fatal(err)
	}

	// Annotations belong to the chain: v2 sees what was annotated on v1.
	list, err := store.ListAnnotations(s2.ID, "alice")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("expected 1 annotation visible from v2, got %d", len(list))
	}

	// A different chain does not see it.
	s3 := mustCreateSession(t, store)
	if _, _, err := store.LinkToChain(s3.ID, "other", "demo.html", ""); err != nil {
		t.Fatal(err)
	}
	list, err = store.ListAnnotations(s3.ID, "alice")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 0 {
		t.Fatalf("expected 0 annotations on unrelated chain, got %d", len(list))
	}
}

func makeLongString(n int) string {
	b := make([]rune, n)
	for i := range b {
		b[i] = '字'
	}
	return string(b)
}
