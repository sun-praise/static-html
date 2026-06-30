# Changelog

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
