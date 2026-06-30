## Why

`serverBaseURL()` 当前用容器内部监听端口（`s.port`，典型 `3939`）拼接对外 URL。当 server 跑在反向代理（nginx/caddy 在 443 终止 TLS、proxy_pass 到 `127.0.0.1:3939`）后面时，生成的 URL 形如 `https://sth.sun-praise.com:3939/s/<id>/` —— scheme 已经通过 `X-Forwarded-Proto` 修对了，但端口错了：外部实际是 443（无端口后缀），代码却拼了内部监听端口。

后果：CLI `sth send` 返回的 URL 不能直接分享/打开，要手动删 `:3939`。这对 hermes 等 agent 自动化场景（拿到 URL 就要 fetch）是个摩擦点，也会困扰任何把 sth 部署在反代后面的开源用户。

现状代码（`internal/server/server.go:1369-1388`）：

```go
port := s.port  // 内部监听端口，反代场景 != 外部端口
if (scheme == "http" && port == 80) || (scheme == "https" && port == 443) {
    return fmt.Sprintf("%s://%s", scheme, serverName)
}
return fmt.Sprintf("%s://%s:%d", scheme, serverName, port)
```

`80/443` 这个判断只覆盖「server 直接监听标准端口」，没覆盖「server 监听非标端口、前面有反代在 443 终止」。

## What Changes

- 新增 `--server-port <n>` CLI 标志，显式声明对外公开端口（覆盖内部监听端口）
- `serverBaseURL()` 端口选择优先级：`--server-port` > `X-Forwarded-Port` 请求头 > `s.port`（内部监听端口）
- nginx 示例配置补 `proxy_set_header X-Forwarded-Port 443;`，让默认反代部署零配置生效

## Capabilities

### New Capabilities

- `public-url`: server 在反代后面时生成正确的对外 URL（scheme + 公开端口），支持显式 `--server-port` 与 `X-Forwarded-Port` 自动探测

### Modified Capabilities

（无）

## Impact

- **`internal/server/server.go`**: `New()` 签名加 `serverPort int` 参数；`serverBaseURL()` 改端口选择逻辑
- **`internal/cli/cli.go`**: `runStart()` 解析 `--server-port`，传入 `server.New`；`printUsage()` 加新 flag
- **`internal/server/server_test.go`**: 加反代场景测试（`X-Forwarded-Port` / `--server-port` 优先级）
- **`deploy/nginx/example.conf`**: 补 `proxy_set_header X-Forwarded-Port 443;`
- **`README.md` / `README.cn.md`**: Deployment 段说明反代部署时如何让 URL 干净化
