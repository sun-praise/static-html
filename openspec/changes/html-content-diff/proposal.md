## Why

`version-chain-sessions` 已经把同产物的多次迭代串成 v1→v2→v3，并在预览页注入了版本时间线侧栏 + 相邻版本**元数据** diff（tags/category/project）。但用户最直接的下一个问题——"v2 的 HTML 到底改了哪几行"——还答不上。

现在的 diff 只停留在 metadata 层面。对 agent 反复迭代的 HTML 产物，真正能解释"这一版改了什么"的是内容本身：新增了一个图表、改了某段文案、删了一列。元数据 diff 摸不到这些。这是版本链功能的"灵魂补全"——链化是骨架，内容 diff 才让时间线真正可读。

需要在版本链之上补一层 HTML 行级 diff，让用户在时间线侧栏点开任意相邻版本就能看到红绿行，且**不引入新依赖、不破坏现有 chain 接口**。

## What Changes

- 新增纯函数行级 diff 算法（LCS 动态规划）于 `internal/session/htmldiff.go`，输出 `[]LineOp{Kind, Text, OldNo, NewNo}`。对超过 `MaxDiffLines`（10000 行）的输入返回跳过哨兵，避免 O(n*m) DP 拖垮请求。
- 新增 `Store.DiffSessionHTML(fromSessionID, toSessionID)`：读两份 `StoredEntryFile` 离盘 diff（项目首个把 session 内容读进内存的代码，沿用 `store.Get` + `StoredEntryFile` 模式）；文件缺失降级为空内容而非报错。
- 新增 `GET /api/sessions/{id}/diff?from=vN&to=vM`：`{id}` 是链上任意 session（用于解析链 + owner），`from/to` 是同链 1-based 版本号；返回 `{fromVersion, toVersion, lines:[{kind,text,oldNo,newNo}], summary:{added,removed}}`。跨链 / 越界 / from==to → 400。
- 预览页版本时间线侧栏每个（有前驱的）版本项加"Show HTML diff"按钮：点击懒加载 diff 接口，红绿行内联展开；相等的上下文行折叠为"⋯ N unchanged ⋯"避免淹没改动；按钮旁显示 `+a −b` 摘要；再点折叠。
- **不修改任何 DB schema**：diff 是纯计算，结果不入库。
- **不引入任何新依赖**：LCS 手写，正面回应 `version-chain-sessions` Non-Goals 中的"无 diff 库"。
- **向后兼容硬约束**：现有 `GET /api/sessions/{id}/chain` 响应逐字段不变（diff 走独立接口）；老 session 文件缺失时 diff 降级为空；不破坏现有 send/list/search/preview/chain 路径。

## Capabilities

### Modified Capabilities
- `version-chain`: 在已落地的版本链（自动链化、时间线侧栏、元数据 diff）之上，新增相邻版本 HTML 内容的行级 diff 接口与时间线内联渲染。链化、链查询、元数据 diff 语义不变。

<!-- 不新增 capability，本次是对 version-chain 能力的增量扩展（与元数据 diff 平行的"内容 diff"维度）。 -->

## Impact

- **数据层 / 算法**（`internal/session/`）：新增 `htmldiff.go`（`LineOp` / `LineOpKind` / `DiffLines` / `SplitLines` / `Summarize` / `MaxDiffLines` / `DiffTooLargeText`，纯函数无 Store 依赖）；`version.go` 新增 `DiffSessionHTML` + `readSessionHTML`（首个读 session 内容进内存的代码）。
- **服务端**（`internal/server/`）：`server.go` 的 `routes()` switch 在 `/chain` 与 `/diff`（新增）之间插入 case；新增 `handleGetDiff` + `diffLine` / `diffResponse` / `diffSummary` 类型 + `parseDiffVersions` / `versionSessionID` 辅助函数。
- **注入**（`internal/live/`）：`inject.go` 的 `versionDrawerCSS` 新增 `.sth-version-diffbtn` / `.sth-version-htmldiff` / `.hunk.{add,del,ctx,skip}` 规则；`versionDrawerJS` 的 `item()` 加 diff 按钮 + 容器，新增 `wireDiffButtons` / `renderHtmlDiff` / `formatBadge` 函数，`render()` 计算每个版本的前驱版本号。
- **CLI**：无改动（diff 是浏览器侧能力，不在 CLI 暴露）。
- **依赖**：**不新增任何依赖**。LCS 手写 ~60 行 Go，正面回应上一版 Non-Goals 的"无 diff 库"——这正是本期得以落地的关键（行级 diff 算法成熟、可控、可测，无需引入第三方库）。
- **测试**：新增 `internal/session/htmldiff_test.go`（LCS 正确性 / 空 / 相同 / 全异 / 修改=先删后增 / 大文件保护 / SplitLines / Summarize / KindString）；`version_test.go` 增测 `DiffSessionHTML`（增删改 / 相同 / 文件缺失降级 / 未知 session / 非 nil）；`server_test.go` 增测 `TestGetDiffSuccess` / `TestGetDiffMissingParams` / `TestGetDiffVersionOutOfRange` / `TestGetDiffSameVersionRejected` / `TestGetDiffNotFound`。现有测试不改动即过。
