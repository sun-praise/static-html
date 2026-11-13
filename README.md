# static-html

[![Go Version](https://img.shields.io/badge/Go-1.24+-00ADD8?logo=go)](https://go.dev/)
[![Go Report Card](https://goreportcard.com/badge/github.com/sun-praise/static-html)](https://goreportcard.com/report/github.com/sun-praise/static-html)
[![GitHub release](https://img.shields.io/github/v/release/sun-praise/static-html?include_prereleases)](https://github.com/sun-praise/static-html/releases)
[![License](https://img.shields.io/github/license/sun-praise/static-html)](https://github.com/sun-praise/static-html/blob/main/LICENSE)

**The inbox for HTML your coding agent produces** — a local server where your agent drops the pages it generates, and you open them in the browser.

sth doesn't generate HTML. It's only the place those pages land.

| Who               | Does what                                  |
| ----------------- | ------------------------------------------ |
| Your coding agent | Generates HTML, runs `sth` to publish      |
| **sth**           | Stores, serves, indexes, isolates by user  |
| You               | Browse, search, share                      |

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
sth audit [--db /path/to/sessions.db] [--action <name>] [--actor <user>] [--outcome ok|denied] [--since YYYY-MM-DD] [--limit <n>]
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
by default so you can still share preview links. To also require a login for
previews, add `--protect-previews` (implies `--auth`):

```bash
sth start --auth --protect-previews
```

Under `--protect-previews`, an owned session is visible only to its owner and
the users it is explicitly shared with (see below). Sessions created before
auth was enabled (no owner) remain visible to any logged-in user.

### Sharing a session with another user

Session owners can grant a specific user read access to a preview — without
opening it up to everyone else:

```bash
sth share <session-id> bob              # grant bob read access to the whole session
sth share <session-id> bob --page report.html   # grant access to ONE page only
sth shares <session-id>                 # list who the session is shared with (and scope)
sth unshare <session-id> bob            # revoke the whole-session grant
sth unshare <session-id> bob --page report.html # revoke the page grant
```

A **page share** (`--page`, a path inside the session) grants exactly that
HTML page: sibling HTML pages and the session root stay 403, and only render
assets (css/js/images/fonts/media) stay reachable so the page renders. Other
files in the session — JSON payloads, text dumps, PDFs, archives — are session
content rather than page plumbing, so they need a whole-session share. Use this
to share a single report out of a multi-page upload. A whole-session share and
page shares are independent grants and can coexist.

These commands work against a remote server (`--server URL --api-key <key>`,
or `STH_API_KEY`) or a local database (`--db /path/to/sessions.db`), like the
other metadata commands.

You can also manage the share list in the browser: on the home page each of
your sessions has a 👥 button that opens a panel listing the share roster,
with a form to grant access by username (optionally scoped to a single page
path) and a × to revoke it. The panel authenticates with your login cookie
(cross-origin requests are rejected); no API key is needed.

A shared user can open the `/s/<id>/` preview and its drawer data (metadata,
version chain, related documents), and sees the session listed under
"Shared with you" on the home page. Editing, deleting, downloading, and share
management remain owner-only.

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

### Audit log

Every server records security-relevant events in the `audit_events` SQLite
table: login success/failure, logout, rejected credentials, share grants and
revocations, preview allow/deny, and session create/delete. Each row stores a
UTC timestamp, the actor (username + id snapshot), action, target, outcome,
client IP, User-Agent and a short detail.

The log is **local-only** and the query never changes business data. There is
deliberately no HTTP endpoint: the server has no admin role, so exposing the
log over the API would let any logged-in user read everyone else's activity.
`sth audit` still opens the SQLite DB read-write (it runs schema init and sets
WAL mode), so a read-only mount needs a writable copy. Query on the host:

```bash
sth audit --db /path/to/sessions.db
sth audit --action auth.login_failed --since 2026-11-01
sth audit --actor alice --outcome denied --limit 50
# container deployment (the binary is named html-server):
docker exec sth-app html-server audit --db /data/sessions.db
```

`--audit-retention-days` (or `STH_AUDIT_RETENTION_DAYS`) controls retention —
default 90 days, `0` keeps rows indefinitely. The server prunes once at start
and then every 24 hours; `sth audit --prune-days 90` prunes on demand.

Privacy boundaries: passwords, API keys, session cookies, request bodies and
query strings are **never** recorded. Failed logins do not persist the submitted
username at all (people sometimes paste a password into the username field) —
they record a fixed reason plus source metadata. Anonymous 401s are coalesced
per client IP and path within a 60-second window so a 401 flood cannot fill the
table, and `/logout` is only audited when it actually invalidates a session.
Only HTML page views are audited — css/js/image requests and the live-reload
websocket are skipped. The client IP comes from the first `X-Forwarded-For`
entry (the client IP behind a single trusted reverse proxy); a client can spoof
that header, so treat IP values as evidence rather than proof, and never use
them for authorization.

Coverage: only **server-side** events are audited. Local CLI commands that
write directly to the DB (`sth delete`, `sth user add`, `user issue-key` /
`revoke-key`) and manual SQL edits bypass the server and therefore produce no
audit rows.

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
