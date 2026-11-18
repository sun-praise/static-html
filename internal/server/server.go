package server

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sun-praise/static-html/internal/analytics"
	"github.com/sun-praise/static-html/internal/live"
	"github.com/sun-praise/static-html/internal/session"
)

const (
	DefaultHost      = "0.0.0.0"
	DefaultPort      = 3939
	DefaultServerURL = "http://127.0.0.1:3939"
	maxUploadBytes   = 64 << 20
	maxArchiveFiles  = 2048
)

// hostnamePattern matches valid hostnames with dot-separated labels.
// Each label starts and ends with [a-zA-Z0-9]; inner characters allow hyphens.
// Consecutive dots are inherently rejected because each label requires
// at least one alphanumeric character between dots.
var hostnamePattern = regexp.MustCompile(`^[a-zA-Z0-9]([a-zA-Z0-9\-]*[a-zA-Z0-9])?(\.[a-zA-Z0-9]([a-zA-Z0-9\-]*[a-zA-Z0-9])?)*$`)

func isValidServerName(name string) bool {
	if ip := net.ParseIP(name); ip != nil {
		return ip.To4() != nil
	}
	return hostnamePattern.MatchString(name)
}

type Server struct {
	host       string
	port       int
	serverName string
	// serverPort, when > 0, overrides the port used in generated URLs.
	// 0 means "derive from X-Forwarded-Port header or fall back to port".
	serverPort int
	// uploadRoot, when non-empty, overrides the directory used to store
	// uploaded session snapshots. Empty falls back to the platform default
	// (~/.local/state/sth/uploads under $XDG_STATE_HOME or $HOME). Set via
	// --upload-root so container deployments can place uploads on a
	// persistent volume alongside the DB.
	uploadRoot string
	// authEnabled gates API-key authentication. When false (the default),
	// no credentials are read or checked anywhere, preserving the legacy
	// open behavior. When true, mutating endpoints and list/search/peers/
	// download require a valid Bearer key; previews remain open unless
	// protectPreviews is also set.
	authEnabled bool
	// protectPreviews, when true (implies authEnabled), additionally
	// requires an authenticated user for GET /s/<id>/... and /s/<id>/ws. An
	// owned session is visible only to its owner and the users it is shared
	// with (see authorizePreview); legacy unowned sessions stay visible to
	// any authenticated user.
	protectPreviews bool
	// allowRegistration gates the /register page. It is meaningful only when
	// authEnabled is true (when auth is off the whole auth layer is a no-op).
	// Defaults to true via SetAllowRegistration so deployments are open by
	// default; set false to require CLI-managed accounts only.
	allowRegistration bool
	store             *session.Store
	httpServer        *http.Server
	listener          net.Listener
	liveMgr           *live.Manager
	// Audit state. auditRetentionDays/auditPruneStop/auditPruneDone are only
	// accessed with mu held (Start, Stop and SetAuditRetentionDays all hold
	// it); auditReject* have their own mutex, and auditUsernameCache is a
	// concurrency-safe sync.Map.
	auditRetentionDays int
	auditPruneStop     chan struct{}
	auditPruneDone     chan struct{}
	auditRejectMu      sync.Mutex
	auditRejectWindow  int64
	auditRejectSeen    map[string]struct{}
	auditUsernameCache sync.Map
	mu                 sync.RWMutex
}

type createSessionRequest struct {
	FilePath string   `json:"filePath"`
	Title    string   `json:"title"`
	Tags     []string `json:"tags"`
	Category string   `json:"category"`
	Project  string   `json:"project"`
}

type createSessionResponse struct {
	SessionID string `json:"sessionId"`
	URL       string `json:"url"`
	EntryFile string `json:"entryFile"`
	RootDir   string `json:"rootDir"`
	ChainID   string `json:"chainId"`
	VersionNo int    `json:"versionNo"`
}

type homePageData struct {
	Sessions       []homePageSession
	SharedSessions []homePageSession
	CurrentUser    string // authenticated username, empty when auth is off
	Search         string
	FilterTag      string
	FilterCat      string
	// FilterCatLabel is FilterCat with the uncategorized sentinel mapped back
	// to its display name for the active-filter chip.
	FilterCatLabel string
	FilterProj     string
	ClearSearch    string
	ClearTag       string
	ClearCat       string
	ClearProj      string
	Page           int
	TotalPages     int
	HasPrev        bool
	HasNext        bool
	PrevPage       int
	NextPage       int
	PageRange      []int
	PageURL        map[int]string
	Analytics      template.HTML // Umami tracker on the public deployment, empty otherwise

	// Layout switches the home-page presentation: "blocks" (facet chip rows),
	// "sidebar" (facet column next to the list) or "overview" (category cards
	// with a Sessions/Categories view toggle). Persisted via the sth_layout
	// cookie; see homeLayout.
	Layout string
	// View is "sessions" or "categories"; only the overview layout renders it.
	View string
	// LayoutURLs maps each layout name to the home-page URL that switches to
	// it while preserving the current query (filters, page, view).
	LayoutURLs map[string]string
	// CatItems / TagItems are the facet chips/rows. Category counts ignore the
	// active category filter (and vice versa for tags) so the full facet list
	// stays visible; the "未分类" pseudo-bucket carries Muted.
	CatItems []facetItem
	TagItems []facetItem
	// AllDocsURL clears the category filter; AllDocsCount counts documents
	// under every other active filter.
	AllDocsURL   string
	AllDocsCount int
	// TotalDocs counts documents under every active filter (the Sessions tab
	// count on the overview layout).
	TotalDocs int
	// ViewSessionsURL / ViewCategoriesURL toggle the overview layout's view.
	ViewSessionsURL   string
	ViewCategoriesURL string
	// CatCards feeds the overview categories view: one card per category with
	// its most recent documents. Nil on other layouts/views.
	CatCards []catCardItem
}

// facetItem is one category/tag facet rendered as a chip, sidebar row or card
// header. Key is the value used in filter URLs; it equals Value except for
// the "未分类" bucket, whose URL carries the uncategorized sentinel instead of
// the display label. Muted marks the "未分类" pseudo-bucket.
type facetItem struct {
	Value  string
	Key    string
	Count  int
	URL    string
	Active bool
	Muted  bool
}

// catCardItem is one overview category card: facet count plus a small set of
// recent documents inside that category.
type catCardItem struct {
	Category string
	Key      string
	Count    int
	URL      string
	Muted    bool
	Recent   []homePageSession
}

type homePageSession struct {
	ID          string
	Name        string
	Title       string
	DisplayName string
	EntryFile   string
	CreatedAt   string
	PreviewPath string
	Tags        []string
	Category    string
	Project     string
	Owner       string // sharing owner's username, set for shared entries only
	// SharePath is the page path granted to the viewer; empty for whole-
	// session grants. Set for shared entries only.
	SharePath string
}

