## 1. Server Core Changes

- [ ] 1.1 修改 `DefaultHost` 常量从 `"127.0.0.1"` 为 `"0.0.0.0"`
- [ ] 1.2 新增 `Origins()` 方法，遍历网络接口返回所有可访问 URL 列表
- [ ] 1.3 修改 `Origin()` 在绑定 `0.0.0.0` 时返回 `127.0.0.1` 地址

## 2. CLI Output

- [ ] 2.1 修改 `runStart()` 输出，展示多个访问地址（每行一个 URL）

## 3. Script Updates

- [ ] 3.1 更新 `skills/static-html-preview/scripts/start-server.sh` 默认 host 为 `0.0.0.0`

## 4. Verification

- [ ] 4.1 测试默认启动（无 `--host`）绑定 `0.0.0.0` 并展示多地址
- [ ] 4.2 测试指定 `--host` 时只展示单地址
- [ ] 4.3 验证 `localhost` 和 `127.0.0.1` 均可访问
