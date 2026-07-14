## Context

`sth` 已通过 `support-user-apikey` 落地了 API key 鉴权：`users` + `api_keys` 两表、`authMiddleware` 按 path 分类、`Authorization: Bearer` 头校验、CLI `sth send/watch` 携带 key。这套体系对 CLI 友好，但浏览器用户被裸 401 挡在门外——浏览器原生无法设置 `Authorization` 头，且 `users` 表无密码字段，无法做表单登录。本次变更在不破坏 API key 体系的前提下，补齐浏览器侧的"登录/注册/会话保持"。

现有相关实现要点（决策参照）：
- 路由仍是 `server.go` `routes()` 的单 `switch`（D5 决策，不引入路由库）。
- `authMiddleware`（`auth.go:29`）按 path 分类：预览默认开放、其余要求 Bearer、401 是纯文本 + `WWW-Authenticate` 头。
- 身份经未导出 context key（`userCtxKey`）注入，`currentUser(r)` 读取；owner 隔离/过滤全部基于此。
- 配置 flag/env 双通道、flag 优先（`popBoolFlagWithPresence` + `resolveBool`）。
- 迁移走 `ensureColumns` + `CREATE TABLE IF NOT EXISTS`；DB 私有于 `Store`。
- `design.md` D3 明确**针对 API key** 否决 bcrypt（高熵、每请求校验需快）——但这不约束密码字段，人脑密码是低熵输入，必须慢哈希。

## Goals / Non-Goals

**Goals:**
- 浏览器用户能通过 `/register` 自助注册、`/login` 登录，登录后正常浏览会话列表。
- server-side session cookie 保持登录态，DB 只存 token 哈希（防 DB 泄漏即劫持），与 API key 同等安全模型。
- CSRF 安全：不引入 CSRF token 库的前提下，杜绝 cookie 认证带来的跨站写风险。
- 完全向后兼容：`--auth` 关闭逐字节不变；`--auth` 开启且只用 API key 时行为不变；CLI 通道零改动。
- 预览 `/s/<id>/` 保持开放（不破坏分享链接）。

**Non-Goals:**
- 不做 OAuth/SSO、多因素认证、邮箱验证、密码重置邮件——本期是够用的最小登录体验。
- 不做"记住我"之外的细粒度 session 策略（如同设备数限制、强制重新登录）。
- 不在登录页内嵌"签发 API key"功能（留作后续，解决"cookie 不能写导致上传需另拿 key"的体验断点）。
- 不引入第三方 session/cookie 库（如 gorilla/sessions），自建以贴合项目"无新依赖偏好"。
- 不引入第三方路由库或 CSRF 中间件库。

## Decisions

### D8: 会话机制——server-side session cookie，自建
**选择**：登录成功生成 `sths_` + 32 字节 base64url token，DB `login_sessions` 表只存 `SHA-256(token)` 哈希 + user_id + 过期时间；cookie 名 `sth_session`，`HttpOnly` + `SameSite=Lax` + `Path=/` + `Max-Age=30天` + `Secure`（HTTPS 时）。
**理由**：与 API key 同等的安全模型（明文不落库，DB 泄漏不等于 cookie 劫持）；server-side 可即时吊销（删行）；自建不引第三方库，符合 D5 的"不引库"基调。`SameSite=Lax` 允许 top-level GET 导航带 cookie（满足登录回跳 `next` 参数），同时天然阻止跨站 POST，与 D10 的 CSRF 策略协同。
**关于 salt**：API key 校验靠存储的 `KeyPrefixLen` 前缀扫描候选行，故需逐行 salt 后再比对；session token 不走前缀路径，直接以 `SHA-256(token)` 作为主键做 O(1) 查找。token 自带 256 位熵，无 salt 仍使暴力预像攻击不可行——DB 泄漏后攻击者需对 256 位随机值求 SHA-256 逆，与 salted 路径同等安全。因此 session 表不存 salt。
**备选**：(a) stateless 签名 cookie（JWT-like）——被否决，无法即时吊销，且需管理签名密钥轮换；(b) `gorilla/sessions`——被否决，引入依赖违背项目风格。