var homePageTemplate = template.Must(template.New("home").Parse(`<!doctype html>
<html lang="en">
  <head>
    <meta charset="utf-8">
    <title>HTML Preview Server</title>
    {{.Analytics}}
    <style>
      :root {
        color-scheme: light;
        font-family: "IBM Plex Sans", "Segoe UI", sans-serif;
      }
      body {
        margin: 0;
        padding: 2rem;
        background-color: #ebe7dc;
        background-image: linear-gradient(180deg, #f6f4ee 0%, #ebe7dc 100%);
        background-attachment: fixed;
        background-repeat: no-repeat;
        color: #171717;
      }
      main {
        max-width: 900px;
        margin: 0 auto;
        background: rgba(255, 255, 255, 0.8);
        border: 1px solid #d6d1c6;
        border-radius: 16px;
        padding: 2rem;
        box-shadow: 0 20px 50px rgba(0, 0, 0, 0.08);
      }
      h1 {
        margin-top: 0;
      }
      .logout-bar {
        display: flex;
        justify-content: flex-end;
        align-items: center;
        gap: 0.75rem;
        margin-bottom: 1rem;
        font-size: 0.85rem;
        color: #5a5a5a;
      }
      .logout-bar .who strong {
        color: #171717;
        font-weight: 600;
      }
      .logout-bar form {
        margin: 0;
      }
      .logout-btn {
        padding: 0.25rem 0.7rem;
        border: 1px solid #d6d1c6;
        border-radius: 8px;
        background: #fafaf7;
        color: #2b2a26;
        font-size: 0.85rem;
        cursor: pointer;
      }
      .logout-btn:hover {
        background: #2b2a26;
        color: #fff;
      }
      code {
        display: inline-block;
        margin-left: 0.75rem;
        padding: 0.15rem 0.4rem;
        border-radius: 6px;
        background: #f2efe6;
      }
      ul {
        padding-left: 1.2rem;
      }
      li {
        margin-bottom: 0.8rem;
      }
      time {
        margin-left: 0.75rem;
        color: #5a5a5a;
      }
      .search-bar {
        display: flex;
        gap: 0.5rem;
        margin-bottom: 1rem;
      }
      .search-bar input {
        flex: 1;
        padding: 0.5rem 0.75rem;
        border: 1px solid #d6d1c6;
        border-radius: 8px;
        font-size: 0.95rem;
        background: #fafaf7;
      }
      .search-bar button {
        padding: 0.5rem 1rem;
        border: 1px solid #d6d1c6;
        border-radius: 8px;
        background: #f2efe6;
        cursor: pointer;
        font-size: 0.95rem;
      }
      .search-bar button:hover {
        background: #e6e2d6;
      }
      .filters {
        display: flex;
        flex-wrap: wrap;
        gap: 0.4rem;
        margin-bottom: 1rem;
      }
      .filter-tag {
        display: inline-flex;
        align-items: center;
        gap: 0.3rem;
        padding: 0.2rem 0.6rem;
        border-radius: 12px;
        font-size: 0.85rem;
        cursor: pointer;
        text-decoration: none;
      }
      .filter-tag.tag {
        background: #dbeafe;
        color: #1e40af;
      }
      .filter-tag.tag:hover {
        background: #bfdbfe;
      }
      .filter-tag.category {
        background: #dcfce7;
        color: #166534;
      }
      .filter-tag.category:hover {
        background: #bbf7d0;
      }
      .filter-tag.project {
        background: #fef3c7;
        color: #92400e;
      }
      .filter-tag.project:hover {
        background: #fde68a;
      }
      .filter-tag .remove {
        font-weight: bold;
        margin-left: 0.2rem;
      }
      .meta {
        margin-top: 0.25rem;
        display: flex;
        flex-wrap: wrap;
        gap: 0.3rem;
        align-items: center;
      }
      .meta .tag {
        display: inline-block;
        padding: 0.1rem 0.5rem;
        border-radius: 10px;
        font-size: 0.8rem;
        background: #dbeafe;
        color: #1e40af;
      }
      .meta .category {
        display: inline-block;
        padding: 0.1rem 0.5rem;
        border-radius: 10px;
        font-size: 0.8rem;
        background: #dcfce7;
        color: #166534;
      }
      .meta .project {
        display: inline-block;
        padding: 0.1rem 0.5rem;
        border-radius: 10px;
        font-size: 0.8rem;
        background: #fef3c7;
        color: #92400e;
      }
      .delete-btn {
        margin-left: 0.5rem;
        padding: 0.1rem 0.4rem;
        border: none;
        border-radius: 4px;
        background: none;
        color: #aaa;
        cursor: pointer;
        font-size: 0.85rem;
        vertical-align: middle;
      }
      .delete-btn:hover {
        background: #fee2e2;
        color: #dc2626;
      }
      .action-btn {
        margin-left: 0.3rem;
        padding: 0.1rem 0.4rem;
        border: none;
        border-radius: 4px;
        background: none;
        color: #aaa;
        cursor: pointer;
        font-size: 0.85rem;
        vertical-align: middle;
        text-decoration: none;
        display: inline-block;
      }
      .action-btn:hover {
        background: #e6e2d6;
        color: #171717;
      }
      .pagination {
        display: flex;
        justify-content: center;
        align-items: center;
        gap: 0.25rem;
        margin-top: 1.5rem;
        padding-top: 1rem;
        border-top: 1px solid #d6d1c6;
        flex-wrap: wrap;
      }
      .pagination a, .pagination span {
        display: inline-block;
        padding: 0.3rem 0.6rem;
        border-radius: 6px;
        text-decoration: none;
        font-size: 0.9rem;
        min-width: 1.5rem;
        text-align: center;
      }
      .pagination a {
        background: #f2efe6;
        color: #171717;
        border: 1px solid #d6d1c6;
      }
      .pagination a:hover {
        background: #e6e2d6;
      }
      .pagination .current {
        background: #171717;
        color: #fff;
        border: 1px solid #171717;
      }
      .pagination .disabled {
        color: #aaa;
        background: transparent;
        border: 1px solid transparent;
        cursor: default;
      }
      .share-panel {
        margin-top: 0.4rem;
        padding: 0.5rem 0.6rem;
        border: 1px solid #d6d1c6;
        border-radius: 8px;
        background: #fafaf7;
        font-size: 0.85rem;
        max-width: 360px;
      }
      .share-panel .share-row {
        display: flex;
        justify-content: space-between;
        align-items: center;
        padding: 0.15rem 0;
      }
      .share-panel .share-scope {
        color: #5a5a5a;
        font-size: 0.78rem;
        margin-left: 0.3rem;
      }
      .share-panel .share-remove {
        border: none;
        background: none;
        color: #aaa;
        cursor: pointer;
        font-size: 0.9rem;
        padding: 0 0.3rem;
      }
      .share-panel .share-remove:hover {
        color: #dc2626;
      }
      .share-panel .share-empty {
        color: #5a5a5a;
      }
      .share-panel .share-add {
        display: flex;
        gap: 0.4rem;
        margin-top: 0.4rem;
      }
      .share-panel .share-add input {
        flex: 1;
        min-width: 0;
        padding: 0.25rem 0.5rem;
        border: 1px solid #d6d1c6;
        border-radius: 6px;
        background: #fff;
        font-size: 0.85rem;
      }
      .share-panel .share-add button {
        padding: 0.25rem 0.7rem;
        border: 1px solid #d6d1c6;
        border-radius: 6px;
        background: #f2efe6;
        cursor: pointer;
        font-size: 0.85rem;
      }
      .share-panel .share-error {
        margin-top: 0.3rem;
        color: #dc2626;
      }
      .layout-bar {
        display: flex;
        justify-content: flex-end;
        align-items: center;
        gap: 0.4rem;
        margin-bottom: 1rem;
        font-size: 0.85rem;
        color: #5a5a5a;
      }
      .layout-bar select {
        padding: 0.3rem 0.5rem;
        border: 1px solid #d6d1c6;
        border-radius: 8px;
        background: #fafaf7;
        color: #171717;
        font-size: 0.85rem;
        cursor: pointer;
      }
      .cat-block {
        margin-bottom: 1.25rem;
      }
      .cat-block h2 {
        font-size: 1rem;
        margin: 0.2rem 0 0.6rem;
      }
      .cat-chips {
        display: flex;
        flex-wrap: wrap;
        gap: 0.4rem;
      }
      .cat-chip {
        display: inline-flex;
        align-items: center;
        gap: 0.35rem;
        padding: 0.25rem 0.7rem;
        border-radius: 14px;
        background: #dcfce7;
        color: #166534;
        font-size: 0.85rem;
        cursor: pointer;
        text-decoration: none;
        border: 1px solid transparent;
      }
      .cat-chip b {
        font-weight: 600;
        font-size: 0.75rem;
        background: rgba(22, 101, 52, 0.12);
        border-radius: 8px;
        padding: 0 0.35rem;
      }
      .cat-chip:hover {
        background: #bbf7d0;
      }
      .cat-chip.active {
        background: #166534;
        color: #fff;
      }
      .cat-chip.muted {
        background: #f2efe6;
        color: #5a5a5a;
      }
      .cat-chip.muted b {
        background: rgba(90, 90, 90, 0.15);
        color: #5a5a5a;
      }
      .cat-chip.muted.active {
        background: #5a5a5a;
        color: #fff;
      }
      .cat-chip.muted.active b {
        background: rgba(255, 255, 255, 0.25);
        color: #fff;
      }
      .cat-chip.all {
        background: #f2efe6;
        color: #171717;
      }
      .cat-chip.all b {
        background: rgba(23, 23, 23, 0.1);
        color: #171717;
      }
      .cat-chip.all.active {
        background: #171717;
        color: #fff;
      }
      .cat-chip.all.active b {
        background: rgba(255, 255, 255, 0.25);
        color: #fff;
      }
      .tag-chip {
        display: inline-flex;
        align-items: center;
        gap: 0.35rem;
        padding: 0.25rem 0.7rem;
        border-radius: 14px;
        background: #dbeafe;
        color: #1e40af;
        font-size: 0.85rem;
        cursor: pointer;
        text-decoration: none;
        border: 1px solid transparent;
      }
      .tag-chip b {
        font-weight: 600;
        font-size: 0.75rem;
        background: rgba(30, 64, 175, 0.12);
        border-radius: 8px;
        padding: 0 0.35rem;
      }
      .tag-chip:hover {
        background: #bfdbfe;
      }
      .tag-chip.active {
        background: #1e40af;
        color: #fff;
      }
      .tag-chip.active b {
        background: rgba(255, 255, 255, 0.25);
        color: #fff;
      }
      .facet-empty {
        font-size: 0.85rem;
        color: #5a5a5a;
      }
      .two-col {
        display: grid;
        grid-template-columns: 190px 1fr;
        gap: 1.5rem;
        align-items: start;
      }
      .cat-side {
        border-right: 1px solid #d6d1c6;
        padding-right: 1.25rem;
      }
      .cat-side h2 {
        font-size: 0.95rem;
        margin: 0.1rem 0 0.5rem;
      }
      .cat-side h2.side-heading {
        margin-top: 1rem;
      }
      .cat-row {
        display: flex;
        justify-content: space-between;
        align-items: center;
        padding: 0.3rem 0.55rem;
        border-radius: 8px;
        font-size: 0.9rem;
        color: #171717;
        text-decoration: none;
        cursor: pointer;
        margin-bottom: 0.15rem;
      }
      .cat-row b {
        font-size: 0.75rem;
        background: #dcfce7;
        color: #166534;
        padding: 0 0.4rem;
        border-radius: 8px;
      }
      .cat-row:hover {
        background: #f2efe6;
      }
      .cat-row.active {
        background: #166534;
        color: #fff;
      }
      .cat-row.active b {
        background: rgba(255, 255, 255, 0.25);
        color: #fff;
      }
      .cat-row.muted {
        color: #5a5a5a;
      }
      .cat-row.muted b {
        background: #f2efe6;
        color: #5a5a5a;
      }
      .cat-row.muted.active {
        background: #5a5a5a;
        color: #fff;
      }
      .cat-row.muted.active b {
        background: rgba(255, 255, 255, 0.25);
        color: #fff;
      }
      .cat-row.all b {
        background: #f2efe6;
        color: #171717;
      }
      .cat-row.all.active {
        background: #171717;
        color: #fff;
      }
      .cat-row.all.active b {
        background: rgba(255, 255, 255, 0.25);
        color: #fff;
      }
      .cat-row.tagrow b {
        background: #dbeafe;
        color: #1e40af;
      }
      .cat-row.tagrow.active {
        background: #1e40af;
        color: #fff;
      }
      .cat-row.tagrow.active b {
        background: rgba(255, 255, 255, 0.25);
        color: #fff;
      }
      .tag-row {
        margin-bottom: 1.25rem;
      }
      @media (max-width: 700px) {
        .two-col {
          grid-template-columns: 1fr;
        }
        .cat-side {
          border-right: none;
          border-bottom: 1px solid #d6d1c6;
          padding: 0 0 0.8rem;
          margin-bottom: 0.8rem;
        }
      }
      .view-tabs {
        display: flex;
        gap: 0.4rem;
        margin-bottom: 1rem;
      }
      .view-tab {
        padding: 0.3rem 0.9rem;
        border: 1px solid #d6d1c6;
        border-radius: 8px;
        background: #f2efe6;
        font-size: 0.9rem;
        cursor: pointer;
        text-decoration: none;
        color: #171717;
      }
      .view-tab:hover {
        background: #e6e2d6;
      }
      .view-tab.active {
        background: #171717;
        color: #fff;
        border-color: #171717;
      }
      .cat-grid {
        display: grid;
        grid-template-columns: repeat(auto-fill, minmax(240px, 1fr));
        gap: 0.9rem;
        margin-bottom: 1.25rem;
      }
      .cat-card {
        display: block;
        border: 1px solid #d6d1c6;
        border-radius: 12px;
        background: #fafaf7;
        padding: 0.9rem 1rem;
        cursor: pointer;
        text-decoration: none;
        color: #171717;
      }
      .cat-card:hover {
        border-color: #166534;
        box-shadow: 0 6px 18px rgba(0, 0, 0, 0.06);
      }
      .cat-card h3 {
        margin: 0;
        font-size: 0.95rem;
        display: flex;
        justify-content: space-between;
        align-items: center;
      }
      .cat-card h3 b {
        font-size: 0.8rem;
        background: #dcfce7;
        color: #166534;
        border-radius: 8px;
        padding: 0 0.45rem;
      }
      .cat-card ul {
        margin: 0.5rem 0 0;
        padding-left: 1rem;
        font-size: 0.8rem;
        color: #5a5a5a;
      }
      .cat-card ul li {
        margin-bottom: 0.15rem;
      }
      .cat-card .view-all {
        display: inline-block;
        margin-top: 0.5rem;
        font-size: 0.8rem;
        color: #166534;
        font-weight: 600;
      }
      .cat-card.muted h3 b {
        background: #f2efe6;
        color: #5a5a5a;
      }
      .cat-card.muted .view-all {
        color: #5a5a5a;
      }
    </style>
  </head>
  <body>
    <main>
      {{- if .CurrentUser }}
      <div class="logout-bar">
        <span class="who">Signed in as <strong>{{ .CurrentUser }}</strong></span>
        <form method="post" action="/logout">
          <button type="submit" class="logout-btn">Sign out</button>
        </form>
      </div>
      {{- end }}
      <h1>HTML Preview Server</h1>
      <p>Register a file with <code>sth send path/to/file.html</code> and open the returned session URL.</p>
      <form class="search-bar" method="get" action="/">
        <input type="text" name="q" placeholder="Search documents..." value="{{ .Search }}" />
        <button type="submit">Search</button>
      </form>
      <div class="layout-bar">
        <label for="layout-select">视图</label>
        <select id="layout-select">
          <option value="blocks" data-url="{{index .LayoutURLs "blocks"}}"{{if eq .Layout "blocks"}} selected{{end}}>区块</option>
          <option value="sidebar" data-url="{{index .LayoutURLs "sidebar"}}"{{if eq .Layout "sidebar"}} selected{{end}}>侧边栏</option>
          <option value="overview" data-url="{{index .LayoutURLs "overview"}}"{{if eq .Layout "overview"}} selected{{end}}>概览</option>
        </select>
      </div>
      {{- if eq .Layout "sidebar" }}
      <div class="two-col">
        <aside class="cat-side">
          <h2>Categories</h2>
          <a class="cat-row all{{if not .FilterCat}} active{{end}}" href="{{ .AllDocsURL }}"><span>All documents</span><b>{{ .AllDocsCount }}</b></a>
          {{- range .CatItems }}
          <a class="cat-row{{if .Muted}} muted{{end}}{{if .Active}} active{{end}}" href="{{ .URL }}"><span>{{ .Value }}</span><b>{{ .Count }}</b></a>
          {{- end }}
          <h2 class="side-heading">Tags</h2>
          {{- range .TagItems }}
          <a class="cat-row tagrow{{if .Active}} active{{end}}" href="{{ .URL }}"><span>{{ .Value }}</span><b>{{ .Count }}</b></a>
          {{- else }}
          <span class="facet-empty">No tags yet.</span>
          {{- end }}
        </aside>
        <div class="col-main">
          {{template "homeMainCol" .}}
        </div>
      </div>
      {{- else if eq .Layout "overview" }}
      <div class="view-tabs">
        <a class="view-tab{{if ne .View "categories"}} active{{end}}" href="{{ .ViewSessionsURL }}">Sessions ({{ .TotalDocs }})</a>
        <a class="view-tab{{if eq .View "categories"}} active{{end}}" href="{{ .ViewCategoriesURL }}">Categories ({{ len .CatItems }})</a>
      </div>
      {{- if .TagItems }}
      <div class="cat-chips tag-row">
        {{- range .TagItems }}
        <a class="tag-chip{{if .Active}} active{{end}}" href="{{ .URL }}">{{ .Value }} <b>{{ .Count }}</b></a>
        {{- end }}
      </div>
      {{- end }}
      {{- if eq .View "categories" }}
      <div class="cat-grid">
        {{- range .CatCards }}
        <a class="cat-card{{if .Muted}} muted{{end}}" href="{{ .URL }}">
          <h3>{{ .Category }} <b>{{ .Count }}</b></h3>
          <ul>
            {{- range .Recent }}
            <li>{{ .DisplayName }}</li>
            {{- else }}
            <li>Empty.</li>
            {{- end }}
          </ul>
          <span class="view-all">View all →</span>
        </a>
        {{- end }}
      </div>
      {{- else }}
      {{template "homeMainCol" .}}
      {{- end }}
      {{- else }}
      <div class="cat-block">
        <h2>Categories</h2>
        <div class="cat-chips">
          <a class="cat-chip all{{if not .FilterCat}} active{{end}}" href="{{ .AllDocsURL }}">全部 <b>{{ .AllDocsCount }}</b></a>
          {{- range .CatItems }}
          <a class="cat-chip{{if .Muted}} muted{{end}}{{if .Active}} active{{end}}" href="{{ .URL }}">{{ .Value }} <b>{{ .Count }}</b></a>
          {{- end }}
        </div>
      </div>
      <div class="cat-block">
        <h2>Tags</h2>
        <div class="cat-chips">
          {{- range .TagItems }}
          <a class="tag-chip{{if .Active}} active{{end}}" href="{{ .URL }}">{{ .Value }} <b>{{ .Count }}</b></a>
          {{- else }}
          <span class="facet-empty">No tags yet.</span>
          {{- end }}
        </div>
      </div>
      {{template "homeMainCol" .}}
      {{- end }}
      {{- if .SharedSessions }}
      <h2>Shared with you</h2>
      <ul>
        {{- range .SharedSessions }}
        <li>
          <a href="{{ .PreviewPath }}">{{ .DisplayName }}</a>
          <time datetime="{{ .CreatedAt }}">{{ .CreatedAt }}</time>
          <span class="meta project">by {{ .Owner }}</span>
          {{- if .SharePath }}
          <span class="meta tag">page: {{ .SharePath }}</span>
          {{- end }}
          {{- if or .Tags .Category .Project }}
          <div class="meta">
            {{- range .Tags }}
            <a href="?tag={{ . }}" class="tag">{{ . }}</a>
            {{- end }}
            {{- if .Category }}
            <a href="?category={{ .Category }}" class="category">{{ .Category }}</a>
            {{- end }}
            {{- if .Project }}
            <a href="?project={{ .Project }}" class="project">{{ .Project }}</a>
            {{- end }}
          </div>
          {{- end }}
        </li>
        {{- end }}
      </ul>
      {{- end }}
    </main>
    <script>
    var listEl = document.querySelector('.session-list');
    if (listEl) {
      listEl.addEventListener('click', onListClick);
      listEl.addEventListener('submit', onListSubmit);
    }

    var layoutSel = document.getElementById('layout-select');
    if (layoutSel) {
      layoutSel.addEventListener('change', function() {
        var opt = layoutSel.options[layoutSel.selectedIndex];
        if (opt && opt.dataset.url) { window.location.href = opt.dataset.url; }
      });
    }

    function onListClick(e) {
      var btn = e.target.closest('.delete-btn');
      if (btn) {
        var li = btn.closest('li');
        var name = li.querySelector('a').textContent;
        if (!confirm('Delete "' + name + '"?')) return;
        fetch('/api/sessions/' + li.dataset.sessionId, { method: 'DELETE' }).then(function(r) {
          if (r.ok) { li.remove(); } else { alert('Delete failed: ' + r.status); }
        }).catch(function() { alert('Network error'); });
        return;
      }
      var shareBtn = e.target.closest('.share-btn');
      if (shareBtn) {
        var url = window.location.origin + shareBtn.dataset.shareUrl;
        if (navigator.share) {
          navigator.share({ url: url }).catch(function() {});
        } else if (navigator.clipboard) {
          navigator.clipboard.writeText(url).then(function() {
            var original = shareBtn.textContent;
            shareBtn.textContent = 'Copied!';
            setTimeout(function() { shareBtn.textContent = original; }, 1500);
          }).catch(function() {});
        }
        return;
      }
      var sharesBtn = e.target.closest('.shares-btn');
      if (sharesBtn) {
        var panel = sharePanelFor(sharesBtn.closest('li'));
        if (panel.hidden) {
          panel.hidden = false;
          loadShares(panel);
        } else {
          panel.hidden = true;
        }
        return;
      }
      var removeBtn = e.target.closest('.share-remove');
      if (removeBtn) {
        var row = removeBtn.closest('.share-row');
        var panel2 = removeBtn.closest('.share-panel');
        var li2 = removeBtn.closest('li');
        var target = '/api/sessions/' + li2.dataset.sessionId + '/shares/' + encodeURIComponent(row.dataset.user);
        if (row.dataset.path) target += '?path=' + encodeURIComponent(row.dataset.path);
        apiCall('DELETE', target, null, panel2, function(data) {
          renderShares(panel2, data);
        });
      }
    }

    function onListSubmit(e) {
      var form = e.target.closest('.share-add');
      if (!form) return;
      e.preventDefault();
      var user = (form.querySelector('input[name=user]').value || '').trim();
      var pagePath = (form.querySelector('input[name=page]').value || '').trim();
      var panel = form.closest('.share-panel');
      var li = form.closest('li');
      if (!user) return;
      apiCall('PUT', '/api/sessions/' + li.dataset.sessionId + '/shares', { user: user, path: pagePath }, panel, function(data) {
        renderShares(panel, data);
      });
    }

    function apiCall(method, url, body, panel, onDone) {
      var opts = { method: method, credentials: 'same-origin' };
      if (body) {
        opts.headers = { 'Content-Type': 'application/json' };
        opts.body = JSON.stringify(body);
      }
      fetch(url, opts)
        .then(function(r) {
          return r.json().then(function(data) { return { ok: r.ok, data: data }; });
        })
        .then(function(res) {
          if (!res.ok) {
            showError(panel, (res.data && res.data.error) || 'Request failed.');
            return;
          }
          onDone(res.data);
        })
        .catch(function() { showError(panel, 'Network error'); });
    }

    function sharePanelFor(li) {
      var panel = li.querySelector('.share-panel');
      if (!panel) {
        panel = document.createElement('div');
        panel.className = 'share-panel';
        panel.hidden = true;
        li.appendChild(panel);
      }
      return panel;
    }

    function loadShares(panel) {
      var li = panel.closest('li');
      panel.innerHTML = '<div class="share-list">Loading…</div><div class="share-error"></div>';
      apiCall('GET', '/api/sessions/' + li.dataset.sessionId + '/shares', null, panel, function(data) {
        renderShares(panel, data);
      });
    }

    function renderShares(panel, data) {
      var rows = '';
      var shares = (data && data.shares) || [];
      for (var i = 0; i < shares.length; i++) {
        var name = escapeHtml(shares[i].username);
        var scope = shares[i].path ? escapeHtml(shares[i].path) : 'whole session';
        var pathAttr = shares[i].path ? escapeHtml(shares[i].path) : '';
        rows += '<div class="share-row" data-user="' + name + '" data-path="' + pathAttr + '">' +
          '<span>' + name + ' <span class="share-scope">' + scope + '</span></span>' +
          '<button class="share-remove" type="button" title="Revoke access">&times;</button></div>';
      }
      if (!rows) rows = '<div class="share-empty">Not shared with anyone.</div>';
      panel.innerHTML =
        '<div class="share-list">' + rows + '</div>' +
        '<form class="share-add">' +
        '<input name="user" placeholder="username" required maxlength="64">' +
        '<input name="page" placeholder="page (optional)" maxlength="200">' +
        '<button type="submit">Share</button></form>' +
        '<div class="share-error"></div>';
    }

    function showError(panel, msg) {
      var el = panel.querySelector('.share-error');
      if (el) el.textContent = msg;
    }

    function escapeHtml(s) {
      return String(s).replace(/[&<>"']/g, function(c) {
        return { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c];
      });
    }
    </script>
  </body>
</html>
{{- define "homeMainCol" }}
      {{- if or .FilterTag .FilterCat .FilterProj .Search }}
      <div class="filters">
        {{- if .Search }}
        <a href="{{ .ClearSearch }}" class="filter-tag tag">Search: {{ .Search }} <span class="remove">&times;</span></a>
        {{- end }}
        {{- if .FilterTag }}
        <a href="{{ .ClearTag }}" class="filter-tag tag">Tag: {{ .FilterTag }} <span class="remove">&times;</span></a>
        {{- end }}
        {{- if .FilterCat }}
        <a href="{{ .ClearCat }}" class="filter-tag category">Category: {{ .FilterCatLabel }} <span class="remove">&times;</span></a>
        {{- end }}
        {{- if .FilterProj }}
        <a href="{{ .ClearProj }}" class="filter-tag project">Project: {{ .FilterProj }} <span class="remove">&times;</span></a>
        {{- end }}
      </div>
      {{- end }}
      <ul class="session-list">
      {{- if .Sessions }}
        {{- range .Sessions }}
        <li data-session-id="{{ .ID }}">
          <a href="{{ .PreviewPath }}">{{ .DisplayName }}</a>
          <time datetime="{{ .CreatedAt }}">{{ .CreatedAt }}</time>
          <a class="action-btn download-btn" href="/api/sessions/{{ .ID }}/download" title="Download" download>⬇</a>
          <button class="action-btn share-btn" data-share-url="{{ .PreviewPath }}" title="Share">🔗</button>
          {{- if $.CurrentUser }}
          <button class="action-btn shares-btn" title="Manage shares">👥</button>
          {{- end }}
          <button class="delete-btn" data-session-id="{{ .ID }}" title="Delete session">&times;</button>
          {{- if or .Tags .Category .Project }}
          <div class="meta">
            {{- range .Tags }}
            <a href="?tag={{ . }}" class="tag">{{ . }}</a>
            {{- end }}
            {{- if .Category }}
            <a href="?category={{ .Category }}" class="category">{{ .Category }}</a>
            {{- end }}
            {{- if .Project }}
            <a href="?project={{ .Project }}" class="project">{{ .Project }}</a>
            {{- end }}
          </div>
          {{- end }}
        </li>
        {{- end }}
      {{- else }}
        <li>No preview sessions found.</li>
      {{- end }}
      </ul>
      {{- if gt .TotalPages 1 }}
      <nav class="pagination">
        {{- if .HasPrev }}
        <a href="{{ index $.PageURL $.PrevPage }}">← Prev</a>
        {{- else }}
        <span class="disabled">← Prev</span>
        {{- end }}
        {{- range .PageRange }}
          {{- if eq . $.Page }}
        <span class="current">{{ . }}</span>
          {{- else }}
        <a href="{{ index $.PageURL . }}">{{ . }}</a>
          {{- end }}
        {{- end }}
        {{- if .HasNext }}
        <a href="{{ index $.PageURL $.NextPage }}">Next →</a>
        {{- else }}
        <span class="disabled">Next →</span>
        {{- end }}
      </nav>
      {{- end }}
{{- end }}`))

