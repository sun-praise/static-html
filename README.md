# static-html

A local HTML preview server with a CLI for registering HTML files as browser-viewable sessions.

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

- `sth tag` adds tags to a session; use `--rm` to remove tags.
- `sth categorize` sets the session category. Omit `[category]` to clear it.
- `sth project` sets the session project. Omit `[project]` to clear it.
- `sth list` filters sessions by exact metadata values and prints matching results as JSON.
- `sth search` does full-text matching across session metadata (tags, category, project, filename). Supports the same `--tag`, `--category`, and `--project` filters as `list` for narrowing results.

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
