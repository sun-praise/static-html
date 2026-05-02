---
name: sth
description: Start or use the `sth` local HTML preview server from sun-praise/static-html. Use when the user mentions `sth`, wants `sth start`, `sth send`, `sth tag`, `sth categorize`, `sth project`, `sth list`, or `sth search`, needs to preview a local HTML file in a browser, manage session metadata, or register preview sessions from the CLI.
---

# STH Preview

Use this skill when a user wants a quick local preview for an HTML file and its relative assets, manage metadata (tags, categories, projects), or search sessions.

## Workflow

1. Bootstrap or locate the repo:

```bash
bash scripts/bootstrap-repo.sh
```

This script:
- uses `STATIC_HTML_REPO_DIR` if it already points at a checkout
- otherwise clones or updates `sun-praise/static-html`
- builds `dist/sth` when the Go source fingerprint changes

2. Start the preview server:

```bash
bash scripts/start-server.sh [port]
```

Defaults:
- host: `127.0.0.1`
- port: `3939`

3. Register an HTML file:

```bash
bash scripts/send-file.sh /absolute/or/relative/file.html [server_url]
```

The command uploads the HTML file and sibling assets from the same directory, then prints a session URL like `http://127.0.0.1:3939/s/<id>/`.

4. Manage session metadata:

```bash
# Add tags
sth tag <session-id> <tag...>

# Remove a tag
sth tag --rm <session-id> <tag...>

# Set category
sth categorize <session-id> [category]

# Set project
sth project <session-id> [project]

# List sessions (with optional filters)
sth list [--tag <tag>] [--category <cat>] [--project <proj>]

# Search sessions
sth search <query>
```

All metadata commands accept `--db /path/to/sessions.db` to override the database path.

## Environment overrides

- `STATIC_HTML_REPO_DIR`: existing checkout to reuse
- `STATIC_HTML_REPO_REF`: branch or ref to clone/update, default `main`
- `STATIC_HTML_SKIP_UPDATE=1`: skip fetch/pull when reusing an existing checkout
- `STATIC_HTML_HOST`: server host, default `127.0.0.1` (set this to your LAN host when running on another machine)
- `STATIC_HTML_PORT`: default port for `start-server.sh`
- `STATIC_HTML_SERVER_URL`: default server URL for `send-file.sh`

## Notes

- This skill expects `git`, `go`, and a working Go toolchain.
- The GitHub repo is private; authenticated `gh` or `git` access is required.
- The server renders the HTML as-is and serves relative assets from the uploaded directory snapshot for that file.
