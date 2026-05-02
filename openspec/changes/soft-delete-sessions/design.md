## Context

The static-html server stores sessions in a SQLite database. The `sessions` table has no deletion mechanism — sessions persist forever in listings and search results. The store layer (`internal/session/store.go`) handles all DB access, the server layer (`internal/server/server.go`) exposes HTTP routes, and the CLI (`internal/cli/cli.go`) provides terminal commands. SQLite is accessed via `database/sql` with `go-sqlite3`.

Schema migrations use the `ensureColumns()` pattern in `store.go` — checking `PRAGMA table_info` and conditionally `ALTER TABLE` to add missing columns. This provides zero-downtime forward migration without a separate migration tool.

## Goals / Non-Goals

**Goals:**
- Mark sessions as deleted via a `deleted_at` timestamp in the database
- Hide soft-deleted sessions from homepage listing, search results, and filtered views
- Provide a `DELETE /api/sessions/:id` HTTP endpoint
- Provide a `sth delete <session-id>` CLI command
- Preserve all data on disk — no file removal

**Non-Goals:**
- Hard delete / purge of session data or files
- Restore/undo of soft-deleted sessions
- Admin view to list soft-deleted sessions
- Direct-URL access control for deleted sessions (they remain accessible via `/s/<id>/`)

## Decisions

**1. Store `deleted_at` as integer (Unix nanoseconds) in the `sessions` table**

Use the same `INTEGER` pattern as `created_at_unix`. SQLite has no native datetime type, and the existing codebase uses `UnixNano()` for timestamps. A nullable integer column (`DEFAULT NULL`) fits naturally.

*Alternative considered:* Separate TEXT column with ISO-8601 — rejected because it breaks the existing timestamp convention and complicates range queries.

**2. Filter at the SQL level with `WHERE deleted_at IS NULL`**

Add the filter directly in `ListDocuments` and `SearchDocuments` SQL queries. This is the simplest and most reliable approach — no risk of a code path forgetting to filter.

`Get(id)` will NOT filter by `deleted_at` — direct URL access to deleted sessions remains possible.

**3. Schema migration via `ensureColumns()`**

Add `deleted_at` column detection to the existing `ensureColumns()` method, using `ALTER TABLE sessions ADD COLUMN deleted_at INTEGER DEFAULT NULL`. This follows the established pattern and is backwards-compatible.

**4. New `SoftDelete(id string) error` method on Store**

Returns `ErrSessionNotFound` if the session doesn't exist. Sets `deleted_at` to `time.Now().UTC().UnixNano()`. No-op if already soft-deleted (idempotent).

**5. Route pattern: `DELETE /api/sessions/:id`**

Use the existing `extractSessionIDFromMetaPath` helper to parse the session ID from the URL. Return 404 if session not found, 200 on success (idempotent — deleting an already-deleted session returns 200).

**6. CLI: `sth delete <session-id> [--db path]`**

Follows the same pattern as `sth tag` and `sth categorize`. Accepts `--db` flag for custom DB path. Prints confirmation message to stdout.

## Risks / Trade-offs

- **Idempotent deletes mask errors** → A typo in the session ID silently returns 200. Acceptable since the CLI prints the ID for verification.
- **No restore mechanism** → If a user accidentally deletes, there's no UI to undo. Mitigation: the data is still on disk and `deleted_at` can be manually cleared in SQLite.
- **`deleted_at IS NULL` added to all list/search queries** → Slight query overhead. Negligible for SQLite at this scale, and an index on `deleted_at` would only help if a large fraction of sessions are deleted.
