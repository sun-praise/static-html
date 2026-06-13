## ADDED Requirements

### Requirement: Peers API endpoint
The system SHALL provide a `GET /api/sessions/{id}/peers` endpoint that returns documents sharing the same category or project as the specified session.

#### Scenario: Successful peers query
- **WHEN** a GET request is sent to `/api/sessions/abc123/peers` for a session with category "ui" and project "my-app"
- **THEN** the system SHALL return HTTP 200 with JSON containing `current` (the session's own info), `byCategory` (array of sessions sharing the same category), and `byProject` (array of sessions sharing the same project)

#### Scenario: Session not found
- **WHEN** a GET request is sent to `/api/sessions/nonexistent/peers`
- **THEN** the system SHALL return HTTP 404 with an error message

#### Scenario: Deleted session
- **WHEN** a GET request is sent for a soft-deleted session's peers
- **THEN** the system SHALL return HTTP 404

### Requirement: Peers by category
The `byCategory` field SHALL contain all non-deleted sessions that share the same `category` value as the specified session, excluding the session itself. Each entry SHALL include `sessionId`, `name`, and `createdAt`.

#### Scenario: Multiple sessions share category
- **WHEN** sessions A, B, C all have category "ui" and session A's peers are queried
- **THEN** `byCategory` SHALL contain B and C

#### Scenario: No category set
- **WHEN** the specified session has no category
- **THEN** `byCategory` SHALL be an empty array

#### Scenario: No other sessions share the category
- **WHEN** only the specified session has category "unique-cat"
- **THEN** `byCategory` SHALL be an empty array

#### Scenario: Deleted sessions excluded
- **WHEN** a soft-deleted session shares the same category
- **THEN** that session SHALL NOT appear in `byCategory`

### Requirement: Peers by project
The `byProject` field SHALL contain all non-deleted sessions that share the same `project` value as the specified session, excluding the session itself. Each entry SHALL include `sessionId`, `name`, and `createdAt`.

#### Scenario: Multiple sessions share project
- **WHEN** sessions A, B, C all have project "my-app" and session A's peers are queried
- **THEN** `byProject` SHALL contain B and C

#### Scenario: No project set
- **WHEN** the specified session has no project
- **THEN** `byProject` SHALL be an empty array

#### Scenario: Deleted sessions excluded
- **WHEN** a soft-deleted session shares the same project
- **THEN** that session SHALL NOT appear in `byProject`

### Requirement: Peers result ordering
Both `byCategory` and `byProject` arrays SHALL be ordered by creation time descending (most recent first).

#### Scenario: Ordering verified
- **WHEN** sessions B (created Jan 1), C (created Jan 3), D (created Jan 2) share a category with A
- **THEN** `byCategory` SHALL list C, D, B in that order

### Requirement: Peers result limit
Each peer group (`byCategory`, `byProject`) SHALL return at most 20 entries.

#### Scenario: More than 20 peers exist
- **WHEN** 30 sessions share the same category
- **THEN** `byCategory` SHALL contain exactly 20 entries (the 20 most recent)

### Requirement: Peers result deduplication
A session that appears in both `byCategory` and `byProject` SHALL appear in both arrays. No deduplication across groups.

#### Scenario: Session in both groups
- **WHEN** session B shares both category and project with session A
- **THEN** session B SHALL appear in both `byCategory` and `byProject`
