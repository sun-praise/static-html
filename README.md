# static-html

[![Go Version](https://img.shields.io/badge/Go-1.24+-00ADD8?logo=go)](https://go.dev/)
[![Go Report Card](https://goreportcard.com/badge/github.com/sun-praise/static-html)](https://goreportcard.com/report/github.com/sun-praise/static-html)
[![GitHub release](https://img.shields.io/github/v/release/sun-praise/static-html?include_prereleases)](https://github.com/sun-praise/static-html/releases)
[![License](https://img.shields.io/github/license/sun-praise/static-html)](https://github.com/sun-praise/static-html/blob/main/LICENSE)

A local HTML preview server with a CLI for registering HTML files as browser-viewable sessions.

[中文](README.cn.md)

## Requirements

- Go 1.24+

## Build

```bash
go build -o dist/sth ./cmd/html-server
```

## Usage

Start the local server:

```bash
./dist/sth start
```

By default, sessions are stored in SQLite at `$XDG_STATE_HOME/sth/sessions.db` or `~/.local/state/sth/sessions.db`.
The session store uses `github.com/mattn/go-sqlite3`, so builds typically require CGO to be enabled and a working C toolchain. Some environments may also need SQLite development libraries installed.

Register an HTML file:

```bash
./dist/sth send ./fixtures/basic/index.html
```

The `send` command uploads the HTML file and all regular files under the same source directory as a zip archive, then prints a session URL. Open that URL in a browser to view the HTML file with its relative assets served from the uploaded snapshot on the server.

## Commands

```bash
sth start [--host 127.0.0.1] [--port 3939] [--db /path/to/sessions.db]
sth send <file.html> [--server http://127.0.0.1:3939]
sth tag [--rm] <session-id> <tag...>
sth categorize <session-id> [category]
sth project <session-id> [project]
sth list [--tag <tag>] [--category <cat>] [--project <proj>]
sth search <query> [--tag <tag>] [--category <cat>] [--project <proj>]
```

All commands accept `--db /path/to/sessions.db` to override the database path.

### `sth tag` — manage session tags

```bash
sth tag <session-id> <tag...>
sth tag --rm <session-id> <tag...>
```

Adds one or more tags to a session. Pass `--rm` before the session ID to remove the listed tags instead. Tags are free-form strings stored alongside the session record.

### `sth categorize` — set or clear a session category

```bash
sth categorize <session-id> [category]
```

Sets a single category label on the session. Omit `[category]` to clear the existing category.

### `sth project` — set or clear a session project

```bash
sth project <session-id> [project]
```

Sets a single project label on the session. Omit `[project]` to clear the existing project.

### `sth list` — filter sessions by metadata

```bash
sth list [--tag <tag>] [--category <cat>] [--project <proj>]
```

Filters sessions by exact match on the given metadata fields and prints matching results as JSON. Combine multiple flags to narrow results (all flags must match). With no flags, all sessions are returned.

### `sth search` — full-text search across sessions

```bash
sth search <query> [--tag <tag>] [--category <cat>] [--project <proj>]
```

Performs full-text matching across session metadata (tags, category, project, filename). Supports the same `--tag`, `--category`, and `--project` filters as `list` for narrowing results, so search and metadata filters can be combined in a single command.

#### `sth list` vs `sth search`

- `sth list` filters by exact metadata field values (`--tag`, `--category`, `--project`).
- `sth search <query>` does full-text matching across session content.
- They can be combined: `sth search <query> --tag <tag>` narrows text results to a specific tag.

## Test

```bash
go test ./...
```

## Troubleshooting

### `sth send` says server can't access HTML file path

This usually means the CLI on the machine running `send` is using a different `sth` version than the service host.

Checklist:

- Ensure the `send` command path on the client is the expected binary, e.g.:
  ```bash
  which sth
  /path/to/sth send ...
  ```
- Make sure both client and server use the same versioned build.
- After updating source or binaries, restart the systemd service on the server host:
  ```bash
  systemctl --user restart static-html.service
  ```
- Use `--server` explicitly when the client and server are on different machines:
  ```bash
  sth send /absolute/path/to/index.html --server http://192.168.2.14:3939
  ```

If the client is stale, replace it and retry; old clients can still send legacy `entryFile` metadata and trigger cross-host path errors even if the server is updated.

## Codex Skill

This repo also ships an installable Codex skill at `skills/static-html-preview`.

Install it from GitHub with the local installer helper:

```bash
~/.codex/skills/.system/skill-installer/scripts/install-skill-from-github.py \
  --repo sun-praise/static-html \
  --path skills/static-html-preview
```

After installing the skill, restart Codex to pick it up.
