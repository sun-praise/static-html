package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sun-praise/static-html/internal/server"
	"github.com/sun-praise/static-html/internal/session"
)

// mcpVersion is the version reported in the MCP initialize handshake. It
// tracks the MCP tool surface, independent of the sth release version.
var mcpVersion = "0.1.0"

// mcpConfig holds the server-wide defaults for MCP tool calls: the local --db
// target, the default remote connection (--server / --api-key), and whether
// admin-class tools (user management + audit) are exposed to the connected
// agent.
type mcpConfig struct {
	dbPath    string
	serverURL string
	apiKey    string

	// Admin-class tools (user_add / user_issue_key / user_revoke_key /
	// audit) are registered by default; every call to them is enforced by
	// requireAdmin against the caller's identity: with an API key
	// configured, it must belong to a user holding the admin role; with no
	// key at all there is no identity and calls fail closed.
	// adminOverrideSet / adminOverrideValue carry the --allow-admin-tools
	// flag, which forces calls allowed or denied regardless of identity.
	adminOverrideSet   bool
	adminOverrideValue bool

	// store caches the opened SQLite store for the process lifetime: admin
	// calls would otherwise open the database twice (identity check + the
	// operation itself), re-running schema DDL each time.
	store   *session.Store
	storeMu sync.Mutex
}

// openStore returns the process-lifetime store, opening it on first use.
// The caller must not close it: mcpServe closes it when the server stops.
func (c *mcpConfig) openStore() (*session.Store, error) {
	c.storeMu.Lock()
	defer c.storeMu.Unlock()
	if c.store != nil {
		return c.store, nil
	}

	var store *session.Store
	var err error
	if c.dbPath != "" {
		store, err = session.NewSQLiteStore(c.dbPath)
	} else {
		store, err = session.NewStore()
	}
	if err != nil {
		return nil, err
	}
	c.store = store
	return store, nil
}

// parseMCPConfig parses `sth mcp` arguments into the server configuration.
// Flag precedence mirrors the rest of the CLI: an explicit flag beats the
// STH_ALLOW_ADMIN_TOOLS environment variable. Leftover positional arguments
// are rejected: with a privilege gate on this command, a stray token (say,
// `--allow-admin-tools false` in the space-separated form the boolean parser
// does not understand) must fail loudly instead of being silently dropped.
// The flag does not decide by itself — resolveAdminTools applies it as an
// override on top of the caller's identity.
func parseMCPConfig(args []string) (*mcpConfig, error) {
	args, allowAdminSet, allowAdminFlag, err := popBoolFlagWithPresence(args, "allow-admin-tools")
	if err != nil {
		return nil, err
	}

	flags, positionals, err := parseArgs(args)
	if err != nil {
		return nil, err
	}
	if len(positionals) > 0 {
		return nil, fmt.Errorf("unexpected argument %q (sth mcp takes only --flags)", positionals[0])
	}

	cfg := &mcpConfig{
		dbPath:    flags["db"],
		serverURL: flags["server"],
		apiKey:    resolveAPIKey(flags),
	}
	// The flag is a forced override; absent a flag, the environment may set
	// one. Both true|1 and false|0 are honored — a plain "false" must deny,
	// not fall through to identity — so an unparseable value is an error
	// rather than a silent fail-open. The value only takes effect through
	// requireAdmin; without an override, the caller's identity decides per
	// call, and no key means denied. (requireAdmin replaced the earlier
	// resolveAdminTools draft; docs elsewhere may still use either name.)
	if allowAdminSet {
		cfg.adminOverrideSet, cfg.adminOverrideValue = true, allowAdminFlag
	} else if raw := strings.TrimSpace(os.Getenv("STH_ALLOW_ADMIN_TOOLS")); raw != "" {
		switch strings.ToLower(raw) {
		case "true", "1":
			cfg.adminOverrideSet, cfg.adminOverrideValue = true, true
		case "false", "0":
			cfg.adminOverrideSet, cfg.adminOverrideValue = true, false
		default:
			return nil, fmt.Errorf("invalid STH_ALLOW_ADMIN_TOOLS value %q (expected true|1|false|0)", raw)
		}
	}
	return cfg, nil
}

// requireAdminFor enforces the caller identity on every admin-class tool
// call, against the database the call targets: a remote server is checked
// via GET /api/me, the local store via VerifyAPIKey + IsAdmin. An explicit
// --allow-admin-tools=false denies unconditionally, =true allows
// unconditionally. Without an override the configured API key must resolve
// to a user holding the admin role — a member or invalid key, or no key at
// all, fails the call.
func (c *mcpConfig) requireAdminFor(serverURL, apiKey string, remote bool) error {
	if c.adminOverrideSet {
		if !c.adminOverrideValue {
			return errors.New("admin tools are disabled on this MCP server (--allow-admin-tools=false)")
		}
		return nil
	}

	if apiKey == "" {
		apiKey = c.apiKey
	}
	if apiKey == "" {
		return errors.New("no API key configured: admin tools need the key of an admin user (--api-key), or force with --allow-admin-tools=true")
	}

	var admin bool
	var username string
	var err error
	if remote {
		admin, username, err = c.remoteIsAdmin(serverURL, apiKey)
	} else {
		admin, username, err = c.localIsAdmin(apiKey)
	}
	if err != nil {
		return err
	}
	if !admin {
		if username == "" {
			return errors.New("admin role required: the API key belongs to an ordinary member")
		}
		return fmt.Errorf("admin role required: %q is not an admin", username)
	}
	return nil
}

// localIsAdmin verifies the key against the local store and reports whether
// its user holds the admin role. An unknown or revoked key is an error
// rather than a silent downgrade.
func (c *mcpConfig) localIsAdmin(apiKey string) (admin bool, username string, err error) {
	store, err := c.openStore()
	if err != nil {
		return false, "", err
	}
	userID, ok, err := store.VerifyAPIKey(apiKey)
	if err != nil {
		return false, "", err
	}
	if !ok {
		return false, "", errors.New("the configured API key does not match any active key in the local store (if this key belongs to a remote server, pass --server)")
	}
	if admin, err = store.IsAdmin(userID); err != nil {
		return false, "", err
	}
	if user, err := store.FindUserByID(userID); err == nil {
		username = user.Username
	}
	return admin, username, nil
}

