## Why

用户在预览 HTML 文档（`/s/{id}/`）时处于"孤岛"状态 — 无法快速跳转到同分类、同项目的其他文档，也无法便捷地返回主页。需要退出预览、手动回到主页再查找，打断工作流。

## What Changes

- 在 HTML 预览页面注入一个浮动按钮（复用现有 `InjectMiddleware` 机制）
- 点击浮动按钮展开右侧抽屉，展示：
  - 同 category 的其他文档列表（可点击跳转预览）
  - 同 project 的其他文档列表（可点击跳转预览）
  - 返回主页链接
- 新增 `GET /api/sessions/{id}/peers` API 端点，返回同 category 和同 project 的相关文档
- 抽屉数据通过点击时 fetch API 获取（懒加载）

## Capabilities

### New Capabilities
- `peers-api`: 返回与指定 session 共享 category 或 project 的其他文档列表的 API 端点
- `preview-drawer`: 注入到预览页面的浮动按钮和抽屉 UI 组件

### Modified Capabilities

（无已有 spec 需要修改）

## Impact

- **`internal/live/inject.go`**: 注入脚本从纯 WebSocket reload 扩展为 reload + 浮层 UI + fetch 逻辑
- **`internal/server/server.go`**: 新增 `/api/sessions/{id}/peers` 路由和 handler
- **`internal/session/store.go`**: 新增 `GetPeers(sessionID, limit)` 方法
- **前端**: 纯内联 HTML/CSS/JS（与现有 live-reload 注入方式一致），无外部依赖
