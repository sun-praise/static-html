package cli

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sun-praise/static-html/internal/server"
	"github.com/sun-praise/static-html/internal/session"
)

// zipNames lists the entry names of an in-memory zip archive.
func zipNames(data []byte) ([]string, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(zr.File))
	for _, f := range zr.File {
		names = append(names, f.Name)
	}
	return names, nil
}

// meOK answers GET /api/me with a member identity — enough for
// resolveAdmin-style startup probes to get a parseable response in tests
// that do not assert on identity (call-time requireAdmin would still deny).
func meOK() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"userId":"u1","username":"someone","role":"","isAdmin":false}`))
	}
}

// authRecorder collects values written by httptest handler goroutines so the
// test goroutine can read them safely under -race. Fatal may not be called
// from a handler goroutine; handlers use t.Errorf + http.Error instead.
type authRecorder struct {
	mu   sync.Mutex
	vals [][]byte
}

func (a *authRecorder) add(v []byte) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.vals = append(a.vals, v)
}

func (a *authRecorder) snapshot() [][]byte {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([][]byte, len(a.vals))
	copy(out, a.vals)
	return out
}

// newMCPClient connects an MCP client to an in-process sth MCP server with
// the given config over in-memory transports, exercising the real protocol
// path (initialize, tools/list, tools/call) without stdio plumbing.
func newMCPClient(t *testing.T, cfg *mcpConfig) *mcp.ClientSession {
	t.Helper()

	ctx := context.Background()
	s := mcp.NewServer(&mcp.Implementation{Name: "sth-test", Version: "test"}, nil)
	registerMCPTools(s, cfg)

	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	if _, err := s.Connect(ctx, serverTransport, nil); err != nil {
		t.Fatal(err)
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "mcp-test-client", Version: "0"}, nil).Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

func callTool(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	result, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("CallTool(%s): %v", name, err)
	}
	return result
}

func structured[T any](t *testing.T, result *mcp.CallToolResult) T {
	t.Helper()
	if result.IsError {
		t.Fatalf("tool call failed: %s", toolErrorText(t, result))
	}
	raw, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatalf("marshal structured content: %v", err)
	}
	var out T
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal structured content into %T: %v", out, err)
	}
	return out
}

func toolErrorText(t *testing.T, result *mcp.CallToolResult) string {
	t.Helper()
	if !result.IsError {
		t.Fatalf("expected a tool error, got success: %+v", result)
	}
	var texts []string
	for _, c := range result.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			texts = append(texts, tc.Text)
		}
	}
	return strings.Join(texts, " ")
}

func newMCPTestDB(t *testing.T) (string, *session.Store) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "mcp-test.db")
	store, err := session.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	return dbPath, store
}

func TestMCPToolsListed(t *testing.T) {
	t.Parallel()

	// All tools are registered by default; admin tools enforce their
	// identity requirement at call time instead.
	dbPath, _ := newMCPTestDB(t)
	cs := newMCPClient(t, &mcpConfig{dbPath: dbPath})

	resp, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}

	want := map[string]bool{
		"send": true, "tag": true, "categorize": true, "project": true,
		"title": true, "list": true, "search": true, "delete": true,
		"share": true, "unshare": true, "shares": true,
		"user_add": true, "user_issue_key": true, "user_revoke_key": true,
		"user_list": true, "audit": true,
	}
	got := map[string]bool{}
	for _, tool := range resp.Tools {
		got[tool.Name] = true
	}
	if len(got) != len(want) {
		t.Fatalf("expected %d tools, got %d: %v", len(want), len(got), got)
	}
	for name := range want {
		if !got[name] {
			t.Errorf("missing tool %q", name)
		}
	}
}

func TestMCPListAndSearchLocal(t *testing.T) {
	t.Parallel()

	dbPath, store := newMCPTestDB(t)
	s1, err := store.Create("/tmp/report.html")
	if err != nil {
		t.Fatal(err)
	}
	s2, err := store.Create("/tmp/demo.html")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AddTags(s1.ID, "go"); err != nil {
		t.Fatal(err)
	}
	if err := store.AddTags(s2.ID, "web"); err != nil {
		t.Fatal(err)
	}

	cs := newMCPClient(t, &mcpConfig{dbPath: dbPath})

	list := structured[documentListResult](t, callTool(t, cs, "list", nil))
	if len(list.Documents) != 2 {
		t.Fatalf("expected 2 documents, got %d: %+v", len(list.Documents), list.Documents)
	}

	tagged := structured[documentListResult](t, callTool(t, cs, "list", map[string]any{"tag": "go"}))
	if len(tagged.Documents) != 1 || tagged.Documents[0].SessionID != s1.ID {
		t.Fatalf("expected only %s, got %+v", s1.ID, tagged.Documents)
	}

	search := structured[documentListResult](t, callTool(t, cs, "search", map[string]any{"query": "report"}))
	if len(search.Documents) != 1 || search.Documents[0].SessionID != s1.ID {
		t.Fatalf("expected search to find %s, got %+v", s1.ID, search.Documents)
	}

	bad := callTool(t, cs, "list", map[string]any{"offset": 5})
	if text := toolErrorText(t, bad); !strings.Contains(text, "offset requires a positive limit") {
		t.Fatalf("unexpected error text: %q", text)
	}
}

func TestMCPTagCategorizeProjectTitleLocal(t *testing.T) {
	t.Parallel()

	dbPath, store := newMCPTestDB(t)
	s, err := store.Create("/tmp/page.html")
	if err != nil {
		t.Fatal(err)
	}

	// No admin override: proves the non-admin tools work under the default
	// (deny-by-default) admin posture.
	cs := newMCPClient(t, &mcpConfig{dbPath: dbPath})

	msg := structured[messageResult](t, callTool(t, cs, "tag", map[string]any{
		"session_id": s.ID, "tags": []string{"go", "demo"},
	}))
	if !strings.Contains(msg.Message, "Added tags to") {
		t.Fatalf("unexpected message: %q", msg.Message)
	}

	msg = structured[messageResult](t, callTool(t, cs, "tag", map[string]any{
		"session_id": s.ID, "tags": []string{"demo"}, "remove": true,
	}))
	if !strings.Contains(msg.Message, "Removed tags from") {
		t.Fatalf("unexpected message: %q", msg.Message)
	}

	for _, tc := range []struct {
		tool, field, value string
	}{
		{"categorize", "category", "reports"},
		{"project", "project", "acme"},
		{"title", "title", "Quarterly report"},
	} {
		msg := structured[messageResult](t, callTool(t, cs, tc.tool, map[string]any{
			"session_id": s.ID, tc.field: tc.value,
		}))
		if !strings.Contains(msg.Message, "Set "+tc.field+" of") {
			t.Fatalf("%s: unexpected message: %q", tc.tool, msg.Message)
		}
	}

	meta, err := store.GetMetadata(s.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(meta.Tags) != 1 || meta.Tags[0] != "go" {
		t.Fatalf("unexpected tags after remove: %+v", meta.Tags)
	}
	if meta.Category != "reports" || meta.Project != "acme" || meta.Title != "Quarterly report" {
		t.Fatalf("unexpected metadata: %+v", meta)
	}

	errTool := callTool(t, cs, "tag", map[string]any{"session_id": s.ID, "tags": []string{}})
	if text := toolErrorText(t, errTool); !strings.Contains(text, "at least one tag is required") {
		t.Fatalf("unexpected error text: %q", text)
	}
}

func TestMCPDeleteLocal(t *testing.T) {
	t.Parallel()

	dbPath, store := newMCPTestDB(t)
	s, err := store.Create("/tmp/gone.html")
	if err != nil {
		t.Fatal(err)
	}

	cs := newMCPClient(t, &mcpConfig{dbPath: dbPath})

	msg := structured[messageResult](t, callTool(t, cs, "delete", map[string]any{"session_id": s.ID}))
	if !strings.Contains(msg.Message, "deleted") {
		t.Fatalf("unexpected message: %q", msg.Message)
	}

	docs, err := store.ListDocuments(session.FilterOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 0 {
		t.Fatalf("expected session to be deleted, got %+v", docs)
	}

	errTool := callTool(t, cs, "delete", map[string]any{"session_id": "missing"})
	if text := toolErrorText(t, errTool); !strings.Contains(text, `session "missing" not found`) {
		t.Fatalf("unexpected error text: %q", text)
	}
}

func TestMCPUserLifecycle(t *testing.T) {
	t.Parallel()

	dbPath, _ := newMCPTestDB(t)
	cs := newMCPClient(t, &mcpConfig{dbPath: dbPath, adminOverrideSet: true, adminOverrideValue: true})

	created := structured[userCreatedResult](t, callTool(t, cs, "user_add", map[string]any{"name": "alice"}))
	if created.Username != "alice" || created.ID == "" {
		t.Fatalf("unexpected user: %+v", created)
	}

	users := structured[userListResult](t, callTool(t, cs, "user_list", nil))
	if len(users.Users) != 1 || users.Users[0].ActiveKeys != 0 {
		t.Fatalf("unexpected users: %+v", users.Users)
	}

	key := structured[issuedKeyResult](t, callTool(t, cs, "user_issue_key", map[string]any{"name": "alice"}))
	if key.APIKey == "" || key.KeyID == "" {
		t.Fatalf("unexpected issued key: %+v", key)
	}

	users = structured[userListResult](t, callTool(t, cs, "user_list", nil))
	if users.Users[0].ActiveKeys != 1 {
		t.Fatalf("expected 1 active key, got %+v", users.Users[0])
	}

	msg := structured[messageResult](t, callTool(t, cs, "user_revoke_key", map[string]any{"id_or_prefix": key.KeyID}))
	if !strings.Contains(msg.Message, "Revoked key") {
		t.Fatalf("unexpected message: %q", msg.Message)
	}

	// Prefixes shorter than session.KeyPrefixLen fail closed, as in the CLI.
	short := callTool(t, cs, "user_revoke_key", map[string]any{"id_or_prefix": key.APIKey[:8]})
	if text := toolErrorText(t, short); !strings.Contains(text, "no active key matches") {
		t.Fatalf("unexpected error text: %q", text)
	}

	users = structured[userListResult](t, callTool(t, cs, "user_list", nil))
	if users.Users[0].ActiveKeys != 0 {
		t.Fatalf("expected 0 active keys after revoke, got %+v", users.Users[0])
	}

	dup := callTool(t, cs, "user_add", map[string]any{"name": "alice"})
	if text := toolErrorText(t, dup); !strings.Contains(text, "already taken") {
		t.Fatalf("unexpected error text: %q", text)
	}

	missing := callTool(t, cs, "user_issue_key", map[string]any{"name": "nobody"})
	if text := toolErrorText(t, missing); !strings.Contains(text, `user "nobody" not found`) {
		t.Fatalf("unexpected error text: %q", text)
	}
}

func TestMCPShareLocal(t *testing.T) {
	t.Parallel()

	dbPath, store := newMCPTestDB(t)
	s, err := store.Create("/tmp/shared.html")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateUser("alice"); err != nil {
		t.Fatal(err)
	}

	cs := newMCPClient(t, &mcpConfig{dbPath: dbPath})

	msg := structured[messageResult](t, callTool(t, cs, "share", map[string]any{"session_id": s.ID, "username": "alice"}))
	if !strings.Contains(msg.Message, "whole session") {
		t.Fatalf("unexpected message: %q", msg.Message)
	}

	shares := structured[shareListResult](t, callTool(t, cs, "shares", map[string]any{"session_id": s.ID}))
	if len(shares.Shares) != 1 || shares.Shares[0].Username != "alice" {
		t.Fatalf("unexpected shares: %+v", shares.Shares)
	}

	msg = structured[messageResult](t, callTool(t, cs, "share", map[string]any{
		"session_id": s.ID, "username": "alice", "page": "report.html",
	}))
	if !strings.Contains(msg.Message, "page: report.html") {
		t.Fatalf("unexpected message: %q", msg.Message)
	}

	shares = structured[shareListResult](t, callTool(t, cs, "shares", map[string]any{"session_id": s.ID}))
	if len(shares.Shares) != 2 || shares.Shares[1].ScopeLabel() != "report.html" {
		t.Fatalf("unexpected shares after page grant: %+v", shares.Shares)
	}

	msg = structured[messageResult](t, callTool(t, cs, "unshare", map[string]any{
		"session_id": s.ID, "username": "alice", "page": "./report.html",
	}))
	if !strings.Contains(msg.Message, "Unshared") {
		t.Fatalf("unexpected message: %q", msg.Message)
	}

	msg = structured[messageResult](t, callTool(t, cs, "unshare", map[string]any{"session_id": s.ID, "username": "alice"}))
	if !strings.Contains(msg.Message, "Unshared") {
		t.Fatalf("unexpected message: %q", msg.Message)
	}

	again := callTool(t, cs, "unshare", map[string]any{"session_id": s.ID, "username": "alice"})
	if text := toolErrorText(t, again); !strings.Contains(text, "is not shared on session") {
		t.Fatalf("unexpected error text: %q", text)
	}

	unknown := callTool(t, cs, "share", map[string]any{"session_id": s.ID, "username": "nobody"})
	if text := toolErrorText(t, unknown); !strings.Contains(text, "not found") {
		t.Fatalf("unexpected error text: %q", text)
	}
}

func TestMCPSendRemote(t *testing.T) {
	t.Parallel()

	var (
		gotEntry           string
		gotTitle           string
		gotCategory        string
		gotProject         string
		gotTags            []string
		gotArchiveHasIndex bool
		gotArchiveHasCSS   bool
		gotAuthorization   authRecorder
	)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/sessions", func(w http.ResponseWriter, r *http.Request) {
		gotAuthorization.add([]byte(r.Header.Get("Authorization")))
		if err := r.ParseMultipartForm(10 << 20); err != nil {
			http.Error(w, "bad form", http.StatusBadRequest)
			return
		}
		gotEntry = r.FormValue("entryFile")
		gotTitle = r.FormValue("title")
		gotCategory = r.FormValue("category")
		gotProject = r.FormValue("project")
		gotTags = r.MultipartForm.Value["tags"]

		archive, _, err := r.FormFile("archive")
		if err != nil {
			http.Error(w, "missing archive", http.StatusBadRequest)
			return
		}
		defer archive.Close()
		data, err := io.ReadAll(archive)
		if err != nil {
			t.Errorf("read archive: %v", err)
			http.Error(w, "unreadable archive", http.StatusBadRequest)
			return
		}
		entries, err := zipNames(data)
		if err != nil {
			t.Errorf("open archive: %v", err)
			http.Error(w, "bad archive", http.StatusBadRequest)
			return
		}
		for _, name := range entries {
			switch name {
			case "index.html":
				gotArchiveHasIndex = true
			case "style.css":
				gotArchiveHasCSS = true
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"url":"http://example.test/s/abc123","chainId":"chain1","versionNo":3}`))
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "index.html"), []byte("<!doctype html><title>ok</title>"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "style.css"), []byte("body{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	cs := newMCPClient(t, &mcpConfig{})
	result := callTool(t, cs, "send", map[string]any{
		"file":     filepath.Join(root, "index.html"),
		"title":    "Demo",
		"tag":      "demo,test",
		"category": "reports",
		"project":  "acme",
		"server":   ts.URL,
		"api_key":  "k-test",
	})
	if result.IsError {
		t.Fatalf("send failed: %s", toolErrorText(t, result))
	}
	out := structured[sendResult](t, result)
	if out.URL != "http://example.test/s/abc123" || out.ChainID != "chain1" || out.VersionNo != 3 {
		t.Fatalf("unexpected send result: %+v", out)
	}

	if gotEntry != "index.html" || gotTitle != "Demo" || gotCategory != "reports" || gotProject != "acme" {
		t.Fatalf("unexpected form fields: entry=%q title=%q category=%q project=%q", gotEntry, gotTitle, gotCategory, gotProject)
	}
	if strings.Join(gotTags, ",") != "demo,test" {
		t.Fatalf("unexpected tags: %v", gotTags)
	}
	if !gotArchiveHasIndex || !gotArchiveHasCSS {
		t.Fatalf("archive missing assets: index=%t css=%t", gotArchiveHasIndex, gotArchiveHasCSS)
	}
	authCalls := gotAuthorization.snapshot()
	if len(authCalls) != 1 || string(authCalls[0]) != "Bearer k-test" {
		t.Fatalf("unexpected authorization header: %+v", authCalls)
	}
}

