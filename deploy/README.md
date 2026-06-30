# Deploying `sth` to a Linux host

This guide deploys the `sth` server as a Docker container behind an existing
host nginx, reachable at `https://sth.sun-praise.com`.

```
            ┌───────────────┐  443  ┌──────────────┐  3939  ┌─────────────┐
Internet ──▶│  host nginx   │──────▶│ docker proxy │────────▶│ sth-app     │
            │  TLS (certbot)│       │ 127.0.0.1    │         │ (alpine)    │
            └───────────────┘       └──────────────┘         └─────────────┘
                                                                │
                                                  sth_data:/data │ sth_backup:/backup
```

## Prerequisites (host)

The host (referred to as `jd`) must already have:

- [x] **Docker 24+** with the **compose v2 plugin** (`docker compose version`)
- [x] **nginx** with a site-enabled convention (`/etc/nginx/sites-enabled/`)
- [x] **certbot** (or another ACME client) configured for `sth.sun-praise.com`,
      writing certs to `/etc/letsencrypt/live/sth.sun-praise.com/`
- [x] A user with permission to run `docker` (member of the `docker` group, or
      use `sudo`)
- [x] **Lingering enabled** for that user so user-level systemd units survive
      SSH logout:
      ```bash
      sudo loginctl enable-linger "$USER"
      ```

If any of these is missing, install it first; this guide does not cover
bootstrapping the host.

## DNS

Add a record at your DNS provider pointing `sth.sun-praise.com` to the host's
public IP (A and/or AAAA). Wait for propagation before requesting the
certificate.

```bash
dig +short sth.sun-praise.com   # should print the host IP
```

## TLS certificate (first time only)

```bash
sudo certbot certonly --nginx -d sth.sun-praise.com
# Verify:
sudo ls /etc/letsencrypt/live/sth.sun-praise.com/
```

certbot's renewal timer (`systemctl status certbot.timer`) will handle renewals;
the nginx config below reloads nginx automatically via the deploy hook.

## Deploy steps

### 1. Clone the repo to the host

```bash
sudo mkdir -p /opt/sth && sudo chown "$USER:$USER" /opt/sth
git clone https://github.com/sun-praise/static-html.git /opt/sth
cd /opt/sth
```

### 2. Configure the image tag

```bash
cp .env.example .env
# Edit .env to pin a tag if you don't want `latest`:
#   STH_IMAGE_TAG=1.3.0
```

### 3. Pull and start the container

```bash
docker compose pull
docker compose up -d
docker compose ps            # app should be Up (healthy)
docker compose logs -f app   # check "HTML server listening on..."
```

### 4. Enable the nginx site

```bash
sudo ln -s /opt/sth/deploy/nginx/sth.sun-praise.com.conf \
           /etc/nginx/sites-enabled/sth.sun-praise.com.conf
sudo nginx -t
sudo systemctl reload nginx
```

### 5. Verify end-to-end

```bash
# Direct (inside the host) — should return HTML. The Host header makes the
# container emit session URLs under sth.sun-praise.com instead of 127.0.0.1.
curl -fsS -H 'Host: sth.sun-praise.com' http://127.0.0.1:3939/ | head -1

# Through nginx, over TLS — should return the same HTML
curl -fsS https://sth.sun-praise.com/ | head -1

# WebSocket upgrade path should reach the server (101 Switching Protocols)
curl -fsS -i -H 'Connection: Upgrade' -H 'Upgrade: websocket' \
     -H 'Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==' \
     -H 'Sec-WebSocket-Version: 13' \
     https://sth.sun-praise.com/s/none/ws | head -1 || true
```

### 6. Enable daily backups

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

Backups live in the `sth_backup` volume under `/backup/<YYYY-MM-DD>/sessions.db`.

```bash
# 1. Stop the app so no writes race the restore
docker compose stop app

# 2. Copy the chosen backup over the live DB
docker run --rm -v sth_data:/data -v sth_backup:/backup alpine \
    sh -c 'cp /backup/2026-06-28/sessions.db /data/sessions.db'

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

## Layout

```
deploy/
├── README.md                              # this file
├── nginx/
│   └── sth.sun-praise.com.conf            # server block (80 + 443, WS-aware)
└── backup/
    ├── sth-backup.sh                      # sqlite3 .backup + 14-day prune
    ├── sth-backup.service                 # oneshot user unit
    └── sth-backup.timer                   # daily 03:00 trigger
```