// remoteIsAdmin resolves the key's identity via GET /api/me on the server.
func (c *mcpConfig) remoteIsAdmin(serverURL, apiKey string) (admin bool, username string, err error) {
	result, err := doJSONRequest(http.MethodGet, serverURL, "/api/me", apiKey, nil)
	if err != nil {
		return false, "", fmt.Errorf("resolving identity from %s: %w", serverURL, err)
	}
	admin, _ = result["isAdmin"].(bool)
	username, _ = result["username"].(string)
	return admin, username, nil
}

// closeStore closes the cached process-lifetime store, if one was opened.
func (c *mcpConfig) closeStore() {
	c.storeMu.Lock()
	defer c.storeMu.Unlock()
	if c.store != nil {
		_ = c.store.Close()
		c.store = nil
	}
}

// runMCP runs the `sth mcp` subcommand: an MCP server over stdio that exposes
// the CLI functionality as tools. The MCP protocol occupies stdout, so this
// command must not write anything there; diagnostics go to stderr.
func runMCP(args []string, _ io.Writer, stderr io.Writer) error {
	cfg, err := parseMCPConfig(args)
	if err != nil {
		return err
	}

	// Without --server the configured key only ever reaches the implicit
	// default (http://127.0.0.1:3939); say so instead of failing silently.
	if cfg.apiKey != "" && cfg.serverURL == "" {
		fmt.Fprintf(stderr, "note: --api-key/STH_API_KEY is set without --server; it is only attached to calls targeting %s (pass --server to change the default)\n", server.DefaultServerURL)
	}

	switch {
	case cfg.adminOverrideSet && cfg.adminOverrideValue:
		fmt.Fprintln(stderr, "note: admin tools forced on (--allow-admin-tools=true); identity checks skipped")
	case cfg.adminOverrideSet:
		fmt.Fprintln(stderr, "note: admin tools forced off (--allow-admin-tools=false); calls will be denied")
	case cfg.apiKey == "":
		fmt.Fprintln(stderr, "note: no --api-key: admin tools denied (configure the key of an admin user, or force with --allow-admin-tools=true)")
	default:
		fmt.Fprintln(stderr, "note: admin tools enforce identity: calls require the API key to belong to an admin user")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	return mcpServe(ctx, cfg, stderr)
}

func mcpServe(ctx context.Context, cfg *mcpConfig, stderr io.Writer) error {
	defer cfg.closeStore()

	s := mcp.NewServer(&mcp.Implementation{Name: "sth", Version: mcpVersion}, nil)
	registerMCPTools(s, cfg)

	fmt.Fprintf(stderr, "sth mcp: serving MCP over stdio (db=%q, server=%q); protocol on stdout, diagnostics here\n", cfg.dbPath, cfg.serverURL)

	return s.Run(ctx, &mcp.StdioTransport{})
}

func registerMCPTools(s *mcp.Server, cfg *mcpConfig) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "send",
		Description: "Upload a local HTML file to an sth server, creating a preview session (or a new version of an existing chain). Sibling web assets in the entry file's directory (or --root) are uploaded with it unless single=true. Mirrors `sth send`. Returns the preview URL. Note: this reads files from this machine — only point it at content meant to be published.",
	}, cfg.toolSend)
	mcp.AddTool(s, &mcp.Tool{
		Name:        "tag",
		Description: "Add or remove tags on a session. Mirrors `sth tag [--rm]`. Operates on the local DB, or on a remote server when server is set; pass local=true to force the local DB.",
	}, cfg.toolTag)
	mcp.AddTool(s, &mcp.Tool{
		Name:        "categorize",
		Description: "Set the category of a session. Mirrors `sth categorize`. Pass local=true to force the local DB.",
	}, cfg.toolCategorize)
	mcp.AddTool(s, &mcp.Tool{
		Name:        "project",
		Description: "Set the project of a session. Mirrors `sth project`. Operates on the local DB, or on a remote server when server is set; pass local=true to force the local DB.",
	}, cfg.toolProject)
	mcp.AddTool(s, &mcp.Tool{
		Name:        "title",
		Description: "Set the display title of a session (max 200 characters). Mirrors `sth title`. Operates on the local DB, or on a remote server when server is set; pass local=true to force the local DB.",
	}, cfg.toolTitle)
	mcp.AddTool(s, &mcp.Tool{
		Name:        "list",
		Description: "List sessions in the local DB with optional tag/category/project filters. Mirrors `sth list`.",
	}, cfg.toolList)
	mcp.AddTool(s, &mcp.Tool{
		Name:        "search",
		Description: "Full-text search over sessions in the local DB (names, titles, tags, category, project). Mirrors `sth search`.",
	}, cfg.toolSearch)
	mcp.AddTool(s, &mcp.Tool{
		Name:        "delete",
		Description: "Soft-delete a session in the local DB. Mirrors `sth delete`.",
	}, cfg.toolDelete)
	mcp.AddTool(s, &mcp.Tool{
		Name:        "share",
		Description: "Grant a user read access to a session (whole session, or a single page via page). Mirrors `sth share`. Operates on the local DB, or on a remote server when server is set; pass local=true to force the local DB.",
	}, cfg.toolShare)
	mcp.AddTool(s, &mcp.Tool{
		Name:        "unshare",
		Description: "Revoke a user's share grant on a session. Mirrors `sth unshare`. Operates on the local DB, or on a remote server when server is set; pass local=true to force the local DB.",
	}, cfg.toolUnshare)
	mcp.AddTool(s, &mcp.Tool{
		Name:        "shares",
		Description: "List the share grants of a session. Mirrors `sth shares`. Operates on the local DB, or on a remote server when server is set; pass local=true to force the local DB.",
	}, cfg.toolShares)
	mcp.AddTool(s, &mcp.Tool{
		Name:        "user_list",
		Description: "List users with their active key counts. Mirrors `sth user list`. Operates on the local DB, or on the connected server when server is set — the remote admin API requires an admin key.",
	}, cfg.toolUserList)
	mcp.AddTool(s, &mcp.Tool{
		Name:        "user_add",
		Description: "Create a user; pass admin=true to create another admin. Mirrors `sth user add`. Operates on the connected database: the local DB by default, the remote instance's database when server is set. Admin capability: calls are denied without an API key; pass an admin user's key or force with --allow-admin-tools=true.",
	}, cfg.toolUserAdd)
	mcp.AddTool(s, &mcp.Tool{
		Name:        "user_issue_key",
		Description: "Issue a new API key for a user. The plaintext key is shown only once. Mirrors `sth user issue-key`. Operates on the connected database: the local DB by default, the remote instance's database when server is set. Admin capability: calls are denied without an API key; pass an admin user's key or force with --allow-admin-tools=true.",
	}, cfg.toolUserIssueKey)
	mcp.AddTool(s, &mcp.Tool{
		Name:        "user_revoke_key",
		Description: "Revoke an active API key by id or unique id prefix. Mirrors `sth user revoke-key`. Operates on the connected database: the local DB by default, the remote instance's database when server is set. Admin capability: calls are denied without an API key; pass an admin user's key or force with --allow-admin-tools=true.",
	}, cfg.toolUserRevokeKey)
	mcp.AddTool(s, &mcp.Tool{
		Name:        "audit",
		Description: "Query the audit log (logins, auth denials, share changes, uploads/deletes — covers every user), or prune old events with prune_days. Mirrors `sth audit`. Operates on the connected database: the local DB by default, the remote instance's database when server is set. Admin capability: calls are denied without an API key; pass an admin user's key or force with --allow-admin-tools=true.",
	}, cfg.toolAudit)
}