func TestMCPSendValidation(t *testing.T) {
	t.Parallel()

	cs := newMCPClient(t, &mcpConfig{})

	noTitle := callTool(t, cs, "send", map[string]any{
		"file": "/tmp/x.html", "title": "", "tag": "t", "category": "c", "project": "p",
	})
	if text := toolErrorText(t, noTitle); !strings.Contains(text, "--title is required") {
		t.Fatalf("unexpected error text: %q", text)
	}

	missingFile := callTool(t, cs, "send", map[string]any{
		"file": "/nonexistent/nope.html", "title": "T", "tag": "t", "category": "c", "project": "p",
	})
	if text := toolErrorText(t, missingFile); !strings.Contains(text, "does not exist") {
		t.Fatalf("unexpected error text: %q", text)
	}
}

func TestMCPTagRemote(t *testing.T) {
	t.Parallel()

	type recordedCall struct {
		method, path, body string
	}
	var calls []recordedCall
	mux := http.NewServeMux()
	mux.HandleFunc("/api/sessions/s1/tags", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		calls = append(calls, recordedCall{method: r.Method, path: r.URL.Path, body: string(body)})
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	// Per-call server parameter selects remote mode.
	cs := newMCPClient(t, &mcpConfig{})
	msg := structured[messageResult](t, callTool(t, cs, "tag", map[string]any{
		"session_id": "s1", "tags": []string{"a"}, "server": ts.URL, "api_key": "k",
	}))
	if !strings.Contains(msg.Message, "Added tags to s1: a") {
		t.Fatalf("unexpected message: %q", msg.Message)
	}
	if len(calls) != 1 || calls[0].method != http.MethodPut || calls[0].path != "/api/sessions/s1/tags" {
		t.Fatalf("unexpected calls: %+v", calls)
	}
	if calls[0].body != `{"tags":["a"]}` {
		t.Fatalf("unexpected body: %q", calls[0].body)
	}

	// remove=true switches to DELETE.
	msg = structured[messageResult](t, callTool(t, cs, "tag", map[string]any{
		"session_id": "s1", "tags": []string{"a"}, "remove": true, "server": ts.URL,
	}))
	if !strings.Contains(msg.Message, "Removed tags from s1") {
		t.Fatalf("unexpected message: %q", msg.Message)
	}
	if len(calls) != 2 || calls[1].method != http.MethodDelete {
		t.Fatalf("unexpected calls: %+v", calls)
	}
}

// TestMCPServerDefaultServer verifies that a server-wide --server default
// selects remote mode for dual-mode tools even without a per-call override.
func TestMCPServerDefaultServer(t *testing.T) {
	t.Parallel()

	var gotTitle string
	var gotPath string
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/me", meOK())
	mux.HandleFunc("PUT /api/sessions/s1/title", func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		body, _ := io.ReadAll(r.Body)
		var payload map[string]string
		_ = json.Unmarshal(body, &payload)
		gotTitle = payload["title"]
		_, _ = w.Write([]byte(`{}`))
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	cs := newMCPClient(t, &mcpConfig{serverURL: ts.URL, apiKey: "env-key"})
	msg := structured[messageResult](t, callTool(t, cs, "title", map[string]any{"session_id": "s1", "title": "New title"}))
	if !strings.Contains(msg.Message, "Set title of s1 to New title") {
		t.Fatalf("unexpected message: %q", msg.Message)
	}
	if gotPath != "/api/sessions/s1/title" || gotTitle != "New title" {
		t.Fatalf("unexpected remote call: path=%q title=%q", gotPath, gotTitle)
	}
}

func TestMCPSharesRemote(t *testing.T) {
	t.Parallel()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/sessions/s1/shares", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"shares":[{"userId":"u1","username":"alice","path":"","createdAt":"2026-01-01T00:00:00Z"}]}`))
	})
	mux.HandleFunc("PUT /api/sessions/s1/shares", func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]string
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil || payload["user"] != "bob" || payload["path"] != "report.html" {
			http.Error(w, "unexpected body", http.StatusBadRequest)
			return
		}
		_, _ = w.Write([]byte(`{"shares":[{"userId":"u2","username":"bob","path":"report.html","createdAt":"2026-01-02T00:00:00Z"}]}`))
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	cs := newMCPClient(t, &mcpConfig{serverURL: ts.URL})

	shares := structured[shareListResult](t, callTool(t, cs, "shares", map[string]any{"session_id": "s1"}))
	if len(shares.Shares) != 1 || shares.Shares[0].Username != "alice" {
		t.Fatalf("unexpected shares: %+v", shares.Shares)
	}

	msg := structured[messageResult](t, callTool(t, cs, "share", map[string]any{
		"session_id": "s1", "username": "bob", "page": "report.html",
	}))
	if !strings.Contains(msg.Message, "bob (report.html)") {
		t.Fatalf("unexpected message: %q", msg.Message)
	}
}

