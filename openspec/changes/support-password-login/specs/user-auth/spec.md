## ADDED Requirements

### Requirement: 浏览器会话登录（Cookie）

系统 SHALL 提供基于用户名/密码的浏览器登录。登录成功后，系统 MUST 向客户端设置一个名为 `sth_session` 的 HttpOnly cookie，其值为一次性生成的高熵随机 token（至少 256 位熵）。系统 MUST 仅在数据库中持久化该 token 的单向哈希（SHA-256），不得持久化明文 token。cookie 的属性 MUST 包含 `HttpOnly`、`SameSite=Lax`、`Path=/`；当请求通过 HTTPS 到达时，cookie 还 MUST 设置 `Secure`。系统 SHALL 提供 `/logout` 接口使当前 session cookie 立即失效（删除对应的 session 记录并清除客户端 cookie）。

session token 的有效期默认为 30 天。校验时，系统 MUST 拒绝已过期或已删除的 session。

#### Scenario: 登录成功并设置 cookie
- **WHEN** 已注册用户在 `/login` 提交正确的用户名与密码
- **THEN** 系统 MUST 返回 302 重定向到首页（或 `next` 指定的站内路径），并在响应中设置有效的 `sth_session` cookie

#### Scenario: 登录失败不设置 cookie
- **WHEN** 用户提交错误密码或不存在的用户名
- **THEN** 系统 MUST 不设置任何 session cookie，并重新渲染登录页带错误提示

#### Scenario: session token 仅存哈希
- **WHEN** 登录成功后查询数据库 `login_sessions` 表
- **THEN** 表中 MUST 仅包含 token 的单向哈希值，不含明文 token

#### Scenario: 登出使 cookie 失效
- **WHEN** 已登录用户 POST `/logout`
- **THEN** 系统 MUST 删除该 session 记录，清除客户端 cookie，之后该 token 的请求 MUST 被视为未鉴权

#### Scenario: 过期 session 被拒绝
- **WHEN** 一个 session 的 `expires_at` 已过期，客户端携带对应 cookie 请求
- **THEN** 系统 MUST 视为未鉴权

### Requirement: 用户自助注册

系统 SHALL 提供 `/register` 页面允许用户自助注册账号（用户名 + 密码）。注册 MUST 同时创建 `users` 行与对应的 `user_credentials` 行（密码以 bcrypt 哈希存储）。注册成功后系统 MUST 自动登录（设置 session cookie）并重定向到首页，无需用户再次手动登录。

系统 SHALL 提供注册开关，可通过以下任一方式设置，优先级为 flag > env > 默认值（**默认开启**）：
- 命令行 flag：`sth start --allow-registration=false`
- 环境变量：`STH_ALLOW_REGISTRATION=false`

当注册关闭时，`/register` GET MUST 显示"已关闭注册"提示页，`/register` POST MUST 返回 403 且不创建任何账号。

密码最小长度 MUST 为 8 个字符。系统 MUST 校验两次输入的密码一致。

#### Scenario: 注册成功并自动登录
- **WHEN** 用户在 `/register` 提交合法的用户名与密码（两次一致，密码≥8 字符），且注册开关开启
- **THEN** 系统 MUST 创建用户、存储密码哈希、设置 session cookie，并 302 重定向到首页

#### Scenario: 用户名重复被拒绝
- **WHEN** 注册时用户名已被占用
- **THEN** 系统 MUST 拒绝注册，重新渲染表单提示用户名已被占用，不创建任何账号

#### Scenario: 密码过短被拒绝
- **WHEN** 注册时密码少于 8 字符
- **THEN** 系统 MUST 拒绝注册并提示密码长度要求

#### Scenario: 注册关闭
- **WHEN** 以 `--allow-registration=false` 启动，用户访问 `/register`
- **THEN** GET MUST 显示关闭提示，POST MUST 返回 403 且不创建账号

### Requirement: 密码哈希存储

系统 MUST 使用 bcrypt 哈希存储用户密码，不得以明文或可逆方式持久化密码。`user_credentials` 表与 `users` 表为可选的一对一关系——存在无密码的 `users` 行是合法状态（如纯 API key 用户），此类用户 MUST 无法通过 `/login` 登录（`VerifyPassword` 返回 false）。

#### Scenario: 密码以 bcrypt 存储
- **WHEN** 用户设置密码
- **THEN** 数据库 MUST 存储 bcrypt 哈希，明文密码不出现在任何持久化存储中

#### Scenario: 无密码用户无法登录
- **WHEN** 一个没有 `user_credentials` 记录的用户尝试在 `/login` 用任意密码登录
- **THEN** 系统 MUST 拒绝登录（视为密码错误）

## MODIFIED Requirements

### Requirement: API key 校验
鉴权模式开启时，系统 SHALL 通过请求头 `Authorization: Bearer <key>` 提取客户端提交的 API key 进行校验。**对于 mutating 方法（POST/PUT/DELETE）的受保护接口，系统 MUST 仅接受 Bearer 凭据，拒绝任何 session cookie 凭据**，以规避 CSRF。对于 GET/HEAD 浏览类受保护接口，系统 SHALL 接受 session cookie **或** Bearer 任一有效凭据。任何被判定为未鉴权的 mutating 请求 MUST 返回 `401 Unauthorized`。

#### Scenario: mutating 接口只接受 Bearer
- **WHEN** 鉴权模式下，客户端仅携带有效 session cookie（无 Bearer 头）发起 POST/PUT/DELETE
- **THEN** 系统 MUST 返回 401，不执行写操作

#### Scenario: GET 接口接受 cookie 或 Bearer
- **WHEN** 鉴权模式下，客户端携带有效 session cookie（无 Bearer 头）发起 GET 列表请求
- **THEN** 系统 MUST 接受请求并返回该用户拥有的 session 列表

### Requirement: 浏览器未鉴权重定向

鉴权模式开启时，对于 GET/HEAD 受保护路径（如列表 `/`），若客户端未携带任何有效凭据且请求 `Accept` 头表明期望 HTML 响应（含 `text/html`），系统 MUST 返回 `302` 重定向到 `/login?next=<原路径>`。`next` 参数 MUST 校验以单个 `/` 开头（站内绝对路径），拒绝以 `//` 或协议前缀开头的值，以防开放重定向。若客户端不期望 HTML（如 curl、CLI），系统 MUST 维持原有的 `401 Unauthorized` + `WWW-Authenticate: Bearer` 响应。

#### Scenario: 浏览器未登录被重定向
- **WHEN** 鉴权模式下，未携带凭据的浏览器（`Accept: text/html`）访问 `/`
- **THEN** 系统 MUST 返回 302 重定向到 `/login?next=/`

#### Scenario: 非浏览器客户端维持 401
- **WHEN** 鉴权模式下，未携带凭据的 curl（无 `Accept: text/html`）访问 `/`
- **THEN** 系统 MUST 返回 401 + `WWW-Authenticate: Bearer`

#### Scenario: next 参数防开放重定向
- **WHEN** 登录请求的 `next` 参数为 `//evil.com` 或 `https://evil.com`
- **THEN** 系统 MUST 忽略该值，登录成功后重定向到 `/`（默认首页）

#### Scenario: 预览路径不被重定向
- **WHEN** 鉴权模式下（`--protect-previews` 未启用），未携带凭据访问 `/s/<id>/`
- **THEN** 系统 MUST 正常返回预览内容，不重定向到登录页
