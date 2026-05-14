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

# Strips leading/trailing whitespace and collapses internal newlines to spaces.
trim() {
  local v="$1"
  v="${v#"${v%%[![:space:]]*}"}"
  v="${v%"${v##*[![:space:]]}"}"
  v="${v//$'\n'/ }"
  printf '%s' "$v"
}

if [ "$#" -lt 1 ] || [ "${1:-}" = "--help" ] || [ "${1:-}" = "-h" ]; then
  usage
fi

target_file=""
server_url="${STATIC_HTML_SERVER_URL:-http://127.0.0.1:3939}"
tag=""
category=""
project=""

while [ "$#" -gt 0 ]; do
  case "$1" in
    --help|-h)
      usage ;;
    --tag)
      if [ -z "${2:-}" ]; then echo "Error: --tag requires a value" >&2; usage; fi
      tag="$(trim "$2")"; shift 2 ;;
    --tag=*)
      tag="$(trim "${1#--tag=}")"; [ -z "$tag" ] && { echo "Error: --tag requires a value" >&2; usage; }; shift ;;
    --category)
      if [ -z "${2:-}" ]; then echo "Error: --category requires a value" >&2; usage; fi
      category="$(trim "$2")"; shift 2 ;;
    --category=*)
      category="$(trim "${1#--category=}")"; [ -z "$category" ] && { echo "Error: --category requires a value" >&2; usage; }; shift ;;
    --project)
      if [ -z "${2:-}" ]; then echo "Error: --project requires a value" >&2; usage; fi
      project="$(trim "$2")"; shift 2 ;;
    --project=*)
      project="$(trim "${1#--project=}")"; [ -z "$project" ] && { echo "Error: --project requires a value" >&2; usage; }; shift ;;
    --server)
      if [ -z "${2:-}" ]; then echo "Error: --server requires a value" >&2; usage; fi
      server_url="$(trim "$2")"; shift 2 ;;
    --server=*)
      server_url="$(trim "${1#--server=}")"; [ -z "$server_url" ] && { echo "Error: --server requires a value" >&2; usage; }; shift ;;
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
  # Limitations compared to realpath/readlink: symlinks are not resolved, and a dangling
  # symlink will use the link name as basename rather than the target name.
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
allowed_exts="html htm"
filename="$(basename "$target_file")"
lowername="$(echo "$filename" | tr '[:upper:]' '[:lower:]')"
_ext_match=false
for _ext in $allowed_exts; do
  [[ "$lowername" == *".$_ext" ]] && { _ext_match=true; break; }
done
if [ "$_ext_match" = false ]; then
  echo "Error: file must have .${allowed_exts// / or .} extension: $filename" >&2
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