func TestMCPAuditLocal(t *testing.T) {
	t.Parallel()

	dbPath, store := newMCPTestDB(t)
	if err := store.RecordAudit(session.AuditEvent{Actor: "alice", Action: "login", Outcome: session.AuditOutcomeOK}); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordAudit(session.AuditEvent{Actor: "mallory", Action: "login", Outcome: session.AuditOutcomeDenied}); err != nil {
		t.Fatal(err)
	}

	cs := newMCPClient(t, &mcpConfig{dbPath: dbPath, adminOverrideSet: true, adminOverrideValue: true})

	out := structured[auditResult](t, callTool(t, cs, "audit", nil))
	if len(out.Events) != 2 {
		t.Fatalf("expected 2 events, got %+v", out.Events)
	}

	denied := structured[auditResult](t, callTool(t, cs, "audit", map[string]any{"outcome": "denied"}))
	if len(denied.Events) != 1 || denied.Events[0].Actor != "mallory" {
		t.Fatalf("unexpected denied events: %+v", denied.Events)
	}

	pruned := structured[auditResult](t, callTool(t, cs, "audit", map[string]any{"prune_days": 30}))
	if pruned.Pruned != 0 || len(pruned.Events) != 0 {
		t.Fatalf("expected nothing pruned, got %+v", pruned)
	}

	bad := callTool(t, cs, "audit", map[string]any{"since": "not-a-date"})
	if text := toolErrorText(t, bad); !strings.Contains(text, "since must be YYYY-MM-DD") {
		t.Fatalf("unexpected error text: %q", text)
	}

	negative := callTool(t, cs, "audit", map[string]any{"prune_days": -1})
	if text := toolErrorText(t, negative); !strings.Contains(text, "prune-days must be a positive integer") {
		t.Fatalf("unexpected error text: %q", text)
	}
}

