## Why

Sessions created in the system are permanent — once created, they remain in the database and appear in all listings and search results indefinitely. Users have no way to remove clutter or hide obsolete sessions. This makes the session list noisy and hard to navigate over time.

## What Changes

- Add a `deleted_at` column (DATETIME, nullable) to the `sessions` table for soft-delete tracking
- New API endpoint `DELETE /api/sessions/:id` that sets `deleted_at` to the current timestamp
- All list and search queries will filter out soft-deleted sessions (`WHERE deleted_at IS NULL`)
- New CLI command `sth delete <session-id>` to soft-delete from the terminal
- Soft-deleted sessions remain accessible via direct URL (optional behavior)

## Capabilities

### New Capabilities
- `session-soft-delete`: Soft-delete mechanism for sessions — marking sessions as deleted via `deleted_at` timestamp, filtering them from listings and search, while preserving data on disk

### Modified Capabilities

## Impact

- **Database**: Schema migration adding `deleted_at` column to `sessions` table
- **API**: New `DELETE /api/sessions/:id` endpoint; existing `GET /api/sessions` and search endpoints change behavior (filtered results)
- **CLI**: New `sth delete` subcommand
- **Code**: `internal/session/store.go` (queries, new delete method), `internal/server/server.go` (new handler, query filters), `internal/cli/cli.go` (new command)
