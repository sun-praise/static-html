## Context

`version-chain-sessions`（D14–D18）落地了版本链：`(project, entry_file basename, owner)` 自动归链、`GET /api/sessions/{id}/chain`、预览页时间线侧栏、相邻版本**元数据** diff（D17 明确把 HTML 内容 diff 排除在外，列为"留作下一版独立 capability"）。

本期就是这个"下一版"。现有相关实现要点（决策参照）：

- `Store.Get(sid).StoredEntryFile` 给出 session 入口 HTML 的**绝对磁盘路径**（`store.go` 的 `CreateUploaded` 从 `os.MkdirTemp(uploadRoot, "session-*")` 构建）。同链的多个 session 是 `uploadRoot` 下的兄弟 `session-*` 目录，互相独立可读。
- 但此前**没有任何代码把 session 内容读进内存**——preview 走 `http.ServeFile`、download 走 `io.Copy` 进 zip、live reload 走 fsnotify。本期是首个 `os.ReadFile(session.StoredEntryFile)` 的地方。
- `inject.go` 的 `InjectMiddleware` 已成熟：缓冲 `/s/` 响应、对 `text/html` 在 `</head>` 前注入。peers drawer 与 version drawer 共享 `drawerBytes` 注入路径。version drawer 的 `item()` / `render()` 已是现成模板。
- 项目对依赖的态度：**反复声明"无新依赖偏好"**（`support-password-login` D8、`version-chain` Non-Goals 明确写"无 diff 库"）。但偏好可破——bcrypt / websocket / fsnotify 都是正当理由才加的。
- 决策号续接：上一版止于 D18，本期从 **D19** 起号。
- `go.mod`：`go 1.24.1`，无任何 diff 库（sergi/go-diff、pmezard/difflib 均不在依赖图）。

## Goals / Non-Goals

**Goals:**
- 在时间线侧栏点开任意相邻版本对，看到 HTML 行级 diff（新增行 / 删除行），让"v2 改了什么"可读。
- 零新依赖：LCS 手写，正面回应上一版"无 diff 库"Non-Goal。
- 向后兼容：chain 接口逐字段不变；diff 走独立按需接口；老 session 文件缺失降级而非报错。
- 大文件安全：超阈值输入返回跳过哨兵，不让 O(n*m) DP 拖垮请求。

**Non-Goals:**
- 不做词级 / 字符级 diff——行级已能回答"改了哪块"，词级需分词 + 标签边界处理，性价比低。
- 不做 DOM 树 diff——需引 `golang.org/x/net/html`，破坏无新依赖约束；且 agent 产物行级 diff 已足够结构化。
- 不做全屏并排对比预览——需 iframe 沙箱 + 并排布局，工程量大；侧栏内联展开已能看改动概览。
- 不做跨任意两个版本的 diff——本期仅相邻版本（时间线上每个版本对它的直接前驱）；任意版本对比留待 UI 反馈。
- 不做 diff 持久化 / 缓存——diff 是纯计算，每次请求即时算，永远反映当前文件状态（live reload 改了文件后 diff 自动更新）。
- 不在 CLI 暴露 `sth diff`——diff 是浏览器侧可视化能力，CLI 用户用 `git diff` 等工具更顺手。

## Decisions

### D19: diff 粒度——行级 LCS，手写，无依赖
**选择**：`DiffLines(old, new []string) []LineOp` 用经典 LCS 动态规划（O(n*m) 时间和内存，`dp[i][j]` = 后缀 LCS 长度），输出 `LineOp{Kind: Equal|Add|Delete, Text, OldNo, NewNo}`。修改表现为"先 delete 旧行再 add 新行"（LCS 的自然结果，与 unified diff 习惯一致）。纯 stdlib，~60 行 Go。
**理由**：(1) agent 产出的 HTML 通常是结构化的（每行一个标签/一段文案），行级 diff 已能精确定位"哪块改了"。(2) LCS 是最经典、最可控、最可测的 diff 算法，无需引入第三方库——这直接化解了上一版 Non-Goals 的"无 diff 库"约束，是本期得以落地的关键。(3) 修改=先删后增符合 unified diff 直觉，前端按 `kind` 染色即可。
**备选**：(a) 词级 diff（diffmatchpatch 风格）——否决，需分词 + HTML 标签边界处理复杂，且对"改了哪块"这个核心问题的边际收益低于行级；(b) DOM 树 diff——否决，需引 `golang.org/x/net/html`，破坏无新依赖约束；(c) 引 `sergi/go-diff` / `pmezard/difflib`——否决，上一版 Non-Goals 明确写了"无 diff 库"，且 LCS 手写成本可控。

