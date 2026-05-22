## Context

static-html 是一个 Go CLI + HTTP 服务器，用于上传和预览 HTML 文件。当前架构：
- Go 后端用标准 `net/http` 提供静态文件服务
- SQLite 存储会话元数据
- 文件以 ZIP 快照形式上传，解压后通过 `/s/<sessionId>/` 路径直接提供
- 前端为服务端渲染 HTML，无构建流程

用户通过 `sth send` 上传文件后获得预览 URL，但每次修改需重新上传。

## Goals / Non-Goals

**Goals:**
- 用户在浏览器打开 session 页面后，后端文件更新时前端自动刷新
- 支持通过 `sth send --session <id>` 增量更新已有 session
- 提供 `sth watch` 命令监听本地文件变更并自动推送到远程 session
- 单个 session 支持多个浏览器同时连接

**Non-Goals:**
- 不实现 HMR（Hot Module Replacement），仅整页刷新
- 不支持协作编辑
- 不修改现有 `sth send` 的默认行为（不传 --session 仍创建新 session）
- 不做 WebSocket 消息的认证/授权（仅限 localhost 使用场景）

## Decisions

### 1. WebSocket 库选择：`nhooyr.io/websocket`

**选择**: `nhooyr.io/websocket`（原名 `websocket`）
**备选**: `gorilla/websocket`

理由：`nhooyr.io/websocket` 基于标准库 `net/http`，API 更简洁，支持 context 取消，无外部依赖。`gorilla/websocket` 项目已归档不再维护。

### 2. 文件变更检测：fsnotify + 轮询兜底

**选择**: 使用 `fsnotify` 监听 session 目录变更，对无法监听的事件系统（某些 NFS/Docker 环境）提供定时轮询作为 fallback
**备选**: 纯轮询、inotify 直接调用

理由：fsnotify 跨平台且封装良好。轮询兜底保证可靠性。

### 3. Live Reload 注入方式：HTTP 中间件修改响应

**选择**: 在 HTML 响应中通过 io.Reader 注入 `<script>` 标签
**备选**: 修改存储的 HTML 文件、通过独立 JS 文件加载

理由：不修改原始文件，仅在 HTTP 响应层面注入，保持存储完整性。对非 HTML 文件（CSS、JS、图片）不注入。

### 4. 增量更新：PUT /api/sessions/:id/files

**选择**: 新增 REST 端点接收文件上传，覆盖 session 目录中对应文件，触发 WebSocket 推送
**备选**: 通过 WebSocket 双向通信上传文件

理由：复用现有 multipart 上传逻辑，WebSocket 保持单向（仅推送通知），职责清晰。

### 5. `sth watch` 实现方案：文件监听 + HTTP 调用

**选择**: watch 命令监听本地目录，文件变更时调用 `/api/sessions/:id/files` 端点推送更新
**备选**: 直接写文件到 session 目录（仅限本地模式）

理由：通过 HTTP API 保证 watch 命令可用于远程服务器场景，不依赖本地文件系统访问。

## Risks / Trade-offs

- **[fsnotify 延迟]** 文件写入可能触发多次事件 → 使用 debounce（300ms）合并短时间内的多次变更
- **[内存占用]** 每个活跃 session 需要一个 fsnotify watcher → 限制同时监听的 session 数量，仅在有 WebSocket 连接时启动 watcher
- **[连接断开]** 浏览器网络中断 → 前端实现自动重连（指数退避），重连后自动刷新
- **[大文件传输]** 增量上传大文件时可能阻塞 → 设定文件大小限制（默认 50MB）
- **[HTML 注入边界]** 压缩/编码的 HTML 可能注入失败 → 仅对 `Content-Type: text/html` 响应注入，且检查是否有 `<head>` 或 `<body>` 标签
