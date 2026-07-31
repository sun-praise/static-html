## Context

`sth` 是"agent 发布、人类浏览的 HTML 收件箱"。当前（截至 v2.1.0）session 是完全扁平的：每次 `sth send` 创建一条孤立记录，靠 `tag` / `category` / `project` 三个 metadata 字段手动组织。但 agent 的真实工作模式是**反复迭代同一份产物**——同一个 dashboard、同一份报告会被 send 很多遍。结果首页平铺一堆同名条目，迭代轨迹丢失。

现有相关实现要点（决策参照）：

- 元数据三表 `document_tags` / `document_categories` / `document_projects`（`metadata.go:42-57`）是按 session 维度的扁平映射，**没有"产物身份"概念**，不能承载版本链语义。
- `document_projects` 是 session → project 的 1:1 映射，不是分组表；不可直接复用做链主键。
- `sessions` 表主键 `session_id`，已有列 `entry_file` / `stored_entry_file`（`store.go:232` + `ensureColumns`）；`entry_file` 存的是 basename（如 `index.html`），不区分目录前缀。
- 路由仍是 `server.go` `routes()` 的单 `switch`（D5 决策，不引入路由库）；`/api/sessions/{id}/<sub>` 路径族由 `extractSessionIDFromMetaPath` 解析，必须在 `isExactSessionPath` case 之前注册新子路径。
- owner 隔离已成熟（D4）：`sessions.user_id` + `currentUser(r)` + `requireOwner`；预览页注入已成熟（preview-drawer spec）：`internal/live.InjectMiddleware` 缓冲响应、按 `</head>` / `</body>` 注入 HTML 片段。
- 迁移走 `ensureColumns` + `CREATE TABLE IF NOT EXISTS`；`Store` 私有 `*sql.DB`；所有写方法直接挂 `*Store`。
- `document_chains` 之前不存在（grep `chain_id` / `parent_version` / `version_no` 全空），本次为全新能力。

## Goals / Non-Goals

**Goals:**
- 零 agent 改动：现有 skill 与 `sth send` 工作流逐字段兼容，不要求 agent 传任何版本相关字段。
- 同产物迭代自动归链：`(project, entry_file basename, owner)` 相同的多次 send 自动串成 v1→v2→v3。
- 浏览器侧可视化：预览页注入版本时间线侧栏，能看迭代轨迹、能跳转相邻版本、能看到相邻版本的元数据 diff。
- 元数据 diff：tags 增删、category / project 变更，在相邻版本对之间显式呈现。
- 软删兼容：删除链中间版本时，链结构保留，时间线跳过该版本（不重排版本号）。
- owner 隔离：开启 `--auth` 时不同 owner 的同 (project, entry_file) 各自独立成链；关闭时全局共享一条。
- 完全向后兼容：老 session 不迁移、不阻塞；链化失败降级为孤立 session；不破坏现有 send / list / search / preview 路径。

**Non-Goals:**
- 不做 HTML **内容** diff（unified diff 或并排渲染）——这是非平凡工程（涉及 iframe 沙箱、DOM diff、性能），留作下一版独立 capability。
- 不引入 `--parent <id>` 显式覆盖自动链化——自动启发式满足本期需求，显式 API 留待用户反馈后再加。
- 不在首页按链聚合折叠——本期仅在预览页加时间线侧栏；首页聚合改动 list/search 分页语义，留作下版。
- 不做版本号重排——软删后版本号保留以维持可追溯性。
- 不做跨链比较 / 跨链搜索。
- 不引入新依赖（无第三方路由库、无 diff 库、无前端框架）。

## Decisions

