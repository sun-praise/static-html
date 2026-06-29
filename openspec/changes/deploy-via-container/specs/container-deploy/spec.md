## ADDED Requirements

### Requirement: Multi-stage Dockerfile with OCI labels

The repository SHALL ship a `Dockerfile` that produces a runnable container image for `html-server`.

The image MUST:
- Use a multi-stage build where the final stage is based on `alpine:3.21`
- Run the binary as a non-root user (uid 1000)
- Expose port 3939
- Include a `HEALTHCHECK` directive that probes `http://localhost:3939/`
- Set OCI labels `org.opencontainers.image.source`, `org.opencontainers.image.version`, and `org.opencontainers.image.created` so that `docker inspect` can attribute the image
- Build the binary with `CGO_ENABLED=1` (required by `github.com/mattn/go-sqlite3`)

#### Scenario: Build succeeds with CGO enabled
- **WHEN** `docker build .` is run on a repository checkout at tag `v1.3.0`
- **THEN** the build SHALL produce a single image whose entrypoint is `html-server`

#### Scenario: Image runs as non-root
- **WHEN** the image is started with default settings
- **THEN** the process SHALL run as uid 1000 and bind port 3939

#### Scenario: Healthcheck detects a healthy container
- **WHEN** the server is listening on port 3939
- **THEN** the docker `HEALTHCHECK` SHALL report the container as `healthy` within 5 seconds

### Requirement: docker-compose service definition

The repository SHALL ship a `docker-compose.yml` that defines the deployment of the `sth` server.

The compose file MUST:
- Define a service named `app` using image `ghcr.io/sun-praise/static-html:latest`
- Map `127.0.0.1:3939` on the host to port 3939 in the container (no public exposure)
- Mount a named volume `sth_data` at `/data` inside the container
- Set `restart: unless-stopped`
- Pass `start --bind 0.0.0.0 --port 3939 --server-name sth.sun-praise.com --db /data/sessions.db` as the container command
- Define volumes `sth_data` and `sth_backup` (the latter for the daily backup)

#### Scenario: First-time deploy
- **WHEN** an operator runs `docker compose up -d` on a host without prior state
- **THEN** the `app` service SHALL start, listen on `127.0.0.1:3939` of the host, and the volume `sth_data` SHALL be created automatically

#### Scenario: Restart preserves data
- **WHEN** an operator runs `docker compose restart app`
- **THEN** the database and session snapshots stored under `/data` SHALL persist across the restart

#### Scenario: Port is not publicly exposed
- **WHEN** the host firewall allows traffic only on ports 80 and 443
- **THEN** direct connections to host port 3939 from outside the host SHALL be refused (because the compose port mapping is bound to 127.0.0.1)

### Requirement: nginx server block for the public domain

The repository SHALL ship an nginx server block at `deploy/nginx/sth.sun-praise.com.conf` that can be symlinked into an existing nginx configuration.

The server block MUST:
- Listen on port 443 ssl with `server_name sth.sun-praise.com`
- Terminate TLS using certificates at `/etc/letsencrypt/live/sth.sun-praise.com/fullchain.pem` and `privkey.pem`
- Redirect all HTTP traffic on port 80 to HTTPS
- `proxy_pass` to `http://127.0.0.1:3939`
- Set `proxy_http_version 1.1`, `proxy_set_header Upgrade $http_upgrade`, and `proxy_set_header Connection "upgrade"` so WebSocket upgrades from `/s/<id>/ws` are forwarded
- Set `proxy_read_timeout 86400s` so idle WebSocket connections are not closed by nginx
- Forward `X-Forwarded-Proto`, `X-Forwarded-For`, and the original `Host` header

#### Scenario: HTTPS request reaches the container
- **WHEN** a client issues `GET https://sth.sun-praise.com/` and the container is running
- **THEN** the request SHALL be proxied to `http://127.0.0.1:3939/` and the response SHALL be returned over TLS

#### Scenario: HTTP redirects to HTTPS
- **WHEN** a client issues `GET http://sth.sun-praise.com/`
- **THEN** nginx SHALL respond with a 301 redirect to `https://sth.sun-praise.com/`

#### Scenario: WebSocket upgrade succeeds through nginx
- **WHEN** a browser opens a WebSocket to `wss://sth.sun-praise.com/s/<id>/ws`
- **THEN** nginx SHALL forward the `Upgrade: websocket` and `Connection: Upgrade` headers and the connection SHALL remain open for at least 60 seconds

### Requirement: Daily backup of the SQLite database

The repository SHALL ship a backup script `deploy/backup/sth-backup.sh` and accompanying systemd units `deploy/backup/sth-backup.{service,timer}` that together perform a hot backup of the SQLite database once per day.

The backup script MUST:
- Locate the running `app` container by label `com.docker.compose.service=app`
- Execute `sqlite3 /data/sessions.db ".backup '/backup/<UTC-date>/sessions.db'"` inside the container
- Mount the `sth_backup` volume at `/backup` inside the container
- Delete backups older than 14 days from `/backup`

The systemd timer MUST:
- Run as a user unit
- Trigger the service at 03:00 local time every day
- Be persistent (catch up missed runs on boot)

#### Scenario: Successful daily backup
- **WHEN** the timer fires and the `app` container is running
- **THEN** a new file `sth_backup:/backup/<UTC-date>/sessions.db` SHALL exist and be a valid SQLite database (verifiable via `sqlite3 ... ".schema"`)

#### Scenario: App is stopped
- **WHEN** the timer fires but no `app` container is running
- **THEN** the script SHALL log a clear error and exit non-zero, without modifying the backup volume

#### Scenario: Old backups are pruned
- **WHEN** a backup file in `/backup` is older than 14 days
- **THEN** the script SHALL remove it after a successful fresh backup

### Requirement: CI image build and push to GHCR

The repository SHALL ship a GitHub Actions workflow `.github/workflows/docker.yml` that builds and publishes the container image to GitHub Container Registry.

The workflow MUST:
- Trigger on `push` of tags matching `v*` and on `push` to `main`
- Use `docker/build-push-action` with `platforms: linux/amd64,linux/arm64`
- Tag the image as `ghcr.io/sun-praise/static-html:<version>` (from the tag name or `latest` for `main`)
- Authenticate using the workflow's `GITHUB_TOKEN` and the `packages: write` permission

#### Scenario: Tag push publishes a release image
- **WHEN** a tag `v1.4.0` is pushed
- **THEN** the image `ghcr.io/sun-praise/static-html:1.4.0` SHALL be published for both `linux/amd64` and `linux/arm64`

#### Scenario: Main push publishes a rolling tag
- **WHEN** a commit is pushed to `main`
- **THEN** the image `ghcr.io/sun-praise/static-html:latest` SHALL be updated

### Requirement: Deployment documentation

The repository SHALL ship `deploy/README.md` that documents, in order, the steps to deploy `sth` to a fresh Linux host (nginx already running with certbot configured for `sth.sun-praise.com`).

The documentation MUST cover:
- Prerequisite check: docker, docker compose plugin, `loginctl enable-linger`
- DNS record: A/AAAA `sth.sun-praise.com` → host public IP
- nginx site enable: symlink the provided server block
- First deploy: `docker compose up -d` with verification commands
- Upgrade procedure
- Backup verification
- Common operations (logs, restart, rollback)
