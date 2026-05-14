#!/usr/bin/env bash

set -euo pipefail

usage() {
  cat >&2 <<EOF
Usage: send-file.sh <file.html> [options]

Options:
  --tag TAG          Tag(s) for the session (required)
  --category CAT     Category for the session (required)
  --project PROJ     Project for the session (required)
  --server URL       Server URL (default: \$STATIC_HTML_SERVER_URL or http://127.0.0.1:3939)
EOF
  exit 1
}

if [ "$#" -lt 1 ]; then
  usage
fi

target_file=""
server_url="${STATIC_HTML_SERVER_URL:-http://127.0.0.1:3939}"
tag=""
category=""
project=""

while [ "$#" -gt 0 ]; do
  case "$1" in
    --tag)
      tag="$2"; shift 2 ;;
    --category)
      category="$2"; shift 2 ;;
    --project)
      project="$2"; shift 2 ;;
    --server)
      server_url="$2"; shift 2 ;;
    -*)
      echo "Unknown option: $1" >&2; usage ;;
    *)
      if [ -z "$target_file" ]; then
        target_file="$1"; shift
      else
        echo "Unexpected argument: $1" >&2; usage
      fi ;;
  esac
done

if [ -z "$target_file" ]; then
  echo "Error: <file.html> is required" >&2
  usage
fi
if [ -z "$tag" ]; then
  echo "Error: --tag is required" >&2
  usage
fi
if [ -z "$category" ]; then
  echo "Error: --category is required" >&2
  usage
fi
if [ -z "$project" ]; then
  echo "Error: --project is required" >&2
  usage
fi

target_file="$(cd "$(dirname "$target_file")" && pwd)/$(basename "$target_file")"
if [ ! -f "$target_file" ]; then
  echo "Error: file not found: $target_file" >&2
  exit 1
fi

tmpdir="$(mktemp -d)"
cleanup() { rm -rf "$tmpdir"; }
trap cleanup EXIT

cp "$target_file" "$tmpdir/"
filename="$(basename "$target_file")"

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_dir="$("$script_dir/bootstrap-repo.sh")"

cd "$repo_dir"
exec "$repo_dir/dist/sth" send "$tmpdir/$filename" \
  --server "$server_url" \
  --tag "$tag" \
  --category "$category" \
  --project "$project"
