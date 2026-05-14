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

Note: Only the specified HTML file is uploaded. If you need to include sibling
resources (CSS/JS/images), use 'sth send' directly with the appropriate directory.
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
      [ -z "${2:-}" ] && { echo "Error: --tag requires a value" >&2; usage; }
      tag="$2"; shift 2 ;;
    --tag=*)
      tag="${1#--tag=}"; [ -z "$tag" ] && { echo "Error: --tag requires a value" >&2; usage; }; shift ;;
    --category)
      [ -z "${2:-}" ] && { echo "Error: --category requires a value" >&2; usage; }
      category="$2"; shift 2 ;;
    --category=*)
      category="${1#--category=}"; [ -z "$category" ] && { echo "Error: --category requires a value" >&2; usage; }; shift ;;
    --project)
      [ -z "${2:-}" ] && { echo "Error: --project requires a value" >&2; usage; }
      project="$2"; shift 2 ;;
    --project=*)
      project="${1#--project=}"; [ -z "$project" ] && { echo "Error: --project requires a value" >&2; usage; }; shift ;;
    --server)
      [ -z "${2:-}" ] && { echo "Error: --server requires a value" >&2; usage; }
      server_url="$2"; shift 2 ;;
    --server=*)
      server_url="${1#--server=}"; [ -z "$server_url" ] && { echo "Error: --server requires a value" >&2; usage; }; shift ;;
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

if [[ ! "$server_url" =~ ^https?:// ]]; then
  echo "Error: server URL must start with http:// or https://" >&2
  exit 1
fi

trim() { local v="$1"; v="${v#"${v%%[![:space:]]*}"}"; v="${v%"${v##*[![:space:]]}"}"; printf '%s' "$v"; }
tag="$(trim "$tag")"
category="$(trim "$category")"
project="$(trim "$project")"

original_file="$target_file"
if command -v realpath >/dev/null 2>&1; then
  target_file="$(realpath -- "$target_file" 2>/dev/null)" || {
    echo "Error: file not found or inaccessible: $original_file" >&2
    exit 1
  }
elif command -v readlink >/dev/null 2>&1; then
  target_file="$(readlink -f -- "$target_file" 2>/dev/null)" || {
    echo "Error: file not found or inaccessible: $original_file" >&2
    exit 1
  }
else
  target_file="$(cd "$(dirname "$target_file")" 2>/dev/null && pwd)/$(basename "$target_file")" || {
    echo "Error: cannot resolve path: $original_file" >&2
    exit 1
  }
  if [ ! -e "$target_file" ]; then
    echo "Error: file not found or inaccessible: $original_file" >&2
    exit 1
  fi
fi
if [ ! -f "$target_file" ]; then
  echo "Error: file not found: $target_file" >&2
  exit 1
fi

tmpdir="$(mktemp -d)"
chmod 700 "$tmpdir"
cleanup() { [ -d "${tmpdir:-}" ] && rm -rf "$tmpdir"; }
trap cleanup EXIT

cp -- "$target_file" "$tmpdir/"
filename="$(basename "$target_file")"

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_dir="$("$script_dir/bootstrap-repo.sh")"

cd "$repo_dir"
# Do NOT use exec here; the shell must remain alive so the EXIT trap can clean up $tmpdir.
"$repo_dir/dist/sth" send "$tmpdir/$filename" \
  --server "$server_url" \
  --tag "$tag" \
  --category "$category" \
  --project "$project"
