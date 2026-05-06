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

When calling `sth send` directly, `--tag` accepts multiple comma-separated values:

```bash
sth send file.html --tag tag1,tag2,tag3 --category cat --project proj
```

4. Manage session metadata:

```bash
# Add tags (supports multiple tags at once)
sth tag <session-id> <tag1> [tag2] ...

# Remove tags (supports multiple tags at once)
sth tag --rm <session-id> <tag1> [tag2] ...

# Set category (omit [category] to clear)
sth categorize <session-id> [category]

# Set project (omit [project] to clear)
sth project <session-id> [project]

# List sessions (with optional filters)
sth list [--tag <tag>] [--category <cat>] [--project <proj>]

# Search sessions (with optional metadata filters)
sth search <query> [--tag <tag>] [--category <cat>] [--project <proj>]
```

All metadata commands accept `--db /path/to/sessions.db` to override the database path.

### `sth list` vs `sth search`

- `sth list` filters by exact metadata field values (`--tag`, `--category`, `--project`).
- `sth search <query>` does full-text matching across session metadata (tags, category, project, filename). Filters and search can be combined, e.g. `sth search <query> --tag <tag>`.

## Environment overrides

- `STATIC_HTML_REPO_DIR`: existing checkout to reuse
- `STATIC_HTML_REPO_REF`: branch or ref to clone/update, default `main`
- `STATIC_HTML_SKIP_UPDATE=1`: skip fetch/pull when reusing an existing checkout
- `STATIC_HTML_HOST`: server host, default `127.0.0.1` (set this to your LAN host when running on another machine)
- `STATIC_HTML_PORT`: default port for `start-server.sh`
- `STATIC_HTML_SERVER_URL`: default server URL for `send-file.sh`

## FAQ

### Tag 可以有一个还是多个？

**支持多个。** 系统设计上每个 session 可以关联多个 tag：

- `sth send` 用逗号分隔：`--tag tag1,tag2,tag3`
- `sth tag` 用空格分隔多个位置参数：`sth tag <session-id> tag1 tag2 tag3`
- Web UI 也会把多个 tag 渲染成独立的标签 pill

### 为什么页面上某个 session 只显示一个 tag，而且看起来像 JSON（如 `["日报"]`）？

这是因为**创建该 session 的客户端把 tag 作为 JSON 字符串发送了**，而不是 JSON 数组。服务器把这个 JSON 字符串当作**单个 tag** 存入了数据库。

例如，客户端错误地发送了：
```json
{"tags": "[\"日报\"]"}
```

服务器会将其反序列化为 `["[\"日报\"]"]`，最终页面上只显示一个内容为 `["日报"]` 的 tag。

**正确做法**：发送 JSON 数组或直接使用 `sth send --tag`（内部会自动处理为多个值）。

## Notes

- This skill expects `git`, `go`, and a working Go toolchain.
- The GitHub repo is private; authenticated `gh` or `git` access is required.
- The server renders the HTML as-is and serves relative assets from the uploaded directory snapshot for that file.
