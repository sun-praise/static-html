#!/usr/bin/env bash
#
# Hot backup of the `sth` SQLite database.
#
# Runs as a systemd user unit (see sth-backup.{service,timer}). Uses the
# SQLite Online Backup API (sqlite3 .backup) to take a consistent snapshot
# while the app is serving requests.
#
# Output goes to the `sth_backup` docker volume, mounted at /backup inside
# the app container. Old snapshots are pruned (default: > 14 days).
#
# Requirements:
#   * docker on PATH, with permission to talk to the daemon
#   * exactly one running container carrying the label
#     `com.docker.compose.service=app` (Compose injects this automatically)
#   * the `sth_backup` volume must be mounted at /backup inside the container
#
set -euo pipefail

# ---- Configurable knobs (env overrides) ---------------------------------
# Label used to discover the app container. Compose injects
# com.docker.compose.service=<service> automatically, so the default matches
# the `app` service defined in docker-compose.yml.
CONTAINER_LABEL="${STH_CONTAINER_LABEL:-com.docker.compose.service=app}"
# Allow explicit override (e.g. when running outside compose).
CONTAINER_NAME="${STH_CONTAINER_NAME:-}"
DB_PATH="${STH_DB_PATH:-/data/sessions.db}"
BACKUP_DIR="${STH_BACKUP_DIR:-/backup}"
RETENTION_DAYS="${STH_BACKUP_RETENTION_DAYS:-14}"
# -------------------------------------------------------------------------

log() { printf '%s [sth-backup] %s\n' "$(date -u +%FT%TZ)" "$*"; }

# 1. Resolve the target container — by explicit name if given, otherwise by
#    label. Fail loudly if zero or more than one match is found.
if [[ -n "${CONTAINER_NAME}" ]]; then
    target="${CONTAINER_NAME}"
elif ! command -v docker >/dev/null 2>&1; then
    log "ERROR: docker CLI not found on PATH"
    exit 1
else
    mapfile -t matches < <(docker ps -q --filter "label=${CONTAINER_LABEL}" --filter status=running)
    case "${#matches[@]}" in
        0)
            log "ERROR: no running container matches label '${CONTAINER_LABEL}'"
            exit 1
            ;;
        1)
            target="${matches[0]}"
            ;;
        *)
            log "ERROR: expected exactly one container with label '${CONTAINER_LABEL}', found ${#matches[@]}:"
            for id in "${matches[@]}"; do
                log "  - $(docker inspect -f '{{.Name}} ({{.State.Status}})' "$id")"
            done
            exit 1
            ;;
    esac
fi

# 2. Sanity check: container must exist and be running.
if ! docker inspect "${target}" >/dev/null 2>&1; then
    log "ERROR: container ${target} not found"
    exit 1
fi
state="$(docker inspect -f '{{.State.Running}}' "${target}")"
if [[ "${state}" != "true" ]]; then
    log "ERROR: container ${target} is not running (state=${state})"
    exit 1
fi

# 3. Sanity check: source DB exists inside the container.
if ! docker exec "${target}" test -f "${DB_PATH}"; then
    log "ERROR: ${DB_PATH} not present inside ${target}"
    exit 1
fi

# 3. Take the hot backup.
date_stamp="$(date -u +%F)"   # YYYY-MM-DD
target_dir="${BACKUP_DIR%/}/${date_stamp}"
target_file="${target_dir}/sessions.db"

log "writing ${target_file}"
docker exec "${target}" \
    sh -c "mkdir -p '${target_dir}' && sqlite3 '${DB_PATH}' \".backup '${target_file}'\""

# 4. Verify the backup is a readable SQLite database.
integrity="$(docker exec "${target}" \
    sh -c "sqlite3 '${target_file}' 'PRAGMA integrity_check;'")"
if [[ "${integrity}" != "ok" ]]; then
    log "ERROR: integrity_check failed for ${target_file}: ${integrity}"
    exit 2
fi

# 5. Prune old backups (best-effort; do not fail the run).
if [[ "${RETENTION_DAYS}" =~ ^[0-9]+$ ]] && [[ "${RETENTION_DAYS}" -gt 0 ]]; then
    log "pruning backups older than ${RETENTION_DAYS} days"
    docker exec "${target}" \
        sh -c "find '${BACKUP_DIR}' -mindepth 1 -maxdepth 1 -type d -mtime +${RETENTION_DAYS} -print -exec rm -rf {} +" \
        || log "WARNING: prune step failed (non-fatal)"
fi

log "done"