### D14: 链化策略——自动按 `(project, entry_file basename, owner)`，零 agent 改动
**选择**：服务端在 `handleCreateUploadedSession` 写入元数据后自动调用 `LinkToChain(sessionID, project, entryFile, ownerID)`，由 `(project, basename(entryFile), ownerID)` 三元组定位链。命中则 `version_no = MAX + 1`，未命中则新建链 `version_no = 1`。整个操作在单个事务内完成。
**理由**：agent 通常不会主动管理版本关系（它每次 send 是独立的，没有"上一版 id"的概念）。要求 agent 传 `--parent` 会破坏现有 skill 与所有已部署工作流，违反"零 agent 改动"硬约束。`(project, entry_file)` 是用户可感知的"产物身份"——同一个 dashboard 迭代时，project 通常稳定、入口文件名通常稳定（都是 `index.html` / `report.html`），是最佳隐式分组键。`basename` 而非全路径，是因为 agent 可能在不同临时目录 send 同一份产物（`/tmp/build-1234/index.html` vs `/tmp/build-5678/index.html`），全路径会让它们错误地分裂成两条链。
**owner 的处理**：`--auth` 开启时按 owner 隔离（不同人不会互相串链）；关闭时 `user_id IS NULL`，全局共享一条。这与 D4 owner 隔离决策完全对齐。
**备选**：(a) `--parent <id>` 显式链化——否决，要求 agent 维护状态，破坏零改动约束；(b) 用内容哈希做链键——否决，相同内容的两次 send 不应合并版本（agent 改了又改回会算成同一版本，语义混乱），且哈希需读全文件、成本高；(c) 用 `(project, category, entry_file)` 三元组——否决，category 是可选维度、可能为空，且同一产物迭代时 category 可能变化，做链键不稳定。

### D15: 链关联——`sessions.chain_id` 外键 + `document_chains` 链主表
**选择**：新增独立表 `document_chains (chain_id PK, project, entry_file, user_id, created_at_unix, UNIQUE(project, entry_file, user_id))`；`sessions` 加 `chain_id TEXT` 外键列 + `version_no INTEGER` 列 + 复合索引 `(chain_id, version_no)`。链关联通过 `sessions.chain_id` 而非 session→session 的 parent 指针。
**理由**：链是 session 集合的属性，不是 session 间的关系。独立链表 + 外键让"查链上所有版本"是 `WHERE chain_id = ?` 一次扫描，而非遍历 parent 指针的 O(n) 链表追踪。链表自身也能承载链级元数据（创建时间、未来可能的链标题/标签）。
**与 `document_projects` 的关系**：`document_projects` 仍是 session → project 字符串的 1:1 映射（D14 的链键之一），不变。`document_chains.project` 是冗余存储（链级别的 project 标签），便于不 JOIN sessions 也能解析链身份；二者保持一致由 `LinkToChain` 保证（链一旦创建，其 project/entry_file 不可变，即使后续 session 的 project metadata 变了——链身份冻结在首次创建时）。
**`ON DELETE SET NULL`**：`sessions.chain_id` 外键设 `ON DELETE SET NULL` 而非 CASCADE——删除链不应级联删 session（链只是聚合视图，session 是事实数据）。`document_chains` 行本身几乎不删（链无显式删除路径，session 全部软删后链行残留是无害的死数据）。
**备选**：(a) 在 `sessions` 加 `parent_session_id` 自引用列——否决，遍历链是 O(n) 链表追踪，且无法直接 GROUP_CONCAT；(b) 用 `document_projects` 直接做链主表（`project` 字符串当链键）——否决，`project` 是 session 维度的可变字段，且 `user_id` 维度无处安放。

### D16: 软删版本——保留链结构，时间线跳过该版本，不重排版本号
**选择**：`SoftDelete` 现有行为不变（`UPDATE sessions SET deleted_at = ?`）。`listChainVersions` / `GetChainOfSession` / `DiffChainMetadata` 所有查询统一加 `s.deleted_at IS NULL`，被软删的版本在时间线上消失但 `version_no` 不重排。`document_chains` 行不受影响（外键 `ON DELETE SET NULL` 只在 session 硬删时触发，软删走 UPDATE）。
**理由**：与现有软删语义（`store.go:171` + `idx_sessions` 一系列查询的 `deleted_at IS NULL` 习惯）完全一致。不重排版本号是关键——版本号是历史事实，重排会让"v2 改了什么"这种引用失去稳定性（用户引用 v3 的反馈在重排后变成 v2）。
**代价**：版本号会出现"空洞"（如 v1, v3，v2 被软删）。这是可接受的：用户看到 v1 / v3 时能立即推断"中间有版本被删了"，比"v1, v2 看起来连续但实际中间删过"更诚实。
**备选**：(a) 禁止删除链中间版本——否决，过度保护，用户可能确实想删某个错误版本；(b) 删除后重排——否决，破坏版本号稳定性，且需批量 UPDATE 所有后续版本号，事务复杂。

