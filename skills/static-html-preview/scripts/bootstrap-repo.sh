#!/usr/bin/env bash

set -euo pipefail

repo_dir="${STATIC_HTML_REPO_DIR:-$HOME/.cache/static-html/repo}"
repo_ref="${STATIC_HTML_REPO_REF:-main}"
repo_https_url="${STATIC_HTML_REPO_URL:-https://github.com/sun-praise/static-html.git}"
deps_stamp="$repo_dir/node_modules/.static-html-skill-lock"

ensure_checkout() {
  if [ -d "$repo_dir/.git" ]; then
    if [ "${STATIC_HTML_SKIP_UPDATE:-0}" != "1" ]; then
      git -C "$repo_dir" fetch origin "$repo_ref" --depth=1
      git -C "$repo_dir" checkout "$repo_ref"
      git -C "$repo_dir" pull --ff-only origin "$repo_ref"
    fi
    return
  fi

  rm -rf "$repo_dir"
  mkdir -p "$(dirname "$repo_dir")"

  if command -v gh >/dev/null 2>&1; then
    gh repo clone sun-praise/static-html "$repo_dir" -- --branch "$repo_ref"
  else
    git clone --branch "$repo_ref" "$repo_https_url" "$repo_dir"
  fi
}

ensure_dependencies() {
  local fingerprint
  fingerprint="$(
    cat "$repo_dir/package.json" "$repo_dir/package-lock.json" | sha256sum | awk '{print $1}'
  )"

  if [ -d "$repo_dir/node_modules" ] && [ -f "$deps_stamp" ] && [ "$(cat "$deps_stamp")" = "$fingerprint" ]; then
    return
  fi

  npm --prefix "$repo_dir" ci 1>&2
  mkdir -p "$(dirname "$deps_stamp")"
  printf '%s\n' "$fingerprint" > "$deps_stamp"
}

ensure_checkout
ensure_dependencies
printf '%s\n' "$repo_dir"
