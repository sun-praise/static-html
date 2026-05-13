## ADDED Requirements

### Requirement: Server binds to all network interfaces
Server SHALL bind to `0.0.0.0` by default, allowing access from all network interfaces including localhost and LAN.

#### Scenario: Default startup binds to all interfaces
- **WHEN** server starts without `--host` flag
- **THEN** server SHALL listen on `0.0.0.0:<port>`

#### Scenario: Custom host overrides default
- **WHEN** server starts with `--host 192.168.1.100`
- **THEN** server SHALL listen on `192.168.1.100:<port>` only

### Requirement: Detect and return all accessible origins
Server SHALL provide an `Origins()` method that returns a list of all accessible URLs (localhost and LAN IPs).

#### Scenario: Server on default host with multiple network interfaces
- **WHEN** server is bound to `0.0.0.0:3939` and machine has LAN IP `192.168.2.14`
- **THEN** `Origins()` SHALL return at least `["http://127.0.0.1:3939", "http://192.168.2.14:3939"]`

#### Scenario: Server on specific host
- **WHEN** server is bound to `192.168.1.100:3939`
- **THEN** `Origins()` SHALL return `["http://192.168.1.100:3939"]`

### Requirement: Display all access URLs on startup
CLI SHALL display all accessible URLs when server starts.

#### Scenario: Multiple access URLs displayed
- **WHEN** server starts with default host and has LAN IP `192.168.2.14`
- **THEN** CLI output SHALL include both `http://127.0.0.1:3939` and `http://192.168.2.14:3939`

#### Scenario: Single access URL displayed
- **WHEN** server starts with `--host 127.0.0.1`
- **THEN** CLI output SHALL show only `http://127.0.0.1:3939`
