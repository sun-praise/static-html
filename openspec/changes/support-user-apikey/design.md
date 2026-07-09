## Context

`sth` 当前是一个无鉴权的本地 HTML 预览服务器：所有接口完全开放，`sessions` 表无 owner 概念。这契合单人本地预览的设计意图，但在共享/团队部署时存在越权覆盖、删除与数据泄露风险。本变更是项目首次引入身份概念，需要同时满足"本地零配置零负担"与"共享部署可鉴权隔离"两种形态，且不得破坏现有行为与 `/s/<id>/` 分享用法。

现有相关实现要点（供决策参照）：
- 路由为 `internal/server/server.go` 中 `routes()` 的单个 `switch`，唯一的中间件是 `live.InjectMiddleware`（响应改写，非鉴权）。
- 数据库迁移走 `internal/session/store.go` 的 `ensureColumns`（基于 `PRAGMA table_info` 的渐进式加列）与 `CREATE TABLE IF NOT EXISTS` 的幂等建表。
- 配置全部来自 CLI flag / 环境变量，无配置文件；`Server` 结构体字段持有运行时配置。
- `sth send` / `sth watch` 目前只设置 `Content-Type` 头，无任何凭据。

## Goals / Non-Goals

**Goals:**
- 提供默认关闭、可一键开启的鉴权模式，开启后多用户彼此隔离。
- API key 仅存哈希，签发明文仅一次性输出。
- CLI（`send`/`watch`）能透传凭据，Python 助手与部署配置同步支持。
- 保留"鉴权开启但预览仍可分享"的能力，同时提供收紧预览保护的选项。
- 向后兼容：鉴权关闭时行为与旧版逐字节一致。

**Non-Goals:**
- 不引入密码登录、Web 注册界面、OAuth/SSO、多因素认证——身份仅通过本地 `sth user` 管理并由 API key 表征。
- 不做细粒度 RBAC（如只读 vs 读写角色）；隔离粒度为 owner 级。
- 不为预览链接实现 token/签名 URL 分享机制（`--protect-previews` 是开/关二态，不提供"带 token 的可分享链接"）。
- 不改动 live-reload / peers / metadata 的既有功能语义，仅在其外层叠加鉴权与 owner 过滤。

## Decisions

### D1: 鉴权开关默认关闭，flag/env 双通道，flag 优先
**选择**：`--auth` flag + `STH_AUTH` env，flag 优先；默认关闭。
**理由**：向后兼容是硬约束（现有 docker-compose、个人本地用法依赖零鉴权）。flag 优先于 env 符合既有 `parseArgs` 风格，便于临时覆盖。
**备选**：默认开启鉴权——被否决，会破坏所有现有部署。

### D2: 预览鉴权独立开关 `--protect-previews`，默认关闭
**选择**：即便 `--auth` 开启，`/s/<id>/` 与 `/s/<id>/ws` 默认仍开放，仅当显式加 `--protect-previews` 时才要求 key。
**理由**：`sth` 的核心 UX 是"上传 → 拿到链接 → 分享给他人浏览器查看"。若鉴权一刀切，分享场景失效。独立开关把"防写入越权"与"防预览泄露"解耦，让部署者按需取舍。
**备选**：用带 token 的签名 URL 分享（如 `/s/<id>/?token=...`）——列为 Non-Goal，复杂度高且改变分享链接形态，留待未来。

### D3: API key 存储——SHA-256 哈希 + 随机盐
**选择**：签发时生成高熵随机 key（`crypto/rand`，≥32 字节，hex/base64 编码），对 key 计算 `SHA-256(salt || key)`，库里存 `salt` 与 `hash`，不存明文；校验时用同样 salt 重算比对。
**理由**：避免明文泄露即等于凭据泄露；SHA-256 对 API key 这类高熵输入足够（非人脑密码，无需 bcrypt 的慢哈希开销），校验路径每次请求都会走，需要快。
**备选**：bcrypt/argon2——被否决，API key 熵足够高，慢哈希会给每次请求增加无谓延迟；且 key 本身可吊销/轮换。
**key 格式**：前缀 `sth_` + 随机部分，便于人眼识别与 `revoke-key` 的前缀匹配。

### D4: 身份落在 `sessions.user_id`，迁移走既有 `ensureColumns`
**选择**：`sessions` 新增可空 `user_id` 列；鉴权开启时创建 session 必填当前 user id，关闭时为 NULL；新增 `users`、`api_keys` 两表，幂等建表。
**理由**：复用 `ensureColumns`（`store.go`）既有迁移模式，无需引入独立 migration 框架；NULL 表示"无 owner / 鉴权关闭时创建"，语义清晰，不破坏旧行。
**隔离实现**：所有 list/search/peers 查询在鉴权模式分支里加 `WHERE user_id = ?`；所有按 `session_id` 寻址的接口（变更类增量写/删除/改元数据，**以及下载 `handleDownload`**）先 `SELECT user_id FROM sessions WHERE session_id=?` 校验归属，不匹配返回 403。下载与变更同等对待，避免鉴权开启后仍可通过 `/api/sessions/<id>/download` 跨用户读取他人 session 归档。预览接口（`handlePreview`、`/s/<id>/ws`）不受 owner 校验约束，仅受 D2 的 key 校验约束——预览的本意是给人看，不属于 owner 私有读。

