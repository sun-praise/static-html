## 1. CLI: `--server-port` flag

- [x] 1.1 `runStart()` 解析 `--server-port`，校验为 1-65535 的整数，否则报错退出
- [x] 1.2 `printUsage()` 在 `start` 行追加 `[--server-port <n>]`
- [x] 1.3 把 `serverPort` 传入 `server.New()`
- [x] 1.4 `strconv` import

## 2. Server: 端口解析优先级

- [x] 2.1 `Server` 结构体加 `serverPort int` 字段（0 表示未设）
- [x] 2.2 `New()` 签名加 `serverPort int` 参数 + 范围校验
- [x] 2.3 `serverBaseURL()` 端口选择逻辑改为：`flag > X-Forwarded-Port header > s.port`
- [x] 2.4 `X-Forwarded-Port` 解析失败（非数字/越界）静默 fallback，不报错
- [x] 2.5 端口省略规则（80/443）用解析后的外部端口判断，不是监听端口（抽出 `resolveExternalPort` helper）

## 3. nginx 模板

- [x] 3.1 `deploy/nginx/example.conf` 加 `proxy_set_header X-Forwarded-Port 443;`

## 4. 测试

- [x] 4.1 `server_test.go`：反代场景（header `X-Forwarded-Port: 443`）生成无端口后缀的 https URL
- [x] 4.2 `server_test.go`：`--server-port` flag 覆盖 header 和监听端口
- [x] 4.3 `server_test.go`：无 flag 无 header 时回退到监听端口（行为不变）
- [x] 4.4 `server_test.go`：非法 `X-Forwarded-Port` 静默 fallback（含 abc/0/-1/70000/空 5 case）
- [x] 4.5 `cli_test.go`：`--server-port foo` / `0` / `-1` / `70000` 报错退出

## 5. 文档

- [x] 5.1 `deploy/README.md` 加「URL generation behind a reverse proxy」小节 + troubleshooting 行；nginx 模板自带 `X-Forwarded-Port 443`，主 README 无需额外说明（README 无 Deployment 段）

## 6. 验证

- [x] 6.1 `go vet ./...` 干净
- [x] 6.2 `go test ./...` 全过（4 包 ok）
- [ ] 6.3 手动 e2e：jd 实例 nginx 加 header 后，`sth send` 返回 `https://sth.sun-praise.com/s/<id>/`（无 `:3939`）—— 需先合并 + 发版 + 部署
