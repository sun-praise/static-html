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

# Remove leading and trailing whitespace from a string
trim() { local v="$1"; v="${v#"${v%%[![:space:]]*}"}"; v="${v%"${v##*[![:space:]]}"}"; printf '%s' "$v"; }

if [ "$#" -lt 1 ]; then
  usage
fi

target_file=""
server_url="${STATIC_HTML_SERVER_URL:-http://127.0.0.1:3939}"
tag=""
category=""
project=""

require_value() {
  local opt="$1" val="${2:-}"
  if [ -z "$val" ]; then
    echo "Error: $opt requires a value" >&2
    usage
  fi
}

while [ "$#" -gt 0 ]; do
  case "$1" in
    --tag)
      require_value "--tag" "${2:-}"; tag="$2"; shift 2 ;;
    --tag=*)
      tag="${1#--tag=}"; require_value "--tag" "$tag"; shift ;;
    --category)
      require_value "--category" "${2:-}"; category="$2"; shift 2 ;;
    --category=*)
      category="${1#--category=}"; require_value "--category" "$category"; shift ;;
    --project)
      require_value "--project" "${2:-}"; project="$2"; shift 2 ;;
    --project=*)
      project="${1#--project=}"; require_value "--project" "$project"; shift ;;
    --server)
      require_value "--server" "${2:-}"; server_url="$2"; shift 2 ;;
    --server=*)
      server_url="${1#--server=}"; require_value "--server" "$server_url"; shift ;;
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

if [[ ! "$server_url" =~ ^https?:// ]]; then
  echo "Error: server URL must start with http:// or https://" >&2
  exit 1
fi

tag="$(trim "$tag")"
category="$(trim "$category")"
project="$(trim "$project")"

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
  # Best-effort path resolution via cd+pwd; correctness is guaranteed by the -f check below.
  _base="$(basename "$target_file")"
  _dir="$(cd "$(dirname "$target_file")" 2>/dev/null && pwd)" || {
    echo "Error: cannot resolve path: $original_file" >&2
    exit 1
  }
  target_file="$_dir/$_base"
  if [ ! -e "$target_file" ]; then
    echo "Error: file not found or inaccessible: $original_file" >&2
    exit 1
  fi
fi
if [ ! -f "$target_file" ]; then
  echo "Error: file not found: $target_file" >&2
  exit 1
fi
filename="$(basename "$target_file")"
lowername="$(echo "$filename" | tr '[:upper:]' '[:lower:]')"
if [[ "$lowername" != *.html && "$lowername" != *.htm ]]; then
  echo "Error: file must have .html or .htm extension: $filename" >&2
  exit 1
fi

tmpdir="$(mktemp -d)"
chmod 700 "$tmpdir"
cleanup() { [ -d "${tmpdir:-}" ] && rm -rf "$tmpdir"; }
trap cleanup EXIT

cp -- "$target_file" "$tmpdir/"

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_dir="$("$script_dir/bootstrap-repo.sh")"

cd "$repo_dir"
# Do NOT use exec here; the shell must remain alive so the EXIT trap can clean up $tmpdir.
"$repo_dir/dist/sth" send "$tmpdir/$filename" \
  --server "$server_url" \
  --tag "$tag" \
  --category "$category" \
  --project "$project"
