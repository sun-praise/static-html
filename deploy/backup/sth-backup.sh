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
#   * the `app` container must be running and reachable by name (sth-app)
#   * the `sth_backup` volume must be mounted at /backup inside the container
#
set -euo pipefail

# ---- Configurable knobs (env overrides) ---------------------------------
CONTAINER_NAME="${STH_CONTAINER_NAME:-sth-app}"
DB_PATH="${STH_DB_PATH:-/data/sessions.db}"
BACKUP_DIR="${STH_BACKUP_DIR:-/backup}"
RETENTION_DAYS="${STH_BACKUP_RETENTION_DAYS:-14}"
# -------------------------------------------------------------------------

log() { printf '%s [sth-backup] %s\n' "$(date -u +%FT%TZ)" "$*"; }

# 1. Sanity check: container must exist and be running.
if ! docker inspect "${CONTAINER_NAME}" >/dev/null 2>&1; then
    log "ERROR: container ${CONTAINER_NAME} not found"
    exit 1
fi
state="$(docker inspect -f '{{.State.Running}}' "${CONTAINER_NAME}")"
if [[ "${state}" != "true" ]]; then
    log "ERROR: container ${CONTAINER_NAME} is not running (state=${state})"
    exit 1
fi

# 2. Sanity check: source DB exists inside the container.
if ! docker exec "${CONTAINER_NAME}" test -f "${DB_PATH}"; then
    log "ERROR: ${DB_PATH} not present inside ${CONTAINER_NAME}"
    exit 1
fi

# 3. Take the hot backup.
date_stamp="$(date -u +%F)"   # YYYY-MM-DD
target_dir="${BACKUP_DIR%/}/${date_stamp}"
target_file="${target_dir}/sessions.db"

log "writing ${target_file}"
docker exec "${CONTAINER_NAME}" \
    sh -c "mkdir -p '${target_dir}' && sqlite3 '${DB_PATH}' \".backup '${target_file}'\""

# 4. Verify the backup is a readable SQLite database.
integrity="$(docker exec "${CONTAINER_NAME}" \
    sh -c "sqlite3 '${target_file}' 'PRAGMA integrity_check;'")"
if [[ "${integrity}" != "ok" ]]; then
    log "ERROR: integrity_check failed for ${target_file}: ${integrity}"
    exit 2
fi

# 5. Prune old backups (best-effort; do not fail the run).
if [[ "${RETENTION_DAYS}" =~ ^[0-9]+$ ]] && [[ "${RETENTION_DAYS}" -gt 0 ]]; then
    log "pruning backups older than ${RETENTION_DAYS} days"
    docker exec "${CONTAINER_NAME}" \
        sh -c "find '${BACKUP_DIR}' -mindepth 1 -maxdepth 1 -type d -mtime +${RETENTION_DAYS} -print -exec rm -rf {} +" \
        || log "WARNING: prune step failed (non-fatal)"
fi

log "done"
