# Deploying `sth` behind nginx

This is a **reference runbook** for running the `sth` container behind an
existing host nginx with TLS. It is intentionally generic — substitute your
own domain, host, and certificate paths wherever you see `<your-domain>` or
`<host>`.

```
            ┌───────────────┐  443  ┌──────────────┐  3939  ┌─────────────┐
Internet ──▶│  host nginx   │──────▶│ docker proxy │────────▶│ sth-app     │
            │  TLS (certbot)│       │ 127.0.0.1    │         │ (alpine)    │
            └───────────────┘       └──────────────┘         └─────────────┘
                                                                │
                                                  sth_data:/data │ sth_backup:/backup
```

## What stays generic vs. what you customize

| Artifact | Generic (ships in repo) | Yours (don't commit) |
|---|---|---|
| `Dockerfile`, `.dockerignore` | ✅ | |
| `.github/workflows/docker.yml` (GHCR publish) | ✅ | |
| `docker-compose.yml` | ✅ template | `STH_SERVER_NAME` via `.env` |
| `deploy/nginx/example.conf` | ✅ template | copy → `<your-domain>.conf` |
| `deploy/backup/sth-backup.{sh,service,timer}` | ✅ reference | install on host |
| nginx site config, cert paths, `/opt/sth` path | | ✅ host-local |

## Prerequisites (host)

The host must already have:

- [x] **Docker 24+** with the **compose v2 plugin** (`docker compose version`)
- [x] **nginx** with a site-enabled convention (`/etc/nginx/sites-enabled/`)
- [x] **certbot** (or another ACME client) configured for your domain
- [x] A user with permission to run `docker` (member of the `docker` group, or
      use `sudo`)
- [x] **Lingering enabled** for that user so user-level systemd units survive
      SSH logout:
      ```bash
      sudo loginctl enable-linger "$USER"
      ```

## DNS

Add a record at your DNS provider pointing `<your-domain>` to the host's
public IP (A and/or AAAA). Wait for propagation before requesting the
certificate.

```bash
dig +short <your-domain>   # should print the host IP
```

## TLS certificate (first time only)

```bash
sudo certbot certonly --nginx -d <your-domain>
# Verify:
sudo ls /etc/letsencrypt/live/<your-domain>/
```

certbot's renewal timer (`systemctl status certbot.timer`) will handle renewals.

## Deploy steps

### 1. Clone the repo to the host

```bash
sudo mkdir -p /opt/sth && sudo chown "$USER:$USER" /opt/sth
git clone https://github.com/sun-praise/static-html.git /opt/sth
cd /opt/sth
```

### 2. Configure

```bash
cp .env.example .env
# Edit .env:
#   STH_SERVER_NAME=<your-domain>
#   STH_IMAGE_TAG=latest   # or pin to a tag
```

### 3. Pull and start the container

```bash
docker compose pull
docker compose up -d
docker compose ps            # app should be Up (healthy)
docker compose logs -f app   # check "HTML server listening on..."
```

### 4. Enable the nginx site

Copy the template and substitute your domain:

```bash
sudo cp deploy/nginx/example.conf /etc/nginx/sites-available/<your-domain>.conf
sudo sed -i 's/<your-domain>/<your-domain>/g' /etc/nginx/sites-available/<your-domain>.conf
sudo ln -s /etc/nginx/sites-available/<your-domain>.conf /etc/nginx/sites-enabled/
sudo nginx -t
sudo systemctl reload nginx
```

### 5. Verify end-to-end

```bash
# Direct (inside the host) — should return HTML. The Host header makes the
# container emit session URLs under <your-domain> instead of 127.0.0.1.
curl -fsS -H "Host: <your-domain>" http://127.0.0.1:3939/ | head -1

# Through nginx, over TLS — should return the same HTML
curl -fsS https://<your-domain>/ | head -1

# WebSocket upgrade path should reach the server (101 Switching Protocols)
curl -fsS -i -H 'Connection: Upgrade' -H 'Upgrade: websocket' \
     -H 'Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==' \
     -H 'Sec-WebSocket-Version: 13' \
     https://<your-domain>/s/none/ws | head -1 || true
```

### 6. Enable daily backups (optional)

The backup script + systemd units under `deploy/backup/` are a **reference
implementation**. Review them, then install on the host:

```bash
# Install the backup script somewhere on PATH (the systemd unit assumes ~/.local/bin)
mkdir -p ~/.local/bin ~/.config/systemd/user
ln -sf /opt/sth/deploy/backup/sth-backup.sh ~/.local/bin/sth-backup.sh
cp /opt/sth/deploy/backup/sth-backup.{service,timer} ~/.config/systemd/user/

systemctl --user daemon-reload
systemctl --user enable --now sth-backup.timer
systemctl --user list-timers sth-backup.timer
```

Trigger a one-shot backup immediately to verify:

```bash
systemctl --user start sth-backup.service
systemctl --user status sth-backup.service
# List the resulting file:
docker exec sth-app ls -la /backup/$(date -u +%F)/
```

## Operations

### Upgrade

```bash
cd /opt/sth
git pull --ff-only
docker compose pull
docker compose up -d
```

### Rollback to a known tag

```bash
cd /opt/sth
STH_IMAGE_TAG=1.3.0 docker compose pull app
STH_IMAGE_TAG=1.3.0 docker compose up -d
```

### View logs

```bash
docker compose logs -f app
docker compose logs --tail=200 app
```

### Restart / stop

```bash
docker compose restart app
docker compose down              # stop, keep volumes
docker compose down -v           # ⚠️ also deletes sth_data and sth_backup
```

### Restore from backup

Backups live in the `sth_backup` volume under `/backup/<YYYY-MM-DD>/` and contain both `sessions.db` and the `uploads/` tree (one snapshot per day, kept in sync).

```bash
# 1. Stop the app so no writes race the restore
docker compose stop app

# 2. Copy the chosen backup over the live DB and uploads tree.
#    (Omit the uploads line if that day's backup has no uploads/ dir, e.g.
#    the instance predates --upload-root.)
docker run --rm -v sth_data:/data -v sth_backup:/backup alpine \
    sh -c 'cp /backup/2026-06-28/sessions.db /data/sessions.db &&
           rm -rf /data/uploads &&
           cp -a /backup/2026-06-28/uploads /data/uploads'

# 3. Restart
docker compose up -d
```

### Manual (ad-hoc) backup

```bash
~/.local/bin/sth-backup.sh
```

## Troubleshooting

| Symptom | Check |
|---|---|
| 502 Bad Gateway from nginx | `docker compose ps` (app healthy?); `curl http://127.0.0.1:3939/` |
| 504 Gateway Timeout | nginx can reach the host but the container is stuck — `docker compose logs app` |
| WebSocket live-reload not working | Confirm `proxy_http_version 1.1` and the `Upgrade`/`Connection` headers are in the server block; check browser DevTools Network tab for the `/ws` upgrade |
| Cert renewal fails | `sudo certbot renew --dry-run`; verify DNS still points at this host |
| Backup unit fails | `systemctl --user status sth-backup.service`; check `journalctl --user -u sth-backup.service` |
| Permission denied on `/backup` | The app container runs as uid 1000; the backup script `docker exec`s into it so the same uid must be able to write `/backup` (it can — the volume is owned by the container) |
| Session URLs have `:3939` suffix | nginx is not setting `X-Forwarded-Port: 443` (the template does by default — check your site conf). For multi-hop proxies or untrusted headers, set `--server-port 443` in `docker-compose.yml` |

## URL generation behind a reverse proxy

The container listens on 3939, but nginx terminates TLS on 443. For generated session URLs to come out as `https://<your-domain>/s/<id>/` (no `:3939`), the server needs to know the public port. Two options:

- **Default (zero-config):** the included `deploy/nginx/example.conf` sets `proxy_set_header X-Forwarded-Port 443;`. Copy it verbatim and URLs work.
- **Override:** for multi-hop proxies or when you don't trust proxy headers, pass `--server-port 443` in the compose `command:` array. This takes priority over any header.

## Layout

```
deploy/
├── README.md                              # this reference runbook
├── nginx/
│   └── example.conf                       # template server block (80 + 443, WS-aware)
└── backup/
    ├── sth-backup.sh                      # sqlite3 .backup + uploads cp + 14-day prune
    ├── sth-backup.service                 # oneshot user unit
    └── sth-backup.timer                   # daily 03:00 trigger
```
