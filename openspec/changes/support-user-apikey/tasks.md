## 1. 数据层：users / api_keys 表与迁移

- [ ] 1.1 在 `internal/session/store.go` 新增 `users` 表建表语句（id、username UNIQUE、created_at），接入 `init()` 幂等建表
- [ ] 1.2 新增 `api_keys` 表建表语句（id、key_hash、salt、hash_algo、user_id FK、created_at、revoked_at、expires_at 可空），幂等建表
- [ ] 1.3 通过 `ensureColumns` 为 `sessions` 表增加可空 `user_id` 列；验证旧库自动加列、旧行 NULL
- [ ] 1.4 实现 `CreateUser(name) (User, error)`、`ListUsers() ([]User, error)`，用户名冲突返回明确错误
- [ ] 1.5 实现 `IssueAPIKey(userID) (plaintext, KeyRecord, error)`：`crypto/rand` 生成 `sth_` 前缀高熵 key，计算 `SHA-256(salt||key)`，仅存 salt+hash+algo
- [ ] 1.6 实现 `VerifyAPIKey(plaintext) (userID, ok, error)`：用存储的 salt 重算哈希并匹配未吊销记录
- [ ] 1.7 实现 `RevokeAPIKey(idOrPrefix) error` 与 `SetSessionOwner(sessionID, userID) error`
- [ ] 1.8 为上述 store 方法编写表驱动单元测试（含用户名冲突、吊销后校验失败、哈希不存明文）

## 2. 服务端：鉴权中间件与请求身份

- [ ] 2.1 在 `Server` 结构体（`internal/server/server.go`）新增 `authEnabled bool`、`protectPreviews bool` 字段及构造注入
- [ ] 2.2 实现 `authMiddleware(http.Handler) http.Handler`：解析 `Authorization: Bearer <key>`，调用 `VerifyAPIKey`，将 user 注入 `r.Context()`；失败返回 401
- [ ] 2.3 将 `authMiddleware` 挂在 `live.InjectMiddleware` 之前，仅在 `authEnabled` 时生效
- [ ] 2.4 定义受保护路径集合（写接口必进；预览路径受 `protectPreviews` 控制），未带凭据访问受保护路径返回 401
- [ ] 2.5 提供 `currentUser(ctx) (userID, ok)` 辅助函数供各 handler 读取身份
- [ ] 2.6 `--auth` 启动时在 stderr 打印鉴权姿态摘要（是否保护预览、当前用户数）

## 3. 服务端：owner 归属与隔离

- [ ] 3.1 `handleCreateUploadedSession` / `handleCreatePathSession`：鉴权模式下把当前 user 写入 `sessions.user_id`
- [ ] 3.2 `handleUpdateFiles`、`handleDeleteSession`、`handleAddTags`、`handleSetCategory`、`handleSetProject`：鉴权模式下先校验 session 归属，非 owner 返回 403
- [ ] 3.3 列表/搜索/peers 查询（`store.go` 的 `ListRecent`、`metadata.go` 的 `ListDocuments`/`SearchDocuments`/peers）：鉴权模式下追加 `WHERE user_id = ?` 分支
- [ ] 3.4 下载接口 `handleDownload`：鉴权模式下校验 owner，非 owner 返回 403
- [ ] 3.5 预览 `handlePreview` 与 `/s/<id>/ws`：仅当 `protectPreviews` 时走鉴权与 owner 校验

## 4. 启动配置：flag / 环境变量

- [ ] 4.1 `parseArgs`（`internal/cli/cli.go`）与 `runStart` 新增 `--auth`、`--protect-previews` flag
- [ ] 4.2 新增 `STH_AUTH`、`STH_PROTECT_PREVIEWS` 环境变量读取，flag 优先级高于 env
- [ ] 4.3 校验：开启 `--protect-previews` 时若未开 `--auth`，启动报错或自动隐含开启（明确选定一种并在日志说明）

## 5. CLI：send / watch 凭据传递

- [ ] 5.1 `runSend`、`runWatch` 新增 `--api-key` flag 与 `STH_API_KEY` 环境变量（flag 优先）
- [ ] 5.2 携带 `Authorization: Bearer <key>` 头；鉴权目标未知时按"有 key 即附加"实现，并在收到 401 时输出可操作提示
- [ ] 5.3 Python 助手 `skills/sth/scripts/send-file.py` 透传 `--api-key` / `STH_API_KEY`
- [ ] 5.4 更新 `skills/sth/SKILL.md` 或相关文档说明凭据传递

## 6. CLI：`sth user` 管理子命令

- [ ] 6.1 在 `internal/cli/cli.go` 调度器注册 `user` 子命令及 `add`/`issue-key`/`revoke-key`/`list` 子动作
- [ ] 6.2 `user add <name>`：调用 store 创建用户，输出结果
- [ ] 6.3 `user issue-key <name>`：调用 `IssueAPIKey`，明文 key 仅一次性 stdout 输出
- [ ] 6.4 `user revoke-key <id|prefix>`：吊销指定 key
- [ ] 6.5 `user list`：列出用户与其 key 数量
- [ ] 6.6 `user` 子命令直连本地 DB 文件（`--db` 可覆盖路径），不依赖 server 在线

## 7. 部署与配置文件

- [ ] 7.1 `docker-compose.yml` 的 server 服务 command / environment 暴露 `STH_AUTH`、`STH_PROTECT_PREVIEWS`、`STH_API_KEY`
- [ ] 7.2 `.env.example` 补充上述变量及注释说明
- [ ] 7.3 `Dockerfile` 无需改动则确认并记录；如需注入 env 则更新

## 8. 测试与文档

- [ ] 8.1 鉴权中间件单元测试：缺凭据/无效/已吊销 key 均 401；有效 key 通过
- [ ] 8.2 owner 隔离端到端测试：A 的列表不含 B、A 操作 B 的 session 返回 403、预览默认开放、`--protect-previews` 下预览 401
- [ ] 8.3 向后兼容测试：`--auth` 关闭时所有现有 `cli_test.go` 用例不改动即通过
- [ ] 8.4 CLI 凭据测试：`--api-key` 与 `STH_API_KEY` 均正确附加头；flag 优先于 env
- [ ] 8.5 更新 README 与 `sth --help` 文本，说明 `--auth`、`--protect-previews`、`--api-key`、`sth user` 用法
- [ ] 8.6 在 `openspec validate --change support-user-apikey` 通过且所有上述测试绿后，运行 `openspec archive` 归档 spec