// TestMCPDispatchRejectsBadFlags checks the `mcp` command wiring through Run
// without starting a stdio server.
func TestMCPDispatchRejectsBadFlags(t *testing.T) {
	t.Parallel()

	err := Run([]string{"mcp", "--db"}, &strings.Builder{}, &strings.Builder{})
	if err == nil || !strings.Contains(err.Error(), "missing value for --db") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestMCPKeyNotForwardedToOverriddenServer pins the host-bound credential
// rule: the server-wide API key follows the configured default server only.
// A per-call server override runs without credentials unless the caller
// passes an explicit per-call key.
func TestMCPKeyNotForwardedToOverriddenServer(t *testing.T) {
	t.Parallel()

	recordingMux := func(auth *authRecorder) *http.ServeMux {
		mux := http.NewServeMux()
		mux.HandleFunc("GET /api/me", meOK())
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			auth.add([]byte(r.Header.Get("Authorization")))
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{}`))
		})
		return mux
	}

	var defaultAuth authRecorder
	defaultServer := httptest.NewServer(recordingMux(&defaultAuth))
	defer defaultServer.Close()

	var overrideAuth authRecorder
	overrideServer := httptest.NewServer(recordingMux(&overrideAuth))
	defer overrideServer.Close()

	cs := newMCPClient(t, &mcpConfig{serverURL: defaultServer.URL, apiKey: "secret"})

	// Override to another host: no credentials are attached.
	structured[messageResult](t, callTool(t, cs, "tag", map[string]any{
		"session_id": "s1", "tags": []string{"a"}, "server": overrideServer.URL,
	}))
	if calls := overrideAuth.snapshot(); len(calls) != 1 || len(calls[0]) != 0 {
		t.Fatalf("server-wide key leaked to overridden server: %+v", calls)
	}

	// Same host as the default: the key is attached.
	structured[messageResult](t, callTool(t, cs, "tag", map[string]any{
		"session_id": "s1", "tags": []string{"a"}, "server": defaultServer.URL,
	}))
	if calls := defaultAuth.snapshot(); len(calls) != 1 || string(calls[0]) != "Bearer secret" {
		t.Fatalf("default server should carry the configured key: %+v", calls)
	}

	// A trailing slash on the override URL still counts as a different host.
	overrideServerTrimmed := strings.TrimSuffix(overrideServer.URL, "/") + "/"
	structured[messageResult](t, callTool(t, cs, "tag", map[string]any{
		"session_id": "s1", "tags": []string{"a"}, "server": overrideServerTrimmed,
	}))
	if calls := overrideAuth.snapshot(); len(calls) != 2 || len(calls[1]) != 0 {
		t.Fatalf("trailing-slash override unexpectedly carried credentials: %+v", calls)
	}

	// Override with an explicit per-call key: that key is attached.
	structured[messageResult](t, callTool(t, cs, "tag", map[string]any{
		"session_id": "s1", "tags": []string{"a"}, "server": overrideServer.URL, "api_key": "k-percall",
	}))
	calls := overrideAuth.snapshot()
	if len(calls) != 3 || string(calls[2]) != "Bearer k-percall" {
		t.Fatalf("unexpected per-call auth: %+v", calls)
	}
}

func TestMCPLocalForcesLocalDB(t *testing.T) {
	t.Parallel()

	var remoteHits authRecorder
	remoteServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		remoteHits.add(nil)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer remoteServer.Close()

	dbPath, store := newMCPTestDB(t)
	s, err := store.Create("/tmp/local.html")
	if err != nil {
		t.Fatal(err)
	}

	cs := newMCPClient(t, &mcpConfig{dbPath: dbPath, serverURL: remoteServer.URL})
	msg := structured[messageResult](t, callTool(t, cs, "tag", map[string]any{
		"session_id": s.ID, "tags": []string{"local-tag"}, "local": true,
	}))
	if !strings.Contains(msg.Message, "Added tags to") {
		t.Fatalf("unexpected message: %q", msg.Message)
	}
	if calls := remoteHits.snapshot(); len(calls) != 0 {
		t.Fatalf("local=true should not hit the remote server, got %d hits", len(calls))
	}

	meta, err := store.GetMetadata(s.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(meta.Tags) != 1 || meta.Tags[0] != "local-tag" {
		t.Fatalf("expected tag written to the local DB, got %+v", meta.Tags)
	}
}

// TestMCPSendKeyNotForwarded checks the same host-bound rule for send, whose
// default chain differs from the dual-mode tools.
func TestMCPSendKeyNotForwarded(t *testing.T) {
	t.Parallel()

	newSendMux := func(hooks *authRecorder) *http.ServeMux {
		mux := http.NewServeMux()
		mux.HandleFunc("GET /api/me", meOK())
		mux.HandleFunc("POST /api/sessions", func(w http.ResponseWriter, r *http.Request) {
			hooks.add([]byte(r.Header.Get("Authorization")))
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"url":"http://example.test/s/x"}`))
		})
		return mux
	}

	var defaultHooks authRecorder
	defaultServer := httptest.NewServer(newSendMux(&defaultHooks))
	defer defaultServer.Close()

	var overrideHooks authRecorder
	overrideServer := httptest.NewServer(newSendMux(&overrideHooks))
	defer overrideServer.Close()

	root := t.TempDir()
	entry := filepath.Join(root, "index.html")
	if err := os.WriteFile(entry, []byte("<!doctype html><title>ok</title>"), 0o644); err != nil {
		t.Fatal(err)
	}

	cs := newMCPClient(t, &mcpConfig{serverURL: defaultServer.URL, apiKey: "secret"})
	sendArgs := func(server string) map[string]any {
		return map[string]any{
			"file": entry, "title": "T", "tag": "t", "category": "c", "project": "p", "server": server,
		}
	}

	structured[sendResult](t, callTool(t, cs, "send", sendArgs(overrideServer.URL)))
	if calls := overrideHooks.snapshot(); len(calls) != 1 || len(calls[0]) != 0 {
		t.Fatalf("send leaked credentials to overridden server: %+v", calls)
	}

	structured[sendResult](t, callTool(t, cs, "send", sendArgs("")))
	if calls := defaultHooks.snapshot(); len(calls) != 1 || string(calls[0]) != "Bearer secret" {
		t.Fatalf("default server should carry the configured key, got %+v", calls)
	}
}

