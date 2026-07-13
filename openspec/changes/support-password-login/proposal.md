## Why

`sth` 目前的鉴权完全依赖 API key（`support-user-apikey` 已落地）：用户与 key 由本地 CLI（`sth user add` / `sth user issue-key`）管理，写操作与列表接口都要求 `Authorization: Bearer <key>`。这对 CLI 工作流（`sth send` / `sth watch`）足够，但对**浏览器用户极不友好**：

- 没有任何登录页面。用户想用浏览器查看自己的会话列表时，会被裸 `401 Unauthorized` 文本挡住，必须先在命令行 `sth user add` + `sth user issue-key` 拿到一长串明文 key，再想办法塞进浏览器请求头——浏览器原生无法设置 `Authorization` 头。
- 现有 `users` 表没有密码字段，用户只能由管理员通过 CLI 创建，无法自助注册。
- 缺少最基础的"登录 / 登出"Web 体验，限制了 `sth` 作为共享/团队部署时的人机可达性。

需要在不破坏现有 API key 体系（CLI 仍依赖它）的前提下，补充浏览器友好的用户名/密码注册、登录页与会话保持（Cookie），让"人"也能舒服地用。

## What Changes

- 新增 `/login`、`/register`、`/logout` 三个 Web 页面/接口，提供表单式登录与注册体验。
- 引入 server-side session cookie（`sth_session`），登录成功后种 cookie 维持浏览器会话；DB 只存 cookie token 的 SHA-256 哈希，与 API key 同等安全（明文不落库，DB 泄漏不等于 cookie 劫持）。
- 引入 `user_credentials` 表（bcrypt 哈希存储密码，与 `users` 一对一），不污染现有无密码的 `users` 行。
- 引入 `login_sessions` 表存 server-side session token 哈希、归属 user、过期时间。
- `authMiddleware` 扩展鉴权路径分类：
  - GET 类浏览请求（列表 `/`）接受 **Cookie 或 Bearer** 任一有效凭据；都没有且客户端是浏览器（`Accept: text/html`）时 **302 重定向到 `/login`** 而非裸 401。
  - 所有 **mutating** 方法（POST/PUT/DELETE）**只接受 Bearer，拒绝 Cookie**——以此天然规避 CSRF，无需引入 CSRF token 机制。
  - `/login` `/register` `/logout` 与预览 `/s/<id>/` 一并豁免，预览保持开放（不破坏分享链接核心用法）。
- 新增 `--allow-registration` flag 与 `STH_ALLOW_REGISTRATION` env（**默认 true**，允许开放注册），可关闭以仅允许管理员通过 CLI 预建账号。
- **向后兼容硬约束**：`--auth` 关闭时行为逐字节不变；`--auth` 开启但用户不使用新页面时，原有 Bearer/API key 通道完全保留，CLI `sth send/watch` 不受任何影响。

## Capabilities

### Modified Capabilities
- `user-auth`: 在原有 API key 鉴权之上，叠加密码登录、注册、server-side session cookie；扩展中间件的路径分类与重定向行为；新增注册开关。原 API key 的签发、校验、owner 隔离语义不变。

<!-- 无新增 capability，本次是对现有 user-auth 能力的增量扩展。 -->

## Impact

- **数据库**（`internal/session/`）：新增 `user_credentials`、`login_sessions` 两表（`CREATE TABLE IF NOT EXISTS` 幂等建表，复用 `init()` 路径）；不修改现有 `users` / `api_keys` / `sessions` schema。
- **服务端**（`internal/server/`）：新增 `loginauth.go`（handlers + 内联模板 + cookie 工具）；`auth.go` 的 `authMiddleware` 扩展路径分类与 cookie 解析；`server.go` 的 `routes()` switch 增加 3 个 case、`Server` 增加 `allowRegistration` 字段与 setter。
- **CLI**（`internal/cli/cli.go`）：`runStart` 增加 `--allow-registration` flag 与 `STH_ALLOW_REGISTRATION` env 解析。
- **依赖**：新增 `golang.org/x/crypto/bcrypt`（项目首个密码学哈希库，专为低熵人脑密码，与 API key 的高熵 SHA-256 路径不冲突）。
- **测试**：新增 `loginauth_test.go` 覆盖注册/登录/登出/cookie 中间件/重定向/向后兼容；现有 `auth_test.go` / `auth_e2e_test.go` 不改动即过。