func New(host string, port int, store *session.Store, serverName string, serverPort int, uploadRoot string) (*Server, error) {
	if host == "" {
		host = DefaultHost
	}

	if serverName != "" {
		if !isValidServerName(serverName) {
			return nil, errors.New("server: --server-name must be a valid IPv4 address or hostname (letters, digits, dots, hyphens)")
		}
	}

	if serverPort < 0 || serverPort > 65535 {
		return nil, fmt.Errorf("server: --server-port must be in range 1-65535, got %d", serverPort)
	}

	if store == nil {
		return nil, errors.New("server: store must not be nil")
	}

	srv := &Server{
		host:       host,
		port:       port,
		serverName: serverName,
		serverPort: serverPort,
		store:      store,
		uploadRoot: uploadRoot,
		liveMgr: live.NewManager(func(sessionID, dir string, notify func()) (context.CancelFunc, error) {
			return live.WatchDir(context.Background(), dir, notify)
		}),
	}

	srv.httpServer = &http.Server{
		Handler: srv.authMiddleware(live.InjectMiddleware(srv.routes())),
	}

	return srv, nil
}

// SetAuthEnabled toggles API-key authentication. It must be called before
// Start. When enabled, mutating endpoints and list/search/peers/download
// require a valid Bearer key; previews remain open unless protectPreviews
// is also true. Disabling auth also clears protectPreviews, since preview
// protection relies on the auth key-verification infrastructure — the two
// must never be in a state where previews are "protected" but auth is off.
func (s *Server) SetAuthEnabled(enabled bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.authEnabled = enabled
	if !enabled {
		s.protectPreviews = false
	}
}

