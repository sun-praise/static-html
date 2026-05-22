## Why

当前 sth 以静态快照方式提供 HTML 预览——上传后内容固定不变。用户在本地迭代开发时，每次修改都需要重新 `sth send`，无法实现保存即预览的流畅体验。添加 realtime update 能力，让开发者进入 session 后自动获得最新内容，显著提升前端开发反馈速度。

## What Changes

- 新增 WebSocket 连接端点，浏览器可通过 `/s/<sessionId>/ws` 建立长连接
- 后端监听 session 对应目录的文件变更（fsnotify），变更时通过 WebSocket 推送通知
- 前端在预览页面注入一段 JS，接收 WebSocket 消息后自动刷新页面内容
- `sth send` 支持增量更新模式：发送文件到已有 session，触发 realtime push 而非创建新 session
- 新增 `sth watch` 命令：监听本地文件变更，自动同步到指定 session

## Capabilities

### New Capabilities

- `websocket-live-reload`: WebSocket 端点、文件变更检测、前端自动刷新注入
- `session-incremental-update`: 向已有 session 增量推送文件更新，无需重建 session
- `sth-watch-command`: CLI watch 命令，监听本地变更并自动同步到远程 session

### Modified Capabilities

## Impact

- **后端**: 需引入 `gorilla/websocket`（或 `nhooyr.io/websocket`）和 `fsnotify` 依赖；server 需处理 WebSocket 升级和文件监听 goroutine
- **前端**: 预览页面需注入 live reload 脚本（仅限 HTML 文件）
- **API**: 新增 `/api/sessions/:id/files` 端点用于增量更新
- **CLI**: 新增 `watch` 子命令
- **依赖**: 新增 WebSocket 库、文件监听库（fsnotify）
