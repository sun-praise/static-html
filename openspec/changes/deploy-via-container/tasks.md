## 1. Dockerfile 补全

- [x] 1.1 在 builder 阶段注入构建期 `--build-arg` 接收 `VERSION` 与 `COMMIT`，写入 ldflags（沿用 goreleaser 的语义）
- [x] 1.2 在 runtime 阶段添加 OCI labels：`org.opencontainers.image.source=https://github.com/sun-praise/static-html`、`org.opencontainers.image.version=`，`org.opencontainers.image.created=` 通过 `BUILD_DATE` build-arg
- [x] 1.3 保留现有 `HEALTHCHECK`、`USER appuser`、`EXPOSE 3939`
- [x] 1.4 验证：`docker build --build-arg VERSION=1.3.0 --build-arg COMMIT=test --build-arg BUILD_DATE=$(date -u +%FT%TZ) .` 成功
- [x] 1.5 预创建 `/data`、`/backup` 并 chown 给 uid 1000，使命名 volume 首次挂载时继承 owner（修复 appuser 无法写 DB 的部署 bug）
- [x] 1.6 安装 `sqlite` 与 `tini`（前者供热备份，后者做 PID 1 信号转发）

## 2. docker-compose.yml

- [x] 2.1 写入根目录 `docker-compose.yml`，定义 `app` 服务
- [x] 2.2 镜像固定为 `ghcr.io/sun-praise/static-html:${STH_IMAGE_TAG:-latest}`，通过环境变量覆盖便于回滚
- [x] 2.3 `ports: ["127.0.0.1:3939:3939"]`（仅本机）
- [x] 2.4 `volumes: ["sth_data:/data", "sth_backup:/backup"]` + 顶层 `volumes:` 声明 `sth_data` 与 `sth_backup`（命名 volume）
- [x] 2.5 `restart: unless-stopped`
- [x] 2.6 `command: ["start", "--bind", "0.0.0.0", "--port", "3939", "--server-name", "sth.sun-praise.com", "--db", "/data/sessions.db"]`
- [x] 2.7 日志切割（`max-size: 10m`, `max-file: 5`）+ 容器 label `com.docker.compose.service=app`（供备份脚本发现）
- [x] 2.8 验证：`docker compose -f docker-compose.yml config -q` 退出 0；env 覆盖 `STH_IMAGE_TAG=1.3.0` 生效

## 3. nginx 反代配置

- [x] 3.1 写入 `deploy/nginx/sth.sun-praise.com.conf`
- [x] 3.2 80 端口 server block：`return 301 https://$host$request_uri;`（保留 `/.well-known/acme-challenge/` 给 certbot）
- [x] 3.3 443 端口 server block：启用 SSL + HTTP/2，证书路径 `/etc/letsencrypt/live/sth.sun-praise.com/{fullchain,privkey}.pem`，HSTS、X-Content-Type-Options、X-Frame-Options、Referrer-Policy
- [x] 3.4 `proxy_pass http://127.0.0.1:3939`（统一 location /）
- [x] 3.5 WebSocket 头转发：`proxy_http_version 1.1; proxy_set_header Upgrade $http_upgrade; proxy_set_header Connection $connection_upgrade;`（map 兜底 keep-alive）
- [x] 3.6 `proxy_read_timeout 86400s; proxy_send_timeout 86400s;`
- [x] 3.7 `client_max_body_size 128m`（对齐服务端 64 MiB 上传上限）
- [x] 3.8 验证：`nginx -t` 通过（用临时自签证书隔离语法检查）

## 4. 备份：脚本 + systemd 单元

- [x] 4.1 写入 `deploy/backup/sth-backup.sh`，幂等可重入，`set -euo pipefail`
- [x] 4.2 通过 label `com.docker.compose.service=app` 定位运行中的容器（Compose 自动注入该 label）；`STH_CONTAINER_NAME` 可显式覆盖；零或多匹配时 fail-loud
- [x] 4.3 调用 `docker exec` 在容器内跑 `sqlite3 /data/sessions.db ".backup '/backup/<UTC-date>/sessions.db'"`
- [x] 4.4 不修改 app 镜像：复用 app 镜像自带的 `sqlite3` CLI
- [x] 4.5 清理 `/backup` 内早于 14 天的目录（`find -mindepth 1 -maxdepth 1 -type d -mtime +14 -exec rm -rf {} +`）
- [x] 4.6 写入 `deploy/backup/sth-backup.service`（Type=oneshot）
- [x] 4.7 写入 `deploy/backup/sth-backup.timer`（OnCalendar=*-*-* 03:00:00, Persistent=true, AccuracySec=5min）
- [x] 4.8 验证：脚本针对 running 容器产出 integrity_check=ok 的备份；针对停止/不存在的容器正确返回非 0

## 5. CI 镜像构建

- [x] 5.1 写入 `.github/workflows/docker.yml`
- [x] 5.2 触发条件：`push: tags: ['v*']` 与 `push: branches: [main]`，附 `workflow_dispatch`
- [x] 5.3 permissions: `packages: write`, `contents: read`
- [x] 5.4 步骤：checkout → setup-qemu → setup-buildx → docker login ghcr → metadata → build-and-push
- [x] 5.5 平台：`linux/amd64,linux/arm64`
- [x] 5.6 tag 策略：`docker/metadata-action` 生成 semver (`1.4.0`, `1.4`, `1`)、branch (`main`)、`sha-<short>`、`latest`
- [x] 5.7 缓存：`type=gha,scope=docker` / `mode=max`
- [x] 5.8 build-args 注入 `VERSION`/`COMMIT`/`BUILD_DATE` 给 Dockerfile

## 6. 部署文档

- [x] 6.1 写入 `deploy/README.md`，含前置（docker、compose、loginctl、certbot 已有、DNS 记录）
- [x] 6.2 首次部署：clone repo 到 `/opt/sth`、`cp .env.example .env`、`docker compose pull`、`docker compose up -d`
- [x] 6.3 nginx：`ln -s /opt/sth/deploy/nginx/sth.sun-praise.com.conf /etc/nginx/sites-enabled/` + `nginx -t && systemctl reload nginx`
- [x] 6.4 备份：`ln -s .../sth-backup.sh ~/.local/bin/` + `cp sth-backup.{service,timer} ~/.config/systemd/user/` + `systemctl --user enable --now sth-backup.timer`
- [x] 6.5 升级 / 回滚 / 日志 / 恢复 / 故障排查清单
- [x] 6.6 主 `README.md` 添加 "Deployment" 章节指针

## 7. 验证

- [x] 7.1 `go build ./cmd/html-server` 成功（确保本工作区不破坏 Go 编译）
- [x] 7.2 `go vet ./...` 干净
- [x] 7.3 `go test ./...` 通过（4 包 ok，1 无测试）
- [x] 7.4 `docker compose config -q` 退出 0；env 覆盖验证通过
- [x] 7.5 `docker build .` 成功；OCI labels 全部填充（version/revision/created/source/...）
- [x] 7.6 容器运行 smoke test：HTTP 200、`/data/sessions.db` 由 uid 1000 创建、热备份 `integrity_check=ok`
- [x] 7.7 nginx 配置语法检查通过（临时自签证书隔离验证）
