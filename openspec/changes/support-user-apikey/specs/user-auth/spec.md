## ADDED Requirements

### Requirement: 鉴权模式开关

系统 SHALL 提供一个启动时可配置的鉴权开关，默认关闭。关闭时，所有接口的行为 MUST 与未引入鉴权前完全一致（不读取、不校验任何凭据）。开启时，系统 MUST 按本规范各要求强制鉴权。

开关可通过以下任一方式设置，优先级为命令行 flag > 环境变量 > 默认值（关闭）：
- 命令行 flag：`sth start --auth`
- 环境变量：`STH_AUTH=true` / `STH_AUTH=1`

#### Scenario: 鉴权关闭时行为不变（默认）
- **WHEN** 未设置 `--auth` 也未设置 `STH_AUTH` 启动服务，客户端不带任何凭据发起上传、删除、列表、预览请求
- **THEN** 所有请求被正常处理，响应与未引入本特性的版本完全一致

#### Scenario: 通过 flag 开启鉴权
- **WHEN** 执行 `sth start --auth` 启动服务
- **THEN** 服务 MUST 进入鉴权模式，对所有受保护接口强制校验 API key

#### Scenario: 通过环境变量开启鉴权
- **WHEN** 设置 `STH_AUTH=true` 并启动服务
- **THEN** 服务 MUST 进入鉴权模式

#### Scenario: flag 优先级高于环境变量
- **WHEN** 同时设置 `STH_AUTH=true` 但命令行带 `--auth=false`（或等价的显式关闭）启动
- **THEN** 服务 MUST 遵循命令行的显式设置

### Requirement: 用户与 API key 数据模型

系统 SHALL 维护 `users` 表，每行代表一个用户，至少包含：唯一内部 id、唯一用户名、创建时间。系统 SHALL 维护 `api_keys` 表，每行代表一把 API key，至少包含：唯一 key 标识（仅在校验时使用）、key 的哈希值（非明文）、生成该哈希所用的**盐（salt）**、记录哈希**算法标识（hash_algo，如 `sha256`）**、所属 user id、创建时间、可选的吊销/禁用标记。salt 与 hash_algo 必须逐行持久化，使每把 key 可独立校验、并支持未来平滑切换哈希算法。系统 MUST 仅存储 key 的单向哈希，不得以明文持久化 key。

#### Scenario: API key 仅存哈希
- **WHEN** 签发一把新 API key 并写入数据库
- **THEN** 数据库中 `api_keys` 表仅记录该 key 的单向哈希值，不记录明文 key

#### Scenario: 用户名唯一
- **WHEN** 尝试创建与已有用户同名的用户
- **THEN** 操作 MUST 失败并返回明确的冲突错误

#### Scenario: API key 归属用户
- **WHEN** 查询任意一把 API key 的归属
- **THEN** 系统 MUST 能解析出其所属的唯一 user id

### Requirement: API key 校验

鉴权模式开启时，系统 SHALL 通过请求头 `Authorization: Bearer <key>` 提取客户端提交的 API key，对其做哈希后与 `api_keys` 表中存储的哈希比对。仅当存在匹配且该 key 未被吊销时，请求被视为已鉴权，并解析出所属 user 作为当前请求身份。任何被判定为未鉴权的受保护请求 MUST 返回 `401 Unauthorized`。

#### Scenario: 有效 key 通过鉴权
- **WHEN** 鉴权模式下，客户端在受保护接口请求中携带 `Authorization: Bearer <valid-key>`
- **THEN** 系统 MUST 接受请求，并将该 key 所属 user 作为当前请求身份

#### Scenario: 缺失凭据被拒绝
- **WHEN** 鉴权模式下，受保护接口请求未携带 `Authorization` 头
- **THEN** 系统 MUST 返回 `401 Unauthorized`，不执行任何业务逻辑

#### Scenario: 无效 key 被拒绝
- **WHEN** 鉴权模式下，请求携带的 `Authorization` 值不匹配任何未吊销 key
- **THEN** 系统 MUST 返回 `401 Unauthorized`

#### Scenario: 已吊销 key 被拒绝
- **WHEN** 鉴权模式下，请求携带的 key 对应记录已被标记为吊销
- **THEN** 系统 MUST 返回 `401 Unauthorized`

### Requirement: Session 归属与隔离

鉴权模式开启时，创建 session 的请求 MUST 将当前鉴权 user 记录为该 session 的 owner（写入 `sessions.user_id`）。对于读取类接口（列表、搜索、peers、下载），系统 MUST 仅返回当前 user 拥有的 session。对于变更类接口（增量写、删除、改元数据），系统 MUST 拒绝操作非本人拥有的 session，返回 `403 Forbidden`。

#### Scenario: 创建 session 记录 owner
- **WHEN** 鉴权模式下，已鉴权 user 上传新 session
- **THEN** 该 session 的 `user_id` MUST 被设为当前 user 的 id

#### Scenario: 列表仅返回本人 session
- **WHEN** 鉴权模式下，user A 请求会话列表/搜索
- **THEN** 结果 MUST 仅包含 user A 拥有的 session，不得包含其他 user 的 session

#### Scenario: 不得操作他人 session
- **WHEN** 鉴权模式下，user A 尝试对 user B 拥有的 session 做增量写、删除或改元数据
- **THEN** 系统 MUST 返回 `403 Forbidden`，不修改任何数据

### Requirement: 预览保护策略