// resolveRemote decides between remote-server and local-DB mode for the dual
// mode tools: a per-call server parameter wins over the server-wide default;
// when neither is set the call operates on the local DB. An explicit local
// flag forces local-DB mode even when a server default is configured.
//
// Credentials are host-bound: the server-wide API key is only attached when
// the call targets the configured server (defaulting to
// server.DefaultServerURL). A per-call server override runs without
// credentials unless the caller supplies an explicit per-call key, so a
// prompt-injected call cannot leak the configured key to another host.
func (c *mcpConfig) resolveRemote(serverParam, apiKeyParam string, local bool) (serverURL, apiKey string, remote bool) {
	if local {
		return "", "", false
	}
	serverURL = serverParam
	if serverURL == "" {
		serverURL = c.serverURL
	}
	if serverURL == "" {
		return "", "", false
	}
	if apiKeyParam != "" {
		return serverURL, apiKeyParam, true
	}
	defaultURL := c.serverURL
	if defaultURL == "" {
		defaultURL = server.DefaultServerURL
	}
	if trimSlash(serverURL) == trimSlash(defaultURL) {
		return serverURL, c.apiKey, true
	}
	return serverURL, "", true
}

// trimSlash normalizes a server base URL for host comparison so trailing
// slashes do not silently disable credential attachment.
func trimSlash(url string) string {
	return strings.TrimRight(url, "/")
}

// messageResult is the structured output of mutation tools: one human
// readable line, matching the CLI's stdout wording.
type messageResult struct {
	Message string `json:"message"`
}

// --- send ---

type sendParams struct {
	File     string `json:"file" jsonschema:"path to the HTML entry file to upload"`
	Title    string `json:"title" jsonschema:"display title for the session (max 200 characters)"`
	Tag      string `json:"tag" jsonschema:"comma-separated tags, e.g. \"report,demo\""`
	Category string `json:"category" jsonschema:"category of the session"`
	Project  string `json:"project" jsonschema:"project the session belongs to"`
	Server   string `json:"server,omitempty" jsonschema:"sth server base URL, e.g. http://127.0.0.1:3939 (default: the server's --server, else http://127.0.0.1:3939)"`
	Root     string `json:"root,omitempty" jsonschema:"archive root directory containing the entry file; its web assets are uploaded too"`
	APIKey   string `json:"api_key,omitempty" jsonschema:"API key for authenticated servers (overrides the server-wide --api-key)"`
	Single   bool   `json:"single,omitempty" jsonschema:"upload only the entry file, ignoring sibling assets"`
}

func (c *mcpConfig) toolSend(ctx context.Context, req *mcp.CallToolRequest, in sendParams) (*mcp.CallToolResult, sendResult, error) {
	target, err := resolveUploadTarget(in.File, in.Title, in.Tag, in.Category, in.Project, in.Root, in.Root != "", in.Single)
	if err != nil {
		return nil, sendResult{}, err
	}

	serverURL := in.Server
	if serverURL == "" {
		if c.serverURL != "" {
			serverURL = c.serverURL
		} else {
			serverURL = server.DefaultServerURL
		}
	}
	// Host-bound credentials: the server-wide key only follows the configured
	// default server (defaulting to server.DefaultServerURL), never a per-call
	// override pointing elsewhere. URLs compare without trailing slashes.
	apiKey := in.APIKey
	if apiKey == "" {
		defaultURL := c.serverURL
		if defaultURL == "" {
			defaultURL = server.DefaultServerURL
		}
		if trimSlash(serverURL) == trimSlash(defaultURL) {
			apiKey = c.apiKey
		}
	}

	result, err := uploadSite(target, serverURL, apiKey)
	if err != nil {
		return nil, sendResult{}, err
	}
	return nil, result, nil
}

// --- tag ---

type tagParams struct {
	SessionID string   `json:"session_id" jsonschema:"id of the session to tag"`
	Tags      []string `json:"tags" jsonschema:"tags to add or remove"`
	Remove    bool     `json:"remove,omitempty" jsonschema:"remove the tags instead of adding them (like sth tag --rm)"`
	Server    string   `json:"server,omitempty" jsonschema:"operate against a remote sth server instead of the local DB"`
	APIKey    string   `json:"api_key,omitempty" jsonschema:"API key for the remote server"`
	Local     bool     `json:"local,omitempty" jsonschema:"force local-DB operation even when a --server default is configured"`
}

