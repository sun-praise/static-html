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

The `send` command prints a session URL. Open that URL in a browser to view the HTML file with relative assets served from the source directory.

## Commands

```bash
sth start [--host 127.0.0.1] [--port 3939] [--db /path/to/sessions.db]
sth send <file.html> [--server http://127.0.0.1:3939]
```

## Test

```bash
go test ./...
```

## Codex Skill

This repo also ships an installable Codex skill at `skills/static-html-preview`.

Install it from GitHub with the local installer helper:

```bash
~/.codex/skills/.system/skill-installer/scripts/install-skill-from-github.py \
  --repo sun-praise/static-html \
  --path skills/static-html-preview
```

After installing the skill, restart Codex to pick it up.
