## ADDED Requirements

### Requirement: Incremental file update endpoint
The system SHALL provide a PUT endpoint at `/api/sessions/:id/files` that accepts multipart form data to update files in an existing session.

#### Scenario: Update existing file in session
- **WHEN** a client sends a PUT request to `/api/sessions/:id/files` with multipart form data containing one or more files
- **AND** the session exists
- **THEN** the server SHALL overwrite the corresponding files in the session's root directory
- **AND** return HTTP 200 with `{"status": "ok", "files_updated": <count>}`

#### Scenario: Add new file to session
- **WHEN** a client sends a PUT request with a file that does not exist in the session directory
- **THEN** the server SHALL create the file in the correct relative path within the session directory
- **AND** trigger a reload notification to all connected WebSocket clients

#### Scenario: Session not found
- **WHEN** a client sends a PUT request to `/api/sessions/:id/files` for a non-existent session
- **THEN** the server SHALL return HTTP 404

#### Scenario: File size limit
- **WHEN** an uploaded file exceeds 50MB
- **THEN** the server SHALL return HTTP 413 with an error message

### Requirement: Incremental update triggers live reload
When files are updated via the incremental update endpoint, the system SHALL notify connected WebSocket clients.

#### Scenario: Update triggers reload
- **WHEN** files are successfully updated via `/api/sessions/:id/files`
- **THEN** the system SHALL send a `{"type":"reload"}` message to all WebSocket clients connected to that session