func (c *mcpConfig) toolTag(ctx context.Context, req *mcp.CallToolRequest, in tagParams) (*mcp.CallToolResult, messageResult, error) {
	if len(in.Tags) == 0 {
		return nil, messageResult{}, errors.New("at least one tag is required")
	}

	serverURL, apiKey, remote := c.resolveRemote(in.Server, in.APIKey, in.Local)
	if remote {
		method := http.MethodPut
		if in.Remove {
			method = http.MethodDelete
		}
		sid, err := pathSeg(in.SessionID)
		if err != nil {
			return nil, messageResult{}, err
		}
		if _, err := doJSONRequest(method, serverURL, "/api/sessions/"+sid+"/tags", apiKey, map[string]any{"tags": in.Tags}); err != nil {
			return nil, messageResult{}, err
		}
	} else {
		store, err := c.openStore()
		if err != nil {
			return nil, messageResult{}, err
		}
		if in.Remove {
			err = store.RemoveTags(in.SessionID, in.Tags...)
		} else {
			err = store.AddTags(in.SessionID, in.Tags...)
		}
		if err != nil {
			return nil, messageResult{}, err
		}
	}

	if in.Remove {
		return nil, messageResult{Message: fmt.Sprintf("Removed tags from %s: %s", in.SessionID, strings.Join(in.Tags, ", "))}, nil
	}
	return nil, messageResult{Message: fmt.Sprintf("Added tags to %s: %s", in.SessionID, strings.Join(in.Tags, ", "))}, nil
}

// --- categorize / project / title ---

type categorizeParams struct {
	SessionID string `json:"session_id" jsonschema:"id of the session"`
	Category  string `json:"category" jsonschema:"category to set"`
	Server    string `json:"server,omitempty" jsonschema:"operate against a remote sth server instead of the local DB"`
	APIKey    string `json:"api_key,omitempty" jsonschema:"API key for the remote server"`
	Local     bool   `json:"local,omitempty" jsonschema:"force local-DB operation even when a --server default is configured"`
}

func (c *mcpConfig) toolCategorize(ctx context.Context, req *mcp.CallToolRequest, in categorizeParams) (*mcp.CallToolResult, messageResult, error) {
	return c.setSessionField(in.SessionID, "category", in.Category, in.Server, in.APIKey, in.Local)
}

type projectParams struct {
	SessionID string `json:"session_id" jsonschema:"id of the session"`
	Project   string `json:"project" jsonschema:"project to set"`
	Server    string `json:"server,omitempty" jsonschema:"operate against a remote sth server instead of the local DB"`
	APIKey    string `json:"api_key,omitempty" jsonschema:"API key for the remote server"`
	Local     bool   `json:"local,omitempty" jsonschema:"force local-DB operation even when a --server default is configured"`
}

func (c *mcpConfig) toolProject(ctx context.Context, req *mcp.CallToolRequest, in projectParams) (*mcp.CallToolResult, messageResult, error) {
	return c.setSessionField(in.SessionID, "project", in.Project, in.Server, in.APIKey, in.Local)
}

type titleParams struct {
	SessionID string `json:"session_id" jsonschema:"id of the session"`
	Title     string `json:"title" jsonschema:"display title to set (max 200 characters)"`
	Server    string `json:"server,omitempty" jsonschema:"operate against a remote sth server instead of the local DB"`
	APIKey    string `json:"api_key,omitempty" jsonschema:"API key for the remote server"`
	Local     bool   `json:"local,omitempty" jsonschema:"force local-DB operation even when a --server default is configured"`
}

func (c *mcpConfig) toolTitle(ctx context.Context, req *mcp.CallToolRequest, in titleParams) (*mcp.CallToolResult, messageResult, error) {
	return c.setSessionField(in.SessionID, "title", in.Title, in.Server, in.APIKey, in.Local)
}

// setSessionField is the shared implementation of categorize/project/title:
// the field name doubles as the remote API path segment and JSON body key.
func (c *mcpConfig) setSessionField(sessionID, field, value, serverParam, apiKeyParam string, local bool) (*mcp.CallToolResult, messageResult, error) {
	serverURL, apiKey, remote := c.resolveRemote(serverParam, apiKeyParam, local)
	if remote {
		sid, err := pathSeg(sessionID)
		if err != nil {
			return nil, messageResult{}, err
		}
		if _, err := doJSONRequest(http.MethodPut, serverURL, "/api/sessions/"+sid+"/"+field, apiKey, map[string]any{field: value}); err != nil {
			return nil, messageResult{}, err
		}
		return nil, messageResult{Message: fmt.Sprintf("Set %s of %s to %s", field, sessionID, value)}, nil
	}

	store, err := c.openStore()
	if err != nil {
		return nil, messageResult{}, err
	}
	switch field {
	case "category":
		err = store.SetCategory(sessionID, value)
	case "project":
		err = store.SetProject(sessionID, value)
	case "title":
		err = store.SetTitle(sessionID, value)
	default:
		return nil, messageResult{}, fmt.Errorf("unknown session field: %s", field)
	}
	if err != nil {
		return nil, messageResult{}, err
	}
	return nil, messageResult{Message: fmt.Sprintf("Set %s of %s to %s", field, sessionID, value)}, nil
}

// --- list / search ---

type listParams struct {
	Tag      string `json:"tag,omitempty" jsonschema:"filter by tag"`
	Category string `json:"category,omitempty" jsonschema:"filter by category"`
	Project  string `json:"project,omitempty" jsonschema:"filter by project"`
	Limit    int    `json:"limit,omitempty" jsonschema:"max number of sessions to return"`
	Offset   int    `json:"offset,omitempty" jsonschema:"number of sessions to skip (requires limit > 0)"`
}

type documentListResult struct {
	Documents []session.DocumentInfo `json:"documents"`
}

func (c *mcpConfig) toolList(ctx context.Context, req *mcp.CallToolRequest, in listParams) (*mcp.CallToolResult, documentListResult, error) {
	filter, err := filterFromParams(in.Tag, in.Category, in.Project, in.Limit, in.Offset)
	if err != nil {
		return nil, documentListResult{}, err
	}

	store, err := c.openStore()
	if err != nil {
		return nil, documentListResult{}, err
	}
	docs, err := store.ListDocuments(filter)
	if err != nil {
		return nil, documentListResult{}, err
	}
	return nil, documentListResult{Documents: nonNilDocuments(docs)}, nil
}

type searchParams struct {
	Query    string `json:"query" jsonschema:"full-text query matched against names, titles, tags, categories and projects"`
	Tag      string `json:"tag,omitempty" jsonschema:"filter by tag"`
	Category string `json:"category,omitempty" jsonschema:"filter by category"`
	Project  string `json:"project,omitempty" jsonschema:"filter by project"`
	Limit    int    `json:"limit,omitempty" jsonschema:"max number of sessions to return"`
	Offset   int    `json:"offset,omitempty" jsonschema:"number of sessions to skip (requires limit > 0)"`
}

