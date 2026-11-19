# Changelog

## [2.8.0] - 2026-11-19

### Added
- Per-reader HTML annotations on preview pages (Closes #106): select text on any preview → bubble offers 「划线」(underline) or 「批注」(note); underlines take an optional tag, notes a comment — both stored server-side per session chain, so every version of a document shows the same annotation set and quotes re-anchor against the rendered version (quotes that no longer exist are flagged 「未在当前页面找到原文」 instead of breaking)
- Annotation storage: `session_annotations` SQLite table (FK → sessions with CASCADE; soft-deleted sessions keep their data), scoped to the session **chain** with per-reader `owner_id` isolation — every viewer sees and manages only their own annotations; auth-disabled deployments share one pool. Chain-scoped inserts/lists/deletes, nanosecond timestamps keep insertion order = reading order
- Annotation API: `GET/POST /api/sessions/{id}/annotations`, `DELETE /api/sessions/{id}/annotations/{aid}` — reads via `requireViewer`, deletes owner-only; with `--auth`, mutations authenticate with a Bearer key **or** the same-origin login cookie (CSRF-safe, same path as the share panel), so the browser UI works without exposing keys; request bodies capped at 64 KiB
- Preview-page annotation UI: amber ✎ round button (with count badge) joins the existing bottom-right button stack; margin cards numbered by document order (hidden ≤1180px); drawer listing all annotations with locate / delete / Markdown export (title / source / quote / comment / tag / time — clipboard copy with `execCommand` fallback, `.md` download). Highlights render via the CSS Custom Highlight API — zero DOM changes; re-anchoring prefers occurrences whose surrounding text matches the stored prefix/suffix anchors
- `endPointAt` extends a highlight one character past the last mapped position, so quotes ending at the very end of the document highlight fully
- Admin HTTP API: `GET/POST /api/admin/users`, `PUT /api/admin/users/{name}/role`, `POST /api/admin/users/{name}/keys`, `DELETE /api/admin/keys/{idOrPrefix}`, `GET /api/admin/audit`, `POST /api/admin/audit/prune` — Bearer key must belong to an admin-role user (member → 403 "admin role required", missing/invalid key → 401); every mutation is recorded in the audit log with the acting admin as actor

### Changed
- **Behavior change**: MCP admin tools (`user_add` / `user_issue_key` / `user_revoke_key` / `user_list` / `audit`) now operate on the database of the **connected instance** — with `--server` they call that instance's admin HTTP API (mutations land in the remote store and remote audit log); without `--server` they keep operating on the local `--db` store. The 2.7.0 semantics (remote admin identity trusted for the local store) is gone: pointing admin tools at a server can no longer touch the MCP process's local database

### Fixed
- Round-button residue on single-version sessions: the drawer stylesheet's `display:flex !important` overrode the script's plain inline `display` toggles — all script-side display switching now uses `setProperty(..., 'important')` (also fixes buttons not hiding while the drawer is open)

## [2.7.0] - 2026-11-19

### Added
- MCP server support: `sth mcp [--db ...] [--server ...] [--api-key ...]` runs a Model Context Protocol server over stdio exposing the CLI functionality as up to 16 tools (4 admin-gated, see below) (`send`, `tag`, `categorize`, `project`, `title`, `list`, `search`, `delete`, `share`, `unshare`, `shares`, `user_add`, `user_issue_key`, `user_revoke_key`, `user_list`, `audit`). Dual-mode tools (tag/categorize/project/title/share family) keep the CLI's local-DB vs remote-server semantics; local-only tools (`list`/`search`/`delete`/`user_*`/`audit`) operate on the `--db` store. Long-running commands (`start`, `watch`) stay CLI-only. Built on the official `modelcontextprotocol/go-sdk` v1.0.0 (requires go ≥ 1.23, compatible with the existing go 1.24 CI)
- MCP credentials are host-bound: the server-wide `--api-key` is attached only to calls targeting the configured `--server`; a per-call `server` override runs keyless unless it passes its own `api_key`. Dual-mode tools accept `local: true` to force local-DB operation even when a `--server` default is configured
- MCP admin-class tools (`user_add` / `user_issue_key` / `user_revoke_key` / `audit`) are enabled by default and enforced per call: with an API key configured, each call requires it to resolve to a user with the admin role — locally against the store, or via the new `GET /api/me` on `--server`; member or invalid keys get an "admin role required" error. **Upgrade action**: without any key, calls are denied (no identity is not admin) — setups relying on the old key-less operator access should set `STH_ALLOW_ADMIN_TOOLS=true` or configure an admin key. `--allow-admin-tools=true|false` / `STH_ALLOW_ADMIN_TOOLS` (`true|1|false|0`; an unparseable value stops `sth mcp` from starting) forces the outcome regardless of identity; admin tools always operate on the local `--db` store, so with `--server` the remote admin identity is trusted for the local store; read-only `user_list` stays open
- Users gain a role column: `sth user add <name> [--admin]`, `sth user promote|demote <name>` manage the admin role (`GET /api/me` reports it); `user_add` MCP tool accepts `admin=true` for admins minting admins, and `user_list` reports roles

## [2.6.0] - 2026-11-18

### Added
- Home-page category/tag facets with three switchable layouts: `blocks`（分类/标签计数 chip 行，默认）、`sidebar`（左侧 facet 列表 + 右侧文档列表）、`overview`（Sessions/Categories 双视图，分类卡片带最近文档预览）。右上角下拉切换，选择存入 `sth_layout` cookie，`?layout=` 可显式覆盖
- Facet 计数互为 facet：分类计数不受当前分类过滤影响（标签反之），tag/project/owner 与搜索词仍会收缩计数；「未分类」桶统计无分类文档、可点击过滤（过滤 URL 使用哨兵值 `__uncategorized__`，不会遮蔽同名真实分类）
- Overview 布局的 Categories 视图（`?view=categories`）按分类出卡片，每卡含计数与最近 3 篇文档，点击卡片即按该分类过滤；卡片最近文档由单条窗口函数查询取回，请求数与分类数量无关
- 搜索态 facet 计数从合并结果集（元数据 + 文件内容命中）推导，避免「全部 0」chip 与实际结果列表不一致
- Store 层新增 `CategoryFacets` / `TagFacets` / `RecentPerCategory` 查询（跟随 owner 与软删标记；搜索词只匹配元数据）

## [2.5.0] - 2026-11-16

### Added
- Session titles: `sth send --title <title>` (required) binds a human-readable display title to each session. Everywhere sessions are listed — home page, `sth list`, `sth search`, related-documents drawer — shows the title, falling back to the entry-file basename for legacy sessions
- `sth title <session-id> <title>` to rename a session after the fact (set-only, mirrors categorize/project)
- `PUT /api/sessions/<id>/title`; session-create payloads (JSON and multipart) accept a `title` field — optional at the API level so older CLI clients keep working
- Full-text search (`sth search` and home-page search) now matches titles; `DocumentInfo`, `DocumentMetadata`, and peers payloads include `title`

### Changed
- **Behavior change**: `sth send` now requires `--title`. Existing scripts/CI invoking `sth send` must add the flag; the HTTP API accepts `title` optionally, so only CLI callers are affected. Titles are capped at 200 characters

## [2.4.0] - 2026-11-13

### Added
- Security audit log (`audit_events` table): login success/failure, logout, rejected credentials, share grant/revoke, preview allow/deny, session create/delete — each with actor snapshot, client IP, User-Agent and outcome. Local-only CLI `sth audit` (`--action`/`--actor`/`--outcome`/`--since`/`--limit`/`--offset`, `--prune-days`); no HTTP endpoint by design, since the server has no admin role and an API would expose everyone's activity to any logged-in user. The CLI opens the DB read-write for schema init but never changes business data
- `--audit-retention-days` / `STH_AUDIT_RETENTION_DAYS` (default 90, `0` disables): prune once at start and every 24h, with the prune goroutine joined on shutdown. Passwords, API keys, session cookies, request bodies and query strings are never recorded; failed logins do not persist the submitted username at all; anonymous 401s are coalesced per proxy-observed peer (`RemoteAddr` host) + path in a 60s window with a hard cap (client-controlled `X-Forwarded-For` is stored only as evidence, never used as the throttle key) and anonymous `/logout` writes nothing. Only HTML page views are audited (render assets and websockets skipped). Client IP comes from the first `X-Forwarded-For` entry and is evidence, not proof. Local CLI commands that write directly to the DB (`sth delete`, `user add`, key issue/revoke) are not audited

## [2.3.0] - 2026-11-10

### Added
- Per-user session sharing, whole-session or single-page scoped: `sth share <id> <user> [--page <path>]` / `sth unshare` / `sth shares`, or `GET/PUT/DELETE /api/sessions/{id}/shares` (PUT body `{"user","path"}`, DELETE `?path=`). A page grant opens exactly that HTML page (sibling pages and the session root stay 403) plus the session's render assets — css/js/images/fonts/media, by extension whitelist — so the page renders; other files in the session (JSON payloads, text dumps, PDFs, archives) need a whole-session grant
- Home-page share panel (👥): view/add/revoke a session's share roster in the browser, optionally scoped to a page. Share-management mutations accept the login cookie in addition to Bearer keys — CSRF stays structurally impossible (SameSite=Lax cookie + Origin check; all other mutating endpoints remain Bearer-only)
- "Shared with you" section on the home page; page-scoped shares link straight to the granted page and are labelled with its path
- New `session_shares` table (composite PK `session_id, user_id, path`, indexed by user) with store methods `ShareSession(·Page)`, `UnshareSession(·Page)`, `ListSessionShares`, `HasShareOn`, `HasAnyShare`, `IsSessionSharedWith`, `ListSharedWithUser`
- Shared users can read the preview drawer data (`/metadata`, `/peers`, `/chain`, `/diff`) for shared sessions; edit/delete/download/share management stay owner-only. The HTML-diff endpoint requires a whole-session grant (its payload is the full text of two versions, which a page grant does not cover)
- Page grants can be revoked with a non-canonical path (`?path=./report.html`) and even after the page is gone from disk: revocation only normalizes, it does not require the file to exist
- `sth share --page` normalizes the path locally (`./x.html` → `x.html`) and rejects escaping or non-HTML paths instead of storing a grant that could never match
- Remote mode for `sth tag/categorize/project/share/unshare/shares` now attaches the API key (`--api-key` / `STH_API_KEY`), fixing 401s against authenticated servers

### Changed
- **`--protect-previews` semantics tightened**: an owned session's preview (`/s/<id>/` and `/s/<id>/ws`) is now visible only to its owner and the users it is shared with — previously any valid key worked. Sessions without an owner (created before auth) remain visible to any logged-in user

### Removed
- OpenSpec workflow artifacts: `openspec/` directory, `.claude`/`.opencode` OpenSpec skills and slash commands, and the `openspec` entry in `.dockerignore` (historical CHANGELOG references kept)

## [2.2.0] - 2026-10-16

### Added
- Self-hosted Umami analytics on the home page and `/s/` preview pages (preview pages via the `InjectMiddleware` snippet path; home page via the template). Injection is gated server-side on the public host (`sth.sun-praise.com`) — local dev and intranet instances never load the tracker at all. The tag's `data-domains` attribute is a second, tracker-side reporting guard. Login/register pages intentionally carry no tracker (credential-page hygiene). Shared tag/host-gate live in the new `internal/analytics` package.

## [2.1.0] - 2026-07-14

### Added
- Browser-friendly username/password authentication on top of the existing API-key system: sign in via a web form instead of a bare 401 (#82)
  - `GET/POST /login` — sign in with username + password
  - `GET/POST /register` — self-service sign-up (open by default, closeable via `--allow-registration=false` / `STH_ALLOW_REGISTRATION=false`)
  - `POST /logout` — invalidate the session cookie
- Homepage shows a top-right "Signed in as `<name>`" + "Sign out" control for logged-in browser users (auth-enabled only) (#82)
- `Store.FindUserByID` to resolve the username from the authenticated user id in the request context (#82)

### Security
- Passwords are bcrypt-hashed in a new `user_credentials` table; API keys keep their SHA-256 path — the two are independent by entropy profile (#82)
- Server-side session cookies (new `login_sessions` table) store only `SHA-256(token)`; a DB leak does not yield live cookies. Cookies are `HttpOnly` + `SameSite=Lax` + `Secure` (HTTPS) (#82)
- CSRF is structural, not token-based: mutating methods accept ONLY a Bearer key and ignore the cookie, so cross-site writes can never authenticate. No CSRF-token library introduced (#82)
- Unknown user and wrong password return the same generic error (no user enumeration) (#82)
- Open-redirect safe: the `?next=` / `?return=` parameter is validated to start with a single `/` (rejects `//host`, `https://...`, `/\`) (#82)

### Changed
- `--auth` enabled GET requests without credentials redirect browsers to `/login?next=<path>` (instead of a bare 401); non-HTML clients (curl, CLI) still receive `401 + WWW-Authenticate: Bearer` (#82)
- Dependency: `golang.org/x/crypto v0.35.0` (pinned, requires go>=1.23) for bcrypt — deliberately kept below v0.54.0 so the project's `go 1.24.1` directive is not forced upward (#82)

### Docs
- README repositioned as an "agent-published, human-viewed" HTML registry: coding agents publish HTML via the CLI, humans browse/search/share it in the browser (#81)

## [2.0.0] - 2026-07-10

### Added
- Optional API-key authentication (`--auth` / `STH_AUTH`, default off for backward compatibility). When enabled, all mutating endpoints and list/search/peers/download require a valid `Authorization: Bearer <key>` header (#78)
- `users` and `api_keys` tables; API keys are stored only as salted SHA-256 hashes (never plaintext) (#78)
- Session ownership (`sessions.user_id`): each session is owned by its creator; users can only read/modify/download their own sessions under auth (#78)
- `--protect-previews` to additionally gate `/s/<id>/` previews behind a key (implies `--auth`) (#78)
- `sth user` subcommand: `add`, `issue-key`, `revoke-key`, `list` for local user/API-key management (#78)
- CLI credential passing for `sth send` / `sth watch` via `--api-key` or `STH_API_KEY` (flag wins over env) (#78)

### Security
- `RevokeAPIKey` escapes LIKE wildcards and runs count+update in a transaction (TOCTOU-safe); fails closed on ambiguous or too-short prefixes (#78)
- Session-not-found returns 404 (not 403) under auth, so clients distinguish "missing" from "forbidden" (#78)
- Store/query failures during key verification return 500, not 401, so DB outages aren't misreported as bad credentials (#78)
- `send-file.py` passes the API key via child environment, not argv, to avoid exposure in process listings (#78)
- `idx_sessions_user_id` for owner-scoped query performance (#78)

### Changed
- **BREAKING** (opt-in): with `--auth` enabled, requests without a valid API key are rejected (401); non-owners get 403 on others' sessions (#78)

### Docs
- README: new Authentication section (#78)
- `docker-compose.yml` exposes `STH_AUTH` / `STH_PROTECT_PREVIEWS` to the server container (`STH_API_KEY` intentionally excluded as it is a client credential) (#78)
- Refresh OpenSpec Claude tooling to v1.5.0 (#77)

## [1.6.0] - 2026-07-08

### Added
- `sth send` packaging flags: `--single` (upload only the entry file) and `--root <dir>` (archive relative to a directory) (#75)

### Changed
- `send-file.sh` skill helper ported to Python (stdlib only), preserving behavior (#76)

## [1.5.0] - 2026-06-30

### Fixed
- Upload files lost on every container rebuild: uploaded session snapshots were written to the ephemeral container layer, so `docker compose pull && up -d` destroyed all files while metadata survived. Added `--upload-root` flag (symmetric with `--db`); `docker-compose.yml` now sets `--upload-root /data/uploads` so uploads persist on the `sth_data` volume (#70)

### Added
- Daily backup now snapshots the uploads tree alongside `sessions.db` into `/backup/<date>/uploads/`, with a session-count sanity check. New env knobs `STH_UPLOADS_PATH` and `STH_BACKUP_UPLOADS=0` (#73)

## [1.4.0] - 2026-06-30

### Added
- Container deployment support: Dockerfile, docker-compose.yml, and `:latest` image publish workflow
- Reverse-proxy public port resolution for clean URLs behind nginx (#69)
- Deploy documentation with nginx reverse-proxy example and systemd backup units

### Docs
- Add live demo link to READMEs (#67)

### Chore
- OpenSpec proposal for `public-url` / reverse-proxy port resolution (#68)
- CI publishes `:latest` Docker tag on default branch

## [1.3.0] - 2026-06-13

### Added
- Realtime HTML update via WebSocket — preview pages now reflect file changes live (#61)
- `sth watch` subcommand for file-system driven reloads
- Inject related-documents drawer into preview pages (#62)
- `internal/live` package: WebSocket hub, file watcher, client, handler, and injection helpers
- `internal/session/metadata` module for richer session metadata handling

### Fixed
- Skip directory walk for single file uploads to avoid spurious failures (#57)

### Docs
- Add FAQ entry for `sth send` upload failure on large directories

### Chore
- Add OpenSpec specs for realtime-html-update, preview-drawer, and peers-api
- Add `.claude/settings.local.json`

## [1.2.0] - 2026-05-15

### Added
- Support separate bind address and server name (`--bind` / `--server-name`)
- Bind to 0.0.0.0 and display all access URLs on startup
- Download and share buttons to web UI
- Pagination support to list, search and web UI
- Delete button to homepage session list
- Make tag, category, project required when creating sessions
- Chinese README, badges, and Apache 2.0 license
- Link check CI workflow

### Fixed
- ServerName validation with allowlist and IPv6 rejection
- Remove dead loop in extractZIPArchive
- Preserve URL hash fragment in buildClearURL
- Add readBodySafe alternative that returns []byte{} on error
- Escape LIKE wildcards in SearchDocuments
- Multi-tag support bugs (#29)
- Skip permission-denied directories in WalkDir (#27)
- Filter non-web files from upload archive to prevent multipart parse failure
- Improve file content search with size limit
- Fix sth send error message format
- Default to skipping git pull in bootstrap-repo.sh

### Changed
- Refactor buildPageURL and buildClearURL to use url.URL struct
- Rename skill directory from static-html-preview to sth

## [1.1.0] - 2026-05-02

### Added
- Session soft-delete support (#17)

## [1.0.0] - 2026-05-02

Initial release.
