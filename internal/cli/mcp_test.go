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

	"github.com/modelcontextprotocol/go-sdk/mcp"
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

	// Default posture: user-management mutation tools are not exposed.
	cs := newMCPClient(t, &mcpConfig{})

	resp, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}

	want := map[string]bool{
		"send": true, "tag": true, "categorize": true, "project": true,
		"title": true, "list": true, "search": true, "delete": true,
		"share": true, "unshare": true, "shares": true,
		"user_list": true,
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
	for _, gated := range []string{"user_add", "user_issue_key", "user_revoke_key", "audit"} {
		if got[gated] {
			t.Errorf("tool %q must not be listed without --allow-admin-tools", gated)
		}
	}
}

func TestMCPUserManagementGatedByDefault(t *testing.T) {
	t.Parallel()

	cs := newMCPClient(t, &mcpConfig{})

	// Calling an unregistered tool is a protocol-level error.
	for _, name := range []string{"user_add", "audit"} {
		if _, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
			Name:      name,
			Arguments: map[string]any{"name": "mallory"},
		}); err == nil {
			t.Fatalf("expected %s to be unavailable without --allow-admin-tools", name)
		}
	}
}

func TestMCPUserManagementExposedWhenAllowed(t *testing.T) {
	t.Parallel()

	dbPath, _ := newMCPTestDB(t)
	cs := newMCPClient(t, &mcpConfig{dbPath: dbPath, allowAdminTools: true})

	resp, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, tool := range resp.Tools {
		got[tool.Name] = true
	}
	for _, name := range []string{"user_add", "user_issue_key", "user_revoke_key", "audit"} {
		if !got[name] {
			t.Errorf("tool %q missing despite --allow-admin-tools", name)
		}
	}

	created := structured[userCreatedResult](t, callTool(t, cs, "user_add", map[string]any{"name": "alice"}))
	if created.Username != "alice" || created.ID == "" {
		t.Fatalf("unexpected user: %+v", created)
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
	cs := newMCPClient(t, &mcpConfig{dbPath: dbPath, allowAdminTools: true})

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

	cs := newMCPClient(t, &mcpConfig{dbPath: dbPath, allowAdminTools: true})

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

	var defaultAuth authRecorder
	defaultServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defaultAuth.add([]byte(r.Header.Get("Authorization")))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer defaultServer.Close()

	var overrideAuth authRecorder
	overrideServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		overrideAuth.add([]byte(r.Header.Get("Authorization")))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
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

// TestMCPParseMCPConfig exercises the real `sth mcp` argument parsing surface:
// default posture, flag/env precedence, and loud rejection of stray tokens.
func TestMCPParseMCPConfig(t *testing.T) {
	// t.Setenv requires a serial test.
	cfg, err := parseMCPConfig(nil)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.allowAdminTools {
		t.Fatal("admin tools must be disabled by default")
	}

	cfg, err = parseMCPConfig([]string{"--allow-admin-tools"})
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.allowAdminTools {
		t.Fatal("expected --allow-admin-tools to enable admin tools")
	}

	t.Setenv("STH_ALLOW_ADMIN_TOOLS", "true")
	cfg, err = parseMCPConfig(nil)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.allowAdminTools {
		t.Fatal("expected STH_ALLOW_ADMIN_TOOLS=true to enable admin tools")
	}

	cfg, err = parseMCPConfig([]string{"--allow-admin-tools=false"})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.allowAdminTools {
		t.Fatal("expected --allow-admin-tools=false to override the environment")
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
