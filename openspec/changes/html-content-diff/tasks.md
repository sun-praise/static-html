## 1. 算法层（internal/session/htmldiff.go）
- [x] 1.1 定义 `LineOpKind`（Equal/Add/Delete）、`LineOp{Kind, Text, OldNo, NewNo}`、`KindString()`。
- [x] 1.2 实现 `DiffLines(old, new []string) []LineOp`：LCS 动态规划（`dp[i][j]` 后缀 LCS），回溯生成 equal/add/delete 序列，修改=先 delete 后 add；1-based 行号。
- [x] 1.3 实现 `SplitLines(text)`：按 `\n` 切，去尾空行（让 `"a\n"` 与 `"a"` 等价）。
- [x] 1.4 实现 `Summarize(ops) DiffSummary{Added, Removed}`。
- [x] 1.5 定义 `MaxDiffLines=10000` + `DiffTooLargeText`，超阈值返回单条哨兵 op；恰好等于阈值仍 diff。
- [x] 1.6 `internal/session/htmldiff_test.go`：相同/纯增/纯删/修改=先删后增/重建双端性质校验/空输入/全异/大文件保护/SplitLines 尾换行/Summarize/KindString。

## 2. 数据层（internal/session/version.go）
- [x] 2.1 实现 `DiffSessionHTML(fromSessionID, toSessionID) ([]LineOp, error)`：`store.Get` 两份 → `os.ReadFile(StoredEntryFile)` → `DiffLines(SplitLines(...))`；任一 session 不存在返 `ErrSessionNotFound`；结果永不为 nil。
- [x] 2.2 `readSessionHTML(sess)`：`StoredEntryFile` 优先，回退 `EntryFile`；任何 I/O 错误返空串（降级，老 session 无文件不报错）。
- [x] 2.3 `version_test.go` 增测：增删改检测 / 相同文件全 equal / 文件缺失降级 / 未知 session 返 NotFound / 结果非 nil（用 `t.TempDir()` 写真实文件 + 新 helper `createChainedSessionWithFile`）。

## 3. 服务端（internal/server/server.go）
- [x] 3.1 `routes()` 在 `/chain` 后插入 `GET /api/sessions/{id}/diff` case（`hasPrefixSuffix` + `isExactSessionPath` 顺序）。
- [x] 3.2 定义 `diffLine{Kind, Text, OldNo, NewNo}` / `diffResponse{FromVersion, ToVersion, Lines, Summary}` / `diffSummary{Added, Removed}`。
- [x] 3.3 实现 `handleGetDiff`：`extractSessionIDFromMetaPath` / `requireSession` / `requireOwner` / `parseDiffVersions` / `GetChainOfSession` 解析链 / `versionSessionID` 校验 from/to 在同链 / `DiffSessionHTML` / 组装响应。
- [x] 3.4 `parseDiffVersions`：from/to 必须为正整数，缺失/非数字→400；`versionSessionID`：按 versionNo 在 versions 里找 sessionId，找不到→400；from==to→400。
- [x] 3.5 `server_test.go` 增测：`TestGetDiffSuccess`（验证 lines + summary + added 文本）/ `TestGetDiffMissingParams` / `TestGetDiffVersionOutOfRange` / `TestGetDiffSameVersionRejected` / `TestGetDiffNotFound`（用新 helper `createChainedSessionWithFile` 写真实文件）。

## 4. 预览页注入（internal/live/inject.go）
- [x] 4.1 `versionDrawerCSS` 新增：`.sth-version-diffbtn`（按钮 + badge）/ `.sth-version-htmldiff`（容器，monospace + max-height 滚动）/ `.hunk.add|.del|.ctx|.skip`（绿/红/灰/折叠样式），沿用 `!important` + `#sth-version-*` 命名空间。
- [x] 4.2 `versionDrawerJS` 的 `render()`：计算每个版本的前驱 version_no（最大且严格小于当前 live version_no，兼容软删空洞 v1,v3）。
- [x] 4.3 `item(v, diff, prevVersionNo)`：有前驱时渲染 `<button class="sth-version-diffbtn" data-from data-to>Show HTML diff</button>` + 空 `.sth-version-htmldiff` 容器。
- [x] 4.4 `wireDiffButtons()`：点击按钮 → fetch `/api/sessions/{id}/diff?from&to` → 渲染进容器；首次拉取后标 `data-loaded`，再点切换展开/折叠 + 按钮文案。
- [x] 4.5 `renderHtmlDiff(data)`：连续 equal 行折叠为单条 `⋯ N unchanged ⋯` skip 行，只保留 add/del；空改动显示 "No changes."。
- [x] 4.6 `formatBadge(summary)`：按钮 badge 显示 `+a −b`。
- [x] 4.7 `escapeHtml` 复用（diff 行文本先 escape 再包 `<span class="hunk">`，不产生 XSS）。

## 5. 文档与校验
- [x] 5.1 撰写 OpenSpec change：`openspec/changes/html-content-diff/` 全套（`.openspec.yaml` / `proposal.md` / `design.md` D19–D23 / `tasks.md` / `specs/version-chain/spec.md`）。
- [x] 5.2 `go test ./...` 全绿。
- [x] 5.3 `go vet ./...` 无警告。
- [x] 5.4 `openspec validate html-content-diff` 通过。
- [ ] 5.5 PR 合并后执行 `openspec archive html-content-diff`（跟随项目惯例，可批量整理时一并归档）。

## 6. PR review 修复（#84 CodeRabbit 评审）
- [x] 6.1 `renderHtmlDiff` 把"diff 太大"哨兵当普通 equal 行折叠（Major）：`diffResponse` 新增显式 `TooLarge bool` 字段，`handleGetDiff` 由 `len(ops)==1 && ops[0].Text==DiffTooLargeText` 计算；前端 `renderHtmlDiff` 优先判 `data.tooLarge` 渲染为 `.hunk.skip` 行，不靠 magic string。
- [x] 6.2 新增 `TestGetDiffTooLargeFilesSkipped`：构造 `MaxDiffLines+1` 行输入，断言响应 `tooLarge=true`、`lines` 单条哨兵、`summary` 归零。