// TestMCPParseMCPConfig exercises the real `sth mcp` argument parsing surface.
// The flag does not decide by itself: parse yields an override that
// requireAdmin applies on top of the caller's identity, so the assertions
// here check the override fields, not the final exposure.
func TestMCPParseMCPConfig(t *testing.T) {
	// t.Setenv requires a serial test.
	cfg, err := parseMCPConfig(nil)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.adminOverrideSet {
		t.Fatal("no flag and no env must leave the override unset")
	}

	cfg, err = parseMCPConfig([]string{"--allow-admin-tools"})
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.adminOverrideSet || !cfg.adminOverrideValue {
		t.Fatal("expected --allow-admin-tools to set a true override")
	}

	t.Setenv("STH_ALLOW_ADMIN_TOOLS", "true")
	cfg, err = parseMCPConfig(nil)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.adminOverrideSet || !cfg.adminOverrideValue {
		t.Fatal("expected STH_ALLOW_ADMIN_TOOLS=true to set a true override")
	}

	cfg, err = parseMCPConfig([]string{"--allow-admin-tools=false"})
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.adminOverrideSet || cfg.adminOverrideValue {
		t.Fatal("expected --allow-admin-tools=false to set a false override")
	}
	t.Setenv("STH_ALLOW_ADMIN_TOOLS", "")

	// The environment may also force a denial: false|0 is an explicit
	// override, not an unset (fail-open would re-expose admin tools).
	t.Setenv("STH_ALLOW_ADMIN_TOOLS", "false")
	cfg, err = parseMCPConfig(nil)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.adminOverrideSet || cfg.adminOverrideValue {
		t.Fatal("expected STH_ALLOW_ADMIN_TOOLS=false to set a false override")
	}
	t.Setenv("STH_ALLOW_ADMIN_TOOLS", "0")
	cfg, err = parseMCPConfig(nil)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.adminOverrideSet || cfg.adminOverrideValue {
		t.Fatal("expected STH_ALLOW_ADMIN_TOOLS=0 to set a false override")
	}
	t.Setenv("STH_ALLOW_ADMIN_TOOLS", "")

	// Unparseable values must fail loudly rather than fall open.
	t.Setenv("STH_ALLOW_ADMIN_TOOLS", "banana")
	if _, err = parseMCPConfig(nil); err == nil || !strings.Contains(err.Error(), "invalid STH_ALLOW_ADMIN_TOOLS") {
		t.Fatalf("expected invalid-env error, got: %v", err)
	}
	t.Setenv("STH_ALLOW_ADMIN_TOOLS", "")

	// The space-separated form the boolean parser does not understand must
	// fail loudly instead of silently leaving admin tools enabled.
	if _, err := parseMCPConfig([]string{"--allow-admin-tools", "false"}); err == nil || !strings.Contains(err.Error(), "unexpected argument") {
		t.Fatalf("expected error for stray positional, got: %v", err)
	}

	cfg, err = parseMCPConfig([]string{"--db", "/tmp/x.db", "--server", "http://127.0.0.1:3939", "--api-key", "k"})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.dbPath != "/tmp/x.db" || cfg.serverURL != "http://127.0.0.1:3939" || cfg.apiKey != "k" {
		t.Fatalf("unexpected config: %+v", cfg)
	}
}