### D9: 密码哈希——bcrypt（仅用于密码字段）
**选择**：`user_credentials.password_hash` 存 `golang.org/x/crypto/bcrypt` 哈希（默认 cost 10）。
**理由**：D3 否决 bcrypt 是**针对 API key**（高熵、需快速校验、每请求都走）；**人脑密码是低熵输入**，必须慢哈希防字典/暴力破解。两条路径互不冲突：API key 仍走 SHA-256（快），密码走 bcrypt（慢）。这是新增 `golang.org/x/crypto/bcrypt` 依赖的唯一原因。
**备选**：argon2id——更现代但需额外参数管理且库更大；bcrypt 成熟、stdlib生态、参数简单，足够本期。
**版本选择**：锁定 `golang.org/x/crypto v0.35.0`（要求 go ≥1.23），而非最新的 v0.54.0（要求 go ≥1.25）。目的是**不抬升项目的 `go` 指令**——项目 go.mod 声明 `go 1.24.1`，升到 1.25 会强制所有消费者升级 toolchain。bcrypt API 在 v0.35 与 v0.54 间无破坏性变更，cost 参数语义一致。
**密码策略**：最小长度 8，无复杂度要求（避免给单人/小团队部署制造无谓摩擦）。

### D10: CSRF 防护——Cookie 只读 + Bearer 写（双重模式按方法分流）
**选择**：`authMiddleware` 按 **HTTP 方法** 分流——
- GET/HEAD（浏览类）：接受 Cookie **或** Bearer 任一有效凭据；浏览器无凭据时 302 `/login`。
- POST/PUT/DELETE（mutating）：**只接受 Bearer，拒绝 Cookie**；无效时返回原 401 + `WWW-Authenticate`。
**理由**：mutating 接口天然 CSRF 暴露面，但因强制 Bearer（浏览器跨站请求无法设置自定义 `Authorization` 头），CSRF 攻击者构造的跨站 POST 必然无 Bearer → 401，攻击无效。无需 CSRF token、无需校验 `Origin`/`Referer`、无需第三方库。
**代价**：浏览器登录后不能直接在页面上发 POST/PUT/DELETE（上传/删除/打标签）。本期接受此代价——上传仍走 CLI（`sth send --api-key`），浏览器只读浏览。登录页内嵌"签发 API key"作为后续增量（见 Non-Goals）。
**备选**：(a) 双重提交 cookie / CSRF token——可让浏览器也能写，但需改所有 mutating handler 注入/校验 token，复杂度高，本期 Non-Goal；(b) 强制校验 `Origin` 头——浏览器会发，但实现细节多且对自定义 header 的 Bearer 客户端不友好，否决。

### D11: 未登录浏览器——302 重定向而非 401
**选择**：`--auth` 开启时，GET 类受保护路径（目前仅 `/` 列表）若客户端 `Accept: text/html`（`acceptsHTML(r)` 判定）且无有效凭据，返回 `302 Location: /login?next=<原路径>`；非 HTML 客户端（如 curl、CLI）维持原 401 + `WWW-Authenticate`，避免破坏 API 客户端。
**理由**：浏览器用户看到的是登录页而非裸 401 文本；API 客户端行为不变。`next` 参数用于登录成功后回跳，校验必须以 `/` 开头（防开放重定向）。
**备选**：一律 401——浏览器体验差，否决。

