package server

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sun-praise/static-html/internal/session"
)

func startTestServer(t *testing.T, store *session.Store) *Server {
	t.Helper()

	srv, err := New("127.0.0.1", 0, store, "", 0, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = srv.Stop(ctx)
	})
	return srv
}

func mustFixturePath(t *testing.T) string {
	t.Helper()

	p, err := filepath.Abs(filepath.Join("..", "..", "fixtures", "basic", "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// createFacetSession seeds the store directly: the upload API requires both
// tags and category on create, but the home page must also cope with sessions
// missing either (legacy rows or cleared metadata).
func createFacetSession(t *testing.T, store *session.Store, category string, tags []string) {
	t.Helper()

	sess, err := store.Create(mustFixturePath(t))
	if err != nil {
		t.Fatal(err)
	}
	if category != "" {
		if err := store.SetCategory(sess.ID, category); err != nil {
			t.Fatal(err)
		}
	}
	if len(tags) > 0 {
		if err := store.AddTags(sess.ID, tags...); err != nil {
			t.Fatal(err)
		}
	}
}

func getHome(t *testing.T, srvURL, rawQuery string) (string, *http.Response) {
	t.Helper()

	req, err := http.NewRequest(http.MethodGet, srvURL+"/?"+rawQuery, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200, got %d body=%s", resp.StatusCode, body)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(body), resp
}

func TestHomeLayoutBlocksFacets(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	srv := startTestServer(t, store)

	createFacetSession(t, store, "报告", []string{"demo"})
	createFacetSession(t, store, "报告", []string{"demo"})
	createFacetSession(t, store, "数据看板", []string{"dashboard"})
	createFacetSession(t, store, "", []string{"demo"})

	body, _ := getHome(t, srv.Origin(), "")

	// Default layout: blocks with category and tag facet chip rows.
	if !strings.Contains(body, `class="cat-chip`) {
		t.Fatal("expected category facet chips on default layout")
	}
	if !strings.Contains(body, `>全部 <b>4</b>`) {
		t.Fatalf("expected 全部 chip counting 4 documents, body=%s", body)
	}
	if !strings.Contains(body, `>报告 <b>2</b>`) || !strings.Contains(body, `>数据看板 <b>1</b>`) {
		t.Fatal("expected category chips with counts")
	}
	if !strings.Contains(body, "未分类 <b>1</b>") {
		t.Fatal("expected uncategorized bucket chip")
	}
	if !strings.Contains(body, `class="tag-chip`) || !strings.Contains(body, `>demo <b>3</b>`) {
		t.Fatal("expected tag facet chips with counts")
	}
	if !strings.Contains(body, `id="layout-select"`) {
		t.Fatal("expected layout dropdown")
	}
}

func TestHomeFacetCategoryCountsLiftOwnFilter(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	srv := startTestServer(t, store)

	createFacetSession(t, store, "报告", []string{"demo"})
	createFacetSession(t, store, "数据看板", nil)

	// With category=报告 active, the category chips must still show every
	// category while the tag counts collapse to the filtered set.
	body, _ := getHome(t, srv.Origin(), "category=%E6%8A%A5%E5%91%8A")
	if !strings.Contains(body, `>数据看板 <b>1</b>`) {
		t.Fatal("expected lifted category facet counts under category filter")
	}
	if !strings.Contains(body, `>demo <b>1</b>`) {
		t.Fatal("expected tag counts narrowed by category filter")
	}
	if !strings.Contains(body, `Category: 报告`) {
		t.Fatal("expected active category filter chip")
	}
}

func TestHomeLayoutSwitchAndCookie(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	srv := startTestServer(t, store)

	createFacetSession(t, store, "报告", []string{"demo"})

	body, resp := getHome(t, srv.Origin(), "layout=sidebar")
	if !strings.Contains(body, `class="two-col"`) || !strings.Contains(body, `class="cat-side"`) {
		t.Fatal("expected sidebar layout markup for layout=sidebar")
	}
	var cookie *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == layoutCookieName {
			cookie = c
		}
	}
	if cookie == nil || cookie.Value != layoutSidebar {
		t.Fatalf("expected %s=sidebar cookie, got %v", layoutCookieName, cookie)
	}

	// Cookie-less requests keep the default layout.
	body, _ = getHome(t, srv.Origin(), "")
	if !strings.Contains(body, `class="cat-chip`) || strings.Contains(body, `class="cat-side"`) {
		t.Fatal("expected default blocks layout without cookie")
	}

	// The dropdown options carry the switching URLs and preserve filters.
	if !strings.Contains(body, `data-url="/?layout=sidebar"`) {
		t.Fatal("expected layout switch URL in dropdown option")
	}
	body, _ = getHome(t, srv.Origin(), "tag=demo&layout=overview")
	if !strings.Contains(body, `data-url="/?layout=blocks&amp;tag=demo"`) {
		t.Fatalf("expected layout switch URL preserving tag filter, body=%s", body)
	}
}

func TestHomeLayoutInvalidFallsBack(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	srv := startTestServer(t, store)

	body, _ := getHome(t, srv.Origin(), "layout=bogus")
	if !strings.Contains(body, `class="cat-chip`) {
		t.Fatal("expected default layout for invalid layout param")
	}
}

func TestHomeOverviewCategoriesView(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	srv := startTestServer(t, store)

	createFacetSession(t, store, "报告", []string{"demo"})
	createFacetSession(t, store, "报告", []string{"周报"})
	createFacetSession(t, store, "数据看板", []string{"dashboard"})

	body, _ := getHome(t, srv.Origin(), "layout=overview&view=categories")
	if !strings.Contains(body, `class="cat-grid"`) || !strings.Contains(body, `class="cat-card`) {
		t.Fatal("expected overview category cards")
	}
	if !strings.Contains(body, `>报告 <b>2</b>`) {
		t.Fatal("expected category card with count")
	}
	if strings.Count(body, `class="cat-card`) != 2 {
		t.Fatalf("expected 2 category cards, body=%s", body)
	}
	// Each card previews recent documents of its category.
	if !strings.Contains(body, `class="view-all"`) {
		t.Fatal("expected view-all link on cards")
	}

	// Sessions view keeps the document list.
	body, _ = getHome(t, srv.Origin(), "layout=overview")
	if strings.Contains(body, `class="cat-grid"`) {
		t.Fatal("expected no card grid on sessions view")
	}
	if !strings.Contains(body, `class="view-tab`) || !strings.Contains(body, `Sessions (3)`) {
		t.Fatal("expected overview view tabs with session count")
	}

	// Facet narrowing applies to the card grid too.
	body, _ = getHome(t, srv.Origin(), "layout=overview&view=categories&tag=demo")
	if strings.Count(body, `class="cat-card`) != 1 {
		t.Fatalf("expected single category card under tag filter, body=%s", body)
	}
}

func TestHomeFacetURLsResetPage(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	srv := startTestServer(t, store)

	createFacetSession(t, store, "报告", []string{"demo"})

	body, _ := getHome(t, srv.Origin(), "page=2")
	if !strings.Contains(body, `category=%E6%8A%A5%E5%91%8A`) || strings.Contains(body, `category=%E6%8A%A5%E5%91%8A&page=2`) {
		t.Fatal("expected facet links to reset pagination")
	}
}

func TestHomeSearchFacetCountsIncludeContentHits(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	srv := startTestServer(t, store)

	createFacetSession(t, store, "报告", []string{"demo"})
	createFacetSession(t, store, "数据看板", nil)

	// "loading" appears in the fixture file body but in no metadata field,
	// so both sessions match via file content only. The facet counts must
	// reflect the merged result set, not the metadata-only store queries.
	body, _ := getHome(t, srv.Origin(), "q=loading")
	if !strings.Contains(body, `>全部 <b>2</b>`) {
		t.Fatal("expected 全部 chip to count content matches in search mode")
	}
	if !strings.Contains(body, `>报告 <b>1</b>`) || !strings.Contains(body, `>数据看板 <b>1</b>`) {
		t.Fatal("expected category chips derived from content-hit result set")
	}
	if !strings.Contains(body, `>demo <b>1</b>`) {
		t.Fatal("expected tag counts derived from content-hit result set")
	}
}

func TestBuildFacetURL(t *testing.T) {
	t.Parallel()

	u, err := url.Parse("/?tag=demo&page=3&q=x")
	if err != nil {
		t.Fatal(err)
	}
	got := buildFacetURL(u, "category", "报告")
	if !strings.Contains(got, "category=") || !strings.Contains(got, "tag=demo") || !strings.Contains(got, "q=x") || strings.Contains(got, "page=") {
		t.Fatalf("unexpected facet URL %q", got)
	}

	cleared := buildFacetClearURL(u, "tag")
	if strings.Contains(cleared, "tag=") || strings.Contains(cleared, "page=") || !strings.Contains(cleared, "q=x") {
		t.Fatalf("unexpected cleared URL %q", cleared)
	}
}

func TestCapFacetItemsKeepsActive(t *testing.T) {
	t.Parallel()

	items := make([]facetItem, 0, 5)
	for _, v := range []string{"a", "b", "c"} {
		items = append(items, facetItem{Value: v, Count: 1, URL: "/?" + v})
	}

	if got := capFacetItems(items, 10); len(got) != 3 {
		t.Fatalf("expected no capping under limit, got %d", len(got))
	}

	items = append(items, facetItem{Value: "active", Count: 1, Active: true, URL: "/?active"})
	got := capFacetItems(items, 2)
	if len(got) != 3 || got[len(got)-1].Value != "active" {
		t.Fatalf("expected active facet kept outside cap, got %v", got)
	}

	plain := []facetItem{{Value: "a"}, {Value: "b"}, {Value: "c", Active: true}}
	got = capFacetItems(plain, 2)
	if len(got) != 3 || !got[len(got)-1].Active || got[0].Value != "a" {
		t.Fatalf("expected active facet re-appended after cap, got %v", got)
	}
}