// newIdentityTestStore creates a store with an admin and a member user and
// returns (adminKey, memberKey).
func newIdentityTestStore(t *testing.T) (string, string, string, *session.Store) {
	t.Helper()
	dbPath, store := newMCPTestDB(t)

	if _, err := store.CreateUser("root"); err != nil {
		t.Fatal(err)
	}
	if err := store.SetUserRole("root", session.RoleAdmin); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateUser("member"); err != nil {
		t.Fatal(err)
	}

	root, err := store.FindUserByUsername("root")
	if err != nil {
		t.Fatal(err)
	}
	member, err := store.FindUserByUsername("member")
	if err != nil {
		t.Fatal(err)
	}
	adminKey, _, err := store.IssueAPIKey(root.ID)
	if err != nil {
		t.Fatal(err)
	}
	memberKey, _, err := store.IssueAPIKey(member.ID)
	if err != nil {
		t.Fatal(err)
	}
	return dbPath, adminKey, memberKey, store
}

func adminToolCount(t *testing.T, cs *mcp.ClientSession) (total int, hasUserAdd bool) {
	t.Helper()
	resp, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range resp.Tools {
		total++
		if tool.Name == "user_add" {
			hasUserAdd = true
		}
	}
	return total, hasUserAdd
}

// TestMCPAdminToolsCallTimeEnforcement covers the identity semantics of the
// admin-class tools: they are registered for everyone, but each call checks
// the caller's key — admin keys pass, member/invalid keys are denied, no key
// runs in local-operator mode and is allowed.
func TestMCPAdminToolsCallTimeEnforcement(t *testing.T) {
	t.Parallel()

	dbPath, adminKey, memberKey, _ := newIdentityTestStore(t)

	// No key: no identity, no admin — denied (fail-closed).
	cs := newMCPClient(t, &mcpConfig{dbPath: dbPath})
	noKey := callTool(t, cs, "user_add", map[string]any{"name": "nope"})
	if text := toolErrorText(t, noKey); !strings.Contains(text, "no API key configured") {
		t.Fatalf("unexpected no-key denial: %q", text)
	}

	// Override=true without a key: forced on, calls allowed.
	cs = newMCPClient(t, &mcpConfig{dbPath: dbPath, adminOverrideSet: true, adminOverrideValue: true})
	structured[userCreatedResult](t, callTool(t, cs, "user_add", map[string]any{"name": "operator-made"}))

	// Member key: denied with an actionable error.
	cs = newMCPClient(t, &mcpConfig{dbPath: dbPath, apiKey: memberKey})
	denied := callTool(t, cs, "user_add", map[string]any{"name": "nope"})
	if text := toolErrorText(t, denied); !strings.Contains(text, "admin role required") {
		t.Fatalf("unexpected denial: %q", text)
	}
	auditDenied := callTool(t, cs, "audit", nil)
	if text := toolErrorText(t, auditDenied); !strings.Contains(text, "admin role required") {
		t.Fatalf("unexpected audit denial: %q", text)
	}

	// Invalid key: denied too, with a hint about remote servers.
	cs = newMCPClient(t, &mcpConfig{dbPath: dbPath, apiKey: "sth_notarealkey"})
	invalid := callTool(t, cs, "user_add", map[string]any{"name": "nope"})
	if text := toolErrorText(t, invalid); !strings.Contains(text, "does not match any active key") {
		t.Fatalf("unexpected invalid-key denial: %q", text)
	}

	// Admin key: allowed.
	cs = newMCPClient(t, &mcpConfig{dbPath: dbPath, apiKey: adminKey})
	structured[userCreatedResult](t, callTool(t, cs, "user_add", map[string]any{"name": "by-admin"}))

	// Forced override=false denies even with an admin key.
	cs = newMCPClient(t, &mcpConfig{
		dbPath: dbPath, apiKey: adminKey,
		adminOverrideSet: true, adminOverrideValue: false,
	})
	forced := callTool(t, cs, "user_add", map[string]any{"name": "nope"})
	if text := toolErrorText(t, forced); !strings.Contains(text, "disabled on this MCP server") {
		t.Fatalf("unexpected forced-off denial: %q", text)
	}
}