预览接口（`GET /s/<id>/...` 与 `GET /s/<id>/ws`）的鉴权 SHALL 独立于其他接口配置。系统 MUST 提供一个 `--protect-previews` 开关（默认关闭）。当 `--protect-previews` 未启用时，即使 `--auth` 已开启，预览接口也 MUST 保持开放，以保留"上传后分享 `/s/<id>/` 链接"的核心用法。当 `--protect-previews` 启用时，预览接口 MUST 要求携带**任意一把有效 API key**（通过 `Authorization: Bearer <key>`），但不 MUST 校验该 key 是否为该 session 的 owner——预览的本意是被分享查看，任何持有有效 key 的用户即可访问。

`--protect-previews` 依赖 `--auth` 提供的 key 校验基础设施。为避免产生"开启预览保护却未开鉴权"的无定义状态，启用 `--protect-previews` SHALL 隐含启用 `--auth`：若调用方仅设置 `--protect-previews` 而未显式设置 `--auth`，系统 MUST 视同 `--auth` 已开启并启动。`--auth` 关闭且 `--protect-previews` 未启用时，预览接口不读取、不校验任何凭据（与所有其他接口一致）。

#### Scenario: 鉴权开启但预览默认开放
- **WHEN** 以 `--auth` 启动但未加 `--protect-previews`，任何人用浏览器访问 `/s/<id>/`
- **THEN** 预览 MUST 正常返回内容，不要求凭据

#### Scenario: 显式收紧预览保护
- **WHEN** 以 `--auth --protect-previews` 启动，未携带有效 key 访问 `/s/<id>/`
- **THEN** 系统 MUST 返回 `401 Unauthorized`

#### Scenario: 预览保护只校验有效 key，不校验 owner
- **WHEN** 以 `--auth --protect-previews` 启动，user A 用自己的有效 key 访问 user B 拥有的 session 的 `/s/<id>/`
- **THEN** 系统 MUST 正常返回预览内容（预览不归属隔离）

#### Scenario: 预览保护隐含开启鉴权
- **WHEN** 仅设置 `--protect-previews` 而未显式设置 `--auth` 启动
- **THEN** 系统 MUST 视同 `--auth` 已开启：所有受保护接口（含预览）均要求有效 key

#### Scenario: 鉴权完全关闭时预览不读取凭据
- **WHEN** 未设置 `--auth` 也未设置 `--protect-previews` 启动，访问 `/s/<id>/`
- **THEN** 系统 MUST 不读取或校验任何 `Authorization` 头，直接返回预览内容

### Requirement: CLI 凭据传递

`sth send` 与 `sth watch` SHALL 支持通过 `--api-key <key>` flag 或 `STH_API_KEY` 环境变量提供 API key，并在发起请求时携带 `Authorization: Bearer <key>` 头。flag 优先级高于环境变量。鉴权关闭时，即使提供了 key，CLI 也 MUST 不附加 `Authorization` 头（保持请求与旧版一致）。

#### Scenario: 通过 flag 携带 key
- **WHEN** 执行 `sth send --api-key <key> ...` 指向开启了鉴权的 server
- **THEN** 上传请求 MUST 携带 `Authorization: Bearer <key>` 头

#### Scenario: 通过环境变量携带 key
- **WHEN** 设置 `STH_API_KEY=<key>` 后执行 `sth send ...`
- **THEN** 上传请求 MUST 携带 `Authorization: Bearer <key>` 头

#### Scenario: flag 优先于环境变量
- **WHEN** 同时设置 `STH_API_KEY=env-key` 并传入 `--api-key flag-key`
- **THEN** 请求 MUST 使用 flag 提供的 `flag-key`

#### Scenario: 鉴权关闭时不附加凭据头
- **WHEN** 指向关闭鉴权的 server，即使提供了 `--api-key`
- **THEN** 请求 MUST 不携带 `Authorization` 头

### Requirement: 用户与 key 管理子命令

系统 SHALL 提供 `sth user` 子命令用于在开启了鉴权的服务端本地管理用户与 API key，至少支持：创建用户（`sth user add <name>`）、为用户签发新 key（`sth user issue-key <name>`，明文 key 仅在签发时一次性输出）、吊销 key（`sth user revoke-key <key-prefix|id>`）、列出用户（`sth user list`）。签发 key 时 MUST 仅在终端输出一次明文，之后不可再读取。`revoke-key` 的参数 MUST 解析为某个 key 的唯一精确 id 或唯一前缀：当给定前缀匹配多条 key 时，命令 MUST **fail closed**（拒绝吊销任何 key），并提示用户提供更长的前缀或完整标识，以避免误吊销。

#### Scenario: 创建用户
- **WHEN** 执行 `sth user add alice`
- **THEN** 系统 MUST 创建用户 alice，并可重复列出该用户

#### Scenario: 签发 key 一次性明文输出
- **WHEN** 执行 `sth user issue-key alice`
- **THEN** 系统 MUST 生成新 key，在终端输出一次明文，并在数据库仅存储其哈希

#### Scenario: 按唯一标识吊销 key
- **WHEN** 执行 `sth user revoke-key <唯一前缀或精确 id>`，且该标识恰好匹配一把未吊销 key
- **THEN** 系统 MUST 吊销该 key，此后该 key 鉴权失败（返回 401）

#### Scenario: 前缀歧义时 fail closed
- **WHEN** 执行 `sth user revoke-key <前缀>`，该前缀匹配多把 key
- **THEN** 系统 MUST 拒绝吊销任何 key 并返回错误，提示使用更长的前缀或完整 id