// SetProtectPreviews toggles preview protection. Implies authEnabled: when
// protectPreviews is true, auth is force-enabled regardless of the prior
// authEnabled value, because preview protection relies on the auth key
// verification infrastructure.
func (s *Server) SetProtectPreviews(protect bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.protectPreviews = protect
	if protect {
		s.authEnabled = true
	}
}

// AuthEnabled reports whether authentication is active (read-only accessor
// for handlers/middleware and startup logging).
func (s *Server) AuthEnabled() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.authEnabled
}

// ProtectPreviews reports whether preview endpoints also require a key.
func (s *Server) ProtectPreviews() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.protectPreviews
}

// SetAllowRegistration toggles whether the /register page creates accounts.
// Must be called before Start. Default is true (open registration); set false
// to restrict account creation to the CLI (sth user add). Has no effect when
// auth is disabled, since the entire auth layer is then a no-op.
func (s *Server) SetAllowRegistration(allow bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.allowRegistration = allow
}

// AllowRegistration reports whether self-service registration is permitted.
func (s *Server) AllowRegistration() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.allowRegistration
}

func (s *Server) Start() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.listener != nil {
		return nil
	}

	listener, err := net.Listen("tcp", fmt.Sprintf("%s:%d", s.host, s.port))
	if err != nil {
		return err
	}

	s.listener = listener

	if s.auditRetentionDays > 0 && s.auditPruneStop == nil {
		s.auditPruneStop = make(chan struct{})
		s.auditPruneDone = make(chan struct{})
		go s.startAuditPrune(s.auditRetentionDays, s.auditPruneStop, s.auditPruneDone)
	}

	go func() {
		err := s.httpServer.Serve(listener)
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			fmt.Fprintf(os.Stderr, "server error: %v\n", err)
		}
	}()

	return nil
}

func (s *Server) Stop(ctx context.Context) error {
	s.mu.Lock()
	if s.listener == nil {
		s.mu.Unlock()
		return nil
	}

	var pruneDone <-chan struct{}
	if s.auditPruneStop != nil {
		close(s.auditPruneStop)
		pruneDone = s.auditPruneDone
		s.auditPruneStop = nil
		s.auditPruneDone = nil
	}

	err := s.httpServer.Shutdown(ctx)
	s.listener = nil
	s.mu.Unlock()

	// Wait outside the lock: holding mu for up to 2s would block readers of
	// AuthEnabled()/ProtectPreviews(). The caller must not close the store
	// before this returns, or an in-flight prune reports "database is closed".
	if pruneDone != nil {
		select {
		case <-pruneDone:
		case <-time.After(2 * time.Second):
			fmt.Fprintln(os.Stderr, "audit: prune loop did not stop within 2s")
		}
	}
	return err
}

func (s *Server) Origin() string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.listener == nil {
		return ""
	}

	address, ok := s.listener.Addr().(*net.TCPAddr)
	if !ok {
		return ""
	}

	// NOTE: scheme is hardcoded to http, consistent with the rest of the codebase.
	if s.serverName != "" {
		return fmt.Sprintf("http://%s:%d", s.serverName, address.Port)
	}
	ip := address.IP
	if ip.IsUnspecified() {
		ip = net.IPv4(127, 0, 0, 1)
	}

	return fmt.Sprintf("http://%s:%d", ip.String(), address.Port)
}

func (s *Server) Origins() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.listener == nil {
		return nil
	}

	address, ok := s.listener.Addr().(*net.TCPAddr)
	if !ok {
		return nil
	}

	port := address.Port

	if s.serverName != "" {
		return []string{
			fmt.Sprintf("http://%s:%d", s.serverName, port),
		}
	}

	if address.IP.IsUnspecified() {
		origins := []string{fmt.Sprintf("http://127.0.0.1:%d", port)}

		addrs, err := net.InterfaceAddrs()
		if err == nil {
			for _, addr := range addrs {
				if ipNet, ok := addr.(*net.IPNet); ok && !ipNet.IP.IsLoopback() && ipNet.IP.To4() != nil {
					origins = append(origins, fmt.Sprintf("http://%s:%d", ipNet.IP.String(), port))
				}
			}
		}

		return origins
	}

	return []string{fmt.Sprintf("http://%s:%d", address.IP.String(), port)}
}

func (s *Server) routes() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/login":
			s.handleLoginGet(w, r)
		case r.Method == http.MethodPost && r.URL.Path == "/login":
			s.handleLoginPost(w, r)
		case r.Method == http.MethodGet && r.URL.Path == "/register":
			s.handleRegisterGet(w, r)
		case r.Method == http.MethodPost && r.URL.Path == "/register":
			s.handleRegisterPost(w, r)
		case r.Method == http.MethodPost && r.URL.Path == "/logout":
			s.handleLogout(w, r)
		case r.Method == http.MethodGet && r.URL.Path == "/":
			s.handleHome(w, r)
		case r.Method == http.MethodPost && r.URL.Path == "/api/sessions":
			s.handleCreateSession(w, r)
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/s/"):
			if strings.HasSuffix(r.URL.Path, "/ws") {
				sid := strings.TrimPrefix(r.URL.Path, "/s/")
				sid = strings.TrimSuffix(sid, "/ws")
				sess, found, err := s.store.Get(sid)
				if err != nil || !found {
					http.NotFound(w, r)
					return
				}
				if !s.authorizePreview(w, r, sess, "", true) {
					return
				}
				live.HandleWebSocket(s.liveMgr, func(id string) string {
					s, ok, _ := s.store.Get(id)
					if ok {
						return s.StoredRootDir
					}
					return ""
				}).ServeHTTP(w, r)
				return
			}
			s.handlePreview(w, r)
		case r.Method == http.MethodPut && hasPrefixSuffix(r.URL.Path, "/api/sessions/", "/tags"):
			s.handleAddTags(w, r)
		case r.Method == http.MethodDelete && hasPrefixSuffix(r.URL.Path, "/api/sessions/", "/tags"):
			s.handleRemoveTags(w, r)
		case r.Method == http.MethodPut && hasPrefixSuffix(r.URL.Path, "/api/sessions/", "/category"):
			s.handleSetCategory(w, r)
		case r.Method == http.MethodDelete && hasPrefixSuffix(r.URL.Path, "/api/sessions/", "/category"):
			s.handleClearCategory(w, r)
		case r.Method == http.MethodPut && hasPrefixSuffix(r.URL.Path, "/api/sessions/", "/project"):
			s.handleSetProject(w, r)
		case r.Method == http.MethodDelete && hasPrefixSuffix(r.URL.Path, "/api/sessions/", "/project"):
			s.handleClearProject(w, r)
		case r.Method == http.MethodPut && hasPrefixSuffix(r.URL.Path, "/api/sessions/", "/title"):
			s.handleSetTitle(w, r)
		case r.Method == http.MethodGet && hasPrefixSuffix(r.URL.Path, "/api/sessions/", "/metadata"):
			s.handleGetMetadata(w, r)
		case r.Method == http.MethodGet && hasPrefixSuffix(r.URL.Path, "/api/sessions/", "/peers"):
			s.handleGetPeers(w, r)
		case r.Method == http.MethodGet && hasPrefixSuffix(r.URL.Path, "/api/sessions/", "/chain"):
			s.handleGetChain(w, r)
		case r.Method == http.MethodGet && hasPrefixSuffix(r.URL.Path, "/api/sessions/", "/shares"):
			s.handleListShares(w, r)
		case r.Method == http.MethodPut && hasPrefixSuffix(r.URL.Path, "/api/sessions/", "/shares"):
			s.handleAddShare(w, r)
		case r.Method == http.MethodDelete && parseShareTargetPathOK(r.URL.Path):
			s.handleRemoveShare(w, r)
		case r.Method == http.MethodGet && hasPrefixSuffix(r.URL.Path, "/api/sessions/", "/diff"):
			s.handleGetDiff(w, r)
		case r.Method == http.MethodGet && hasPrefixSuffix(r.URL.Path, "/api/sessions/", "/download"):
			s.handleDownloadSession(w, r)
		case r.Method == http.MethodDelete && isExactSessionPath(r.URL.Path):
			s.handleDeleteSession(w, r)
		case r.Method == http.MethodPut && hasPrefixSuffix(r.URL.Path, "/api/sessions/", "/files"):
			s.handleUpdateFiles(w, r)
		default:
			http.NotFound(w, r)
		}
	})
}

const defaultPageSize = 20

