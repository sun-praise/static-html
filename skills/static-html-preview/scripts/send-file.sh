#!/usr/bin/env bash

set -euo pipefail

if [ "$#" -lt 1 ]; then
  echo "Usage: send-file.sh <file.html> [server_url]" >&2
  exit 1
fi

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_dir="$("$script_dir/bootstrap-repo.sh")"
target_file="$1"
server_url="${2:-${STATIC_HTML_SERVER_URL:-http://127.0.0.1:3939}}"

cd "$repo_dir"
exec "$repo_dir/dist/html-server" send "$target_file" --server "$server_url"
