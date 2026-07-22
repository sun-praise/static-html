## 1. 数据层：document_chains 表 + sessions 列
- [x] 1.1 在 `internal/session/metadata.go` 的 `initMetadata` 加 `CREATE TABLE IF NOT EXISTS document_chains`（chain_id PK, project, entry_file, user_id NULL, created_at_unix, UNIQUE(project, entry_file, user_id)）与 `idx_document_chains_lookup` 索引。
- [x] 1.2 在 `internal/session/store.go` 的 `ensureColumns` 加 `sessions.chain_id TEXT DEFAULT NULL` 与 `sessions.version_no INTEGER DEFAULT NULL` 两个 idempotent 迁移；加 `idx_sessions_chain (chain_id, version_no)` 索引。
- [x] 1.3 在 `internal/session/metadata.go` 的 `DocumentInfo` 加 `ChainID string` 与 `VersionNo int`（带 JSON 标签）。
- [x] 1.4 扩展 `ListDocuments` 与 `SearchDocuments` 的 SELECT 列表与 `rows.Scan` 读取 `s.chain_id` / `s.version_no`。

## 2. 数据层：链化与查询（internal/session/version.go）
- [x] 2.1 定义 `ChainInfo` / `ChainVersion` / `VersionMetadataDiff` 三个类型（带 JSON 标签）。
- [x] 2.2 实现 `LinkToChain(sessionID, project, entryFile, ownerID) (chainID, versionNo, error)`：单事务，SELECT 现有链 MAX(version_no) 或新建链，UPDATE sessions 行；ownerID 空时 `user_id IS NULL` 全局共享。
- [x] 2.3 实现 `GetChainOfSession(sessionID) (ChainInfo, []ChainVersion, error)`：未链化时合成单版本视图；已链化时返回所有 `deleted_at IS NULL` 版本按 version_no ASC；当前版本置 `Current=true`。
- [x] 2.4 实现 `DiffChainMetadata(chainID) ([]VersionMetadataDiff, error)`：v1 vs 空基线、之后每对相邻版本计算 tags 增删与 category / project 变更。
- [x] 2.5 `internal/session/version_test.go` 覆盖：新链 / 续链 / version_no 自增、不同 project/entry 隔离、owner 隔离、anonymous 全局共享、软删跳过、未链化合成视图、diff 正确性、ListDocuments 含 chainId/versionNo。

## 3. 服务端：chain API + 自动链化（internal/server/）
- [x] 3.1 `server.go` `routes()` 在 `/peers` 与 `/download` 之间插入 `GET /api/sessions/{id}/chain` case（必须在 `isExactSessionPath` 前）。
- [x] 3.2 实现 `handleGetChain`：`extractSessionIDFromMetaPath` 解析、`requireSession` 存在性、`requireOwner` 隔离、调用 `GetChainOfSession` + `DiffChainMetadata`，返回 `chainResponse{chain, current, versions, metadataDiff}`。
- [x] 3.3 `handleCreateUploadedSession` 在 `setSessionMetadata` 成功后调用 `LinkToChain`，失败仅 `fmt.Fprintf(os.Stderr, ...)` 不阻塞主流程。
- [x] 3.4 `createSessionResponse` 扩展 `ChainID` / `VersionNo` JSON 字段。
- [x] 3.5 `internal/server/server_test.go` 增测 `TestGetChainSuccess`（多版本 + diff 校验）/ `TestGetChainSingleVersion`（未链化合成视图）/ `TestGetChainNotFound`（404）。

## 4. 预览页注入版本时间线（internal/live/）
- [x] 4.1 `inject.go` 新增 `versionDrawerCSS`（`#sth-version-*` 命名空间 + `!important` 隔离）。
- [x] 4.2 `inject.go` 新增 `versionDrawerHTML`（默认 `display:none` 的徽标 + 侧滑面板骨架）。
- [x] 4.3 `inject.go` 新增 `versionDrawerJS`：页面加载 probe `/api/sessions/{id}/chain`，链长 > 1 时显示徽标；点击懒加载、缓存复用；时间线纵向渲染 v1→vN，当前高亮；相邻版本下方渲染元数据 diff（`+tag` / `-tag` / `category: a→b`）；错误重试。
- [x] 4.4 把三个新常量追加进 `drawerBytes`，复用 `InjectMiddleware` 注入路径，不改其本体。

## 5. CLI
- [x] 5.1 `internal/cli/cli.go`：`Run` 调度把 `stderr` 传给 `runSend`；`runSend` 签名改为 `(args, stdout, stderr io.Writer)`。
- [x] 5.2 `runSend` 解析响应新增 `ChainID` / `VersionNo`；stdout 仍只打印 URL（保持管道友好），stderr 打印 `version: vN of chain <id>`（仅当 chainId 非空）。
- [x] 5.3 `sth list` / `sth search` 的 JSON 输出自动随 `DocumentInfo` 扩展 `chainId` / `versionNo` 字段（无需 CLI 端额外改动）。

## 6. 文档与校验
- [x] 6.1 撰写 OpenSpec change：`openspec/changes/version-chain-sessions/` 全套（`.openspec.yaml` / `proposal.md` / `design.md` / `tasks.md` / `specs/version-chain/spec.md`）。
- [x] 6.2 `go test ./...` 全绿。
- [x] 6.3 `go vet ./...` 无警告。
- [x] 6.4 `openspec validate version-chain-sessions` 通过。
- [ ] 6.5 PR 合并后执行 `openspec archive version-chain-sessions`。

## 7. PR review 修复（#83 CodeRabbit 评审）
- [x] 7.1 `handleGetChain` 对软删 session 返回 404：`GetChainOfSession` 读 `deleted_at`，软删直接返 `ErrSessionNotFound`；handler 去掉"fabricate current"兜底，改为 `current.SessionID == ""` 即 404。
- [x] 7.2 `LinkToChain` 原子化：改用 `BEGIN IMMEDIATE` 写锁 + `INSERT ... ON CONFLICT DO NOTHING` upsert + 再 SELECT，根除 anonymous（NULL owner）竞态。
- [x] 7.3 删除 `version.go` 冗余 `currentVersion == 0` fallback 循环。
- [x] 7.4 删除冗余 `idx_document_chains_lookup` 索引（UNIQUE 约束已自带 `(project, entry_file, user_id)` 索引），并在 `metadata.go` 文档化 UNIQUE 对 NULL owner 不生效的限制。
- [x] 7.5 新增回归测试：`TestGetChainOfSession_RequestedSessionSoftDeleted_ReturnsNotFound`（store 层）+ `TestGetChainSoftDeletedRequestReturns404`（HTTP 层）。
- [x] 7.6 同步更新 `design.md` D17 与 Risks 的事务安全说明（从 `SetMaxOpenConns(1)` 改述为 `BEGIN IMMEDIATE`）。