### D17: diff 范围——v1 仅元数据（tags/category/project），HTML 内容 diff 留下版
**选择**：`DiffChainMetadata(chainID)` 仅计算相邻版本的 tags 集合差（增/删）与 category / project 字符串变更。v1 与空基线对比（让首版的初始 metadata 也呈现为"added"）。**不**读取任何版本的实际 HTML 文件内容做文本/DOM diff。
**理由**：HTML 内容 diff 是非平凡工程——需要决定 diff 粒度（行/字/AST）、需要沙箱化渲染对比、需要考虑大文件性能、需要决定 inline diff vs 并排预览。把它和链化耦合在一个 change 里会让 PR 过大、风险过高、周期过长。元数据 diff 已经能回答"这次迭代改了哪些 tag / 换了 category 没"这类高频问题，性价比最高。
**`MAX(version_no)+1` 的事务安全**：`LinkToChain` 在 `BEGIN IMMEDIATE` 事务内执行，立即获取 SQLite RESERVED 写锁，把整个"找链或建链 → 算版本号 → UPDATE sessions"串行化，根除并发竞态。这一点对 anonymous（`--auth` 关闭、`user_id NULL`）路径尤其关键：SQLite 的 `UNIQUE(project, entry_file, user_id)` 对 NULL 不生效（SQL 标准里 NULL ≠ NULL），认证 owner 路径靠 UNIQUE + `INSERT ... ON CONFLICT DO NOTHING` 兜底，而 anonymous 路径完全依赖 `BEGIN IMMEDIATE` 的写锁串行化。链行创建用 upsert（`ON CONFLICT DO NOTHING`）+ 再 SELECT，保证无论新建还是已存在都拿到同一个 `chain_id`。
**备选**：(a) v1 一次性做完含 HTML 内容 diff——否决，PR 过大；(b) 完全不做 diff，只列版本——否决，"看上一版改了啥"是用户最直接的需求，不做等于功能不完整。

### D18: 时间线 UI——复用 `internal/live.InjectMiddleware` 的注入路径，仿照 peers drawer
**选择**：在 `inject.go` 新增 `versionDrawerCSS` / `versionDrawerHTML` / `versionDrawerJS` 三个常量，追加进 `drawerBytes`（与 peers drawer 共享同一注入路径），不改 `InjectMiddleware` 本体。徽标默认隐藏，页面加载时 probe `/api/sessions/{id}/chain`，仅当 `versions.length > 1` 时显示徽标（`v3`）。
**理由**：preview-drawer spec 已经验证了"`InjectMiddleware` 缓冲响应 + 在 `</head>` 前注入 HTML 片段"的可行性与性能（仅对 `text/html` 内容类型注入，非 HTML 直通）。peers drawer 是现成的视觉与交互模板（浮动按钮 + 侧滑面板 + 懒加载 + 重试），版本时间线是几乎同构的 UI，复用既定模式比自创一套更一致、更易维护。徽标默认隐藏避免给单次 send 的 session（绝大多数）增加视觉噪音。
**懒加载策略**：徽标显示靠页面加载时的一次 probe 请求（很轻，已用 `credentials:'same-origin'` 兼容 cookie 登录）；点击展开时复用缓存数据，无二次请求。这与 peers drawer 的"首次点击才加载"略不同——因为徽标文字（版本号）必须显示，所以 probe 必须前置；但 probe 仅读取 JSON 不渲染面板，成本可接受。
**样式隔离**：所有选择器以 `#sth-version-` / `.sth-version-` 前缀命名，关键布局属性用 `!important` 抵抗用户 HTML 自带样式的冲突——与 peers drawer（`drawerCSS` 注释明确说明）完全一致的策略。
**备选**：(a) 新增顶层 `/chain/<id>` 页面——否决，跳出预览上下文丢失"我在看 v3"的位置感；(b) 修改 `handlePreview` 服务端渲染时间线——否决，`handlePreview` 是 `http.ServeFile`，注入路径已经在 `InjectMiddleware`，服务端改要重写整个 preview 处理；(c) 首页列表折叠 + 详情侧栏都做——否决，首页聚合改动 list/search 分页语义，本期 Non-Goal。

