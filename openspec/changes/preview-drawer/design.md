## Context

sth 预览页面 (`/s/{id}/`) 直接服务用户上传的 HTML 文件。已有 `InjectMiddleware`（`internal/live/inject.go`）拦截 HTML 响应，在 `</head>` 前注入 WebSocket live-reload 脚本。用户在预览时无法方便地查看同分类/同项目的其他文档或返回主页。

## Goals / Non-Goals

**Goals:**
- 在预览页面注入浮动按钮，点击展开右侧抽屉
- 抽屉展示同 category 和同 project 的其他文档（可点击跳转）
- 提供返回主页入口
- 懒加载：点击时 fetch API 获取数据

**Non-Goals:**
- 不展示同 tag 的文档（后续可扩展）
- 不做 N 跳关系遍历（仅直接同 category/project）
- 不修改用户上传的原始 HTML 文件（纯注入）
- 不引入外部 JS/CSS 框架

## Decisions

### D1: 注入方式 — 复用 InjectMiddleware

**选择**: 扩展现有 `injectScript` 函数，注入额外的 HTML/CSS/JS 片段。

**注入内容分三部分**：
1. **CSS**: 浮动按钮样式 + 抽屉样式（position: fixed, 右侧滑出）
2. **HTML**: 浮动按钮元素 + 抽屉壳子（空列表容器）
3. **JS**: 点击按钮 → fetch API → 填充抽屉 → slide 动画

**替代方案**: 用 iframe 嵌入独立页面 — 增加复杂度，样式隔离问题多。

### D2: 数据获取 — 懒加载 API

**选择**: 新增 `GET /api/sessions/{id}/peers` 端点，点击浮动按钮时才请求。

返回格式：
```json
{
  "current": { "sessionId": "abc", "name": "首页.html", "category": "ui", "project": "my-app" },
  "byCategory": [
    { "sessionId": "def", "name": "登录页.html", "createdAt": "2024-..." }
  ],
  "byProject": [
    { "sessionId": "ghi", "name": "关于页.html", "createdAt": "2024-..." }
  ]
}
```

**替代方案**: 服务端注入时直接嵌入数据 — `InjectMiddleware` 在 `live` 包中，不持有 store 引用。要传递 store 会破坏现有的包边界。懒加载更干净。

### D3: 抽屉 UI 设计

```
┌──────────────────────────────┬─────────────────┐
│                              │ ✕               │
│   用户 HTML 预览内容          │                 │
│                              │ 📂 同分类 (ui)   │
│                              │  • 登录页.html   │
│                              │  • 关于页.html   │
│                              │                 │
│                              │ 📁 同项目 (app)  │
│                              │  • 设置页.html   │
│                              │  • 帮助页.html   │
│                              │                 │
│                      ┌────┐  │ 🏠 返回主页      │
│                      │ ⬡  │  │                 │
│                      └────┘  │                 │
└──────────────────────────────┴─────────────────┘
```

- 浮动按钮: `position: fixed; bottom: 24px; right: 24px;` 圆形 48px
- 抽屉: `position: fixed; top: 0; right: 0; width: 320px; height: 100vh;`
- 抽屉打开时浮动按钮隐藏
- 抽屉使用 transform: translateX 动画滑入

### D4: Store 方法 — GetPeers

新增 `GetPeers(sessionID string, limit int) (*PeersResult, error)`：

```sql
-- 同 category
SELECT s.session_id, COALESCE(s.stored_entry_file, s.entry_file), s.created_at_unix
FROM sessions s
JOIN document_categories dc ON s.session_id = dc.session_id
WHERE dc.category = (SELECT category FROM document_categories WHERE session_id = ?)
  AND s.session_id != ? AND s.deleted_at IS NULL
ORDER BY s.created_at_unix DESC LIMIT ?;

-- 同 project（同理）
```

两部分分别查询，在 Go 层组装为 `PeersResult`。

## Risks / Trade-offs

- **[注入冲突]** 注入的 CSS 可能与用户 HTML 中的样式冲突 → 使用高特异性选择器（`#sth-drawer-*`）和 `!important` 关键属性隔离
- **[性能]** 每次 fetch peers 都查两次 DB → 数据量千级以下，毫秒级响应，可接受
- **[无 category/project 的文档]** 未设置 category 或 project 的文档 → peers 端点返回空列表对应分组，抽屉显示空状态提示
