## Why

`sth` 当前完全没有鉴权：任何能访问到 HTTP 端口的客户端都能上传、增量覆盖、删除任意 session，也能拉取全部列表与搜索结果。这对本地单人预览足够，但部署到团队/共享主机时，任何人都能互相覆盖或删除对方的预览内容，也无法追溯"谁上传的"。需要一种可选的身份与鉴权机制，让 `sth` 能安全地服务于多用户共享部署，同时不破坏现有的本地无鉴权体验与 `/s/<id>/` 链接分享用法。

## What Changes

- 新增可选的鉴权模式，由启动参数 `--auth` 或环境变量 `STH_AUTH` 控制，**默认关闭**，关闭时行为与现状完全一致（向后兼容）。
- 引入 `users` 与 `api_keys` 两张表，API key 归属于某个 user，存储其哈希值（非明文）。
- `sessions` 表新增 `user_id` 列，记录 session 的归属者（鉴权开启时必填，关闭时为空）。
- 鉴权开启时，所有变更类接口（上传、`watch` 增量写、删除、改元数据）要求有效的 API key；列表/搜索/下载接口同样要求 key 并按 owner 过滤。
- 预览链接 `/s/<id>/` 与 WebSocket `/s/<id>/ws` 的鉴权策略独立配置（默认在鉴权模式下仍保持开放，以保留分享预览的核心用法；可通过 `--protect-previews` 收紧）。
- CLI 侧新增 `--api-key` flag 与 `STH_API_KEY` 环境变量，`sth send` / `sth watch` 在请求时携带 `Authorization: Bearer <key>` 头。
- 新增 `sth user` 子命令用于管理用户与 API key（创建用户、签发/吊销 key）。
- **BREAKING**（仅当显式开启 `--auth` 时生效）：未携带有效 key 的请求将被拒绝（401）。默认关闭时不影响任何现有行为。

## Capabilities

### New Capabilities
- `user-auth`: 用户与 API key 的数据模型、签发、校验与请求鉴权，涵盖鉴权开关、预览保护开关、CLI 凭据传递与 `user` 管理子命令。

### Modified Capabilities
<!-- 现有 specs 均与鉴权无关（peers-api、preview-drawer），无需求级变更。 -->

## Impact

- **数据库**（`internal/session/store.go`、`metadata.go`）：新增 `users`、`api_keys` 表；`sessions` 增加 `user_id` 列并走既有 `ensureColumns` 迁移路径；列表/搜索/peers 查询在鉴权模式下按 owner 过滤。
- **服务端**（`internal/server/server.go`）：`Server` 结构体新增鉴权配置；`routes()` 前置鉴权中间件；各 handler 在鉴权模式下读取当前 user 并将其写入 session / 过滤结果。
- **CLI**（`internal/cli/cli.go`、`watch.go`）：`runSend` / `runWatch` 注入 `Authorization` 头；`parseArgs` 新增 `--api-key` / `STH_API_KEY`；新增 `user` 子命令。
- **Python 助手**（`skills/sth/scripts/send-file.py`）：透传 `--api-key` / `STH_API_KEY`。
- **部署**（`docker-compose.yml`、`.env.example`、`Dockerfile`）：暴露 `STH_AUTH` / `STH_API_KEY` 环境变量。
- **测试**：新增鉴权中间件、用户/key 管理、owner 过滤的单元与端到端测试。
