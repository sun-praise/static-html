## ADDED Requirements

### Requirement: WebSocket endpoint for session live reload
The system SHALL provide a WebSocket endpoint at `/s/<sessionId>/ws` that accepts upgrade requests from browsers viewing the session page.

#### Scenario: Successful WebSocket connection
- **WHEN** a browser sends a WebSocket upgrade request to `/s/<sessionId>/ws` for an existing session
- **THEN** the server SHALL upgrade the connection and maintain it as a persistent WebSocket connection

#### Scenario: Session not found
- **WHEN** a browser sends a WebSocket upgrade request to `/s/<sessionId>/ws` for a non-existent session
- **THEN** the server SHALL return HTTP 404 and NOT upgrade the connection

### Requirement: File change detection for sessions
The system SHALL monitor session directories for file changes when at least one WebSocket client is connected, and push reload notifications when changes are detected.

#### Scenario: File change triggers notification
- **WHEN** a file in the session's root directory is created, modified, or deleted
- **AND** at least one WebSocket client is connected to that session
- **THEN** the server SHALL send a JSON message `{"type":"reload"}` to all connected WebSocket clients of that session within 1 second

#### Scenario: Multiple changes debounced
- **WHEN** multiple file changes occur within 300ms in the same session directory
- **THEN** the system SHALL send only one reload notification after the debounce window

#### Scenario: File watcher lifecycle
- **WHEN** the last WebSocket client disconnects from a session
- **THEN** the system SHALL stop the file watcher for that session to free resources

### Requirement: Live reload script injection
The system SHALL inject a JavaScript snippet into HTML responses served from `/s/<sessionId>/` that establishes a WebSocket connection and handles page reload.

#### Scenario: HTML response gets injection
- **WHEN** the server serves a file with `Content-Type: text/html` from a session path
- **THEN** the response SHALL include a `<script>` tag before `</head>` or `</body>` (or at end of document if neither exists) that:
  1. Opens a WebSocket connection to `/s/<sessionId>/ws`
  2. Listens for `{"type":"reload"}` messages
  3. Calls `location.reload()` upon receiving a reload message

#### Scenario: Non-HTML responses are not modified
- **WHEN** the server serves a file with any Content-Type other than `text/html`
- **THEN** the response SHALL NOT be modified

#### Scenario: WebSocket auto-reconnect
- **WHEN** the WebSocket connection is closed or encounters an error
- **THEN** the injected script SHALL attempt to reconnect with exponential backoff (starting at 1 second, max 30 seconds)
- **AND** upon successful reconnection, the page SHALL automatically reload
