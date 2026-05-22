## ADDED Requirements

### Requirement: sth watch command
The CLI SHALL provide a `sth watch` command that monitors a local directory for file changes and automatically pushes updates to a specified session.

#### Scenario: Start watching a directory
- **WHEN** user runs `sth watch <path> --session <sessionId> --server <url>`
- **THEN** the CLI SHALL monitor `<path>` for file changes and push changed files to the specified session via the incremental update API

#### Scenario: File change triggers upload
- **WHEN** a file is created or modified in the watched directory
- **THEN** the CLI SHALL upload the changed file to `/api/sessions/<sessionId>/files` within 500ms
- **AND** print a log message indicating the file was synced

#### Scenario: Debounced uploads
- **WHEN** multiple file changes occur within 300ms
- **THEN** the CLI SHALL batch the changes into a single API call

#### Scenario: Watch with server URL
- **WHEN** `--server` flag is provided
- **THEN** the CLI SHALL use that server URL as the target (default: `http://localhost:8080`)

#### Scenario: Directory does not exist
- **WHEN** the specified watch path does not exist
- **THEN** the CLI SHALL print an error message and exit with code 1

#### Scenario: Session does not exist
- **WHEN** the specified session ID does not exist on the server
- **THEN** the CLI SHALL print an error message and exit with code 1

### Requirement: Watch ignores hidden and temporary files
The watch command SHALL skip files and directories matching common ignore patterns.

#### Scenario: Hidden files ignored
- **WHEN** a file or directory starting with `.` is changed
- **THEN** the CLI SHALL NOT upload that file

#### Scenario: Common temporary patterns ignored
- **WHEN** files matching `*.swp`, `*.tmp`, `.DS_Store` are changed
- **THEN** the CLI SHALL NOT upload those files
