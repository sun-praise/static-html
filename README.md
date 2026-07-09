# static-html

[![Go Version](https://img.shields.io/badge/Go-1.24+-00ADD8?logo=go)](https://go.dev/)
[![Go Report Card](https://goreportcard.com/badge/github.com/sun-praise/static-html)](https://goreportcard.com/report/github.com/sun-praise/static-html)
[![GitHub release](https://img.shields.io/github/v/release/sun-praise/static-html?include_prereleases)](https://github.com/sun-praise/static-html/releases)
[![License](https://img.shields.io/github/license/sun-praise/static-html)](https://github.com/sun-praise/static-html/blob/main/LICENSE)

A local HTML preview server with a CLI for registering HTML files as browser-viewable sessions.

**Live demo:** <https://sth.sun-praise.com> — browse a [sample session](https://sth.sun-praise.com/s/985f96abaaf933aab01acc50cbd62e6b/).

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

By default the file's parent directory is treated as the archive root. Two optional flags make this behavior explicit and overridable:

- `--single` — archive only the entry file itself, skipping the parent-directory walk. Use this when the file lives in a large or unrelated directory (e.g. your home dir, a downloads folder) to avoid bundling unrelated siblings.
- `--root <dir>` — use `<dir>` as the archive root instead of the entry's parent. The entry may be nested anywhere under `<dir>`; the server locates it via the reported relative path. Useful for multi-file sites where the entry is not at the package root.

The two flags are mutually exclusive.

## Commands

```bash
sth start [--host 127.0.0.1] [--port 3939] [--db /path/to/sessions.db]
sth send <file.html> [--server http://127.0.0.1:3939] [--single] [--root <dir>]
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

## Authentication

By default `sth` runs **without authentication**, which is fine for local
single-user previewing. For shared or multi-user deployments, enable optional
API-key authentication.

### Enabling auth

Start the server with `--auth` (or `STH_AUTH=true`):

```bash
sth start --auth
# or: STH_AUTH=true sth start
```

When auth is on, all mutating endpoints and list/search/peers/download require
a valid `Authorization: Bearer <key>` header. `/s/<id>/` previews stay **open**
by default so you can still share preview links. To also require a key for
previews, add `--protect-previews` (implies `--auth`):

```bash
sth start --auth --protect-previews
```

### Managing users and API keys

`sth user` operates directly on the local database (no running server needed):

```bash
sth user add alice                 # create a user
sth user issue-key alice           # print a new API key (shown ONCE)
sth user list                      # list users and active key counts
sth user revoke-key <id|prefix>    # revoke a key (fails closed on ambiguous prefix)
```

The plaintext key is printed only at issue time. Only a salted SHA-256 hash is
stored; there is no way to recover a key after it is issued.

### Sending with a key

`sth send` and `sth watch` accept `--api-key` or the `STH_API_KEY` environment
variable (the flag wins):

```bash
sth send index.html --tag t --category c --project p --api-key sth_xxxx
# or: export STH_API_KEY=sth_xxxx && sth send ...
```

If the server has auth on and no key is provided, the client reports a 401 with
an actionable hint pointing at `--api-key` / `STH_API_KEY`.

### Owner isolation

When auth is on, each session is owned by the user who created it. Users can
only list, search, read, modify, or download their own sessions. Sessions
created before auth was enabled have no owner and are not accessible to any
authenticated user — re-upload them after enabling auth if you need to keep
them.

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

## Agent Skill

This repo ships an agent skill at `skills/sth` that teaches coding agents (Claude Code, Codex, Cursor, etc.) how to use `sth`.

Install it with [npx skills](https://github.com/vercel-labs/skills):

```bash
# Install to current project
npx skills add sun-praise/static-html

# Install globally
npx skills add sun-praise/static-html -g

# Install to specific agents
npx skills add sun-praise/static-html -a claude-code -a cursor
```