### D5: 鉴权作为独立中间件，置于 `live.InjectMiddleware` 之前
**选择**：新增 `authMiddleware`，包裹 `routes()`，在路由匹配前后、业务 handler 前完成身份解析；解析出的 user 通过 `context.Context` 注入下游 handler。
**理由**：与既有单 `switch` 路由风格一致，不引入路由库；中间件前置保证任何 handler 都能从 ctx 拿到当前 user，且未鉴权请求不会触达 handler。
**路由分组**：用两张"受保护路径前缀表"分别表达"写接口集合"与"全部接口集合"，以支持 D2 的预览开关——预览路径单独从受保护集合里排除/纳入。

### D6: CLI 凭据——`--api-key` flag + `STH_API_KEY` env，Bearer 头
**选择**：`send`/`watch` 新增 `--api-key`（优先）与 `STH_API_KEY`（回退）；鉴权关闭时即使有 key 也不附加头。
**理由**：与 D1 的开关对称——客户端是否带凭据应由"目标 server 是否鉴权"决定，但客户端无法可靠探测，故采用"客户端只要被给了 key 就尝试携带；server 侧决定是否校验"。鉴权关闭时不附加头是为了保持请求与旧版一致，避免无关 header 影响测试基线。
**备选**：客户端始终附加头——可接受但会污染关闭模式下的请求快照，否决。

### D7: `sth user` 子命令——本地直接操作 DB
**选择**：`sth user add/issue-key/revoke-key/list` 直接读写本地 SQLite（复用 `session` 包的连接路径），签发时明文一次性 stdout 输出。
**理由**：无独立控制平面，本地 CLI 直连 DB 是与项目体量匹配的最简方案；避免在 HTTP 上开"管理接口"从而引入新的鉴权面。
**注意**：`user` 子命令不需要 server 在线，直接操作 DB 文件；需在文档中说明。

## Risks / Trade-offs

- [开启鉴权的部署误以为预览也受保护] → 默认 `--protect-previews=off` 与文档/启动日志显著提示："预览链接当前对外开放"。`--auth` 启动时在 stderr 打印一行明确的鉴权姿态摘要。
- [API key 哈希算法未来需更换] → 校验集中在单一函数 `verifyAPIKey`，且 `api_keys` 表预留算法标识列（如 `hash_algo`），便于日后平滑切换。
- [`sessions.user_id` 迁移遇旧库无该列] → 复用 `ensureColumns` 的 `PRAGMA table_info` 探测，旧库自动加列、旧行 NULL，零停机；已在 soft-delete 等变更中验证过该路径。
- [CLI 探测不到 server 鉴权状态导致 401 体验差] → `send`/`watch` 收到 401 时给出可操作提示："目标 server 已开启鉴权，请通过 `--api-key` 或 `STH_API_KEY` 提供有效 key"，而非裸报错。
- [`sth user` 直连 DB 与运行中 server 的连接竞争] → 复用现有单连接 + WAL 的并发模型；写操作（吊销/签发）对运行时影响为"下次请求即生效"，可接受。
- [首次开启鉴权时历史 session 无 owner] → 明确语义：历史 session 的 `user_id` 为 NULL，在鉴权模式下**仅对管理员可见**或**不可被任何非空 user 触达**；本期采用"不可触达历史 session"的最简语义，并在 design 中标注为已知限制。

## Migration Plan

1. 合并代码不改变默认行为（`--auth` 默认关闭），可安全发布。
2. 需要鉴权的部署：升级二进制 → `sth user add <owner>` → `sth user issue-key <owner>`（记下一次性明文）→ 以 `STH_AUTH=true`（或 `--auth`）重启 → 客户端配置 `STH_API_KEY`。
3. 回滚：去掉 `--auth`/`STH_AUTH` 重启即恢复无鉴权；DB schema 变更为加列/加表，对旧二进制只读兼容（旧代码不读新列/新表，不影响功能）。

## Open Questions

- 历史 session（`user_id IS NULL`）在鉴权模式下是否需要"管理员/迁移"概念来认领？本期决定不引入管理员角色，历史 session 在鉴权模式下不可触达，如需保留请在开启鉴权前由用户手动重新上传。是否需要后续追加一个 `sth user claim-orphans` 工具？留待反馈。
- 是否需要 key 的过期时间字段？表里预留 `expires_at` 可空列，但本期 `issue-key` 不强制设置，留作未来增强。
