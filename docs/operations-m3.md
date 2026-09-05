# 私有部署与运维（M3）

Droply 是单机、小团队的静态 Pages 发布服务：上传已构建目录，提供根路径项目域名、版本、预览、协作权限和 HTTPS。一个 Go 服务管理 SQLite 与本地磁盘，不需要 Caddy。部署机器的磁盘、DNS、备份及容量由管理员维护；目前没有分布式副本、全球 CDN 或服务器端 Functions。

## 新安装

配置 `api.example.com`、`*.example.com` 与需要使用的基域名指向服务地址。自定义域名须在项目中添加，并按 API/CLI 返回的 DNS TXT 挑战验证；不接受任意未知 Host 的签发请求。生产建议 Linux systemd；CLI 另有 macOS/Windows 构建。

使用仓库的 `scripts/setup.sh` 或 README 中的发行安装脚本安装。新安装使用独立 `droply` 系统用户；不要把不同安装指向同一个 data-dir。HTTP 与 HTTPS 上的 API 和站点由 Host 分流，API 主机为 `api.<domain>`。多个实例不能同时打开同一安装；启动、管理员初始化和离线备份共用排他锁。

账户注册默认封闭，环境变量 `DROPLY_OPEN_REGISTRATION=true` 或 `--open-registration` 才显式开放。首次初始化前停服：

```sh
sudo systemctl stop droply
sudo /usr/local/bin/droply-server init-admin --data-dir /data/droply \
  --email admin@example.com --password-file /secure/initial-password
sudo chown -R droply:droply /data/droply
sudo systemctl start droply
```

密码文件需为 0600，内容 8–72 字节，可带结尾换行。初始化完成后安全移除临时密码文件。`chown` 仅适用于安装器创建的 `droply` 用户；自定义服务使用实际用户。旧安装没有管理员时，可用 `claim-admin --data-dir /data/droply --email existing@example.com` 明确选定已有账户，仍须停服且维持数据库所有权。已有管理员时拒绝重复初始化或认领；这些命令不重置密码。

从工作站登录 `droply login --api-url https://api.example.com`。管理员 `droply invitation create colleague@example.com` 创建单次、绑定邮箱的邀请；受邀者通过 `DROPLY_INVITE` 环境变量提交令牌并运行 `droply register --api-url https://api.example.com`。令牌无需放入 URL。详见[账户说明](identity-m3.md)。

## TLS 与代理选项

裸二进制默认 `--tls-mode http --addr :8080`，不要误认为默认已经对外提供 HTTPS。全部模式都可使用同一访问规则。

| 模式 | 入口和依赖 | 证书维护 |
|---|---|---|
| `http` | `--addr`，通常只监听可信代理可达地址 | TLS 由外部网关管理 |
| `manual` | `--https-addr` + `--tls-cert`/`--tls-key` | 管理员续期并重启加载 |
| `auto` | `--addr :80 --https-addr :443`，公开可达 HTTP-01 | 内置 ACME 签发、持久化、后台续期 |
| `cloudflare` | `--https-addr`，Cloudflare DNS API 凭证及 DNS 可达 | 平台通配符及受授权域名使用 DNS-01，后台续期 |

```sh
# 公网 HTTP-01；必须让 CA 从端口 80 访问挑战
./bin/droply-server --domain example.com --data-dir /data/droply \
  --tls-mode auto --addr :80 --https-addr :443 --acme-email admin@example.com

# DNS-01；凭证文件应可由服务用户读取且限制为该用户
./bin/droply-server --domain example.com --data-dir /data/droply \
  --tls-mode cloudflare --addr :80 --https-addr :443 \
  --acme-email admin@example.com --cloudflare-token-file /etc/droply/cloudflare-token

# 手动证书，需覆盖 API 及实际提供服务的站点名称
./bin/droply-server --domain example.com --data-dir /data/droply \
  --tls-mode manual --addr :8080 --https-addr :8443 \
  --tls-cert /etc/droply/tls/fullchain.pem --tls-key /etc/droply/tls/key.pem

# 已有网关；只信任真实代理来源网段
./bin/droply-server --domain example.com --data-dir /data/droply \
  --tls-mode http --addr 127.0.0.1:8080 --trusted-proxies 127.0.0.1/32
```

`--acme-email` 默认可来自 `DROPLY_ACME_EMAIL`；`--acme-ca` 指定 ACME directory，留空使用 Let's Encrypt 生产环境。测试 CA 的证书不会被普通浏览器信任。`--cert-dir` 默认是 `<data-dir>/certificates`；外部目录必须纳入备份。Cloudflare 令牌也可通过 `DROPLY_CLOUDFLARE_API_TOKEN` 提供，限制到所需 zone 的 `Zone:DNS:Edit`、`Zone:Zone:Read`。自定义域名的 DNS zone 也须能由凭证管理。DNS wildcard 不等于允许任意 Host：每个站点仍检查授权。

