## ADDED Requirements

### Requirement: Soft delete a session
The system SHALL allow marking a session as deleted by setting a `deleted_at` timestamp on the session record. The `deleted_at` field SHALL be stored as an integer (Unix nanoseconds, nullable) in the `sessions` table. Files on disk SHALL NOT be removed.

#### Scenario: Delete an existing session
- **WHEN** a soft delete is requested for a valid session ID
- **THEN** the system SHALL set `deleted_at` to the current UTC timestamp in Unix nanoseconds

#### Scenario: Delete a non-existent session
- **WHEN** a soft delete is requested for a session ID that does not exist
- **THEN** the system SHALL return `ErrSessionNotFound`

#### Scenario: Delete an already-deleted session
- **WHEN** a soft delete is requested for a session that is already soft-deleted
- **THEN** the system SHALL succeed without error (idempotent)

### Requirement: Filter deleted sessions from listings
The system SHALL exclude soft-deleted sessions from all listing queries. Sessions with a non-null `deleted_at` SHALL NOT appear in the homepage session list, filtered views, or search results.

#### Scenario: List recent sessions
- **WHEN** the system lists recent sessions
- **THEN** only sessions with `deleted_at IS NULL` SHALL be returned

#### Scenario: Search sessions
- **WHEN** a search query is executed
- **THEN** only sessions with `deleted_at IS NULL` SHALL appear in results

#### Scenario: Filter sessions by tag, category, or project
- **WHEN** sessions are filtered by tag, category, or project
- **THEN** only sessions with `deleted_at IS NULL` SHALL be returned

### Requirement: Direct URL access to deleted sessions
The system SHALL allow direct URL access to soft-deleted sessions via `/s/<session-id>/`. The `Get` method SHALL NOT filter by `deleted_at`.

#### Scenario: Access a soft-deleted session by direct URL
- **WHEN** a user navigates to `/s/<session-id>/` for a soft-deleted session
- **THEN** the system SHALL serve the session's content as normal

### Requirement: Delete session via HTTP API
The system SHALL expose a `DELETE /api/sessions/:id` endpoint that soft-deletes the specified session.

#### Scenario: Successful HTTP delete
- **WHEN** a `DELETE /api/sessions/:id` request is sent for an existing session
- **THEN** the system SHALL return HTTP 200 with a JSON body confirming deletion

#### Scenario: HTTP delete for non-existent session
- **WHEN** a `DELETE /api/sessions/:id` request is sent for a non-existent session
- **THEN** the system SHALL return HTTP 404 with an error message

### Requirement: Delete session via CLI
The system SHALL provide a `sth delete <session-id>` CLI command that soft-deletes a session. The command SHALL accept an optional `--db` flag for specifying the database path.

#### Scenario: Successful CLI delete
- **WHEN** `sth delete <session-id>` is executed for an existing session
- **THEN** the system SHALL print a confirmation message and exit with code 0

#### Scenario: CLI delete for non-existent session
- **WHEN** `sth delete <session-id>` is executed for a non-existent session
- **THEN** the system SHALL print an error message and exit with a non-zero code

#### Scenario: CLI delete with custom database
- **WHEN** `sth delete <session-id> --db /path/to/sessions.db` is executed
- **THEN** the system SHALL use the specified database file

### Requirement: Schema migration for deleted_at column
The system SHALL automatically add a `deleted_at` column (INTEGER, nullable, default NULL) to the `sessions` table on startup if the column does not already exist.

#### Scenario: Fresh database
- **WHEN** the database is created for the first time
- **THEN** the `sessions` table SHALL include the `deleted_at` column

#### Scenario: Existing database without column
- **WHEN** the database exists but lacks the `deleted_at` column
- **THEN** the system SHALL add the column without data loss