### D20: 渲染方式——时间线侧栏内联展开，上下文折叠
**选择**：版本时间线侧栏每个（有前驱的）版本项加"Show HTML diff"按钮，点击懒加载 diff 接口、红绿行内联展开进 `.sth-version-htmldiff` 容器。**相等的上下文行折叠为单行 `⋯ N unchanged ⋯`**，只保留改动行 ± 紧邻上下文（本期实现是"完全折叠连续 equal 行为一条 skip 行"，不留 hunk 上下文——见 D22 取舍）。按钮旁显示 `+a −b` 摘要。再点折叠。
**理由**：侧栏只有 340px 宽，若展示全部 equal 行会被上下文淹没，改动行反而找不到。折叠 equal 行让改动一目了然——这是 GitHub PR diff 的核心交互模式（hunk 折叠），但在侧栏这个尺寸下我们做得更激进：连续 equal 直接合成一条 skip 行。与现有元数据 diff 同位置（`item()` 内、metadata diff 之下），体验一致。
**备选**：(a) 全屏并排对比（左 v2 右 v3 iframe）——否决，需 iframe 沙箱防 XSS + 并排布局，工程量大，且跳出预览上下文丢失"我在看 v3"的位置感；(b) 只在 CLI 输出 unified diff——否决，没用上预览页生态，且 agent 产物在浏览器看比终端看更直观。

### D21: 获取方式——独立按需 `GET /diff` 接口，不污染 chain 接口
**选择**：新增 `GET /api/sessions/{id}/diff?from=vN&to=vM`，点哪个版本对才拉那个 diff。chain 接口（`GET /chain`）响应逐字段不变。
**理由**：chain 接口现在很轻（纯 DB 查询 + 内存 set diff），每次开侧栏都调一次。若把 HTML diff 塞进 chain 响应，每次开侧栏都要读 N 个版本的磁盘文件做 N 次 LCS——绝大多数版本用户根本不会展开。独立按需接口让"开侧栏"和"看某个 diff"的成本分离：前者恒定，后者按需。这也保持了 chain 接口的向后兼容（逐字段不变）。
**`{id}` 的语义**：path 里的 `{id}` 是链上**任意** session（用于解析链归属 + owner 校验），`from/to` 是同链版本号。这样从任何一个版本预览页都能拉同链任意相邻对的 diff，无需先定位"链主 session"。
**备选**：合并进 chain 接口一次性返回所有相邻 diff——否决，见上；每次开侧栏读 N 次磁盘 + N 次 LCS，浪费且延迟高。

### D22: 大文件保护——超 `MaxDiffLines` 返回显式 `TooLarge` 信号
**选择**：`MaxDiffLines = 10000`。任一侧超阈值时 `DiffLines` 返回 `DiffResult{Ops: [单条哨兵], TooLarge: true}`——`TooLarge` 是**显式结构化字段**，不依赖 op 文本。`DiffSessionHTML` 透传 `DiffResult`，`handleGetDiff` 直接用 `result.TooLarge` 设响应的 `tooLarge` 字段，前端 `renderHtmlDiff` 优先判 `data.tooLarge` 渲染为灰色 skip 行。**恰好等于阈值仍正常 diff**（`TooLarge=false`）。
**理由**：LCS 是 O(n*m) DP，两个 10000 行文件 = 10^8 单元格 × 8 字节 int ≈ 800MB 内存 + 秒级 CPU，会拖垮请求。10000 行对 agent 产物（通常 < 2000 行）是极宽松的上限，正常用例永远触不到；真触到时跳过比卡死好。阈值是常量，将来按需调整。
**为何 `TooLarge` 是字段而非文本推断**：若用 `ops[0].Text == DiffTooLargeText` 判断，一个内容恰好等于 `[diff skipped: file exceeds MaxDiffLines]` 的合法单行 HTML 文档（如讲解本功能的文档站）会被误判为"太大跳过"。`TooLarge` 作为显式字段彻底根除这类内容碰撞——"太大"是输入规模的属性，与内容无关。
**备选**：(a) Myers 算法（O((n+m)·d)，d=编辑距离）——对大文件更快但实现复杂得多，与 D19"手写可控"的基调冲突，且 agent 产物行数远低于阈值，不值得；(b) 超阈值时只 diff 头尾 N 行——否决，语义混乱，不如明确跳过；(c) 用哨兵 op 文本作为信号——否决，内容碰撞风险（见上）。