手动证书启动时验证 API 名称、私钥和有效期；配置还须覆盖实际站点域名。HTTP-only 模式的内置控制台登录仍要求浏览器访问 HTTPS 管理域名，可由可信代理终止 TLS。代理保留原始 Host，并正确设置 `X-Forwarded-Proto`；没有 `--trusted-proxies` 时忽略非可信来源的转发头。不要把整个互联网列为可信代理。

`--hmac-secret` 用于维持已有显式会话密钥；未指定时创建并持久化 `<data-dir>/hmac.key`。不能丢失此文件，否则旧访客会话和控制台 CSRF 状态无法连续。`--site-addr` 仅作额外统一 HTTP 入口兼容，`--caddy-admin` 已忽略，`on-demand` 是 `auto` 的兼容名称；新安装使用上述正式参数。

## 控制台、协作与审计

访问 `https://api.example.com/console/`。界面随服务端内嵌，显示获授权项目、版本、域名、访问统计和审计；发布、回滚与访问设置仍由服务端鉴权。控制台会话与站点访问 Cookie 隔离，不把长期 API token 写入浏览器存储。

```sh
droply projects
droply member list --sub alice --project blog
droply member set colleague@example.com --role deployer --sub alice --project blog
droply member remove 2 --sub alice --project blog
droply audit --sub alice --project blog --limit 50
droply audit --admin --limit 100
```

owner 管理成员、域名和访问规则；deployer 发布、回滚、管理自己签发的项目 token；viewer 只读。移除/降级成员撤销其相应项目 token，不改变其他项目。项目权限不是访客保护：未设置访客规则的生产及预览仍公开。

审计记录 actor、项目/目标、动作、时间和结果，不保存密码、完整 token、Cookie、证书私钥或请求正文。敏感操作先写持久化 pending 记录，写失败则不开始操作；若进程中断或最终写失败，pending 表示结果未确定，应结合部署历史确认。`--audit-retention-days` 默认 90，必须为正；`droply audit --before <next_cursor>` 翻页。详见[控制台说明](console-m3.md)及[审计说明](audit-m3.md)。

## 完整备份与恢复

停止服务及其他写入配置/证书的程序。备份命令持有安装锁，服务器仍在运行时拒绝执行。SQLite 使用 `VACUUM INTO` 包括已提交 WAL 内容；不能简单复制正在运行的数据库文件。

```sh
sudo systemctl stop droply
sudo ./bin/droply-server backup --data-dir /data/droply \
  --output /backups/droply-before-upgrade.tar.gz \
  --config /etc/systemd/system/droply.service \
  --config /etc/droply/env \
  --include /etc/droply/certificates
sudo systemctl start droply
```

首次从 M0/M1/M2 升级时，已安装的旧二进制没有 `backup` 子命令。上面的 `./bin/droply-server` 必须是已构建或已下载校验的 **M3 新二进制**；停服后先用它对旧 data-dir 执行 backup，然后才替换已安装二进制和启动新服务。backup 只读打开源 SQLite 并创建快照，不调用服务数据库构造器，不迁移旧库。

根据实际安装填写配置路径，删除不存在的示例路径。`--config` 至少一项，可重复；额外 PEM、外部 cert-dir、配置引用的密钥文件用 `--include` 明确收集。显式 HMAC 覆盖需另传 `--hmac-key-file` 精确字节。压缩包包括敏感配置和私钥，使用私有加密备份存储。

```sh
sudo /usr/local/bin/droply-server restore \
  --input /backups/droply-before-upgrade.tar.gz --data-dir /data/droply-restored
sudo chown -R droply:droply /data/droply-restored
```

只能恢复到不存在的新目录；损坏、路径穿越、链接、未知格式版本或不支持的数据库版本会拒绝发布。外部材料和路径映射保存在 `.restore/<restore-id>/`，不会写回旧绝对路径。按清单修改服务的 data-dir、cert-dir 与手动 PEM 路径，再于隔离机器/测试入口演练。默认解压上限 64 GiB/100000 条目，可调整；预留至少解压量两倍临时空间。完整说明见[备份恢复](backup-m3.md)。

## 升级与降级