func (s *Server) handleHome(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	search := strings.TrimSpace(q.Get("q"))
	filterTag := strings.TrimSpace(q.Get("tag"))
	filterCat := strings.TrimSpace(q.Get("category"))
	filterProj := strings.TrimSpace(q.Get("project"))
	filterCatLabel := filterCat
	if filterCat == session.UncategorizedFilterValue {
		filterCatLabel = uncategorizedLabel
	}
	layout := homeLayout(r, w)
	view := "sessions"
	if q.Get("view") == "categories" {
		view = "categories"
	}

	page := 1
	if p := q.Get("page"); p != "" {
		if n, err := fmt.Sscanf(p, "%d", &page); err != nil || n != 1 || page < 1 {
			page = 1
		}
	}

	// When auth is on, scope every query to the authenticated user so the
	// home page only lists that user's own sessions.
	ownerID, _ := currentUser(r)

	var items []homePageSession
	var total int
	var totalPages int
	// searchResultDocs holds the search path's merged result set (metadata
	// plus file-content matches); facet counts are derived from it.
	var searchResultDocs []session.DocumentInfo

	if search != "" {
		// Search path: full-text search across metadata + file content.
		// File content search happens in Go, so we must fetch all candidates
		// and paginate in memory. This is the only correct approach when
		// file content matching is required.
		//
		// The candidate queries drop the tag/category constraints: the strict
		// result set is carved out in memory (filterDocsByCatTag), and the
		// base set doubles as the facet/card input where each dimension
		// lifts its own filter (facetCountsFromDocs / recentPerCategoryFromDocs).
		baseFilter := session.FilterOptions{
			Project: filterProj,
			OwnerID: ownerID,
		}
		docs, err := s.store.SearchDocuments(search, baseFilter)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		metaMatched := make(map[string]bool, len(docs))
		for _, d := range docs {
			metaMatched[d.SessionID] = true
		}

		listDocs, err := s.store.ListDocuments(baseFilter)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		queryLower := strings.ToLower(search)
		for _, d := range listDocs {
			if metaMatched[d.SessionID] {
				continue
			}
			if fileContentContains(d.Name, queryLower) {
				docs = append(docs, d)
			}
		}
		searchResultDocs = docs

		docs = filterDocsByCatTag(docs, filterTag, filterCat)

		total = len(docs)
		totalPages = (total + defaultPageSize - 1) / defaultPageSize
		if totalPages < 1 {
			totalPages = 1
		}
		if page > totalPages {
			page = totalPages
		}
		start := (page - 1) * defaultPageSize
		end := start + defaultPageSize
		if end > total {
			end = total
		}
		if start < total {
			items = toHomePageSessions(docs[start:end])
		}
	} else {
		// Non-search paths: use SQL-level LIMIT/OFFSET for efficiency.
		countFilter := session.FilterOptions{
			Tag:      filterTag,
			Category: filterCat,
			Project:  filterProj,
			OwnerID:  ownerID,
		}
		var err error
		total, err = s.store.CountDocuments(countFilter)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		totalPages = (total + defaultPageSize - 1) / defaultPageSize
		if totalPages < 1 {
			totalPages = 1
		}
		if page > totalPages {
			page = totalPages
		}

		docs, err := s.store.ListDocuments(session.FilterOptions{
			Tag:      filterTag,
			Category: filterCat,
			Project:  filterProj,
			OwnerID:  ownerID,
			Limit:    defaultPageSize,
			Offset:   (page - 1) * defaultPageSize,
		})
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		items = toHomePageSessions(docs)
	}

	clearSearch := buildClearURL(r.URL, "q")
	clearTag := buildClearURL(r.URL, "tag")
	clearCat := buildClearURL(r.URL, "category")
	clearProj := buildClearURL(r.URL, "project")

	// Facet counts power the layout switchers' chip rows / sidebar / cards.
	// Each dimension lifts its own filter so the full facet list stays
	// visible: picking "报告" must not collapse the category counts. In
	// search mode the counts are derived from the merged result set so that
	// file-content matches are reflected too — the store facet queries only
	// see metadata, and would contradict the visible result list.
	facetFilter := session.FilterOptions{
		Tag:      filterTag,
		Category: filterCat,
		Project:  filterProj,
		OwnerID:  ownerID,
	}
	var catFacets []session.FacetCount
	var tagFacets []session.FacetCount
	var uncatCount int
	if search != "" {
		catFacets, uncatCount, tagFacets = facetCountsFromDocs(searchResultDocs, filterTag, filterCat)
	} else {
		var err error
		catFacets, uncatCount, err = s.store.CategoryFacets("", facetFilter)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		tagFacets, err = s.store.TagFacets("", facetFilter)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}

	allDocsCount := uncatCount
	catItems := make([]facetItem, 0, len(catFacets)+1)
	for _, fc := range catFacets {
		allDocsCount += fc.Count
		catItems = append(catItems, facetItem{
			Value:  fc.Value,
			Key:    fc.Value,
			Count:  fc.Count,
			URL:    buildFacetURL(r.URL, "category", fc.Value),
			Active: filterCat == fc.Value,
		})
	}
	if uncatCount > 0 || filterCat == session.UncategorizedFilterValue {
		catItems = append(catItems, facetItem{
			Value:  uncategorizedLabel,
			Key:    session.UncategorizedFilterValue,
			Count:  uncatCount,
			URL:    buildFacetURL(r.URL, "category", session.UncategorizedFilterValue),
			Active: filterCat == session.UncategorizedFilterValue,
			Muted:  true,
		})
	}

	tagItems := capFacetItems(buildFacetItems(r.URL, "tag", tagFacets, filterTag), defaultTagFacetLimit)

	layoutURLs := make(map[string]string, len(layoutNames))
	for _, name := range layoutNames {
		lq := r.URL.Query()
		lq.Set("layout", name)
		layoutURLs[name] = (&url.URL{Path: "/", RawQuery: lq.Encode()}).String()
	}

	var catCards []catCardItem
	if layout == layoutOverview && view == "categories" {
		// Non-search mode: one window-function query fetches the recent
		// documents of every category, so a request costs O(1) queries
		// regardless of how many categories exist. Search mode derives the
		// previews from the merged result set so card previews and counts
		// see the same documents (file-content matches included).
		var recentByCat map[string][]session.DocumentInfo
		if search != "" {
			recentByCat = recentPerCategoryFromDocs(searchResultDocs, filterTag, overviewCardRecentLimit)
		} else {
			var err error
			recentByCat, err = s.store.RecentPerCategory("", facetFilter, overviewCardRecentLimit)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
		}
		catCards = make([]catCardItem, 0, len(catItems))
		for _, it := range catItems {
			if it.Count <= 0 {
				continue
			}
			var recent []session.DocumentInfo
			if it.Key == session.UncategorizedFilterValue {
				// The uncategorized bucket is not part of the per-category
				// query above; derive it separately, only when rendered.
				if search != "" {
					recent = recentUncategorizedFromDocs(searchResultDocs, filterTag, overviewCardRecentLimit)
				} else {
					uncatFilter := facetFilter
					uncatFilter.Category = session.UncategorizedFilterValue
					uncatFilter.Limit = overviewCardRecentLimit
					var err error
					recent, err = s.store.ListDocuments(uncatFilter)
					if err != nil {
						http.Error(w, err.Error(), http.StatusInternalServerError)
						return
					}
				}
			} else {
				recent = recentByCat[it.Key]
			}
			catCards = append(catCards, catCardItem{
				Category: it.Value,
				Key:      it.Key,
				Count:    it.Count,
				URL:      it.URL,
				Muted:    it.Muted,
				Recent:   toHomePageSessions(recent),
			})
		}
	}

	pageRange := make([]int, 0, totalPages)
	pageURL := make(map[int]string, totalPages)
	for i := 1; i <= totalPages; i++ {
		pageRange = append(pageRange, i)
		pageURL[i] = buildPageURL(r.URL, i)
	}

	// When auth is on, surface the authenticated username so the page can show
	// who is logged in and a logout control. Empty means "no login context"
	// (auth off), which the template treats as "don't render the logout UI".
	loggedInUser := ""
	var sharedItems []homePageSession
	if uid, ok := currentUser(r); ok {
		if u, err := s.store.FindUserByID(uid); err == nil {
			loggedInUser = u.Username
		}
		sharedDocs, err := s.store.ListSharedWithUser(uid)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		sharedItems = make([]homePageSession, 0, len(sharedDocs))
		for _, d := range sharedDocs {
			previewPath := "/s/" + d.SessionID + "/"
			if d.Path != "" {
				previewPath = "/s/" + d.SessionID + "/" + (&url.URL{Path: d.Path}).EscapedPath()
			}
			displayName := d.Title
			if displayName == "" {
				displayName = filepath.Base(d.Name)
			}
			sharedItems = append(sharedItems, homePageSession{
				ID:          d.SessionID,
				Name:        filepath.Base(d.Name),
				Title:       d.Title,
				DisplayName: displayName,
				EntryFile:   d.Name,
				CreatedAt:   d.CreatedAt,
				PreviewPath: previewPath,
				Tags:        d.Tags,
				Category:    d.Category,
				Project:     d.Project,
				Owner:       d.OwnerUsername,
				SharePath:   d.Path,
			})
		}
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := homePageTemplate.Execute(w, homePageData{
		Sessions:          items,
		SharedSessions:    sharedItems,
		CurrentUser:       loggedInUser,
		Search:            search,
		FilterTag:         filterTag,
		FilterCat:         filterCat,
		FilterCatLabel:    filterCatLabel,
		FilterProj:        filterProj,
		ClearSearch:       clearSearch,
		ClearTag:          clearTag,
		ClearCat:          clearCat,
		ClearProj:         clearProj,
		Page:              page,
		TotalPages:        totalPages,
		HasPrev:           page > 1,
		HasNext:           page < totalPages,
		PrevPage:          page - 1,
		NextPage:          page + 1,
		PageRange:         pageRange,
		PageURL:           pageURL,
		Analytics:         analyticsScript(r),
		Layout:            layout,
		View:              view,
		LayoutURLs:        layoutURLs,
		CatItems:          catItems,
		TagItems:          tagItems,
		AllDocsURL:        buildFacetClearURL(r.URL, "category"),
		AllDocsCount:      allDocsCount,
		TotalDocs:         total,
		ViewSessionsURL:   buildFacetURL(r.URL, "view", "sessions"),
		ViewCategoriesURL: buildFacetURL(r.URL, "view", "categories"),
		CatCards:          catCards,
	}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// analyticsScript renders the Umami tracker for first-party pages. It is
// non-empty only for the public deployment (internal/analytics.PublicHost);
// local dev and intranet hosts get an empty string, so the template emits
// nothing at all.
func analyticsScript(r *http.Request) template.HTML {
	if analytics.EnabledForHost(r.Host) {
		return template.HTML(analytics.ScriptTag)
	}
	return ""
}

func toHomePageSessions(docs []session.DocumentInfo) []homePageSession {
	items := make([]homePageSession, 0, len(docs))
	for _, doc := range docs {
		name := filepath.Base(doc.Name)
		displayName := doc.Title
		if displayName == "" {
			displayName = name
		}
		items = append(items, homePageSession{
			ID:          doc.SessionID,
			Name:        name,
			Title:       doc.Title,
			DisplayName: displayName,
			EntryFile:   doc.Name,
			CreatedAt:   doc.CreatedAt,
			PreviewPath: "/s/" + doc.SessionID + "/",
			Tags:        doc.Tags,
			Category:    doc.Category,
			Project:     doc.Project,
		})
	}
	return items
}

func buildClearURL(u *url.URL, removeKey string) string {
	q := u.Query()
	q.Del(removeKey)
	return (&url.URL{
		Path:     "/",
		RawQuery: q.Encode(),
		Fragment: u.Fragment,
	}).String()
}

func buildPageURL(u *url.URL, page int) string {
	q := u.Query()
	q.Set("page", fmt.Sprintf("%d", page))
	return (&url.URL{
		Path:     "/",
		RawQuery: q.Encode(),
		Fragment: u.Fragment,
	}).String()
}

const (
	layoutCookieName = "sth_layout"
	layoutBlocks     = "blocks"
	layoutSidebar    = "sidebar"
	layoutOverview   = "overview"
)

var (
	defaultLayout = layoutBlocks
	layoutNames   = []string{layoutBlocks, layoutSidebar, layoutOverview}
)

// defaultTagFacetLimit caps how much of the tag long tail the home page
// renders. The active tag is always kept visible even outside the cap.
const defaultTagFacetLimit = 20

// overviewCardRecentLimit is how many recent documents each overview
// category card previews.
const overviewCardRecentLimit = 3

func validLayout(v string) string {
	for _, name := range layoutNames {
		if v == name {
			return name
		}
	}
	return ""
}

// homeLayout resolves the active home-page layout. An explicit ?layout= wins
// and is remembered in a long-lived cookie; otherwise fall back to the
// cookie, then to the default. Invalid values are ignored.
func homeLayout(r *http.Request, w http.ResponseWriter) string {
	if v := validLayout(r.URL.Query().Get("layout")); v != "" {
		http.SetCookie(w, &http.Cookie{
			Name:     layoutCookieName,
			Value:    v,
			Path:     "/",
			MaxAge:   int((365 * 24 * time.Hour).Seconds()),
			HttpOnly: true,
			SameSite: http.SameSiteLaxMode,
		})
		return v
	}
	if c, err := r.Cookie(layoutCookieName); err == nil {
		if v := validLayout(c.Value); v != "" {
			return v
		}
	}
	return defaultLayout
}

// buildFacetURL returns the home-page URL with a facet dimension set to
// value, preserving every other query parameter. Changing a filter resets
// pagination since the result set changes.
func buildFacetURL(u *url.URL, key, value string) string {
	q := u.Query()
	q.Set(key, value)
	q.Del("page")
	return (&url.URL{
		Path:     "/",
		RawQuery: q.Encode(),
	}).String()
}

// buildFacetClearURL removes a facet dimension from the home-page query and
// resets pagination, like buildFacetURL.
func buildFacetClearURL(u *url.URL, key string) string {
	q := u.Query()
	q.Del(key)
	q.Del("page")
	return (&url.URL{
		Path:     "/",
		RawQuery: q.Encode(),
	}).String()
}

func buildFacetItems(u *url.URL, key string, facets []session.FacetCount, active string) []facetItem {
	items := make([]facetItem, 0, len(facets))
	for _, fc := range facets {
		items = append(items, facetItem{
			Value:  fc.Value,
			Count:  fc.Count,
			URL:    buildFacetURL(u, key, fc.Value),
			Active: active == fc.Value,
		})
	}
	return items
}

// capFacetItems trims a facet list to limit entries. An active facet outside
// the top entries is re-appended so the current filter never disappears from
// the page.
func capFacetItems(items []facetItem, limit int) []facetItem {
	if limit <= 0 || len(items) <= limit {
		return items
	}
	trimmed := items[:limit]
	for _, it := range trimmed {
		if it.Active {
			return trimmed
		}
	}
	for _, it := range items[limit:] {
		if it.Active {
			return append(trimmed, it)
		}
	}
	return trimmed
}

// uncategorizedLabel is the display name of the uncategorized facet bucket;
// filter URLs carry the UncategorizedFilterValue sentinel instead.
const uncategorizedLabel = "未分类"

// facetTagPass reports whether a document carries the active tag filter.
func facetTagPass(tags []string, filterTag string) bool {
	if filterTag == "" {
		return true
	}
	for _, t := range tags {
		if t == filterTag {
			return true
		}
	}
	return false
}

// facetCatPass reports whether a document matches the active category filter,
// including the uncategorized sentinel.
func facetCatPass(category, filterCat string) bool {
	switch {
	case filterCat == "":
		return true
	case filterCat == session.UncategorizedFilterValue:
		return category == ""
	default:
		return category == filterCat
	}
}

// filterDocsByCatTag applies the tag/category facet constraints in memory,
// carving the strict result set out of the search base set.
func filterDocsByCatTag(docs []session.DocumentInfo, filterTag, filterCat string) []session.DocumentInfo {
	out := make([]session.DocumentInfo, 0, len(docs))
	for _, d := range docs {
		if facetTagPass(d.Tags, filterTag) && facetCatPass(d.Category, filterCat) {
			out = append(out, d)
		}
	}
	return out
}

// facetCountsFromDocs derives facet counts from the search base set. Each
// dimension is counted over documents that pass the *other* filters only, so
// the facet list lifts its own filter exactly like the store-level queries.
func facetCountsFromDocs(docs []session.DocumentInfo, filterTag, filterCat string) (cats []session.FacetCount, uncategorized int, tags []session.FacetCount) {
	catMap := make(map[string]int)
	tagMap := make(map[string]int)
	for _, d := range docs {
		tagPass := facetTagPass(d.Tags, filterTag)
		catPass := facetCatPass(d.Category, filterCat)
		if tagPass {
			if d.Category == "" {
				uncategorized++
			} else {
				catMap[d.Category]++
			}
		}
		if catPass {
			for _, t := range d.Tags {
				tagMap[t]++
			}
		}
	}
	return sortedFacets(catMap), uncategorized, sortedFacets(tagMap)
}

// recentPerCategoryFromDocs derives the overview cards' recent documents from
// the search base set (tag-narrowed, category-lifted), mirroring
// Store.RecentPerCategory so card previews and counts see the same documents.
func recentPerCategoryFromDocs(docs []session.DocumentInfo, filterTag string, limit int) map[string][]session.DocumentInfo {
	out := make(map[string][]session.DocumentInfo)
	for _, d := range docs {
		if d.Category == "" || !facetTagPass(d.Tags, filterTag) {
			continue
		}
		if len(out[d.Category]) < limit {
			out[d.Category] = append(out[d.Category], d)
		}
	}
	return out
}

// recentUncategorizedFromDocs mirrors recentPerCategoryFromDocs for the
// uncategorized card.
func recentUncategorizedFromDocs(docs []session.DocumentInfo, filterTag string, limit int) []session.DocumentInfo {
	var out []session.DocumentInfo
	for _, d := range docs {
		if d.Category == "" && facetTagPass(d.Tags, filterTag) {
			out = append(out, d)
			if len(out) >= limit {
				break
			}
		}
	}
	return out
}

// sortedFacets orders facet counts by count descending then value ascending,
// matching the store-level facet queries.
func sortedFacets(m map[string]int) []session.FacetCount {
	out := make([]session.FacetCount, 0, len(m))
	for v, c := range m {
		out = append(out, session.FacetCount{Value: v, Count: c})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Value < out[j].Value
	})
	return out
}

func (s *Server) handleCreateSession(w http.ResponseWriter, r *http.Request) {
	contentType := r.Header.Get("Content-Type")
	if strings.HasPrefix(contentType, "multipart/form-data") {
		s.handleCreateUploadedSession(w, r)
		return
	}

	s.handleCreatePathSession(w, r)
}

func (s *Server) handleCreatePathSession(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()

	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "Failed to read request body.")
		return
	}

	var req createSessionRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "Request body must be valid JSON.")
		return
	}

	if req.FilePath == "" {
		writeJSONError(w, http.StatusBadRequest, "filePath is required.")
		return
	}
	if len(req.Tags) == 0 {
		writeJSONError(w, http.StatusBadRequest, "tags is required.")
		return
	}
	if req.Category == "" {
		writeJSONError(w, http.StatusBadRequest, "category is required.")
		return
	}
	if req.Project == "" {
		writeJSONError(w, http.StatusBadRequest, "project is required.")
		return
	}

	// Validate before creating the session row, so a rejected title cannot
	// orphan a session on the server's local disk.
	trimmedTitle, err := session.NormalizeTitle(req.Title)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}

	entryFile, err := filepath.Abs(req.FilePath)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "Failed to resolve filePath.")
		return
	}

	if !IsHTMLFile(entryFile) {
		writeJSONError(w, http.StatusBadRequest, "Only .html and .htm files are supported.")
		return
	}

	if err := ensureFile(entryFile); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			writeJSONError(w, http.StatusNotFound, "HTML file does not exist.")
			return
		}

		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}

	session, err := s.store.Create(entryFile)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	if err := s.assignOwnerIfNeeded(r, session.ID); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "Failed to assign session owner.")
		return
	}

	if err := s.setSessionMetadata(session.ID, req.Tags, req.Category, req.Project, trimmedTitle); err != nil {
		if writeTitleValidationError(w, err) {
			return
		}
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	base := s.serverBaseURL(r)
	response := createSessionResponse{
		SessionID: session.ID,
		URL:       base + "/s/" + session.ID + "/",
		EntryFile: session.EntryFile,
		RootDir:   session.RootDir,
	}

	s.recordAudit(r, auditSessionCreate, response.SessionID, auditOK,
		"entry="+response.EntryFile, "")
	writeJSON(w, http.StatusCreated, response)
}

