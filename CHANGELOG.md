# Changelog

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
