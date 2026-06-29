## Why

`sth` 当前仅以裸机 systemd 方式部署（见 README 中的 `systemctl --user restart static-html.service`），没有正式的容器化交付流程。运维侧需要：

- 同一份 Dockerfile 可在 CI 与本地复现构建
- 镜像通过 GHCR 托管，服务器只负责 `pull` + `restart`
- 公开域名 `sth.sun-praise.com` 必须有可复制的部署手册，含反代、持久化、备份

本次新增的 `deploy/` 目录只承载部署资产（compose / 反代片段 / 备份脚本 / CI 镜像构建），不修改任何运行时行为。

## What Changes

- **新增** `docker-compose.yml`（应用仓库根）：定义 `app` 服务，挂载命名 volume `sth_data` 到 `/data`，对外仅暴露 `127.0.0.1:3939:3939`
- **新增** `deploy/nginx/sth.sun-praise.com.conf`：与已有 nginx 兼容的 server block，反代到上游 `127.0.0.1:3939`，含 WebSocket Upgrade 头转发
- **新增** `deploy/backup/sth-backup.{sh,timer,service}`：systemd 用户级单元，每日 03:00 触发 `sqlite3 .backup` 将 `sessions.db` 复制到 `sth_backup` volume 内的 `/backup/<date>/sessions.db`
- **新增** `.github/workflows/docker.yml`：在 `v*` tag 与 `main` 分支上构建多架构（amd64/arm64）镜像，推送 `ghcr.io/sun-praise/static-html`
- **更新** `Dockerfile`：补全 OCI labels（org.opencontainers.image.*）与 `HEALTHCHECK` 保留
- **更新** 主 `README.md`：增加 “Deployment” 章节，链接到 `deploy/README.md`

## Capabilities

### New Capabilities

- `container-deploy`: 容器化部署全链路（Dockerfile、compose、nginx、备份、CI 镜像构建）

### Modified Capabilities

（无运行时行为变更；仅仓库资产新增与文档更新）

## Impact

- **运行时**：无影响；不修改 `internal/`
- **镜像**：CI 在 tag 推送时多架构构建并 push 到 GHCR；本地 `docker build` 仍可用
- **运维**：`jd 服务器` 上需要：
  1. 安装 docker 与 docker compose plugin
  2. 启用用户级 systemd（`loginctl enable-linger <user>`）
  3. 把 `deploy/nginx/sth.sun-praise.com.conf` 软链到 `/etc/nginx/sites-enabled/`
  4. `docker compose up -d` + `systemctl --user enable --now sth-backup.timer`
- **DNS**：在 `sun-praise.com` 域名下新增 A/AAAA 记录指向 `jd 服务器` 公网 IP
- **TLS**：由现有 nginx（certbot 续期）终止，反代回源 3939
