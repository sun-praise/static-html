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

	// Sessions are soft-deleted (deleted_at marker); the row stays so its
	// annotations stay too. Hard deletes (none today) would cascade.
	if err := store.SoftDelete(s.ID); err != nil {
		t.Fatal(err)
	}

	list, err := store.ListAnnotations(s.ID, "alice")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].ID != created.ID {
		t.Fatalf("expected annotations to survive soft delete, got %+v", list)
	}
}

func makeLongString(n int) string {
	b := make([]rune, n)
	for i := range b {
		b[i] = '字'
	}
	return string(b)
}
