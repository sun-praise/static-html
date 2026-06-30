## ADDED Requirements

### Requirement: External port resolution priority

When the server constructs a session URL, the port component SHALL be resolved with the following priority, highest first:

1. The `--server-port` flag value (if set)
2. The `X-Forwarded-Port` request header (if present and parseable as a positive integer)
3. The internal listener port (`--port`, default 3939)

#### Scenario: Reverse proxy sets X-Forwarded-Port to 443
- **WHEN** the server runs behind a TLS-terminating reverse proxy listening on 443
- **AND** the proxy sets `X-Forwarded-Proto: https` and `X-Forwarded-Port: 443`
- **AND** `--server-name sth.example.com` is configured
- **THEN** a `POST /api/sessions` response SHALL return a URL of the form `https://sth.example.com/s/<id>/` (no port suffix)

#### Scenario: --server-port flag overrides header and listener
- **WHEN** the server is started with `--server-port 8443`
- **AND** an incoming request carries `X-Forwarded-Port: 443`
- **THEN** the generated URL SHALL use port 8443 (e.g. `https://sth.example.com:8443/s/<id>/`)

#### Scenario: No flag and no header falls back to listener port
- **WHEN** the server is started without `--server-port`
- **AND** a request has no `X-Forwarded-Port` header
- **AND** the listener is bound to port 3939
- **THEN** the generated URL SHALL be `http://<server-name>:3939/s/<id>/` (preserving current behavior)

#### Scenario: Invalid X-Forwarded-Port is ignored
- **WHEN** a request carries `X-Forwarded-Port: abc` (non-numeric)
- **AND** no `--server-port` is set
- **THEN** the server SHALL fall back to the listener port without error

### Requirement: --server-port CLI flag

The CLI SHALL accept a `--server-port <n>` flag on the `start` subcommand that overrides the port used in generated session URLs.

#### Scenario: Flag accepted
- **WHEN** the server is started with `--server-port 443`
- **THEN** the flag value SHALL be propagated to the server and used for URL generation, independent of the listener's `--port`

#### Scenario: Non-numeric value rejected
- **WHEN** the server is started with `--server-port foo`
- **THEN** the CLI SHALL return an error and exit non-zero before binding any port

#### Scenario: Out-of-range value rejected
- **WHEN** the server is started with `--server-port 0` or `--server-port 70000`
- **THEN** the CLI SHALL return an error and exit non-zero

#### Scenario: Flag omitted
- **WHEN** the `start` subcommand is invoked without `--server-port`
- **THEN** the server SHALL rely on `X-Forwarded-Port` or the listener port per the priority rules

### Requirement: Port omission rules unchanged

The server SHALL omit the port from the generated URL only when `(scheme == "http" && port == 80) || (scheme == "https" && port == 443)`, where `port` is the resolved external port (not necessarily the listener port). In all other cases the port SHALL be included as `:<port>`.

#### Scenario: External port 443 with https omits suffix
- **WHEN** the resolved external port is 443 and scheme is https
- **THEN** the URL SHALL be `https://sth.example.com/s/<id>/`

#### Scenario: External port 8443 with https keeps suffix
- **WHEN** the resolved external port is 8443
- **THEN** the URL SHALL be `https://sth.example.com:8443/s/<id>/`

### Requirement: nginx example sets X-Forwarded-Port

The `deploy/nginx/example.conf` shipped in the repo SHALL set `proxy_set_header X-Forwarded-Port 443;` so that a standard TLS-terminating deployment produces clean URLs without requiring `--server-port`.

#### Scenario: Default nginx template works out of the box
- **WHEN** an operator copies `deploy/nginx/example.conf`, substitutes their domain, and starts the container with `--server-name <their-domain>`
- **THEN** session URLs SHALL be `https://<their-domain>/s/<id>/` with no port suffix, with no need to set `--server-port`
