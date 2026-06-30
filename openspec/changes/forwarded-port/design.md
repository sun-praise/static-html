## Context

`serverBaseURL()` 负责拼接 session URL 的 base 部分（scheme + host + port），用于 `POST /api/sessions` 和增量更新 API 的响应。当前实现：

```go
func (s *Server) serverBaseURL(r *http.Request) string {
    if s.serverName != "" {
        port := s.port  // 内部监听端口
        if s.listener != nil { /* 从 listener 读真实端口（端口 0 时） */ }
        scheme := determineScheme(r)  // 已正确读 X-Forwarded-Proto
        if (scheme == "http" && port == 80) || (scheme == "https" && port == 443) {
            return fmt.Sprintf("%s://%s", scheme, s.serverName)
        }
        return fmt.Sprintf("%s://%s:%d", scheme, s.serverName, port)
    }
    return baseURL(r)  // 无 server-name 时用请求 Host
}
```

`scheme` 已经修对了（`determineScheme` 读 `X-Forwarded-Proto: https`）。**端口**是唯一缺口：代码用 `s.port`（内部监听端口 `3939`）判断要不要省略，但反代场景下外部端口是 `443`，内部是 `3939`，两者不等，于是拼出 `https://host:3939`。

`X-Forwarded-Proto` 已经信任反代头（line 1356-1357 注释：「trusts the X-Forwarded-Proto header, so the server must run behind a trusted reverse proxy when relying on this logic」）。引入 `X-Forwarded-Port` 与现有设计对称，不引入新的信任面。

## Goals / Non-Goals

**Goals:**
- 反代部署（nginx 在 443 终止 TLS，proxy_pass 到 `127.0.0.1:3939`）时，CLI `sth send` 返回的 URL 不带错误端口后缀
- 零配置优先：nginx 设 `X-Forwarded-Port` 即自动生效，不需要改 compose 或加 flag
- 留显式覆盖通道（`--server-port`）应对不信任 header 或多层反代场景
- 保持向后兼容：不设 header 也不传 flag 时，行为与今天完全一致

**Non-Goals:**
- 不改 `--server-name` 的语义（仍是公开主机名）
- 不改 `Origins()`/`Origin()` 启动日志（那是本机监听地址，不是对外 URL）
- 不引入 `Forwarded` RFC 7239 头解析（`X-Forwarded-*` 已是事实标准，且 `X-Forwarded-Proto` 已经在用）
- 不做端口白名单校验（`--server-port` 是 1-65535 都接受，运维自己负责）

## Decisions

### 1. 端口选择优先级：flag > header > 监听端口

```
--server-port (flag)  >  X-Forwarded-Port (header)  >  s.port (listener)
```

**理由**：
- flag 最显式、最可信，运维主动声明时优先级最高
- header 是反代约定，与已有的 `X-Forwarded-Proto` 对称，零配置场景靠它
- 监听端口是最后兜底，保持无反代场景（本地 `sth start`）行为不变

### 2. 新增 `--server-port` 而非复用 `--port`

**选择**：新增独立 flag
**备选**：复用 `--port` 含义、或加 `--external-port`

理由：`--port` 已固定语义为「内部监听端口」（传给 `http.Server`），复用会破坏含义。新 flag 名字与 `--server-name` 对称（一个对外主机名，一个对外端口），自解释。

### 3. 信任 `X-Forwarded-Port`

**选择**：直接读 header，`strconv.Atoi` 解析，失败则忽略（fallback 到 `s.port`）
**备选**：只信任 `--server-port`，不读 header

理由：
- `X-Forwarded-Proto` 已经在用同样模式（line 1359），代码注释已声明信任反代的前提
- 反代头是这类「感知前端」需求的事实标准（nginx、traefik、caddy、cloudflare 都设）
- 零配置部署（docker-compose + nginx example.conf）靠 header 自动 work，是开源仓库的默认路径

### 4. 端口省略规则不变

仍按 `(scheme=http && port==80) || (scheme=https && port==443)` 省略端口。改的是 `port` 的**来源**（从内部监听端口变为对外端口），不是省略规则。这样：
- 本地 `sth start`（监听 3939，无反代）→ `http://127.0.0.1:3939`（不变）
- 反代 `sth.sun-praise.com`（监听 3939，nginx 443）+ header → `https://sth.sun-praise.com`（修复）
- `--server-port 8443` 显式声明 → `https://host:8443`（覆盖）

## Risks / Trade-offs

- **[header 伪造]** 攻击者直连 `127.0.0.1:3939`（绕过 nginx）伪造 `X-Forwarded-Port` 头 → 与 `X-Forwarded-Proto` 面临的威胁相同，已在代码注释里声明「必须跑在可信反代后面」。compose 端口绑定 `127.0.0.1:3939`（仅本机）已限制直连面。`--server-port` flag 是不信任 header 时的逃生通道。
- **[解析失败]** `X-Forwarded-Port` 非数字或为空 → 静默 fallback 到 `s.port`，日志不报错（与 header 缺失等价，不是错误）。测试覆盖此 case。
- **[多层反代]** X-A → X-B → server，`X-Forwarded-Port` 可能是「最近一跳」的端口而非真正公网端口 → flag 是确定性逃生通道；文档说明多层场景用 `--server-port`。
- **[兼容性]** 旧行为（无 header、无 flag）完全保留，现有用户无感升级。