func (s *Server) handleCreateUploadedSession(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadBytes)
	if err := r.ParseMultipartForm(maxUploadBytes); err != nil {
		writeJSONError(w, http.StatusBadRequest, "Failed to parse multipart form upload.")
		return
	}
	defer func() {
		if r.MultipartForm != nil {
			_ = r.MultipartForm.RemoveAll()
		}
	}()

	entryFile := strings.TrimSpace(r.FormValue("entryFile"))
	if entryFile != "" {
		entryFile = filepath.Base(filepath.Clean(filepath.FromSlash(entryFile)))
		if entryFile == "." || entryFile == "" || entryFile == ".." {
			entryFile = ""
		}
	}
	entryPath, err := normalizeArchivePath(r.FormValue("entryPath"))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "entryPath must be a relative path inside the archive.")
		return
	}
	if !IsHTMLFile(entryPath) {
		writeJSONError(w, http.StatusBadRequest, "Only .html and .htm files are supported.")
		return
	}
	if entryFile == "" {
		entryFile = entryPath
	}

	archiveFile, _, err := r.FormFile("archive")
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "archive file is required.")
		return
	}
	defer archiveFile.Close()

	tags := r.Form["tags"]
	// Keep the raw form value: NormalizeTitle distinguishes an absent title
	// (accepted, for legacy clients) from a whitespace-only one (rejected 400).
	title := r.FormValue("title")
	category := strings.TrimSpace(r.FormValue("category"))
	project := strings.TrimSpace(r.FormValue("project"))

	if len(tags) == 0 {
		writeJSONError(w, http.StatusBadRequest, "tags is required.")
		return
	}
	if category == "" {
		writeJSONError(w, http.StatusBadRequest, "category is required.")
		return
	}
	if project == "" {
		writeJSONError(w, http.StatusBadRequest, "project is required.")
		return
	}

	// Validate before creating the session row or unpacking the archive, so a
	// rejected title cannot orphan a session or leave uploaded files behind.
	trimmedTitle, err := session.NormalizeTitle(title)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}

	uploadRoot, err := s.resolveUploadRoot()
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := os.MkdirAll(uploadRoot, 0o700); err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	sessionDir, err := os.MkdirTemp(uploadRoot, "session-*")
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	cleanupSessionDir := true
	defer func() {
		if cleanupSessionDir {
			_ = os.RemoveAll(sessionDir)
		}
	}()

	if err := extractZIPArchive(archiveFile, sessionDir); err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}

	storedEntryFile := filepath.Join(sessionDir, filepath.FromSlash(entryPath))
	if !IsSubpath(sessionDir, storedEntryFile) {
		writeJSONError(w, http.StatusBadRequest, "entryPath escapes the uploaded archive root.")
		return
	}
	if err := ensureFile(storedEntryFile); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			writeJSONError(w, http.StatusBadRequest, "entryPath does not exist in the uploaded archive.")
			return
		}
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}

	sess, err := s.store.CreateUploaded(entryFile, storedEntryFile)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	cleanupSessionDir = false

	if err := s.assignOwnerIfNeeded(r, sess.ID); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "Failed to assign session owner.")
		return
	}

	if err := s.setSessionMetadata(sess.ID, tags, category, project, trimmedTitle); err != nil {
		if writeTitleValidationError(w, err) {
			return
		}
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Link this upload to its version chain (same project + entry-file
	// basename, optionally owner-scoped). Chain membership is best-effort:
	// a failure here degrades to an unlinked session, never blocks the upload.
	ownerID, _ := currentUser(r)
	chainID, versionNo, linkErr := s.store.LinkToChain(sess.ID, project, sess.EntryFile, ownerID)
	if linkErr != nil && !errors.Is(linkErr, session.ErrSessionNotFound) {
		fmt.Fprintf(os.Stderr, "chain link failed for session %s: %v\n", sess.ID, linkErr)
	}

	base := s.serverBaseURL(r)
	response := createSessionResponse{
		SessionID: sess.ID,
		URL:       base + "/s/" + sess.ID + "/",
		EntryFile: sess.EntryFile,
		RootDir:   sess.RootDir,
		ChainID:   chainID,
		VersionNo: versionNo,
	}

	s.recordAudit(r, auditSessionCreate, response.SessionID, auditOK,
		"entry="+response.EntryFile, "")
	writeJSON(w, http.StatusCreated, response)
}

func (s *Server) handlePreview(w http.ResponseWriter, r *http.Request) {
	sessionID, assetPath, redirectPath, err := parsePreviewPath(r)
	if err != nil {
		if errors.Is(err, errRedirectRequired) {
			http.Redirect(w, r, redirectPath, http.StatusFound)
			return
		}

		http.NotFound(w, r)
		return
	}

	session, found, err := s.store.Get(sessionID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if !found {
		http.Error(w, "Session not found.", http.StatusNotFound)
		return
	}

	if !s.authorizePreview(w, r, session, assetPath, false) {
		return
	}

	if assetPath == "" {
		http.ServeFile(w, r, session.StoredEntryFile)
		return
	}

	targetPath := filepath.Clean(filepath.Join(session.StoredRootDir, filepath.FromSlash(assetPath)))
	if !IsSubpath(session.StoredRootDir, targetPath) {
		http.Error(w, "Path escapes the session root.", http.StatusForbidden)
		return
	}

	if err := ensureFile(targetPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			http.Error(w, "Resource not found.", http.StatusNotFound)
			return
		}

		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	http.ServeFile(w, r, targetPath)
}

func (s *Server) handleDownloadSession(w http.ResponseWriter, r *http.Request) {
	sessionID, ok := extractSessionIDFromMetaPath(r.URL.Path, "/api/sessions/", "/download")
	if !ok {
		writeJSONError(w, http.StatusBadRequest, "Invalid session ID.")
		return
	}

	session, found, err := s.store.Get(sessionID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if !found {
		http.Error(w, "Session not found.", http.StatusNotFound)
		return
	}

	if !s.requireOwner(w, r, sessionID) {
		return
	}

	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s.zip\"", sessionID))

	archive := zip.NewWriter(w)
	defer archive.Close()

	err = filepath.WalkDir(session.StoredRootDir, func(filePath string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}

		info, err := d.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}

		targetPath := filepath.Clean(filePath)
		if !IsSubpath(session.StoredRootDir, targetPath) {
			return fmt.Errorf("path escapes the session root")
		}

		relativePath, err := filepath.Rel(session.StoredRootDir, targetPath)
		if err != nil {
			return err
		}

		fileWriter, err := archive.Create(filepath.ToSlash(relativePath))
		if err != nil {
			return err
		}

		sourceFile, err := os.Open(targetPath)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(fileWriter, sourceFile)
		closeErr := sourceFile.Close()
		if copyErr != nil {
			return copyErr
		}
		return closeErr
	})

	if err != nil {
		// Headers already sent; log and let the truncated response stand.
		fmt.Fprintf(os.Stderr, "download archive error: %v\n", err)
	}
}

var errRedirectRequired = errors.New("redirect required")

func parsePreviewPath(r *http.Request) (string, string, string, error) {
	trimmed := strings.TrimPrefix(r.URL.Path, "/s/")
	if trimmed == r.URL.Path || trimmed == "" {
		return "", "", "", errors.New("invalid preview path")
	}

	parts := strings.SplitN(trimmed, "/", 2)
	sessionID := parts[0]
	if sessionID == "" {
		return "", "", "", errors.New("missing session id")
	}

	if len(parts) == 1 {
		return sessionID, "", "/s/" + sessionID + "/", errRedirectRequired
	}

	escaped := strings.TrimPrefix(r.URL.EscapedPath(), "/s/"+sessionID+"/")
	if escaped == r.URL.EscapedPath() {
		return "", "", "", errors.New("invalid escaped preview path")
	}

	decoded, err := url.PathUnescape(escaped)
	if err != nil {
		return "", "", "", err
	}

	return sessionID, decoded, "", nil
}

func IsHTMLFile(filePath string) bool {
	extension := strings.ToLower(filepath.Ext(filePath))
	return extension == ".html" || extension == ".htm"
}

const maxFileContentSearchSize = 10 << 20 // 10MB

func fileContentContains(filePath, queryLower string) bool {
	f, err := os.Open(filePath)
	if err != nil {
		return false
	}
	defer f.Close()

	data, err := io.ReadAll(io.LimitReader(f, maxFileContentSearchSize))
	if err != nil {
		return false
	}
	return bytes.Contains(bytes.ToLower(data), []byte(queryLower))
}

