# static-html

[![Go Version](https://img.shields.io/badge/Go-1.24+-00ADD8?logo=go)](https://go.dev/)
[![Go Report Card](https://goreportcard.com/badge/github.com/sun-praise/static-html)](https://goreportcard.com/report/github.com/sun-praise/static-html)
[![GitHub release](https://img.shields.io/github/v/release/sun-praise/static-html?include_prereleases)](https://github.com/sun-praise/static-html/releases)
[![License](https://img.shields.io/github/license/sun-praise/static-html)](https://github.com/sun-praise/static-html/blob/main/LICENSE)

本地 HTML 预览服务器，提供 CLI 工具将 HTML 文件注册为可在浏览器中查看的会话。

**在线演示**：<https://sth.sun-praise.com> — 查看[示例会话](https://sth.sun-praise.com/s/985f96abaaf933aab01acc50cbd62e6b/)。

[English](README.md)

## 环境要求

- Go 1.24+

## 构建

```bash
go build -o dist/sth ./cmd/html-server
```

## 使用方法

启动本地服务器：

```bash
./dist/sth start
```

默认情况下，会话存储在 SQLite 数据库中，路径为 `$XDG_STATE_HOME/sth/sessions.db` 或 `~/.local/state/sth/sessions.db`。
会话存储使用 `github.com/mattn/go-sqlite3`，因此构建通常需要启用 CGO 并安装可用的 C 工具链。部分环境可能还需要安装 SQLite 开发库。

注册 HTML 文件：

```bash
./dist/sth send ./fixtures/basic/index.html
```

`send` 命令会将 HTML 文件及其同目录下的所有常规文件打包为 zip 压缩包上传，然后输出一个会话 URL。在浏览器中打开该 URL 即可查看 HTML 文件，其关联的静态资源也会从服务器上传的快照中提供。

默认以文件所在父目录作为打包根目录。提供两个可选 flag，让这个行为显式且可覆盖：

- `--single` —— 只打包该文件本身，跳过父目录遍历。当文件位于一个很大或无关的目录（如 home 目录、下载文件夹）时使用，避免把无关同级文件一起打包。
- `--root <dir>` —— 用 `<dir>` 作为打包根目录，替代默认的父目录。入口文件可以嵌套在 `<dir>` 的任意子路径下，服务器会根据上报的相对路径定位它。适用于多文件站点、入口不在打包根的情形。

两个 flag 互斥，不能同时使用。

## 命令

```bash
sth start [--host 127.0.0.1] [--port 3939] [--db /path/to/sessions.db]
sth send <file.html> [--server http://127.0.0.1:3939] [--single] [--root <dir>]
sth tag [--rm] <session-id> <tag...>
sth categorize <session-id> [category]
sth project <session-id> [project]
sth list [--tag <tag>] [--category <cat>] [--project <proj>]
sth search <query> [--tag <tag>] [--category <cat>] [--project <proj>]
```

所有命令均支持 `--db /path/to/sessions.db` 参数来覆盖数据库路径。

### `sth tag` — 管理会话标签

```bash
sth tag <session-id> <tag...>
sth tag --rm <session-id> <tag...>
```

为会话添加一个或多个标签。在会话 ID 前加 `--rm` 可移除指定标签。标签是与会话记录一起存储的自由格式字符串。

### `sth categorize` — 设置或清除会话分类

```bash
sth categorize <session-id> [category]
```

为会话设置单个分类标签。省略 `[category]` 可清除已有的分类。

### `sth project` — 设置或清除会话项目

```bash
sth project <session-id> [project]
```

为会话设置单个项目标签。省略 `[project]` 可清除已有的项目。

### `sth list` — 按元数据筛选会话

```bash
sth list [--tag <tag>] [--category <cat>] [--project <proj>]
```

按给定元数据字段的精确匹配筛选会话，并以 JSON 格式输出结果。可组合多个标志来缩小范围（所有标志必须同时匹配）。不传标志则返回所有会话。

### `sth search` — 全文搜索会话

```bash
sth search <query> [--tag <tag>] [--category <cat>] [--project <proj>]
```

对会话元数据（标签、分类、项目、文件名）执行全文匹配。支持与 `list` 相同的 `--tag`、`--category` 和 `--project` 过滤条件来缩小结果范围，搜索和元数据过滤可以在一条命令中组合使用。

#### `sth list` 与 `sth search` 的区别

- `sth list` 按元数据字段精确值筛选（`--tag`、`--category`、`--project`）。
- `sth search <query>` 对会话内容进行全文匹配。
- 可以组合使用：`sth search <query> --tag <tag>` 将全文搜索结果限定到特定标签。

## 测试

```bash
go test ./...
```

## 故障排除

### `sth send` 提示服务器无法访问 HTML 文件路径

通常意味着运行 `send` 的客户端 CLI 版本与服务端不一致。

检查清单：

- 确保客户端的 `send` 命令路径指向正确的二进制文件：
  ```bash
  which sth
  /path/to/sth send ...
  ```
- 确保客户端和服务端使用相同版本的构建。
- 更新源码或二进制文件后，重启服务端的 systemd 服务：
  ```bash
  systemctl --user restart static-html.service
  ```
- 当客户端和服务端不在同一台机器时，显式指定 `--server`：
  ```bash
  sth send /absolute/path/to/index.html --server http://192.168.2.14:3939
  ```

如果客户端版本过旧，请替换后重试；旧客户端仍可能发送过时的 `entryFile` 元数据，即使服务端已更新也会触发跨主机路径错误。

## Agent 技能

本仓库附带了一个 agent 技能，位于 `skills/sth`，可以让编码助手（Claude Code、Codex、Cursor 等）自动使用 `sth`。

使用 [npx skills](https://github.com/vercel-labs/skills) 安装：

```bash
# 安装到当前项目
npx skills add sun-praise/static-html

# 全局安装
npx skills add sun-praise/static-html -g

# 安装到指定 agent
npx skills add sun-praise/static-html -a claude-code -a cursor
```
