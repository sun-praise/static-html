#!/usr/bin/env bash

set -euo pipefail

# If an external server is already configured, skip starting a local one.
if [ -n "${STATIC_HTML_SERVER_URL:-}" ]; then
  echo "STATIC_HTML_SERVER_URL is set ($STATIC_HTML_SERVER_URL) — skipping server start."
  exit 0
fi

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_dir="$("$script_dir/bootstrap-repo.sh")"
host="${STATIC_HTML_HOST:-0.0.0.0}"
port="${1:-${STATIC_HTML_PORT:-3939}}"

cd "$repo_dir"
exec "$repo_dir/dist/sth" start --host "$host" --port "$port"
