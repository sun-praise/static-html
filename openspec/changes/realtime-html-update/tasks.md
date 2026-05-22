## 1. 依赖与基础设施

- [x] 1.1 添加 `nhooyr.io/websocket` 依赖到 go.mod
- [x] 1.2 添加 `github.com/fsnotify/fsnotify` 依赖到 go.mod
- [x] 1.3 创建 `internal/live/` 包，定义 Hub、Client 接口和消息类型

## 2. WebSocket Live Reload 核心

- [x] 2.1 实现 WebSocket Hub：管理 session 粒度的客户端集合，支持 register/unregister/broadcast
- [x] 2.2 实现 WebSocket handler：升级 HTTP 连接，注册到 Hub，处理心跳和断连
- [x] 2.3 实现文件监听器：使用 fsnotify 监听 session 目录，变更事件 debounce 后通知 Hub
- [x] 2.4 实现 Hub 与文件监听器的生命周期联动：首个客户端连接时启动 watcher，最后一个断开时停止

## 3. Live Reload 脚本注入

- [x] 3.1 实现 HTTP 中间件：拦截 `/s/<sessionId>/` 下的 HTML 响应，在 `</head>` 或 `</body>` 前注入 WebSocket 脚本
- [x] 3.2 注入脚本实现：建立 WebSocket 连接，监听 reload 消息，调用 `location.reload()`
- [x] 3.3 注入脚本实现自动重连：指数退避策略（1s 起步，最大 30s），重连成功后自动刷新

## 4. 增量更新 API

- [x] 4.1 实现 `PUT /api/sessions/:id/files` 端点：接收 multipart 上传，覆盖 session 目录文件
- [x] 4.2 实现 50MB 文件大小限制，超限返回 HTTP 413
- [x] 4.3 文件更新成功后触发 WebSocket Hub 的 reload 广播
- [x] 4.4 为 session store 添加文件更新相关错误处理（session 不存在返回 404）

## 5. sth watch 命令

- [x] 5.1 在 CLI 中注册 `watch` 子命令，接受 `<path>`、`--session`、`--server` 参数
- [x] 5.2 实现本地文件监听：fsnotify 监听指定目录，debounce 300ms
- [x] 5.3 实现变更文件上传：调用 `/api/sessions/:id/files` 推送变更文件
- [x] 5.4 实现忽略规则：跳过隐藏文件（`.` 开头）、临时文件（`*.swp`、`*.tmp`、`.DS_Store`）
- [x] 5.5 实现启动校验：检查路径是否存在、session 是否有效

## 6. 路由集成与测试

- [x] 6.1 将 WebSocket handler 注册到 server 路由（`/s/<sessionId>/ws`）
- [x] 6.2 将 HTML 注入中间件集成到 session 文件服务链
- [x] 6.3 将增量更新端点注册到 server 路由
- [x] 6.4 编写 WebSocket 连接与消息推送的集成测试
- [x] 6.5 编写增量更新 API 的测试
- [ ] 6.6 手动端到端测试：watch 命令 → 文件变更 → 浏览器自动刷新
