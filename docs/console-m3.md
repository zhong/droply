# 内嵌控制台（M3）

访问 `https://api.<base-domain>/console/`，用团队账户邮箱与密码登录。控制台资源随 Go 二进制内嵌，无前端构建进程或外部 CDN。必须通过 HTTPS 访问；已有可信代理配置可用于代理终止 TLS。

控制台显示当前账户获授权的项目、角色、部署、域名、访问统计和最近 50 条审计记录。viewer 只读；deployer 和 owner 可将可用预览版本发布为生产，或回滚到可用历史生产版本。操作前确认框明确项目和版本；成功后重新读取服务端状态。

owner 可设置或删除项目的 IP/CIDR、密码、会话时长规则。保存是完整替换，旧密码不会回填；保留密码保护必须重新输入。已有企业微信规则的编辑仍通过 CLI 完成，避免表单遗漏字段。删除项目规则后可能恢复子域名继承规则，而不一定公开。服务端始终再次检查权限。失败和会话过期在界面中显示，审计记录可核对成功与失败结果。

会话最长 8 小时，SQLite 仅存随机凭证的 SHA-256 摘要。凭证通过 host-only `__Host-droply_console` Cookie 传递，设置 Secure、HttpOnly、SameSite=Strict，登出立即撤销。管理 Cookie 不认证部署站点。每次 Cookie 认证写操作同时验证 HTTPS 管理 Origin 和独立 CSRF 值；CLI Bearer 认证继续可用。长期 API token 不出现在控制台响应、HTML 或浏览器存储中。重启保留未过期会话，需同时保持数据库和服务器 HMAC key。

## 验证

Go 集成测试使用真实 SQLite 与部署文件：

```sh
go test -tags=integration -race ./internal/server ./internal/store -run '^TestConsole' -count=1
```

浏览器验收依赖仅安装到临时目录，运行 Chromium 对真实 TLS HTTP 服务的登录、权限隔离、预览发布、回滚、访问规则、审计、失败、登出与过期流程；无需外部服务：

```sh
npm install --prefix /tmp/droply-console-browser-deps playwright@1.61.0
/tmp/droply-console-browser-deps/node_modules/.bin/playwright install chromium
DROPLY_CONSOLE_BROWSER=1 \
DROPLY_PLAYWRIGHT_PATH=/tmp/droply-console-browser-deps/node_modules/playwright/index.mjs \
DROPLY_CONSOLE_SCREENSHOT=/tmp/droply-console.png \
go test -tags=integration -race ./internal/server -run '^TestConsoleBrowser$' -count=1 -v
```

测试脚本为 `scripts/console_test.mjs`。测试域名使用保留的 `.localhost`，仅测试客户端忽略 fixture 的自签名证书。生产浏览器不会忽略 TLS 错误。
