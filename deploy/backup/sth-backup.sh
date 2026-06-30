#!/usr/bin/env bash
#
# Hot backup of the `sth` data directory: the SQLite database plus the
# uploaded session snapshots.
#
# Runs as a systemd user unit (see sth-backup.{service,timer}). The DB uses
# the SQLite Online Backup API (sqlite3 .backup) for a consistent snapshot
# while the app is serving requests; the uploads tree is copied with
# `cp -a` (preserves perms, copies recursively).
#
# Output goes to the `sth_backup` docker volume, mounted at /backup inside
# the app container:
#
#   /backup/<YYYY-MM-DD>/sessions.db   (hot backup)
#   /backup/<YYYY-MM-DD>/uploads/      (recursive copy of the uploads tree)
#
# Old snapshots are pruned (default: > 14 days), whole date-stamped
# directories at a time so the DB and uploads for a given day stay in sync.
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
# Directory where the app stores uploaded session snapshots. MUST match the
# --upload-root flag passed to the server (docker-compose.yml sets it to
# /data/uploads). Set STH_BACKUP_UPLOADS=0 to skip the uploads copy entirely
# (e.g. for deployments that intentionally keep uploads off the data volume).
UPLOADS_PATH="${STH_UPLOADS_PATH:-/data/uploads}"
BACKUP_UPLOADS="${STH_BACKUP_UPLOADS:-1}"
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

# 4. Prepare the date-stamped target directory.
date_stamp="$(date -u +%F)"   # YYYY-MM-DD
target_dir="${BACKUP_DIR%/}/${date_stamp}"
target_db="${target_dir}/sessions.db"
target_uploads="${target_dir}/uploads"

# 5. Take the hot DB backup and verify it is a readable SQLite database.
log "writing ${target_db}"
docker exec "${target}" \
    sh -c "mkdir -p '${target_dir}' && sqlite3 '${DB_PATH}' \".backup '${target_db}'\""

integrity="$(docker exec "${target}" \
    sh -c "sqlite3 '${target_db}' 'PRAGMA integrity_check;'")"
if [[ "${integrity}" != "ok" ]]; then
    log "ERROR: integrity_check failed for ${target_db}: ${integrity}"
    exit 2
fi

# 6. Copy the uploads tree so session files survive a container rebuild.
#    The uploads directory is created on demand by the app, so a missing
#    directory means no sessions have been uploaded yet — skip silently in
#    that case rather than failing the backup.
if [[ "${BACKUP_UPLOADS}" == "1" ]]; then
    if docker exec "${target}" test -d "${UPLOADS_PATH}"; then
        # Count session directories at the source so we can verify the copy.
        src_count="$(docker exec "${target}" \
            sh -c "find '${UPLOADS_PATH}' -mindepth 1 -maxdepth 1 -type d -name 'session-*' 2>/dev/null | wc -l")"
        src_count="${src_count// /}"

        log "copying ${UPLOADS_PATH} -> ${target_uploads} (${src_count} session dir(s))"
        docker exec "${target}" \
            sh -c "rm -rf '${target_uploads}' && cp -a '${UPLOADS_PATH}' '${target_uploads}'"

        # Verify the copy has the same session directory count. A mismatch
        # means files were added/removed mid-copy — warn but don't fail the
        # whole run, since the DB snapshot is still valid and useful.
        dst_count="$(docker exec "${target}" \
            sh -c "find '${target_uploads}' -mindepth 1 -maxdepth 1 -type d -name 'session-*' 2>/dev/null | wc -l")"
        dst_count="${dst_count// /}"
        if [[ "${src_count}" != "${dst_count}" ]]; then
            log "WARNING: uploads copy count mismatch (source=${src_count}, backup=${dst_count}); concurrent session create/delete likely — DB snapshot is unaffected"
        fi
    else
        log "uploads dir ${UPLOADS_PATH} does not exist; skipping uploads copy"
    fi
else
    log "BACKUP_UPLOADS=0; skipping uploads copy"
fi

# 7. Prune old backups (best-effort; do not fail the run). Whole date
#    directories are removed, so the DB and uploads for a given day expire
#    together.
if [[ "${RETENTION_DAYS}" =~ ^[0-9]+$ ]] && [[ "${RETENTION_DAYS}" -gt 0 ]]; then
    log "pruning backups older than ${RETENTION_DAYS} days"
    docker exec "${target}" \
        sh -c "find '${BACKUP_DIR}' -mindepth 1 -maxdepth 1 -type d -mtime +${RETENTION_DAYS} -print -exec rm -rf {} +" \
        || log "WARNING: prune step failed (non-fatal)"
fi

log "done"
