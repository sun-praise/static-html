# Changelog

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
