## Context

`sth` 服务器当前通过 `--host` 参数绑定到指定 IP（默认 `127.0.0.1`）。`Origin()` 返回绑定地址。当用户以 LAN IP 启动时（如 `192.168.x.x`），本机只能通过该 IP 访问，无法使用 `localhost` 或 `127.0.0.1`。

核心代码：
- `internal/server/server.go`: `DefaultHost = "127.0.0.1"`，`Origin()` 返回绑定地址
- `internal/cli/cli.go`: 启动输出 `srv.Origin()`
- `scripts/start-server.sh`: `STATIC_HTML_HOST` 默认 `127.0.0.1`

## Goals / Non-Goals

**Goals:**
- 服务器绑定到 `0.0.0.0`，同时支持 localhost 和 LAN IP 访问
- 启动时自动检测并展示所有可用访问地址
- 保持向后兼容（已有 `STATIC_HTML_HOST` 环境变量继续生效）

**Non-Goals:**
- 不做 IP 白名单/黑名单访问控制
- 不修改 WebSocket 或 CORS 策略
- 不改变 `send-file.sh` 的行为

## Decisions

### 1. 默认绑定到 `0.0.0.0`

将 `DefaultHost` 从 `"127.0.0.1"` 改为 `"0.0.0.0"`，使服务器监听所有网络接口。

**替代方案**: 同时启动多个 listener —— 复杂度高，无必要。`0.0.0.0` 是标准做法。

### 2. 网络接口检测

新增 `Origins()` 方法，遍历 `net.Interfaces()` 获取非回环、已启用的接口 IP，与 `127.0.0.1`/`localhost` 一起返回。

返回格式为 `[]string`，如：
```
["http://127.0.0.1:3939", "http://192.168.2.14:3939"]
```

### 3. CLI 输出展示多地址

启动时输出：
```
HTML server listening on:
  - http://127.0.0.1:3939
  - http://192.168.2.14:3939
```

### 4. `Origin()` 保持兼容

`Origin()` 返回 `127.0.0.1` 地址（本机最通用的地址），确保现有代码不受影响。

## Risks / Trade-offs

- [安全] 绑定 `0.0.0.0` 暴露服务到网络 → 此工具用于本地开发预览，风险可接受
- [兼容] `Origin()` 返回值从 LAN IP 变为 `127.0.0.1` → 调用方使用 `Origins()` 获取完整列表