func (c *mcpConfig) toolSearch(ctx context.Context, req *mcp.CallToolRequest, in searchParams) (*mcp.CallToolResult, documentListResult, error) {
	filter, err := filterFromParams(in.Tag, in.Category, in.Project, in.Limit, in.Offset)
	if err != nil {
		return nil, documentListResult{}, err
	}

	store, err := c.openStore()
	if err != nil {
		return nil, documentListResult{}, err
	}
	docs, err := store.SearchDocuments(in.Query, filter)
	if err != nil {
		return nil, documentListResult{}, err
	}
	return nil, documentListResult{Documents: nonNilDocuments(docs)}, nil
}

// filterFromParams validates list/search paging parameters with the same
// rules the CLI applies in parseFilterOptions.
func filterFromParams(tag, category, project string, limit, offset int) (session.FilterOptions, error) {
	filter := session.FilterOptions{
		Tag:      tag,
		Category: category,
		Project:  project,
		Limit:    limit,
		Offset:   offset,
	}
	if limit < 0 {
		return session.FilterOptions{}, errors.New("limit must be a non-negative integer")
	}
	if offset < 0 {
		return session.FilterOptions{}, errors.New("offset must be a non-negative integer")
	}
	if offset > 0 && limit <= 0 {
		return session.FilterOptions{}, errors.New("offset requires a positive limit")
	}
	return filter, nil
}

func nonNilDocuments(docs []session.DocumentInfo) []session.DocumentInfo {
	if docs == nil {
		return []session.DocumentInfo{}
	}
	return docs
}

// --- delete ---

type deleteParams struct {
	SessionID string `json:"session_id" jsonschema:"id of the session to delete"`
}

func (c *mcpConfig) toolDelete(ctx context.Context, req *mcp.CallToolRequest, in deleteParams) (*mcp.CallToolResult, messageResult, error) {
	store, err := c.openStore()
	if err != nil {
		return nil, messageResult{}, err
	}
	if err := store.SoftDelete(in.SessionID); err != nil {
		if errors.Is(err, session.ErrSessionNotFound) {
			return nil, messageResult{}, fmt.Errorf("session %q not found", in.SessionID)
		}
		return nil, messageResult{}, err
	}
	return nil, messageResult{Message: fmt.Sprintf("Session %q deleted.", in.SessionID)}, nil
}

// --- share / unshare / shares ---

type shareParams struct {
	SessionID string `json:"session_id" jsonschema:"id of the session to share"`
	Username  string `json:"username" jsonschema:"user to share with"`
	Page      string `json:"page,omitempty" jsonschema:"restrict the grant to a single HTML page path within the session, e.g. report.html"`
	Server    string `json:"server,omitempty" jsonschema:"operate against a remote sth server instead of the local DB"`
	APIKey    string `json:"api_key,omitempty" jsonschema:"API key for the remote server"`
	Local     bool   `json:"local,omitempty" jsonschema:"force local-DB operation even when a --server default is configured"`
}

func (c *mcpConfig) toolShare(ctx context.Context, req *mcp.CallToolRequest, in shareParams) (*mcp.CallToolResult, messageResult, error) {
	serverURL, apiKey, remote := c.resolveRemote(in.Server, in.APIKey, in.Local)
	if remote {
		sid, err := pathSeg(in.SessionID)
		if err != nil {
			return nil, messageResult{}, err
		}
		result, err := doJSONRequest(http.MethodPut, serverURL, "/api/sessions/"+sid+"/shares", apiKey, map[string]any{"user": in.Username, "path": in.Page})
		if err != nil {
			return nil, messageResult{}, err
		}
		return nil, messageResult{Message: sharesSummary("Shared", in.SessionID, sharesFromResult(result))}, nil
	}

	store, err := c.openStore()
	if err != nil {
		return nil, messageResult{}, err
	}
	page, err := normalizeSharePage(in.Page)
	if err != nil {
		return nil, messageResult{}, err
	}

	target, err := store.FindUserByUsername(in.Username)
	if err != nil {
		return nil, messageResult{}, err
	}
	if owner, status, err := store.SessionOwner(in.SessionID); err == nil && status == session.OwnerOwned && owner == target.ID {
		return nil, messageResult{}, errors.New("the owner already has access to this session")
	}
	if page == "" {
		err = store.ShareSession(in.SessionID, target.ID)
	} else {
		err = store.ShareSessionPage(in.SessionID, target.ID, page)
	}
	if err != nil {
		return nil, messageResult{}, err
	}
	return nil, messageResult{Message: shareDoneMessage("Shared", in.SessionID, in.Username, page)}, nil
}

func (c *mcpConfig) toolUnshare(ctx context.Context, req *mcp.CallToolRequest, in shareParams) (*mcp.CallToolResult, messageResult, error) {
	serverURL, apiKey, remote := c.resolveRemote(in.Server, in.APIKey, in.Local)
	if remote {
		sid, err := pathSeg(in.SessionID)
		if err != nil {
			return nil, messageResult{}, err
		}
		user, err := pathSeg(in.Username)
		if err != nil {
			return nil, messageResult{}, err
		}
		q := url.Values{}
		if in.Page != "" {
			q.Set("path", in.Page)
		}
		result, err := doJSONRequestQuery(http.MethodDelete, serverURL, "/api/sessions/"+sid+"/shares/"+user, q, apiKey, nil)
		if err != nil {
			return nil, messageResult{}, err
		}
		return nil, messageResult{Message: sharesSummary("Unshared", in.SessionID, sharesFromResult(result))}, nil
	}

	store, err := c.openStore()
	if err != nil {
		return nil, messageResult{}, err
	}
	page, err := normalizeSharePage(in.Page)
	if err != nil {
		return nil, messageResult{}, err
	}

	target, err := store.FindUserByUsername(in.Username)
	if err != nil {
		return nil, messageResult{}, err
	}
	if err := store.UnshareSessionPage(in.SessionID, target.ID, page); err != nil {
		if errors.Is(err, session.ErrNotShared) {
			return nil, messageResult{}, fmt.Errorf("user %s is not shared on session %s", in.Username, in.SessionID)
		}
		return nil, messageResult{}, err
	}
	return nil, messageResult{Message: shareDoneMessage("Unshared", in.SessionID, in.Username, page)}, nil
}