// newRemoteSTHServer spins a real sth server on a random loopback port with
// auth enabled, an admin user (root) and a member user (member), returning
// the server URL and both plaintext API keys. MCP admin tools pointed at
// this server exercise the full remote path: identity via GET /api/me plus
// the admin API.
func newRemoteSTHServer(t *testing.T) (string, *session.Store, string, string) {
	t.Helper()
	store, err := session.NewInMemoryStore()
	if err != nil {
		t.Fatal(err)
	}
	srv, err := server.New("127.0.0.1", 0, store, "", 0, "")
	if err != nil {
		t.Fatal(err)
	}
	srv.SetAuthEnabled(true)

	if _, err := store.CreateUser("root"); err != nil {
		t.Fatal(err)
	}
	if err := store.SetUserRole("root", session.RoleAdmin); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateUser("member"); err != nil {
		t.Fatal(err)
	}

	root, err := store.FindUserByUsername("root")
	if err != nil {
		t.Fatal(err)
	}
	member, err := store.FindUserByUsername("member")
	if err != nil {
		t.Fatal(err)
	}
	adminKey, _, err := store.IssueAPIKey(root.ID)
	if err != nil {
		t.Fatal(err)
	}
	memberKey, _, err := store.IssueAPIKey(member.ID)
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
		store.Close()
	})
	return "http://" + srv.ListenAddr(), store, adminKey, memberKey
}

