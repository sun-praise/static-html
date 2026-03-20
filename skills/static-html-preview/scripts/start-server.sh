#!/usr/bin/env bash

set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_dir="$("$script_dir/bootstrap-repo.sh")"
host="${STATIC_HTML_HOST:-127.0.0.1}"
port="${1:-${STATIC_HTML_PORT:-3939}}"

cd "$repo_dir"
exec "$repo_dir/dist/html-server" start --host "$host" --port "$port"
