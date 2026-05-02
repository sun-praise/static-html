## 1. Database Schema

- [ ] 1.1 Add `deleted_at` column detection in `ensureColumns()` in `internal/session/store.go` — check for missing column and add `ALTER TABLE sessions ADD COLUMN deleted_at INTEGER DEFAULT NULL`
- [ ] 1.2 Verify migration works with existing databases by running the test suite

## 2. Store Layer

- [ ] 2.1 Add `SoftDelete(id string) error` method to `Store` in `internal/session/store.go` — set `deleted_at = UnixNano()` for the given session, return `ErrSessionNotFound` if session doesn't exist, idempotent for already-deleted sessions
- [ ] 2.2 Add `WHERE s.deleted_at IS NULL` condition to `ListDocuments` query in `internal/session/metadata.go`
- [ ] 2.3 Add `AND s.deleted_at IS NULL` condition to `SearchDocuments` query in `internal/session/metadata.go`
- [ ] 2.4 Write unit tests for `SoftDelete` in `internal/session/store_test.go` — test success case, non-existent session, idempotent double-delete
- [ ] 2.5 Write unit tests verifying deleted sessions are excluded from `ListDocuments` and `SearchDocuments` results

## 3. HTTP API

- [ ] 3.1 Add `DELETE /api/sessions/:id` route in `routes()` method in `internal/server/server.go`
- [ ] 3.2 Implement `handleDeleteSession` handler — extract session ID, call `store.SoftDelete`, return 200 on success, 404 if not found
- [ ] 3.3 Write HTTP handler test for the delete endpoint in `internal/server/server_test.go` — test 200 success, 404 not found, idempotent 200

## 4. CLI

- [ ] 4.1 Add `delete` command case in `Run()` switch in `internal/cli/cli.go`
- [ ] 4.2 Implement `runDelete` function — parse `--db` flag and session ID positional arg, call store's `SoftDelete`, print confirmation to stdout, return error for non-existent session
- [ ] 4.3 Update `printUsage` to include `sth delete <session-id> [--db /path/to/sessions.db]`
- [ ] 4.4 Write CLI test for delete command in `internal/cli/cli_test.go`

## 5. Integration Verification

- [ ] 5.1 Run full test suite (`go test ./...`) to confirm no regressions
- [ ] 5.2 Manual smoke test: create session, delete it, verify it disappears from homepage but remains accessible via direct URL