// TestMCPAdminToolsRemoteEndToEnd drives the admin tools against a real sth
// server over HTTP: an admin key creates users, issues and revokes keys, and
// reads the audit log on the REMOTE store; a member key is denied everywhere.
func TestMCPAdminToolsRemoteEndToEnd(t *testing.T) {
	t.Parallel()

	serverURL, remoteStore, adminKey, memberKey := newRemoteSTHServer(t)

	// Admin key: user_add lands on the REMOTE store.
	cs := newMCPClient(t, &mcpConfig{serverURL: serverURL, apiKey: adminKey})
	created := structured[userCreatedResult](t, callTool(t, cs, "user_add", map[string]any{
		"name": "bob", "admin": true,
	}))
	if created.Username != "bob" || created.ID == "" {
		t.Fatalf("unexpected creation result: %+v", created)
	}
	if _, err := remoteStore.FindUserByUsername("bob"); err != nil {
		t.Fatalf("bob must exist on the remote store: %v", err)
	}

	// user_list reflects the remote users (root admin + member + bob).
	users := structured[userListResult](t, callTool(t, cs, "user_list", nil))
	if len(users.Users) != 3 {
		t.Fatalf("expected 3 remote users, got %+v", users.Users)
	}
	roles := map[string]string{}
	for _, u := range users.Users {
		roles[u.Username] = u.Role
	}
	if roles["root"] != session.RoleAdmin || roles["bob"] != session.RoleAdmin || roles["member"] != "" {
		t.Fatalf("unexpected remote roles: %v", roles)
	}

	// user_issue_key remote: the plaintext key verifies against the remote
	// store, and the local store is untouched.
	key := structured[issuedKeyResult](t, callTool(t, cs, "user_issue_key", map[string]any{"name": "bob"}))
	if key.APIKey == "" {
		t.Fatalf("no plaintext key returned: %+v", key)
	}
	if _, ok, err := remoteStore.VerifyAPIKey(key.APIKey); err != nil || !ok {
		t.Fatalf("remote-issued key must verify on the remote store (ok=%v err=%v)", ok, err)
	}
	if _, ok, _ := mustLocalStore(t).VerifyAPIKey(key.APIKey); ok {
		t.Fatal("remote key must not verify against the local store")
	}

	// audit remote: the admin API mutations are on the remote log.
	audit := structured[auditResult](t, callTool(t, cs, "audit", map[string]any{"action": "user.add"}))
	found := false
	for _, e := range audit.Events {
		if e.Action == "user.add" && e.Target == "bob" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a user.add event for bob, got %+v", audit.Events)
	}

	// user_revoke_key remote: the remote key stops working.
	structured[messageResult](t, callTool(t, cs, "user_revoke_key", map[string]any{"id_or_prefix": key.KeyID}))
	if _, ok, _ := remoteStore.VerifyAPIKey(key.APIKey); ok {
		t.Fatal("revoked remote key must not verify")
	}

	// Member key: denied everywhere, remote store untouched.
	cs = newMCPClient(t, &mcpConfig{serverURL: serverURL, apiKey: memberKey})
	denied := callTool(t, cs, "user_add", map[string]any{"name": "intruder"})
	if text := toolErrorText(t, denied); !strings.Contains(text, "admin role required") {
		t.Fatalf("unexpected member denial: %q", text)
	}
	if _, err := remoteStore.FindUserByUsername("intruder"); err == nil {
		t.Fatal("intruder must not have been created on the remote store")
	}
	auditDenied := callTool(t, cs, "audit", nil)
	if text := toolErrorText(t, auditDenied); !strings.Contains(text, "admin role required") {
		t.Fatalf("unexpected audit denial: %q", text)
	}
}

// mustLocalStore opens the local default store for cross-store assertions.
func mustLocalStore(t *testing.T) *session.Store {
	t.Helper()
	store, err := session.NewStore()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

// TestMCPAdminCanMintAdmins: user_add with admin=true creates a second admin,
// and user_list reports roles.
func TestMCPAdminCanMintAdmins(t *testing.T) {
	t.Parallel()

	dbPath, _ := newMCPTestDB(t)
	cs := newMCPClient(t, &mcpConfig{dbPath: dbPath, adminOverrideSet: true, adminOverrideValue: true})

	structured[userCreatedResult](t, callTool(t, cs, "user_add", map[string]any{
		"name": "deputy", "admin": true,
	}))

	users := structured[userListResult](t, callTool(t, cs, "user_list", nil))
	if len(users.Users) != 1 || users.Users[0].Role != session.RoleAdmin {
		t.Fatalf("expected deputy to be admin, got %+v", users.Users)
	}
}

// TestUserPromoteDemote covers `sth user promote|demote` through Run: the
// role round-trips, unknown users are reported, and list shows the role.
func TestUserPromoteDemote(t *testing.T) {
	t.Parallel()

	dbPath, store := newMCPTestDB(t)
	if _, err := store.CreateUser("alice"); err != nil {
		t.Fatal(err)
	}

	run := func(args ...string) (string, error) {
		var stdout, stderr strings.Builder
		err := Run(append([]string{"user"}, append(args, "--db", dbPath)...), &stdout, &stderr)
		return stdout.String(), err
	}

	if out, err := run("promote", "alice"); err != nil || !strings.Contains(out, "Promoted") {
		t.Fatalf("promote: out=%q err=%v", out, err)
	}
	isAdmin, err := store.IsAdmin(func() string { u, _ := store.FindUserByUsername("alice"); return u.ID }())
	if err != nil {
		t.Fatal(err)
	}
	if !isAdmin {
		t.Fatal("alice should be admin after promote")
	}

	if out, err := run("demote", "alice"); err != nil || !strings.Contains(out, "Demoted") {
		t.Fatalf("demote: out=%q err=%v", out, err)
	}
	isAdmin, err = store.IsAdmin(func() string { u, _ := store.FindUserByUsername("alice"); return u.ID }())
	if err != nil {
		t.Fatal(err)
	}
	if isAdmin {
		t.Fatal("alice should not be admin after demote")
	}

	if _, err := run("promote", "ghost"); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected not-found error, got: %v", err)
	}

	if out, err := run("list"); err != nil || !strings.Contains(out, "role: member") {
		t.Fatalf("list: out=%q err=%v", out, err)
	}
}
