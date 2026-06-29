## Context

`sth` 是 Go 编写的 HTML 预览服务（v1.3.0，端口 3939，SQLite 存储，1.2.0 起支持 `--server-name` 主机名校验、1.3.0 起支持 WebSocket live reload）。当前部署文档仅含一条 `systemctl --user restart static-html.service` 的故障排查提示，没有任何容器化资产。

目标：把 `sth` 以 Docker 容器形态部署到 `jd 服务器`，对外通过 `https://sth.sun-praise.com` 提供服务。已有 nginx 处理 TLS 终止与证书续期。

`Dockerfile` 已存在但缺 OCI labels（image 来源追溯困难），未提供 compose 文件、无备份机制、无 CI 镜像构建。

## Goals / Non-Goals

**Goals:**
- 一条命令 (`docker compose up -d`) 即可在 `jd 服务器` 拉起服务
- CI 在 tag 推送时自动构建并 push 多架构镜像到 `ghcr.io/sun-praise/static-html`
- 命名 volume `sth_data` 持久化 SQLite 数据库与上传快照；命名 volume `sth_backup` 持久化每日备份
- 公开的 `deploy/` 目录提供可复制的部署手册（从 0 到上线）
- WebSocket (`/s/<id>/ws`) 通过现有 nginx 透传到容器，不破坏 live reload

**Non-Goals:**
- 不替换现有 nginx（用户已有，certbot 续期运作良好）
- 不引入容器化的反代（Caddy/Traefik 容器）—— 反代在 host 上
- 不改变 `sth` 二进制行为；不修改 `internal/` 任何 Go 代码
- 不实现多副本与负载均衡（SQLite 写入并发受限；当前目标是单实例）
- 不实现自动扩缩容 / 滚动更新（CI 推送 + 手动 `docker compose pull && up -d` 即可）

## Decisions

### 1. 反代位置：host 现有 nginx 透传到容器

**选择**：在 host 上新增 `deploy/nginx/sth.sun-praise.com.conf`，`proxy_pass http://127.0.0.1:3939`
**备选**：用 Caddy/Traefik 容器替代

理由：用户已声明 “已有 nginx”，强行引入第二个反代会与现有站点管理与证书续期冲突。最薄一层是新增一个 server block。

### 2. compose 仅暴露 127.0.0.1

**选择**：`ports: ["127.0.0.1:3939:3939"]`
**备选**：`ports: ["3939:3939"]`（绑定 0.0.0.0）

理由：容器端口不应直接暴露到公网。所有外部流量必须经过 nginx，便于：
- 集中 TLS 终止
- 集中限流 / WAF 策略
- 阻止绕过反代的直连

### 3. 数据持久化：命名 volume

**选择**：`sth_data:/data`（容器内 `/data/sessions.db` 与 `/data/sessions/`）
**备选**：bind mount `/opt/sth/data:/data`

理由：命名 volume 由 docker 管理，避免路径与权限耦合；`docker volume inspect sth_data` 即可定位物理路径用于备份导出。bind mount 会把 host 路径硬编码进仓库不可移植。

### 4. 备份：systemd timer + `sqlite3 .backup`

**选择**：在 host 上跑用户级 systemd timer，每日 03:00 调用 `deploy/backup/sth-backup.sh`，脚本用 `sqlite3 .backup` 把 `sessions.db` 复制到 `sth_backup:/backup/<UTC-date>/`
**备选**：纯 cron、容器内 cron、borg/restic

理由：
- `sqlite3 .backup` 是 SQLite 官方推荐的热备份方式（API `sqlite3_db_backup`），不会破坏并发读写
- systemd timer 在 `loginctl enable-linger` 之后即使 SSH 断开也能跑
- 单独 volume 让备份可以独立于应用 volume 旋转

### 5. 镜像构建：GitHub Actions + buildx 多架构

**选择**：`.github/workflows/docker.yml` 在 `push tag v*` 与 `push branch main` 时构建 `linux/amd64,linux/arm64`，推 `ghcr.io/sun-praise/static-html:<tag>` 与 `:latest`
**备选**：单架构、仅 tag 触发

理由：jd 服务器可能是 x86 也可能是 arm（典型开发机）；main 分支触发便于集成验证；tag 触发用于发布。镜像仅 `push` 不 `pull request` 触发，避免 fork 仓库意外构建。

### 6. 入口参数：`start --bind 0.0.0.0 --port 3939 --server-name sth.sun-praise.com --db /data/sessions.db`

**选择**：在 compose `command:` 显式传入所有参数
**备选**：依赖默认值

理由：显式优于隐式；`--server-name` 启动时校验通过（1.2.0 引入），把 host header 校验锁死。

## Risks / Trade-offs

- **[SQLite + volume 备份一致性]** `docker exec sqlite3 .backup` 在容器内执行需要安装 `sqlite3` CLI → 在 `Dockerfile` 留 CLI 给运维临时排障用；备份脚本调用 `docker exec` 走容器内二进制
- **[WS 反代]** nginx 默认会把 `Connection: close` 透传，破坏 WebSocket → server block 必须显式 `proxy_http_version 1.1; proxy_set_header Upgrade $http_upgrade; proxy_set_header Connection "upgrade";`
- **[WS 超时]** nginx 默认 `proxy_read_timeout 60s` 在 live reload 长时间空闲时会断开 → 设 `86400s`
- **[备份体积膨胀]** 备份 volume 不轮转 → 文档明确：保留 14 天，14 天前由 `find -mtime +14 -delete` 清理（在 `sth-backup.sh` 内）
- **[镜像体积]** 多阶段 + alpine 已使镜像在 ~15MB 量级；`go-sqlite3` CGO 强制需要 `gcc musl-dev`（已就位）
- **[权限]** 容器内 uid 1000，host 端 backup 脚本跑在 `loginctl enable-linger` 用户下；如 backup volume 由 root 创建，权限需要 `chown` 或 `chmod a+rw`（在脚本内 `chmod -R a+rwX /var/lib/docker/volumes/sth_backup` 之后写；最简方案是脚本调用 `docker exec` 在容器内做 `.backup`，产物落 `sth_backup` volume，无需权限协调）

## Operational Notes

- 首次上线流程：`docker compose pull && docker compose up -d`（CI 已构建并推 `:latest`）
- 升级流程：打 `v*` tag → CI 推镜像 → 服务器 `cd /opt/sth && docker compose pull && docker compose up -d`
- 紧急回滚：`docker compose down && docker compose up -d ghcr.io/sun-praise/static-html:v1.3.0`
- 备份验证：随机抽一日的备份 volume 内文件，`sqlite3 sessions.db ".schema"` 验证可读
- 健康检查：`/health` 由 server 提供（与 README 中 `wget -qO- http://localhost:3939/` 路径一致）
