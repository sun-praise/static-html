## 1. 数据层：credentials / login_sessions 表

- [ ] 1.1 在 `internal/session/credentials.go` 实现 `initCredentials()`：`CREATE TABLE IF NOT EXISTS user_credentials (user_id PK FK→users, password_hash, updated_at)`，幂等建表；接入 `Store.init()`
- [ ] 1.2 实现 `SetPassword(userID, password) error`：bcrypt(cost 10) 哈希后 upsert（INSERT OR REPLACE）
- [ ] 1.3 实现 `VerifyPassword(userID, password) (ok bool, err error)`：读 hash，bcrypt 比对；无凭据行返回 (false, nil)
- [ ] 1.4 在 `internal/session/loginsession.go` 实现 `initLoginSessions()`：`CREATE TABLE IF NOT EXISTS login_sessions (token_hash PK, user_id FK, created_at, expires_at)` + 两个索引，幂等建表；接入 `Store.init()`
- [ ] 1.5 实现 `CreateLoginSession(userID) (token, err)`：生成 `sth_`+32B base64url token，存 `SHA-256(salt||token)`，返回明文
- [ ] 1.6 实现 `VerifyLoginSession(token) (userID, ok, err)`：用存储 salt 重算 hash 匹配未过期行，返回 user_id
- [ ] 1.7 实现 `DeleteLoginSession(token) err`：登出时按 token hash 删除
- [ ] 1.8 为上述 store 方法编写单元测试（含 bcrypt 正确/错误密码、token 仅存 hash、过期 session 校验失败、登出后失效）

## 2. 服务端：登录/注册/登出 handlers 与模板

- [ ] 2.1 新增 `internal/server/loginauth.go`，定义 `loginPageTemplate`、`registerPageTemplate`（内联完整 HTML+CSS，仿 `homePageTemplate` 风格）
- [ ] 2.2 实现 cookie 工具：`sessionCookieName="sth_session"`、`setSessionCookie(w, token)`、`clearSessionCookie(w)`、`readSessionCookie(r)`、`acceptsHTML(r)`
- [ ] 2.3 实现 `handleLoginGet`：渲染登录表单，读 `next` 参数并校验以 `/` 开头
- [ ] 2.4 实现 `handleLoginPost`：解析表单，`FindUserByUsername` + `VerifyPassword`，成功则 `CreateLoginSession` + 种 cookie + 302 回 `next` 或 `/`；失败重渲染表单带错误提示
- [ ] 2.5 实现 `handleRegisterGet`：`allowRegistration` 关闭时显示关闭提示页，否则渲染注册表单
- [ ] 2.6 实现 `handleRegisterPost`：`allowRegistration` 关闭时 403；否则校验（用户名非空、密码≥8、两次密码一致）→ `CreateUser` + `SetPassword` + `CreateLoginSession` + 种 cookie + 302 `/`
- [ ] 2.7 实现 `handleLogout`：`DeleteLoginSession` + 清 cookie + 302 `/login`

## 3. 服务端：中间件改造

- [ ] 3.1 `auth.go` 的 `authMiddleware` 扩展：在现有预览豁免之外，加 `/login` `/register` `/logout` 豁免（auth 页面不要求凭据）
- [ ] 3.2 方法分流：mutating（POST/PUT/DELETE）只走 `verifyBearer`，不变；GET/HEAD 先 `verifySessionCookie` 后 `verifyBearer`，成功注入 ctx
- [ ] 3.3 浏览器重定向：GET 无凭据且 `acceptsHTML(r)` → `302 /login?next=<path>`；非 HTML 维持原 401 + `WWW-Authenticate`
- [ ] 3.4 提供 `verifySessionCookie(r) (userID, ok, err)` 辅助函数（读 cookie → `VerifyLoginSession`）

## 4. 路由与配置

- [ ] 4.1 `server.go` 的 `routes()` switch 增加 `GET/POST /login`、`GET/POST /register`、`POST /logout` 三个 case
- [ ] 4.2 `Server` 结构体新增 `allowRegistration bool` 字段 + `SetAllowRegistration(bool)` setter（默认 true）
- [ ] 4.3 `cli.go` 的 `runStart` 增加 `--allow-registration` flag（`popBoolFlagWithPresence`）+ `STH_ALLOW_REGISTRATION` env（`resolveBool`），默认 true，调用 `srv.SetAllowRegistration`
- [ ] 4.4 `--allow-registration` 仅在 `--auth` 开启时有意义；`--auth` 关闭时该设置被忽略并在日志中提示（与现有 `note:` 风格一致）

## 5. 依赖

- [ ] 5.1 `go get golang.org/x/crypto/bcrypt`；确认 `go mod tidy` 后 go.mod / go.sum 干净

## 6. 测试与文档

- [ ] 6.1 新增 `internal/server/loginauth_test.go`：注册成功/用户名重复/密码不一致/关闭注册；登录成功/错误密码/不存在用户/cookie 有效；登出后 cookie 失效
- [ ] 6.2 中间件测试：cookie 有效放行 GET、cookie 不参与 mutating、浏览器 302 重定向、CLI Bearer 不受影响、`next` 开放重定向防护
- [ ] 6.3 向后兼容测试：`--auth` 关闭时现有 `auth_test.go` / `auth_e2e_test.go` / `cli_test.go` 不改动即通过
- [ ] 6.4 更新 README / `sth --help` 文本，说明 `/login` `/register` 用法、cookie 写限制、`--allow-registration`
- [ ] 6.5 `go test ./...` 全绿；`openspec validate` 通过；`openspec archive` 在 PR 合并后执行
