package session

import "testing"

func seedFacetDocs(t *testing.T, store *Store) {
	t.Helper()

	seed := []struct {
		path     string
		category string
		tags     []string
	}{
		{"/tmp/report1.html", "报告", []string{"demo", "周报"}},
		{"/tmp/report2.html", "报告", []string{"demo"}},
		{"/tmp/board.html", "数据看板", []string{"dashboard"}},
		{"/tmp/loose.html", "", []string{"demo"}},
		{"/tmp/empty.html", "", nil},
	}
	for _, s := range seed {
		sess, err := store.Create(s.path)
		if err != nil {
			t.Fatal(err)
		}
		if s.category != "" {
			if err := store.SetCategory(sess.ID, s.category); err != nil {
				t.Fatal(err)
			}
		}
		if len(s.tags) > 0 {
			if err := store.AddTags(sess.ID, s.tags...); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func facetValues(cats []FacetCount) map[string]int {
	m := make(map[string]int, len(cats))
	for _, fc := range cats {
		m[fc.Value] = fc.Count
	}
	return m
}

func TestCategoryFacetsCountsAllDimensions(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	seedFacetDocs(t, store)

	cats, uncat, err := store.CategoryFacets("", FilterOptions{})
	if err != nil {
		t.Fatal(err)
	}

	got := facetValues(cats)
	if got["报告"] != 2 || got["数据看板"] != 1 {
		t.Fatalf("expected 报告=2 数据看板=1, got %v", got)
	}
	if uncat != 2 {
		t.Fatalf("expected 2 uncategorized, got %d", uncat)
	}
	if len(cats) != 2 {
		t.Fatalf("expected 2 categories, got %d: %v", len(cats), cats)
	}
	// Ordered by count desc, then name asc.
	if cats[0].Value != "报告" || cats[1].Value != "数据看板" {
		t.Fatalf("unexpected facet order: %v", cats)
	}
}

func TestCategoryFacetsLiftCategoryFilter(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	seedFacetDocs(t, store)

	// The active category must not collapse the category facet list.
	cats, uncat, err := store.CategoryFacets("", FilterOptions{Category: "报告"})
	if err != nil {
		t.Fatal(err)
	}

	got := facetValues(cats)
	if got["报告"] != 2 || got["数据看板"] != 1 || uncat != 2 {
		t.Fatalf("expected full facet list under category filter, got %v uncat=%d", got, uncat)
	}
}

func TestCategoryFacetsNarrowByTag(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	seedFacetDocs(t, store)

	cats, uncat, err := store.CategoryFacets("", FilterOptions{Tag: "demo"})
	if err != nil {
		t.Fatal(err)
	}

	got := facetValues(cats)
	if got["报告"] != 2 || len(cats) != 1 {
		t.Fatalf("expected only 报告=2 under tag=demo, got %v", got)
	}
	if uncat != 1 {
		t.Fatalf("expected 1 uncategorized under tag=demo, got %d", uncat)
	}
}

func TestCategoryFacetsExcludesDeleted(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	seedFacetDocs(t, store)

	docs, err := store.ListDocuments(FilterOptions{Category: "报告"})
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) == 0 {
		t.Fatal("expected seeded documents")
	}
	if err := store.SoftDelete(docs[0].SessionID); err != nil {
		t.Fatal(err)
	}

	cats, _, err := store.CategoryFacets("", FilterOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got := facetValues(cats)["报告"]; got != 1 {
		t.Fatalf("expected 报告=1 after soft delete, got %d", got)
	}
}

func TestTagFacetsCountsAndLiftsTagFilter(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	seedFacetDocs(t, store)

	tags, err := store.TagFacets("", FilterOptions{})
	if err != nil {
		t.Fatal(err)
	}

	got := facetValues(tags)
	if got["demo"] != 3 || got["周报"] != 1 || got["dashboard"] != 1 {
		t.Fatalf("expected demo=3 周报=1 dashboard=1, got %v", got)
	}
	// demo has the highest count and must come first.
	if tags[0].Value != "demo" {
		t.Fatalf("expected demo first, got %v", tags)
	}

	// The active tag must not collapse the tag facet list, while the
	// category filter still narrows it.
	tags, err = store.TagFacets("", FilterOptions{Tag: "demo", Category: "报告"})
	if err != nil {
		t.Fatal(err)
	}
	got = facetValues(tags)
	if got["demo"] != 2 || got["周报"] != 1 || len(tags) != 2 {
		t.Fatalf("expected demo=2 周报=1 under category=报告, got %v", got)
	}
}

func TestListDocumentsFilteredByUncategorized(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	seedFacetDocs(t, store)

	docs, err := store.ListDocuments(FilterOptions{Category: UncategorizedFilterValue})
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 2 {
		t.Fatalf("expected 2 uncategorized documents, got %d", len(docs))
	}
	for _, d := range docs {
		if d.Category != "" {
			t.Fatalf("expected uncategorized document, got category %q", d.Category)
		}
	}

	count, err := store.CountDocuments(FilterOptions{Category: UncategorizedFilterValue})
	if err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("expected count 2, got %d", count)
	}
}