func IsSubpath(rootDir string, targetPath string) bool {
	relativePath, err := filepath.Rel(rootDir, targetPath)
	if err != nil {
		return false
	}

	return relativePath == "." || (relativePath != ".." && !strings.HasPrefix(relativePath, ".."+string(filepath.Separator)))
}

func ensureFile(filePath string) error {
	info, err := os.Stat(filePath)
	if err != nil {
		return err
	}

	if !info.Mode().IsRegular() {
		return fmt.Errorf("target path is not a file")
	}

	return nil
}

func defaultUploadRoot() (string, error) {
	stateDir, err := session.DefaultStateDir()
	if err != nil {
		return "", err
	}

	return filepath.Join(stateDir, "sth", "uploads"), nil
}

// resolveUploadRoot returns the directory used to store uploaded session
// snapshots. It uses s.uploadRoot when set (via --upload-root), otherwise
// falls back to the platform default. Container deployments must set
// --upload-root to a path on a persistent volume so uploads survive
// container restarts alongside the DB.
func (s *Server) resolveUploadRoot() (string, error) {
	if s.uploadRoot != "" {
		return s.uploadRoot, nil
	}
	return defaultUploadRoot()
}

func normalizeArchivePath(value string) (string, error) {
	cleaned := path.Clean(strings.TrimSpace(value))
	if cleaned == "." || cleaned == "" {
		return "", errors.New("empty archive path")
	}
	if strings.HasPrefix(cleaned, "/") || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", errors.New("archive path escapes root")
	}

	return cleaned, nil
}

func extractZIPArchive(file multipartFile, destinationDir string) error {
	size, err := fileSize(file)
	if err != nil {
		return fmt.Errorf("failed to inspect archive: %w", err)
	}

	reader, err := zip.NewReader(file, size)
	if err != nil {
		return errors.New("archive must be a valid zip file")
	}

	if len(reader.File) > maxArchiveFiles {
		return errors.New("uploaded archive contains too many files")
	}

	var totalUncompressed int64
	for _, archivedFile := range reader.File {
		archivePath, err := normalizeArchivePath(archivedFile.Name)
		if err != nil {
			return errors.New("archive contains an invalid file path")
		}

		targetPath := filepath.Join(destinationDir, filepath.FromSlash(archivePath))
		if !IsSubpath(destinationDir, targetPath) {
			return errors.New("archive contains a path that escapes the session root")
		}

		if archivedFile.FileInfo().IsDir() {
			if err := os.MkdirAll(targetPath, 0o755); err != nil {
				return err
			}
			continue
		}

		mode := archivedFile.Mode()
		if mode&os.ModeSymlink != 0 || !mode.IsRegular() {
			return errors.New("archive may only contain regular files")
		}

		if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
			return err
		}

		sourceFile, err := archivedFile.Open()
		if err != nil {
			return err
		}

		targetFile, err := os.OpenFile(targetPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
		if err != nil {
			sourceFile.Close()
			return err
		}

		written, copyErr := copyToLimit(sourceFile, targetFile, maxUploadBytes-totalUncompressed)
		totalUncompressed += written
		closeErr := errors.Join(sourceFile.Close(), targetFile.Close())
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
	}

	return nil
}

func copyToLimit(source io.Reader, target io.Writer, limit int64) (int64, error) {
	if limit < 0 {
		return 0, errors.New("uploaded archive is too large after extraction")
	}

	buffer := make([]byte, 32*1024)
	var copied int64

	for {
		readLimit := len(buffer)
		nRead, err := source.Read(buffer[:readLimit])

		if nRead > 0 {
			if copied+int64(nRead) > limit {
				excess := copied + int64(nRead) - limit
				nReadWritable := nRead - int(excess)
				if nReadWritable > 0 {
					if _, writeErr := target.Write(buffer[:nReadWritable]); writeErr != nil {
						return copied, writeErr
					}
					copied += int64(nReadWritable)
				}
				return copied, errors.New("uploaded archive is too large after extraction")
			}

			copied += int64(nRead)
			if _, writeErr := target.Write(buffer[:nRead]); writeErr != nil {
				return copied, writeErr
			}
		}

		if err != nil {
			if errors.Is(err, io.EOF) {
				return copied, nil
			}
			return copied, err
		}
	}
}

type multipartFile interface {
	io.Reader
	io.ReaderAt
	io.Seeker
	io.Closer
}

func fileSize(file io.Seeker) (int64, error) {
	current, err := file.Seek(0, io.SeekCurrent)
	if err != nil {
		return 0, err
	}
	end, err := file.Seek(0, io.SeekEnd)
	if err != nil {
		return 0, err
	}
	if _, err := file.Seek(current, io.SeekStart); err != nil {
		return 0, err
	}

	return end, nil
}

func writeJSONError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

// determineScheme returns the URL scheme for the request.
// It trusts the X-Forwarded-Proto header, so the server must
// run behind a trusted reverse proxy when relying on this logic.
func determineScheme(r *http.Request) string {
	if r.Header.Get("X-Forwarded-Proto") == "https" || r.TLS != nil {
		return "https"
	}
	return "http"
}

func baseURL(r *http.Request) string {
	return determineScheme(r) + "://" + r.Host
}

func (s *Server) serverBaseURL(r *http.Request) string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.serverName != "" {
		port := resolveExternalPort(r, s.serverPort, s.port, s.listener)
		scheme := determineScheme(r)
		if (scheme == "http" && port == 80) || (scheme == "https" && port == 443) {
			return fmt.Sprintf("%s://%s", scheme, s.serverName)
		}
		return fmt.Sprintf("%s://%s:%d", scheme, s.serverName, port)
	}

	return baseURL(r)
}

// resolveExternalPort picks the port to use in generated URLs.
// Priority: --server-port flag > X-Forwarded-Port header > listener port.
// An invalid or out-of-range header is silently ignored (falls back).
func resolveExternalPort(r *http.Request, serverPort, listenerPort int, listener net.Listener) int {
	if serverPort > 0 {
		return serverPort
	}
	if h := r.Header.Get("X-Forwarded-Port"); h != "" {
		if p, err := strconv.Atoi(h); err == nil && p > 0 && p <= 65535 {
			return p
		}
	}
	port := listenerPort
	if listener != nil {
		if addr, ok := listener.Addr().(*net.TCPAddr); ok {
			port = addr.Port
		}
	}
	return port
}

func hasPrefixSuffix(path, prefix, suffix string) bool {
	return strings.HasPrefix(path, prefix) && strings.HasSuffix(path, suffix)
}

func isExactSessionPath(urlPath string) bool {
	const prefix = "/api/sessions/"
	if !strings.HasPrefix(urlPath, prefix) {
		return false
	}
	trimmed := strings.TrimPrefix(urlPath, prefix)
	return trimmed != "" && !strings.Contains(trimmed, "/")
}

func extractSessionIDFromMetaPath(urlPath, prefix, suffix string) (string, bool) {
	trimmed := strings.TrimPrefix(urlPath, prefix)
	trimmed = strings.TrimSuffix(trimmed, suffix)
	if trimmed == "" || strings.Contains(trimmed, "/") {
		return "", false
	}
	return trimmed, true
}

type tagsRequest struct {
	Tags []string `json:"tags"`
}

type categoryRequest struct {
	Category string `json:"category"`
}

type projectRequest struct {
	Project string `json:"project"`
}

type titleRequest struct {
	Title string `json:"title"`
}

func (s *Server) requireSession(sessionID string) (session.Session, bool, error) {
	return s.store.Get(sessionID)
}

func (s *Server) setSessionMetadata(sessionID string, tags []string, category, project, title string) error {
	// Validate the title before any metadata is written, so a rejected title
	// can never leave half-written tags/category/project behind. The title is
	// optional at the API level so older CLI clients that do not send one
	// keep working; the CLI itself enforces --title for new sessions.
	trimmedTitle, err := session.NormalizeTitle(title)
	if err != nil {
		return err
	}

	if err := s.store.AddTags(sessionID, tags...); err != nil {
		return err
	}
	if err := s.store.SetCategory(sessionID, category); err != nil {
		return err
	}
	if err := s.store.SetProject(sessionID, project); err != nil {
		return err
	}
	if trimmedTitle != "" {
		if err := s.store.SetTitle(sessionID, trimmedTitle); err != nil {
			return err
		}
	}
	return nil
}

// writeTitleValidationError maps title-validation errors from
// setSessionMetadata to a 400 response and reports whether it handled the
// error. Anything else is left to the caller's generic handling.
func writeTitleValidationError(w http.ResponseWriter, err error) bool {
	if errors.Is(err, session.ErrTitleEmpty) || errors.Is(err, session.ErrTitleTooLong) {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return true
	}
	return false
}

func (s *Server) writeMetaOrError(w http.ResponseWriter, sessionID string, storeErr error) {
	if storeErr != nil {
		if errors.Is(storeErr, session.ErrSessionNotFound) {
			writeJSONError(w, http.StatusNotFound, "Session not found.")
			return
		}
		writeJSONError(w, http.StatusInternalServerError, storeErr.Error())
		return
	}
	meta, err := s.store.GetMetadata(sessionID)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, meta)
}

func (s *Server) handleAddTags(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	sessionID, ok := extractSessionIDFromMetaPath(r.URL.Path, "/api/sessions/", "/tags")
	if !ok {
		writeJSONError(w, http.StatusBadRequest, "Invalid session ID.")
		return
	}

	if !s.requireOwner(w, r, sessionID) {
		return
	}

	var req tagsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "Request body must be valid JSON.")
		return
	}
	if len(req.Tags) == 0 {
		writeJSONError(w, http.StatusBadRequest, "At least one tag is required.")
		return
	}

	err := s.store.AddTags(sessionID, req.Tags...)
	s.writeMetaOrError(w, sessionID, err)
}

func (s *Server) handleRemoveTags(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	sessionID, ok := extractSessionIDFromMetaPath(r.URL.Path, "/api/sessions/", "/tags")
	if !ok {
		writeJSONError(w, http.StatusBadRequest, "Invalid session ID.")
		return
	}

	if !s.requireOwner(w, r, sessionID) {
		return
	}

	var req tagsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "Request body must be valid JSON.")
		return
	}
	if len(req.Tags) == 0 {
		writeJSONError(w, http.StatusBadRequest, "At least one tag is required.")
		return
	}

	err := s.store.RemoveTags(sessionID, req.Tags...)
	s.writeMetaOrError(w, sessionID, err)
}

func (s *Server) handleSetCategory(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	sessionID, ok := extractSessionIDFromMetaPath(r.URL.Path, "/api/sessions/", "/category")
	if !ok {
		writeJSONError(w, http.StatusBadRequest, "Invalid session ID.")
		return
	}

	if !s.requireOwner(w, r, sessionID) {
		return
	}

	var req categoryRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "Request body must be valid JSON.")
		return
	}
	if req.Category == "" {
		writeJSONError(w, http.StatusBadRequest, "category is required and cannot be cleared.")
		return
	}

	err := s.store.SetCategory(sessionID, req.Category)
	s.writeMetaOrError(w, sessionID, err)
}

func (s *Server) handleClearCategory(w http.ResponseWriter, r *http.Request) {
	writeJSONError(w, http.StatusBadRequest, "category is required and cannot be cleared.")
}

func (s *Server) handleSetProject(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	sessionID, ok := extractSessionIDFromMetaPath(r.URL.Path, "/api/sessions/", "/project")
	if !ok {
		writeJSONError(w, http.StatusBadRequest, "Invalid session ID.")
		return
	}

	if !s.requireOwner(w, r, sessionID) {
		return
	}

	var req projectRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "Request body must be valid JSON.")
		return
	}
	if req.Project == "" {
		writeJSONError(w, http.StatusBadRequest, "project is required and cannot be cleared.")
		return
	}

	err := s.store.SetProject(sessionID, req.Project)
	s.writeMetaOrError(w, sessionID, err)
}

func (s *Server) handleClearProject(w http.ResponseWriter, r *http.Request) {
	writeJSONError(w, http.StatusBadRequest, "project is required and cannot be cleared.")
}

func (s *Server) handleSetTitle(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	sessionID, ok := extractSessionIDFromMetaPath(r.URL.Path, "/api/sessions/", "/title")
	if !ok {
		writeJSONError(w, http.StatusBadRequest, "Invalid session ID.")
		return
	}

	if !s.requireOwner(w, r, sessionID) {
		return
	}

	var req titleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "Request body must be valid JSON.")
		return
	}
	trimmedTitle, err := session.NormalizeTitle(req.Title)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	if trimmedTitle == "" {
		writeJSONError(w, http.StatusBadRequest, "title is required and cannot be cleared.")
		return
	}

	err = s.store.SetTitle(sessionID, trimmedTitle)
	s.writeMetaOrError(w, sessionID, err)
}