// sharesSummary summarizes the roster returned by the remote share endpoints,
// mirroring what the CLI prints after a remote share/unshare.
func sharesSummary(verb, sessionID string, shares []session.ShareEntry) string {
	if len(shares) == 0 {
		return fmt.Sprintf("%s %s. No shares remain.", verb, sessionID)
	}
	labels := make([]string, 0, len(shares))
	for _, s := range shares {
		labels = append(labels, s.Username+" ("+s.ScopeLabel()+")")
	}
	return fmt.Sprintf("%s %s. Shares: %s.", verb, sessionID, strings.Join(labels, ", "))
}

func shareDoneMessage(verb, sessionID, username, page string) string {
	if page == "" {
		return fmt.Sprintf("%s %s with %s (whole session).", verb, sessionID, username)
	}
	return fmt.Sprintf("%s %s with %s (page: %s).", verb, sessionID, username, page)
}

type sharesParams struct {
	SessionID string `json:"session_id" jsonschema:"id of the session"`
	Server    string `json:"server,omitempty" jsonschema:"operate against a remote sth server instead of the local DB"`
	APIKey    string `json:"api_key,omitempty" jsonschema:"API key for the remote server"`
	Local     bool   `json:"local,omitempty" jsonschema:"force local-DB operation even when a --server default is configured"`
}

type shareListResult struct {
	Shares []session.ShareEntry `json:"shares"`
}

func (c *mcpConfig) toolShares(ctx context.Context, req *mcp.CallToolRequest, in sharesParams) (*mcp.CallToolResult, shareListResult, error) {
	serverURL, apiKey, remote := c.resolveRemote(in.Server, in.APIKey, in.Local)
	if remote {
		sid, err := pathSeg(in.SessionID)
		if err != nil {
			return nil, shareListResult{}, err
		}
		result, err := doJSONRequest(http.MethodGet, serverURL, "/api/sessions/"+sid+"/shares", apiKey, nil)
		if err != nil {
			return nil, shareListResult{}, err
		}
		return nil, shareListResult{Shares: nonNilShares(sharesFromResult(result))}, nil
	}

	store, err := c.openStore()
	if err != nil {
		return nil, shareListResult{}, err
	}
	shares, err := store.ListSessionShares(in.SessionID)
	if err != nil {
		return nil, shareListResult{}, err
	}
	return nil, shareListResult{Shares: nonNilShares(shares)}, nil
}

func nonNilShares(shares []session.ShareEntry) []session.ShareEntry {
	if shares == nil {
		return []session.ShareEntry{}
	}
	return shares
}

// --- user management ---
//
// All five tools operate on the database they are connected to: with
// --server (or the server default) they drive that server's admin API
// using the configured key; otherwise they act on the local --db store.

type userAddParams struct {
	Name   string `json:"name" jsonschema:"username to create"`
	Admin  bool   `json:"admin,omitempty" jsonschema:"grant the admin role to the new user (admins can mint further admins)"`
	Server string `json:"server,omitempty" jsonschema:"operate against a remote sth server instead of the local DB"`
	APIKey string `json:"api_key,omitempty" jsonschema:"API key for the remote server (must belong to an admin)"`
	Local  bool   `json:"local,omitempty" jsonschema:"force local-DB operation even when a --server default is configured"`
}

type userCreatedResult struct {
	Username string `json:"username"`
	ID       string `json:"id"`
}

func (c *mcpConfig) toolUserAdd(ctx context.Context, req *mcp.CallToolRequest, in userAddParams) (*mcp.CallToolResult, userCreatedResult, error) {
	serverURL, apiKey, remote := c.resolveRemote(in.Server, in.APIKey, in.Local)
	if err := c.requireAdminFor(serverURL, apiKey, remote); err != nil {
		return nil, userCreatedResult{}, err
	}

	if remote {
		result, err := doJSONRequest(http.MethodPost, serverURL, "/api/admin/users", apiKey, map[string]any{"username": in.Name, "admin": in.Admin})
		if err != nil {
			return nil, userCreatedResult{}, err
		}
		id, _ := result["userId"].(string)
		return nil, userCreatedResult{Username: in.Name, ID: id}, nil
	}

	store, err := c.openStore()
	if err != nil {
		return nil, userCreatedResult{}, err
	}
	user, err := store.CreateUser(in.Name)
	if err != nil {
		if errors.Is(err, session.ErrUsernameTaken) {
			return nil, userCreatedResult{}, fmt.Errorf("username %q already taken", in.Name)
		}
		return nil, userCreatedResult{}, err
	}
	if in.Admin {
		if err := store.SetUserRole(in.Name, session.RoleAdmin); err != nil {
			return nil, userCreatedResult{}, err
		}
	}
	return nil, userCreatedResult{Username: user.Username, ID: user.ID}, nil
}

type userIssueKeyParams struct {
	Name   string `json:"name" jsonschema:"username to issue an API key for"`
	Server string `json:"server,omitempty" jsonschema:"operate against a remote sth server instead of the local DB"`
	APIKey string `json:"api_key,omitempty" jsonschema:"API key for the remote server (must belong to an admin)"`
	Local  bool   `json:"local,omitempty" jsonschema:"force local-DB operation even when a --server default is configured"`
}

type issuedKeyResult struct {
	Username string `json:"username"`
	KeyID    string `json:"keyId"`
	APIKey   string `json:"apiKey" jsonschema:"plaintext API key; shown only once, never persisted"`
}