### D12: 注册开关——`--allow-registration` 默认 true
**选择**：新增 `--allow-registration` flag + `STH_ALLOW_REGISTRATION` env，沿用 `popBoolFlagWithPresence` + `resolveBool` 模式，**默认 true**（开放注册）。关闭时 `/register` POST 返回 403 且页面提示"已关闭注册"，但 `/register` GET 仍可访问（显示关闭提示）。
**理由**：用户明确要"开放注册让人用得舒服"；同时保留关闭开关，供只想用 CLI 预建账号的部署。默认值与 `--auth` 默认关闭解耦——`--allow-registration` 只在 `--auth` 开启时有意义（`--auth` 关闭时整个鉴权层 no-op，注册页也不该被访问）。
**注意**：注册成功后自动登录（直接种 cookie 跳转 `/`），无需用户再手动登录一次。

### D13: 密码/Session 存储独立成表，不污染 `users`
**选择**：新增 `user_credentials`（user_id PK, password_hash, updated_at）与 `login_sessions`（token_hash PK, user_id FK, created_at, expires_at）两表，均 `CREATE TABLE IF NOT EXISTS` 幂等建表，通过 `Store.init()` 追加调用初始化。
**理由**：现有 `users` 行（CLI 建的、或未来纯 API key 用户）没有密码是合法状态；把密码放进 `users` 要么加可空列（语义模糊）、要么破坏现有行。独立表 + 一对一关系最干净，且 `ON DELETE CASCADE` 让删 user 自动清理凭据与 session。session 独立表同理，便于过期清理与即时吊销。
**备选**：给 `users` 加 `password_hash` 可空列——被否决，语义模糊（NULL = 无密码还是未设置？），且污染 D4 已定型的 schema。

## Risks / Trade-offs

- [浏览器登录后无法在页面上写操作] → D10 的代价。本期文档明确说明上传仍走 CLI；登录页内嵌"签发 API key"按钮作为后续增量。
- [bcrypt cost 10 在弱机上登录延迟 ~100ms] → 可接受（登录非高频）；如成瓶颈可调 cost，但需重新哈希存量。
- [session token 泄漏即劫持] → `HttpOnly` 防 XSS 读取、`SameSite=Lax` 防大部分 CSRF、`Secure` 防 HTTP 明文嗅探、server-side 可即时吊销（删行 / 登出）。综合风险与主流 Web 应用持平。
- [开放注册被滥用刷账号] → D12 默认开放但可关；未来可加 rate limit / 邀请码（Non-Goal）。
- [开放重定向 via `next` 参数] → D11 强制 `next` 必须以 `/` 开头，拒绝 `//evil.com` 与绝对 URL。
- [cookie 在 HTTP（非 HTTPS）下 `Secure` 不生效] → `Secure` 仅在 r.URL.Scheme=="https" 或 TLS 监听时设置；明文 HTTP 部署（本地）cookie 仍工作但无传输保护，与现有 API key 同等暴露面，文档提示。

## Migration Plan

1. 合并代码不改变默认行为（`--auth` 默认关闭），可安全发布；两新表幂等建表，旧库自动获得。
2. 已开启 `--auth` 的部署：升级二进制 → 用户访问站点自动跳 `/register` 或 `/login`；原有 CLI `sth send --api-key` 完全不受影响。
3. 想禁用注册的部署：加 `--allow-registration=false` 或 `STH_ALLOW_REGISTRATION=false`，用户改由 `sth user add` 创建并需自行通过 `/login`（若已设密码）或继续用 CLI key。
4. 回滚：去掉 cookie 登录相关代码即恢复纯 Bearer；DB schema 为加表，对旧二进制只读兼容。

## Open Questions

- 是否需要"登录页内嵌签发 API key"以打通浏览器上传？本期 Non-Goal，留待反馈后作为独立 change。
- session 过期清理（GC）策略——本期登录校验时顺带跳过过期行，不做主动 GC；是否需要后台 goroutine 定期 DELETE？留待性能反馈。
- 是否需要"忘记密码"流程？本期 Non-Goal（管理员可 `sth user reset-password` 直接重置 DB，留待后续 CLI 子命令）。