## Risks / Trade-offs

- **同名 entry_file 误归链**：两个完全不同的产物恰好 project 同名 + entry_file 同名（如都是 `report.html`）会被错误归到一条链。→ 缓解：项目名是用户/agent 显式传的 `--project`，碰撞概率低；且即便碰撞，时间线只是聚合视图，不损坏任何 session 数据，用户可换 project 名重新组织。HTML 内容 diff（下版）会让用户立即发现"这条链里混了无关产物"。
- **probe 请求开销**：每次预览页加载多发一次 `/api/sessions/{id}/chain` 请求。→ 缓解：仅 JSON、走同一 handler、SQLite WAL 读极快；若实测成问题，可改为只在 peers drawer 已加载时附带返回 chain 字段（一次请求双用）。
- **`MAX(version_no)+1` 的并发安全**：理论上多请求同时 send 同一产物时 version_no 可能撞号。→ 已缓解：`LinkToChain` 用 `BEGIN IMMEDIATE` 立即拿写锁，把整个读-改-写串行化；认证 owner 路径额外有 `UNIQUE(project, entry_file, user_id)` + upsert 兜底；anonymous 路径（NULL owner，UNIQUE 不生效）完全靠写锁串行化。
- **老 session 永不自动入链**：升级后老 session 的 `chain_id` / `version_no` 为 NULL，时间线显示为单版本视图。→ 接受：强行迁移需要回溯推断"哪些老 session 应该串成链"，启发式不可靠；下次 send 同产物时新版本从 v1 起算（老 session 视为链外），可接受。

## Migration Plan

1. 升级二进制 → 重启服务。
2. `Store.init` 执行 `ensureColumns`：`sessions` 表自动 `ALTER TABLE ADD COLUMN chain_id` / `version_no`（idempotent，已有列跳过）；`initMetadata` 执行 `CREATE TABLE IF NOT EXISTS document_chains`（idempotent）。
3. 现有 session 行的 `chain_id` / `version_no` 保持 NULL（`DEFAULT NULL`）；首页 / 搜索 / 预览照常工作，时间线对老 session 显示单版本视图。
4. 之后每次新 `sth send` 自动入链；同产物的第二次 send 起开始形成时间线。
5. **回滚**：若需回退，删除 `document_chains` 表与 `sessions.chain_id` / `version_no` 列即可（功能降级为"无链视图"，不损坏任何业务数据）；预览页注入的版本 drawer 因 probe 接口 404 会静默不显示。

## Open Questions

- 老 session 是否需要管理命令 `sth chain backfill` 手动串联？本期 Non-Goal，等用户反馈。
- 链本身是否需要 `--chain-title` / `--chain-tag` 等链级元数据？本期 Non-Goal，链身份冻结在首次创建。
- 当 agent 改变 entry_file basename（如 `index.html` → `dashboard.html`）时，会断链；是否需要在 skills/sth SKILL.md 加一条 "保持入口文件名稳定" 的建议？留待内容 diff 版本一起处理。
- 首页是否需要在 session 行显示 `v2` 这样的版本徽标？本期 Non-Goal，但 `DocumentInfo` 已带 `chainId` / `versionNo` 字段，前端改动即可启用。