func (c *mcpConfig) toolUserIssueKey(ctx context.Context, req *mcp.CallToolRequest, in userIssueKeyParams) (*mcp.CallToolResult, issuedKeyResult, error) {
	serverURL, apiKey, remote := c.resolveRemote(in.Server, in.APIKey, in.Local)
	if err := c.requireAdminFor(serverURL, apiKey, remote); err != nil {
		return nil, issuedKeyResult{}, err
	}

	if remote {
		name, err := pathSeg(in.Name)
		if err != nil {
			return nil, issuedKeyResult{}, err
		}
		result, err := doJSONRequest(http.MethodPost, serverURL, "/api/admin/users/"+name+"/keys", apiKey, nil)
		if err != nil {
			return nil, issuedKeyResult{}, err
		}
		keyID, _ := result["keyId"].(string)
		plaintext, _ := result["apiKey"].(string)
		return nil, issuedKeyResult{Username: in.Name, KeyID: keyID, APIKey: plaintext}, nil
	}

	store, err := c.openStore()
	if err != nil {
		return nil, issuedKeyResult{}, err
	}
	user, err := store.FindUserByUsername(in.Name)
	if err != nil {
		if errors.Is(err, session.ErrUserNotFound) {
			return nil, issuedKeyResult{}, fmt.Errorf("user %q not found", in.Name)
		}
		return nil, issuedKeyResult{}, err
	}

	plaintext, record, err := store.IssueAPIKey(user.ID)
	if err != nil {
		return nil, issuedKeyResult{}, err
	}
	return nil, issuedKeyResult{Username: user.Username, KeyID: record.ID, APIKey: plaintext}, nil
}

type userRevokeKeyParams struct {
	IDOrPrefix string `json:"id_or_prefix" jsonschema:"API key id, or a unique prefix of one"`
	Server     string `json:"server,omitempty" jsonschema:"operate against a remote sth server instead of the local DB"`
	APIKey     string `json:"api_key,omitempty" jsonschema:"API key for the remote server (must belong to an admin)"`
	Local      bool   `json:"local,omitempty" jsonschema:"force local-DB operation even when a --server default is configured"`
}

func (c *mcpConfig) toolUserRevokeKey(ctx context.Context, req *mcp.CallToolRequest, in userRevokeKeyParams) (*mcp.CallToolResult, messageResult, error) {
	serverURL, apiKey, remote := c.resolveRemote(in.Server, in.APIKey, in.Local)
	if err := c.requireAdminFor(serverURL, apiKey, remote); err != nil {
		return nil, messageResult{}, err
	}

	if remote {
		keyRef, err := pathSeg(in.IDOrPrefix)
		if err != nil {
			return nil, messageResult{}, err
		}
		if _, err := doJSONRequest(http.MethodDelete, serverURL, "/api/admin/keys/"+keyRef, apiKey, nil); err != nil {
			return nil, messageResult{}, err
		}
		return nil, messageResult{Message: fmt.Sprintf("Revoked key matching %q", in.IDOrPrefix)}, nil
	}

	store, err := c.openStore()
	if err != nil {
		return nil, messageResult{}, err
	}
	if err := store.RevokeAPIKey(in.IDOrPrefix); err != nil {
		switch {
		case errors.Is(err, session.ErrAPIKeyAmbiguous):
			return nil, messageResult{}, fmt.Errorf("multiple keys match %q; provide a longer prefix or the full key id", in.IDOrPrefix)
		case errors.Is(err, session.ErrAPIKeyNotFound):
			return nil, messageResult{}, fmt.Errorf("no active key matches %q", in.IDOrPrefix)
		default:
			return nil, messageResult{}, err
		}
	}
	return nil, messageResult{Message: fmt.Sprintf("Revoked key matching %q", in.IDOrPrefix)}, nil
}

type userListParams struct {
	Server string `json:"server,omitempty" jsonschema:"operate against a remote sth server instead of the local DB"`
	APIKey string `json:"api_key,omitempty" jsonschema:"API key for the remote server (must belong to an admin; the remote user list is admin-only)"`
	Local  bool   `json:"local,omitempty" jsonschema:"force local-DB operation even when a --server default is configured"`
}

type mcpUser struct {
	Username   string `json:"username"`
	ID         string `json:"id"`
	Role       string `json:"role"`
	ActiveKeys int    `json:"activeKeys"`
	Created    string `json:"created"`
}

type userListResult struct {
	Users []mcpUser `json:"users"`
}

func (c *mcpConfig) toolUserList(ctx context.Context, req *mcp.CallToolRequest, in userListParams) (*mcp.CallToolResult, userListResult, error) {
	serverURL, apiKey, remote := c.resolveRemote(in.Server, in.APIKey, in.Local)

	if remote {
		// The remote user list rides the admin API: a member key gets the
		// server's 403 surfaced as an error.
		result, err := doJSONRequest(http.MethodGet, serverURL, "/api/admin/users", apiKey, nil)
		if err != nil {
			return nil, userListResult{}, err
		}
		raw, _ := result["users"].([]any)
		out := make([]mcpUser, 0, len(raw))
		for _, item := range raw {
			m, ok := item.(map[string]any)
			if !ok {
				continue
			}
			user := mcpUser{Username: stringField(m, "username"), ID: stringField(m, "userId"), Role: stringField(m, "role")}
			if n, ok := m["activeKeys"].(float64); ok {
				user.ActiveKeys = int(n)
			}
			user.Created = stringField(m, "created")
			out = append(out, user)
		}
		return nil, userListResult{Users: out}, nil
	}

	store, err := c.openStore()
	if err != nil {
		return nil, userListResult{}, err
	}
	users, err := store.ListUsers()
	if err != nil {
		return nil, userListResult{}, err
	}

	out := make([]mcpUser, 0, len(users))
	for _, u := range users {
		keyCount, err := store.CountAPIKeysByUser(u.ID)
		if err != nil {
			return nil, userListResult{}, err
		}
		out = append(out, mcpUser{
			Username:   u.Username,
			ID:         u.ID,
			Role:       u.Role,
			ActiveKeys: keyCount,
			Created:    u.CreatedAt.Format("2006-01-02"),
		})
	}
	return nil, userListResult{Users: out}, nil
}

// --- audit ---

type auditParams struct {
	Action    string `json:"action,omitempty" jsonschema:"filter by action, e.g. login, share, session.create"`
	Actor     string `json:"actor,omitempty" jsonschema:"filter by actor username"`
	Outcome   string `json:"outcome,omitempty" jsonschema:"filter by outcome: ok or denied"`
	Since     string `json:"since,omitempty" jsonschema:"only events on/after this date, YYYY-MM-DD"`
	Limit     int    `json:"limit,omitempty" jsonschema:"max number of events to return"`
	Offset    int    `json:"offset,omitempty" jsonschema:"number of events to skip"`
	PruneDays int    `json:"prune_days,omitempty" jsonschema:"instead of listing, delete events older than this many days"`
	Server    string `json:"server,omitempty" jsonschema:"operate against a remote sth server instead of the local DB"`
	APIKey    string `json:"api_key,omitempty" jsonschema:"API key for the remote server (must belong to an admin)"`
	Local     bool   `json:"local,omitempty" jsonschema:"force local-DB operation even when a --server default is configured"`
}

