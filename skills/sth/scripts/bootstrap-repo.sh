#!/usr/bin/env bash

set -euo pipefail

repo_dir="${STATIC_HTML_REPO_DIR:-$HOME/.cache/static-html/repo}"
repo_ref="${STATIC_HTML_REPO_REF:-main}"
repo_https_url="${STATIC_HTML_REPO_URL:-https://github.com/sun-praise/static-html.git}"
binary_path="$repo_dir/dist/sth"
build_stamp="$repo_dir/dist/.static-html-go-build"

ensure_checkout() {
  if [ -d "$repo_dir/.git" ]; then
    if [ "${STATIC_HTML_SKIP_UPDATE:-1}" != "0" ]; then
      return
    fi
    git -C "$repo_dir" fetch origin "$repo_ref" --depth=1
    git -C "$repo_dir" checkout "$repo_ref"
    git -C "$repo_dir" pull --ff-only origin "$repo_ref"
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

ensure_binary() {
  local fingerprint
  fingerprint="$({
    [ -f "$repo_dir/go.mod" ] && cat "$repo_dir/go.mod"
    [ -f "$repo_dir/go.sum" ] && cat "$repo_dir/go.sum"
    find "$repo_dir/cmd" "$repo_dir/internal" -type f -name '*.go' -print0 | sort -z | xargs -0 cat
  } | sha256sum | awk '{print $1}')"

  if [ -x "$binary_path" ] && [ -f "$build_stamp" ] && [ "$(cat "$build_stamp")" = "$fingerprint" ]; then
    return
  fi

  mkdir -p "$(dirname "$binary_path")"
  (cd "$repo_dir" && go build -o "$binary_path" ./cmd/html-server) 1>&2
  printf '%s\n' "$fingerprint" > "$build_stamp"
}

ensure_checkout
ensure_binary
printf '%s\n' "$repo_dir"
