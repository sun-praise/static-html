## Why

`sth` 的核心场景是"agent 反复迭代同一份 HTML 产物"——同一个 dashboard、同一份报告，agent 会 `sth send` 多次。但目前每次 send 都是孤立的 session：首页列表里它们平铺成一堆同名条目，无法回答最基本的问题——

- "我上次这个 dashboard 改到哪了？"
- "这一版相比上一版改了什么？"
- "v3 的 tag 跟 v1 哪些不一样？"

agent 自己也不会主动管理版本关系（它每次 send 都是独立的，不带任何"上一版 id"的概念）。结果就是产物散落、没有回溯，迭代越多越混乱。

需要在**不要求 agent 或 skill 做任何改动**的前提下，把这种隐式的"同产物迭代"显式化为版本时间线，让人在浏览器侧能一眼看清迭代轨迹与相邻版本差异。

## What Changes

- 新增 `document_chains` 表，按 `(project, entry_file, user_id)` 唯一约束标识一条链；`user_id` 在 `--auth` 关闭时为 NULL（全局共享链），开启时按 owner 隔离。
- `sessions` 表新增 `chain_id` / `version_no` 两列（nullable，老数据向后兼容），索引 `(chain_id, version_no)`。
- `handleCreateUploadedSession` 在写入元数据后自动调用 `LinkToChain`：按 `(project, basename(entry_file), owner)` 查找或创建链，分配 `MAX(version_no)+1`。**链化失败仅 log 到 stderr，不阻塞 send 主流程**（降级为孤立 session）。
- 新增 `GET /api/sessions/{id}/chain` 接口，返回链元信息、所有版本（按 version_no 升序、当前版本高亮）以及相邻版本的元数据 diff（tags 增删、category/project 变更）。
- 预览页 `/s/<id>/` 通过 `internal/live.InjectMiddleware` 注入"版本时间线"侧栏：浮动徽标显示当前版本号（如 `v3`），点击懒加载 chain 接口，时间线纵向展示 v1→v2→v3，相邻版本下方显示元数据 diff。链长为 1 时不显示徽标。
- `createSessionResponse` 扩展 `chainId` / `versionNo` 字段；`sth send` 在 stdout 打印 URL（不变）后，于 stderr 打印 `version: vN of chain <id>`，供 agent / 用户感知链位置。
- 软删除链中间版本时，链结构保留，时间线跳过该版本（不重排版本号）。
- **向后兼容硬约束**：`--auth` 关闭时行为不变；`chain_id`/`version_no` nullable，老 session 不迁移不阻塞；现有 `send` / `list` / `search` / `preview` 路径逐字段兼容；`users` / `api_keys` / `user_credentials` / `login_sessions` schema 不动。

## Capabilities

### New Capabilities
- `version-chain`: 按产物身份（project + entry 文件名 + owner）自动将每次上传归入版本链；提供链视图与相邻版本元数据 diff；预览页注入时间线侧栏。版本链是"agent 发布、人类浏览"语义下回溯迭代轨迹的最小必需能力。

## Impact

- **数据库**（`internal/session/`）：`metadata.go` 的 `initMetadata` 新增 `document_chains` 表 + 索引；`store.go` 的 `ensureColumns` 新增 `sessions.chain_id` / `sessions.version_no` 列与 `idx_sessions_chain` 索引；新增 `version.go` 实现 `LinkToChain` / `GetChainOfSession` / `DiffChainMetadata`。
- **服务端**（`internal/server/`）：`server.go` 的 `routes()` switch 在 `/peers` 与 `/download` 之间插入 `/chain` case；新增 `handleGetChain` + `chainResponse` 类型；`handleCreateUploadedSession` 在 `setSessionMetadata` 后调用 `LinkToChain`（降级）；`createSessionResponse` 扩展两字段。
- **注入**（`internal/live/`）：`inject.go` 新增 `versionDrawerCSS` / `versionDrawerHTML` / `versionDrawerJS` 三个常量，追加进 `drawerBytes`，复用 `InjectMiddleware` 现有的 HTML 缓冲注入路径。
- **CLI**（`internal/cli/cli.go`）：`runSend` 接受 `stderr` 参数，解析响应新增 `chainId` / `versionNo` 并在 stderr 打印链信息；`list` / `search` 输出自动随 `DocumentInfo` 扩展（`chainId` / `versionNo` JSON 字段）。
- **依赖**：不新增任何依赖。
- **测试**：新增 `internal/session/version_test.go`（链化/续链/owner 隔离/软删跳过/diff）；`internal/server/server_test.go` 增测 `TestGetChainSuccess` / `TestGetChainSingleVersion` / `TestGetChainNotFound`；现有 `auth_test.go` / `server_test.go` / `e2e` 不改动即过（向后兼容）。