### D23: diff 不入库——纯即时计算
**选择**：`DiffSessionHTML` 每次调用都读两份文件 + 算 LCS，结果**不写 DB**。
**理由**：(1) diff 结果体积大（每个版本对可能是几百行 LineOp），入库会让 sessions.db 膨胀且增加 schema 复杂度。(2) live reload（`realtime-html-update`）可能改了磁盘文件，即时计算永远反映**当前**文件状态，缓存反而会过期。(3) 按需接口（D21）+ LCS 足够快（正常文件 < 10ms），没有缓存的真实收益。
**备选**：(a) diff 结果存表 + 文件 mtime 失效——否决，schema 膨胀 + 失效逻辑复杂 + 收益小；(b) 内存 LRU 缓存——否决，live reload 下缓存失效语义复杂，且单机 QPS 极低（一个人看 diff），无收益。

## Risks / Trade-offs

- **行级 diff 对"同行内小改"显示粗糙**：一行里改一个字，行级 diff 显示为"删整行 + 加整行"，看不出具体改了哪个字。→ 接受：本期目标是"改了哪块"而非"改了哪个字"；词级 diff 留作 Non-Goal。用户看到整行变更已足够定位。
- **折叠 equal 行丢失 hunk 上下文**：连续 equal 行合成一条 skip 行，改动行周围没有紧邻的上下文行。→ 接受 + 可演进：侧栏窄，留 hunk 上下文会让改动被挤到屏幕外；当前"全折叠"最清晰。若用户反馈需要上下文，下版加"展开上下文"按钮（数据已在 `lines` 里，前端改动即可）。
- **首个读 session 内容进内存的代码**：此前所有访问都走流式（ServeFile / io.Copy）。`os.ReadFile` 把整个 HTML 读进内存。→ 缓解：有 `MaxDiffLines` 保护，正常 HTML < 200KB，远低于任何内存压力。
- **跨链 / 越界 version 号的处理**：`from/to` 必须是 `{id}` 所在链的合法 live version 号。→ 用 `GetChainOfSession` 拿 versions 列表校验，非法返回 400（清晰的客户端错误，不泄露内部链结构）。

## Migration Plan

1. 升级二进制 → 重启服务。
2. 无 schema 变更（diff 纯计算），无数据迁移。
3. 既有版本链立即获得 diff 能力：任意多版本链的预览页时间线侧栏自动出现"Show HTML diff"按钮。
4. 老 session（无磁盘文件 / 文件被清）点 diff 会得到空 diff（"No changes."），不报错。
5. **回滚**：删除新增的路由 case 与注入的 CSS/JS 即可降级为"无内容 diff"；chain 接口与时间线侧栏的元数据 diff 不受影响。

## Open Questions

- 是否需要"展开上下文"（在折叠的 skip 行点击展开 N 行 equal 上下文）？本期 Non-Goal，等用户反馈。数据已在 API 响应里，纯前端改动。
- 是否要做跨任意版本（v1 vs v3，跳过 v2）的 diff？本期 Non-Goal（仅相邻），但 API 的 `from/to` 设计已支持任意对，只是 UI 当前只暴露相邻——前端改动即可启用。
- 大文件阈值 10000 是否合适？等真实 agent 产物的行数分布数据再校准。