1. 先构建/下载并校验 M3 新二进制，但不要启动它的服务模式。保留当前旧二进制、服务配置与凭证位置，停止服务；使用 **M3 新二进制的 backup 子命令** 执行上述完整备份（旧版没有此命令）。不要让新旧进程共用 data-dir。
2. 使用已验证的新二进制。安装器 `UPGRADE=1` 只备份并替换二进制，不重启或覆盖服务和数据；`make deploy` 不再自动执行未备份的升级。按当前 `--help` 核对参数。
3. 初次打开已有的旧版本数据库（`user_version=0`）时，新服务在数据库同级 `upgrade-backups/schema-0-before-3-<随机>.db` 创建同步落盘的 SQLite 快照，再执行迁移。快照失败则拒绝迁移；全部迁移成功后才写 `user_version=3`。失败重试会保留每次快照，版本 3 的正常重启不重复生成。
4. 启动后检查下面的验收项，再切换流量。迁移前数据库快照只有数据库，不含 artifacts、HMAC、证书或外部配置，不能替代完整备份。
5. 若要降级，先停止新服务并保留故障现场。用支持该备份格式的新二进制将升级前完整备份恢复到另一新目录，确认其中数据库为旧二进制支持的版本，再用保存的旧二进制及旧配置启动该目录。更新外部路径和目录所有权，验收后切换流量。不要把旧二进制直接指向已经迁移的数据库。

新服务拒绝高于支持上限的数据库版本；早期二进制未必有此保护，因此不能依赖旧程序主动拒绝新库。需要恢复到升级前的真实时间点；升级后新增部署或账户不会自动合并回旧库。仅在已确认所有内容、密钥与配置未变化的专门恢复流程中，才考虑使用数据库单独快照。

现有 artifact manifest 格式为版本 1，M3 不转换已存在的 v1 产物。首次从旧目录布局进入版本化存储时，M1 的恢复流程会幂等迁移当前有效内容并保留原 legacy 目录；重启不会重复生成已提交的迁移。历史记录若没有实际保存的内容，不能凭空恢复成可回滚版本。详见[M1 迁移](migration-m1.md)。

## 磁盘、证书与健康排查

```sh
sudo systemctl status droply
sudo journalctl -u droply --since '30 minutes ago'
curl --fail --silent --show-error https://api.example.com/healthz
df -h /data/droply
du -sh /data/droply/sites/.artifacts /data/droply/certificates
# 使用已经登录的管理员或有项目读取权限的账号
droply certificate api.example.com
droply certificate blog.example.com
droply deployment list --sub alice --project blog --json
droply deployment cleanup --sub alice --project blog --keep 10 --days 30
```

- 无法启动：检查锁占用、端口冲突、配置路径权限、磁盘空间，以及数据库版本/快照错误。不要删除活跃进程的锁文件来绕过锁。
- 上传失败：HTTP 压缩请求上限 50 MiB；默认解包 256 MiB、10000 个条目。`--artifact-max-bytes` 默认 0（仅受磁盘容量限制），包含管理的 artifact 与 staging；空间不足或配额失败不会替换生产。先查询历史，上传不会自动安全重试。
- 回收空间：先运行 cleanup 预览，核对后才追加 `--apply`。默认保护最新 10 次成功部署、30 天内成功部署及生产/引用中的产物。旧 staging 和孤儿按默认 1 小时宽限期回收。不要直接删除 `.artifacts`、manifest 或正在使用的版本。
- 证书状态：`pending` 常见于尚未首次签发；`ready` 检查 `expires_at`；`error`/`expired` 结合脱敏 `last_error`、`retry_at`、时钟、DNS、CA 可达性、端口 80、凭证权限及 cert-dir 可写性排查。失败按退避重试，不要高频重启触发 CA 限流。
- `externally-managed` 表示没有内置 ACME 管理器（HTTP 或 manual 模式），不等于手动证书健康。用浏览器或组织现有 TLS 监测检查域名匹配与到期时间；手动续期后重启服务。
- `/healthz` 位于管理 Host，无需凭证，数据库能执行查询时返回 `200 {"status":"ok"}`，失败返回 503；它不验证磁盘可写、证书到期或所有站点内容，须结合这些检查。
- HTTP 请求必须使用正确 Host。直接请求服务器 IP 得到 404 通常是域名授权分流，不代表进程死亡。控制台需 HTTPS 管理域名；诊断不要使用 `curl -k` 掩盖生产 TLS 问题。

## 验收

```sh
make build
make build-all
make test
make test-integration
go vet ./...
go test -race -tags=integration ./internal/server ./internal/store ./internal/backup
make test-acme
```

ACME 测试会使用本地 Pebble；具体依赖见[HTTPS 测试说明](testing-m0.md)。真实浏览器验收命令见[控制台说明](console-m3.md#验证)。

部署后的验收至少包括：API 账户登录及封闭注册、获授权项目列表、生产 GET/HEAD、预览不改变生产、版本回滚、viewer 写入被拒、撤销成员凭证失效、密码/IP/企业微信规则、受信 TLS 及域名状态、一次新目录完整恢复后 HTTP/TLS 与回滚。使用真实已部署的站点路径；空安装没有生产页。审计中的失败与 pending 应能追溯解释，再开放团队使用。