func (s *Server) handleGetMetadata(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	sessionID, ok := extractSessionIDFromMetaPath(r.URL.Path, "/api/sessions/", "/metadata")
	if !ok {
		writeJSONError(w, http.StatusBadRequest, "Invalid session ID.")
		return
	}

	_, found, err := s.requireSession(sessionID)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !found {
		writeJSONError(w, http.StatusNotFound, "Session not found.")
		return
	}

	if !s.requireViewer(w, r, sessionID) {
		return
	}

	meta, err := s.store.GetMetadata(sessionID)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, meta)
}

func (s *Server) handleGetPeers(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	sessionID, ok := extractSessionIDFromMetaPath(r.URL.Path, "/api/sessions/", "/peers")
	if !ok {
		writeJSONError(w, http.StatusBadRequest, "Invalid session ID.")
		return
	}

	_, found, err := s.requireSession(sessionID)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !found {
		writeJSONError(w, http.StatusNotFound, "Session not found.")
		return
	}

	if !s.requireViewer(w, r, sessionID) {
		return
	}

	ownerID, _ := currentUser(r)
	peers, err := s.store.GetPeersForOwner(sessionID, session.DefaultPeerLimit, ownerID)
	if err != nil {
		if errors.Is(err, session.ErrSessionNotFound) {
			writeJSONError(w, http.StatusNotFound, "Session not found.")
			return
		}
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, peers)
}

// chainResponse is the payload returned by GET /api/sessions/{id}/chain. The
// current session's version is marked in versions[] via ChainVersion.Current.
// metadataDiff describes per-step tag/category/project transitions across the
// whole chain (including v1 vs. an empty baseline), so the timeline UI can
// render "+tag" / "category: a→b" between adjacent versions.
type chainResponse struct {
	Chain        session.ChainInfo             `json:"chain"`
	Current      session.ChainVersion          `json:"current"`
	Versions     []session.ChainVersion        `json:"versions"`
	MetadataDiff []session.VersionMetadataDiff `json:"metadataDiff"`
}

func (s *Server) handleGetChain(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	sessionID, ok := extractSessionIDFromMetaPath(r.URL.Path, "/api/sessions/", "/chain")
	if !ok {
		writeJSONError(w, http.StatusBadRequest, "Invalid session ID.")
		return
	}

	_, found, err := s.requireSession(sessionID)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !found {
		writeJSONError(w, http.StatusNotFound, "Session not found.")
		return
	}

	if !s.requireViewer(w, r, sessionID) {
		return
	}

	info, versions, err := s.store.GetChainOfSession(sessionID)
	if err != nil {
		if errors.Is(err, session.ErrSessionNotFound) {
			writeJSONError(w, http.StatusNotFound, "Session not found.")
			return
		}
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	var current session.ChainVersion
	for _, v := range versions {
		if v.Current {
			current = v
			break
		}
	}
	// GetChainOfSession returns ErrSessionNotFound for missing or soft-deleted
	// sessions, so reaching here with no current version means the chain row
	// exists but holds no live versions (e.g. every member was soft-deleted
	// via a path that bypassed the chain lookup). Treat as not-found rather
	// than fabricating a "current" version from stale data.
	if current.SessionID == "" {
		writeJSONError(w, http.StatusNotFound, "Session not found.")
		return
	}

	metadataDiff := []session.VersionMetadataDiff{}
	if info.ChainID != "" {
		diffs, err := s.store.DiffChainMetadata(info.ChainID)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		metadataDiff = diffs
	}

	writeJSON(w, http.StatusOK, chainResponse{
		Chain:        info,
		Current:      current,
		Versions:     versions,
		MetadataDiff: metadataDiff,
	})
}

// diffLine is one line of an HTML content diff, with a stable kind token for
// JSON consumers ("equal" / "add" / "delete") alongside the 1-based line
// numbers in each document (0 when N/A).
type diffLine struct {
	Kind  string `json:"kind"`
	Text  string `json:"text"`
	OldNo int    `json:"oldNo"`
	NewNo int    `json:"newNo"`
}

// diffResponse is the payload of GET /api/sessions/{id}/diff?from=vN&to=vM.
// Lines is the full line-level diff in reading order; Summary gives additive
// counts so the UI can render a "+a −b" hint without scanning the slice.
// TooLarge is true when either input exceeded MaxDiffLines and the diff was
// skipped: Lines then carries a single explanatory sentinel line and Summary
// is zeroed. The UI branches on TooLarge rather than sniffing the sentinel
// text, so the contract is explicit and locale-independent.
type diffResponse struct {
	FromVersion int         `json:"fromVersion"`
	ToVersion   int         `json:"toVersion"`
	Lines       []diffLine  `json:"lines"`
	Summary     diffSummary `json:"summary"`
	TooLarge    bool        `json:"tooLarge"`
}

type diffSummary struct {
	Added   int `json:"added"`
	Removed int `json:"removed"`
}

// handleGetDiff computes a line-level HTML content diff between two versions
// of the same chain. The {id} in the path identifies any session in the chain
// (used to resolve chain membership + ownership); from / to are 1-based
// version numbers of the same chain. Cross-chain or out-of-range version
// numbers yield 400; a missing or soft-deleted session yields 404.
//
// Access matches the preview drawer's other read endpoints (owner ∪ sharee),
// except that a page-scoped grant is not enough: the payload is the full text
// of two documents, which a single-page grant does not cover.
func (s *Server) handleGetDiff(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	sessionID, ok := extractSessionIDFromMetaPath(r.URL.Path, "/api/sessions/", "/diff")
	if !ok {
		writeJSONError(w, http.StatusBadRequest, "Invalid session ID.")
		return
	}

	_, found, err := s.requireSession(sessionID)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !found {
		writeJSONError(w, http.StatusNotFound, "Session not found.")
		return
	}
	if !s.requireViewerSessionScope(w, r, sessionID) {
		return
	}

	fromVer, toVer, bad := parseDiffVersions(r)
	if bad != "" {
		writeJSONError(w, http.StatusBadRequest, bad)
		return
	}

	// Resolve the chain via the requested session; this also enforces the
	// soft-delete visibility rule (404 on deleted sessions).
	_, versions, err := s.store.GetChainOfSession(sessionID)
	if err != nil {
		if errors.Is(err, session.ErrSessionNotFound) {
			writeJSONError(w, http.StatusNotFound, "Session not found.")
			return
		}
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	fromSID, ok := versionSessionID(versions, fromVer)
	if !ok {
		writeJSONError(w, http.StatusBadRequest, "from version not found in this chain.")
		return
	}
	toSID, ok := versionSessionID(versions, toVer)
	if !ok {
		writeJSONError(w, http.StatusBadRequest, "to version not found in this chain.")
		return
	}
	if fromSID == toSID {
		writeJSONError(w, http.StatusBadRequest, "from and to versions must differ.")
		return
	}

	result, err := s.store.DiffSessionHTML(fromSID, toSID)
	if err != nil {
		if errors.Is(err, session.ErrSessionNotFound) {
			writeJSONError(w, http.StatusNotFound, "Session not found.")
			return
		}
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	lines := make([]diffLine, 0, len(result.Ops))
	for _, op := range result.Ops {
		lines = append(lines, diffLine{
			Kind:  op.KindString(),
			Text:  op.Text,
			OldNo: op.OldNo,
			NewNo: op.NewNo,
		})
	}
	summary := session.Summarize(result.Ops)

	writeJSON(w, http.StatusOK, diffResponse{
		FromVersion: fromVer,
		ToVersion:   toVer,
		Lines:       lines,
		Summary: diffSummary{
			Added:   summary.Added,
			Removed: summary.Removed,
		},
		// TooLarge is propagated verbatim from the diff layer's explicit
		// signal rather than inferred from op text, so a legitimate document
		// whose content equals DiffTooLargeText is not misclassified.
		TooLarge: result.TooLarge,
	})
}

// parseDiffVersions reads the from / to query params as positive integers.
// Returns a human-readable error string (empty when OK).
func parseDiffVersions(r *http.Request) (from, to int, errMsg string) {
	q := r.URL.Query()
	fromStr := strings.TrimSpace(q.Get("from"))
	toStr := strings.TrimSpace(q.Get("to"))
	if fromStr == "" || toStr == "" {
		return 0, 0, "Both 'from' and 'to' version numbers are required."
	}
	f, err := strconv.Atoi(fromStr)
	if err != nil || f < 1 {
		return 0, 0, "'from' must be a positive integer version number."
	}
	t, err := strconv.Atoi(toStr)
	if err != nil || t < 1 {
		return 0, 0, "'to' must be a positive integer version number."
	}
	return f, t, ""
}

// versionSessionID finds the session id for a given version number within a
// chain's version list. Returns ok=false when no live version matches.
func versionSessionID(versions []session.ChainVersion, versionNo int) (string, bool) {
	for _, v := range versions {
		if v.VersionNo == versionNo {
			return v.SessionID, true
		}
	}
	return "", false
}

func (s *Server) handleDeleteSession(w http.ResponseWriter, r *http.Request) {
	sessionID := strings.TrimPrefix(r.URL.Path, "/api/sessions/")

	if !s.requireOwner(w, r, sessionID) {
		return
	}

	err := s.store.SoftDelete(sessionID)
	if err != nil {
		if errors.Is(err, session.ErrSessionNotFound) {
			writeJSONError(w, http.StatusNotFound, "Session not found.")
			return
		}
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	s.recordAudit(r, auditSessionDelete, sessionID, auditOK, "", "")
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

const maxIncrementalBytes = 50 << 20

func (s *Server) handleUpdateFiles(w http.ResponseWriter, r *http.Request) {
	sessionID, ok := extractSessionIDFromMetaPath(r.URL.Path, "/api/sessions/", "/files")
	if !ok {
		writeJSONError(w, http.StatusBadRequest, "Invalid session ID.")
		return
	}

	if !s.requireOwner(w, r, sessionID) {
		return
	}

	sess, found, err := s.store.Get(sessionID)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !found {
		writeJSONError(w, http.StatusNotFound, "Session not found.")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxIncrementalBytes)
	if err := r.ParseMultipartForm(maxIncrementalBytes); err != nil {
		writeJSONError(w, http.StatusBadRequest, "Failed to parse multipart form upload.")
		return
	}
	defer func() {
		if r.MultipartForm != nil {
			_ = r.MultipartForm.RemoveAll()
		}
	}()

	count := 0
	for _, headers := range r.MultipartForm.File {
		for _, hdr := range headers {
			if hdr.Size > maxIncrementalBytes {
				writeJSONError(w, http.StatusRequestEntityTooLarge,
					fmt.Sprintf("File %q exceeds 50MB limit.", hdr.Filename))
				return
			}

			src, err := hdr.Open()
			if err != nil {
				writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("Failed to read file %q.", hdr.Filename))
				return
			}

			relPath := filepath.Clean(filepath.FromSlash(hdr.Filename))
			if relPath == "." || relPath == ".." || strings.HasPrefix(relPath, ".."+string(filepath.Separator)) {
				src.Close()
				continue
			}
			if hdr.Filename == "" || strings.HasSuffix(relPath, string(filepath.Separator)) {
				src.Close()
				continue
			}

			targetPath := filepath.Join(sess.StoredRootDir, relPath)
			if !IsSubpath(sess.StoredRootDir, targetPath) {
				src.Close()
				writeJSONError(w, http.StatusBadRequest, "File path escapes session root.")
				return
			}

			if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
				src.Close()
				writeJSONError(w, http.StatusInternalServerError, err.Error())
				return
			}

			dst, err := os.OpenFile(targetPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
			if err != nil {
				src.Close()
				writeJSONError(w, http.StatusInternalServerError, err.Error())
				return
			}

			_, copyErr := io.Copy(dst, src)
			closeErr := errors.Join(src.Close(), dst.Close())
			if copyErr != nil {
				writeJSONError(w, http.StatusInternalServerError, copyErr.Error())
				return
			}
			if closeErr != nil {
				writeJSONError(w, http.StatusInternalServerError, closeErr.Error())
				return
			}
			count++
		}
	}

	s.liveMgr.BroadcastTo(sessionID, live.ReloadJSON())

	writeJSON(w, http.StatusOK, map[string]any{
		"status":        "ok",
		"files_updated": count,
	})
}