type mcpAuditEvent struct {
	ID        int64  `json:"id"`
	Time      string `json:"time"`
	Actor     string `json:"actor"`
	ActorID   string `json:"actorId,omitempty"`
	Action    string `json:"action"`
	Target    string `json:"target,omitempty"`
	Outcome   string `json:"outcome"`
	IP        string `json:"ip,omitempty"`
	UserAgent string `json:"userAgent,omitempty"`
	Detail    string `json:"detail,omitempty"`
}

type auditResult struct {
	Events []mcpAuditEvent `json:"events,omitempty"`
	Pruned int64           `json:"pruned,omitempty"`
}

func (c *mcpConfig) toolAudit(ctx context.Context, req *mcp.CallToolRequest, in auditParams) (*mcp.CallToolResult, auditResult, error) {
	serverURL, apiKey, remote := c.resolveRemote(in.Server, in.APIKey, in.Local)
	if err := c.requireAdminFor(serverURL, apiKey, remote); err != nil {
		return nil, auditResult{}, err
	}

	if remote {
		return c.remoteAudit(serverURL, apiKey, in)
	}

	store, err := c.openStore()
	if err != nil {
		return nil, auditResult{}, err
	}
	if in.PruneDays > 0 {
		cutoff := time.Now().UTC().Add(-time.Duration(in.PruneDays) * 24 * time.Hour)
		removed, err := store.PruneAuditEvents(cutoff)
		if err != nil {
			return nil, auditResult{}, err
		}
		return nil, auditResult{Pruned: removed}, nil
	}
	if in.PruneDays < 0 {
		return nil, auditResult{}, errors.New("prune-days must be a positive integer")
	}

	filter, err := auditFilterFromParams(in)
	if err != nil {
		return nil, auditResult{}, err
	}

	events, err := store.ListAuditEvents(filter)
	if err != nil {
		return nil, auditResult{}, err
	}

	out := make([]mcpAuditEvent, 0, len(events))
	for _, e := range events {
		out = append(out, mcpAuditEvent{
			ID:        e.ID,
			Time:      time.Unix(e.TSUnix, 0).Format(time.RFC3339),
			Actor:     e.Actor,
			ActorID:   e.ActorID,
			Action:    e.Action,
			Target:    e.Target,
			Outcome:   e.Outcome,
			IP:        e.IP,
			UserAgent: e.UserAgent,
			Detail:    e.Detail,
		})
	}
	return nil, auditResult{Events: out}, nil
}

// remoteAudit reads (GET /api/admin/audit) or prunes (POST
// /api/admin/audit/prune) the remote server's audit log.
func (c *mcpConfig) remoteAudit(serverURL, apiKey string, in auditParams) (*mcp.CallToolResult, auditResult, error) {
	if in.PruneDays > 0 {
		result, err := doJSONRequest(http.MethodPost, serverURL, "/api/admin/audit/prune", apiKey, map[string]any{"days": in.PruneDays})
		if err != nil {
			return nil, auditResult{}, err
		}
		pruned, _ := result["pruned"].(float64)
		return nil, auditResult{Pruned: int64(pruned)}, nil
	}
	if in.PruneDays < 0 {
		return nil, auditResult{}, errors.New("prune-days must be a positive integer")
	}

	q := url.Values{}
	if in.Action != "" {
		q.Set("action", in.Action)
	}
	if in.Actor != "" {
		q.Set("actor", in.Actor)
	}
	if in.Outcome != "" {
		q.Set("outcome", in.Outcome)
	}
	if in.Since != "" {
		if _, err := time.Parse("2006-01-02", in.Since); err != nil {
			return nil, auditResult{}, errors.New("since must be YYYY-MM-DD")
		}
		q.Set("since", in.Since)
	}
	if in.Limit > 0 {
		q.Set("limit", strconv.Itoa(in.Limit))
	} else if in.Limit < 0 {
		return nil, auditResult{}, errors.New("limit must be a positive integer")
	}
	if in.Offset != 0 {
		if in.Offset < 0 {
			return nil, auditResult{}, errors.New("offset must be a non-negative integer")
		}
		q.Set("offset", strconv.Itoa(in.Offset))
	}

	result, err := doJSONRequestQuery(http.MethodGet, serverURL, "/api/admin/audit", q, apiKey, nil)
	if err != nil {
		return nil, auditResult{}, err
	}

	raw, _ := result["events"].([]any)
	out := make([]mcpAuditEvent, 0, len(raw))
	for _, item := range raw {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		var id float64
		if v, ok := m["id"].(float64); ok {
			id = v
		}
		e := mcpAuditEvent{
			ID: int64(id),
			Time:    stringField(m, "time"),
			Actor:   stringField(m, "actor"),
			ActorID: stringField(m, "actorId"),
			Action:  stringField(m, "action"),
			Target:  stringField(m, "target"),
			Outcome: stringField(m, "outcome"),
			IP:      stringField(m, "ip"),
			Detail:  stringField(m, "detail"),
		}
		out = append(out, e)
	}
	return nil, auditResult{Events: out}, nil
}

// auditFilterFromParams validates the local audit filters (same rules the
// CLI applies).
func auditFilterFromParams(in auditParams) (session.AuditFilter, error) {
	filter := session.AuditFilter{
		Action:  in.Action,
		Actor:   in.Actor,
		Outcome: in.Outcome,
	}
	if in.Limit > 0 {
		filter.Limit = in.Limit
	} else if in.Limit < 0 {
		return session.AuditFilter{}, errors.New("limit must be a positive integer")
	}
	if in.Offset < 0 {
		return session.AuditFilter{}, errors.New("offset must be a non-negative integer")
	}
	filter.Offset = in.Offset
	if in.Since != "" {
		ts, err := time.Parse("2006-01-02", in.Since)
		if err != nil {
			return session.AuditFilter{}, errors.New("since must be YYYY-MM-DD")
		}
		filter.Since = ts.UTC()
	}
	return filter, nil
}
