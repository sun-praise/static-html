## Why

当服务器使用 `--host <LAN_IP>` 启动时（如 `192.168.x.x`），只能通过该 IP 访问。本机无法通过 `127.0.0.1` 访问，开发时需要记住 LAN IP，不方便。

## What Changes

- 服务器默认绑定到 `0.0.0.0`（所有网络接口），而不是特定的 IP 地址
- 启动时检测并显示所有可用的访问地址（127.0.0.1、LAN IP）
- `Origin()` 方法返回主地址，新增 `Origins()` 方法返回所有可用地址列表
- CLI 启动输出展示多个访问 URL

## Capabilities

### New Capabilities
- `multi-origin-display`: 服务器启动时检测所有网络接口并展示多个可用访问地址

### Modified Capabilities

## Impact

- `internal/server/server.go`: 修改默认 Host 常量和 `Origin()` 逻辑，新增 `Origins()` 方法
- `internal/cli/cli.go`: 修改启动输出以展示多个访问地址
- `skills/static-html-preview/scripts/start-server.sh`: 更新默认 host 为 `0.0.0.0`
