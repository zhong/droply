# Droply

多用户、多子域名的静态内容发布平台。通过 CLI 快速发布静态网站，自动分配子域名并提供 HTTPS 访问。

[English](README.md)

## 架构

```text
CLI / Browser → Droply HTTP + HTTPS
                    ├─ api.example.com → authentication / deployment API
                    └─ site hosts      → access control / files / statistics
                                         ↓
                                    SQLite + disk
```

Droply 单进程提供 API、静态文件服务和 HTTPS，外部网关可选。所有站点使用同一个访问控制入口。

## 快速开始

### 安装 CLI

一键安装（自动检测操作系统和架构）：

```bash
curl -fsSL https://droplydoc.com/install.sh | bash
```

安装指定版本：

```bash
VERSION=v0.1.0 curl -fsSL https://droplydoc.com/install.sh | bash
```

<details>
<summary>其他安装方式</summary>

从 [最新 Release](https://github.com/zhong/droply/releases/latest) 下载预编译二进制文件：

| 平台 | 二进制文件 |
|------|-----------|
| macOS (Apple Silicon) | `droply-darwin-arm64` |
| macOS (Intel) | `droply-darwin-amd64` |
| Linux (x86_64) | `droply-linux-amd64` |
| Windows (x86_64) | `droply-windows-amd64.exe` |

```bash
# 示例：macOS Apple Silicon
curl -Lo droply https://github.com/zhong/droply/releases/latest/download/droply-darwin-arm64
chmod +x droply
sudo mv droply /usr/local/bin/
```

或者使用 Go 安装：

```bash
go install github.com/zhong/droply/cmd/droply@latest
```

</details>

### 从源码编译

```bash
git clone https://github.com/zhong/droply.git
cd droply
make build
```

生成两个二进制文件：
- `bin/droply-server` — 服务端
- `bin/droply` — CLI 客户端

#### 交叉编译所有平台

```bash
make build-all
```

在 `dist/` 目录下生成所有支持平台的二进制文件。

### 运行测试

```bash
make test
```

### 部署到服务器

在服务器上拉取最新代码、重新编译并重启服务：

```bash
make deploy
```

### 部署官网

项目官网 `droplydoc.com` 通过 droply 自身托管。更新官网：

```bash
cd website
droply deploy
```

## 服务端部署

新安装仅运行 `droply-server`，无需 Caddy。已有安装先阅读 [M0 迁移与恢复指南](docs/migration-m0.md)，不要直接覆盖服务文件。

### Linux 安装

下载脚本后将环境变量传给执行脚本的进程：

```bash
curl -fsSL https://droplydoc.com/setup.sh -o setup.sh
sudo env DOMAIN=example.com TLS_MODE=auto ACME_EMAIL=admin@example.com sh setup.sh
```

脚本安装发行版并强制核验 checksum，创建独立的 `droply` 用户和 systemd 服务；默认使用 80/443。先将根域名、`*.example.com` 和 `api.example.com` 指向服务器。脚本检查监听端口，遇到已有服务、环境文件或数据目录会停止，不会覆盖或卸载已有代理。

其他模式：

```bash
# Cloudflare DNS 通配符证书：令牌放在受保护的文件中
sudo env DOMAIN=example.com TLS_MODE=cloudflare \
  CF_TOKEN_FILE=/root/cloudflare-token ACME_EMAIL=admin@example.com sh setup.sh

# 自带证书，必须包含 api.example.com，站点域名也需被证书覆盖
sudo env DOMAIN=example.com TLS_MODE=manual \
  CERT_PATH=/root/cert.pem KEY_PATH=/root/key.pem sh setup.sh

# 接在已有网关后，默认仅监听 loopback；按实际网关网段设置可信代理
sudo env DOMAIN=example.com TLS_MODE=http HTTP_ADDR=127.0.0.1:8080 \
  TRUSTED_PROXIES=127.0.0.1/32 sh setup.sh

# 安装本地构建的服务器二进制，不访问发行下载服务
sudo env DOMAIN=example.com TLS_MODE=auto \
  LOCAL_BINARY="$PWD/bin/droply-server" sh setup.sh
```

`UPGRADE=1` 只备份并替换已有二进制，保留服务、环境、数据与证书，不自动重启。切换前按照迁移指南备份数据库和内容。`VERSION=vX.Y.Z` 可指定发行版；`DATA_DIR` 可指定新数据目录；`ACME_CA` 可使用 ACME 测试环境。

### 直接启动

```bash
# 单域名自动 HTTPS，证书存放于 data-dir/certificates
./bin/droply-server --domain example.com --data-dir ./data \
  --addr :80 --https-addr :443 --tls-mode auto --acme-email admin@example.com

# DNS 通配符 HTTPS
./bin/droply-server --domain example.com --data-dir ./data \
  --addr :80 --tls-mode cloudflare --cloudflare-token-file ./cloudflare-token

# 自带证书
./bin/droply-server --domain example.com --data-dir ./data \
  --addr :8080 --https-addr :8443 --tls-mode manual --tls-cert ./cert.pem --tls-key ./key.pem

# HTTP，由可选的已有网关终止 TLS
./bin/droply-server --domain example.com --data-dir ./data \
  --addr 127.0.0.1:8080 --tls-mode http --trusted-proxies 127.0.0.1/32
```

80/443 需要相应绑定权限；安装脚本通过 systemd 授予 `CAP_NET_BIND_SERVICE`。自动单域名模式需要公网 ACME 验证可达；Cloudflare DNS 模式需要 `Zone:DNS:Edit` 与 `Zone:Zone:Read` 权限，并覆盖实际签发域名所属的 zone。手动证书由管理员续期并重启服务加载。

| 参数 | 默认值 | 用途 |
|---|---|---|
| `--addr` | `:8080` | API 与站点统一 HTTP 入口，auto 通常设 `:80` |
| `--https-addr` | `:443` | HTTPS 入口 |
| `--tls-mode` | `http` | `http` / `manual` / `auto` / `cloudflare` |
| `--domain` | `droplydoc.com` | 基础域名 |
| `--data-dir` | `/data/droply` | 数据库、内容、持久化会话签名密钥 |
| `--cert-dir` | 数据目录下 `certificates` | ACME 账户与证书存储 |
| `--tls-cert`, `--tls-key` | 空 | 手动 PEM 证书与私钥 |
| `--acme-email`, `--acme-ca` | 空 / Let's Encrypt production | ACME 账户邮箱与服务地址 |
| `--cloudflare-token-file` | 空 | DNS API 令牌文件，也支持 `DROPLY_CLOUDFLARE_API_TOKEN` |
| `--trusted-proxies` | 空 | 可信代理 CIDR 列表；默认忽略转发 IP |
| `--hmac-secret` | 自动持久化 | 保留旧的显式会话签名密钥 |
| `--log-retention-days` | `30` | 访问明细保留天数 |

完整参数见 `droply-server --help`。正常 HTTP 退出最多等待 15 秒；进行中的 DNS 操作可能延迟退出至库超时（最长约 5 分钟），安装的服务预留 360 秒。`--site-addr` 暂时作为额外统一 HTTP 监听兼容旧服务，`--caddy-admin` 被忽略；新安装不应使用它们。`on-demand` 是 `auto` 的兼容名称。

```bash
sudo systemctl status droply
sudo journalctl -u droply -f
```

### 数据目录结构

```
/data/droply/
├── droply.db              SQLite 数据库
└── sites/
    ├── alice/
    │   ├── blog/          alice.droplydoc.com/blog
    │   └── portfolio/     alice.droplydoc.com/portfolio
    └── bob/
        └── docs/          bob.droplydoc.com/docs
```

## CLI 使用指南

### 安装

从 [GitHub Releases](https://github.com/zhong/droply/releases/latest) 下载（参见上方快速开始），或从源码编译 `make build`。

验证安装：

```bash
droply version
```

### 配置

CLI 配置文件位于 `~/.config/droply/config.toml`，支持多个 **context**（连接配置）—— 每个连接的 droply 服务器对应一个 context。

```toml
current_context = "default"

[contexts.default]
api_url = "https://api.droplydoc.com"
token = "dp_xxxxxxxxxxxx"

[contexts.staging]
api_url = "https://api.staging.example.com"
token = "dp_yyyyyyyyyyyy"
```

登录/注册时自动创建和更新此文件。旧的单服务器配置（顶层 `api_url` + `token`）会在首次使用时**静默迁移**为 `contexts.default`。

#### 同时使用多个服务器

```bash
# 显式指定 context 名登录自建服务器
droply auth login --api-url https://api.staging.example.com --context staging

# 或省略 --context，droply 自动从 URL 派生
droply auth login --api-url https://api.staging.example.com   # 派生为 context "example"

# 列出所有 context（* 标记当前激活的）
droply context list

# 切换服务器
droply context use staging

# 仅添加 context，暂不认证
droply context add corp --api-url https://api.corp.example.com

# 删除 context
droply context remove corp

# 临时覆盖单次操作（不持久化）
droply --context staging deploy
```

#### 项目级 context 绑定

项目目录下的 `.droply.toml` 可以绑定特定的 context：

```toml
context = "staging"
subdomain = "alice"
project = "blog"
```

在该目录下执行 `droply` 命令时自动使用 `staging` context。

**优先级**（高到低）：
1. 命令行 `--context X`
2. `.droply.toml` 中的 `context = "X"`
3. `~/.config/droply/config.toml` 中的 `current_context`

### 注册和登录

```bash
# 注册新账号
droply register
# 交互式输入 Email 和 Password

# 登录已有账号
droply login

# 查看当前登录状态
droply whoami

# 登出
droply logout
```

### 管理子域名

每个用户可以创建多个子域名，子域名名称要求：小写字母 + 数字 + 连字符，3-32 个字符。

```bash
# 创建子域名
droply subdomain create alice
# alice.droplydoc.com 现在可用

# 列出所有子域名
droply subdomain list

# 删除子域名（会同时删除其下所有项目）
droply subdomain delete alice
```

### 部署网站

```bash
# 部署当前目录到指定子域名和项目
droply deploy --sub alice --project blog

# 部署指定目录
droply deploy ./dist --sub alice --project blog

# 部署结果示例：
# Packaging ./dist...
# Deploying to alice.droplydoc.com/blog...
# Deployed! Version 1
# URL: https://alice.droplydoc.com/blog
```

#### 使用项目配置文件

在项目根目录创建 `.droply.toml`，后续部署无需每次指定参数：

```toml
subdomain = "alice"
project = "blog"
exclude_paths = ["dist/private", "public/secret.txt"]
exclude_files = ["draft.html", "robots-local.txt"]
```

```bash
# 有 .droply.toml 后，直接运行即可
droply deploy
```

`exclude_paths` 按项目根目录的精确相对路径匹配。命中目录时会排除整个目录，命中文件时只排除该文件。

`exclude_files` 按文件名精确匹配，会排除部署源目录中任意层级的同名文件。

#### 打包排除规则

部署时自动排除以下文件和目录：

- `.git`
- `node_modules`
- `__pycache__`
- `.DS_Store`
- `.env`
- 所有隐藏目录（以 `.` 开头）
- `.droply.toml` 中 `exclude_paths` 列出的精确路径
- `.droply.toml` 中 `exclude_files` 列出的精确文件名

#### 上传限制

单次部署最大 **50MB**。

### 管理项目

```bash
# 列出子域名下的项目
droply project list --sub alice

# 删除项目（同时删除所有文件和部署记录）
droply project delete blog --sub alice
```

### 自定义域名

```bash
# 为项目添加自定义域名
droply domain add blog.example.com --sub alice --project blog
# 输出 CNAME 记录目标，按提示在 DNS 中添加

# 验证 DNS 配置是否正确
droply domain verify blog.example.com --sub alice --project blog

# 查看自定义域名
droply domain list --sub alice --project blog

# 移除自定义域名
droply domain remove blog.example.com --sub alice --project blog
```

添加自定义域名后，在 DNS 服务商处添加 CNAME 或 A 记录指向输出的目标地址，然后运行 `droply domain verify` 确认。还需发布 CLI 输出的 `_droply-verification` 专属 TXT 记录，再重试验证；仅 A/CNAME 指向服务器不能证明所有权。Droply 只服务已验证绑定，并允许为其申请自动证书。

### 访问控制

为子域名或项目设置 IP 白名单和密码保护。支持两个粒度级别：子域名级别（所有项目共享）和项目级别（覆盖子域名规则）。

```bash
# 设置子域名级别访问控制：IP 白名单 + 自动生成密码
droply access set --subdomain alice --ip 10.0.0.0/8 --password auto --expire 24h
# 输出：https://alice.droplydoc.com | Password: a1b2c3d4e5f6g7h8 | IP: 10.0.0.0/8 | Expires: 1d

# 设置永不过期的密码
droply access set --subdomain alice --password auto --expire never
# 输出：https://alice.droplydoc.com | Password: xYz123AbCdEf9876 | Expires: never

# 设置项目级别访问控制（覆盖子域名规则）
droply access set --subdomain alice --project blog --password "my-secret" --expire 7d
# 输出：https://alice.droplydoc.com/blog | Password: my-secret | Expires: 7d

# 查看访问控制规则
droply access get --subdomain alice
droply access get --subdomain alice --project blog

# 移除访问控制
droply access remove --subdomain alice
droply access remove --subdomain alice --project blog
```

设置访问控制后，会输出一行便于复制分享的信息，包含访问地址、密码、IP 限制和过期时间，可以直接粘贴到聊天或邮件中。

#### 访问控制参数

| 参数 | 说明 |
|------|------|
| `--subdomain` | 子域名名称（必填） |
| `--project` | 项目名称（可选，不指定则操作子域名级别） |
| `--ip` | 允许的 IP 或 CIDR（可重复指定多个） |
| `--password` | 密码（`auto` 自动生成，或指定自定义密码，最少 8 位） |
| `--wework` | 启用企业微信扫码登录（允许任何企业成员） |
| `--wework-user` | 仅允许指定的企业微信 user_id；可重复；隐式启用 `--wework` |
| `--expire` | 会话过期时间（如 `1h`、`24h`、`7d`、`never`，默认 `24h`） |

#### 工作原理

- **IP 白名单**：只有来自指定 IP/子网的请求才能访问
- **密码保护**：访问者需要在登录页输入密码，通过后设置 cookie 保持会话
- **企业微信扫码**：访问者用企业微信扫码登录，cookie 与 user_id 和白名单绑定
- **组合使用**：同时配置多种方式时，**任一通过即可访问**（OR 逻辑）。IP 先校验；未命中则展示登录页，按配置显示密码框和/或扫码按钮
- **规则优先级**：项目级规则完全覆盖子域名级规则

所有站点（包括自定义域名）经过 Droply 统一访问控制。受保护响应使用 `Cache-Control: private, no-store`；项目规则覆盖子域会话。

### 企业微信扫码登录

droply 支持企业微信扫码登录作为第三种访问控制方式（与 IP 白名单、密码并列）。访问者点击 "使用企业微信登录" 按钮，用企业微信 App 扫码，如果其 user_id 在白名单中即可访问。

**快速开始：**

```bash
# 服务端：配置企业微信 OAuth（在 /etc/systemd/system/droply.service 中）
Environment="DROPLY_WEWORK_CORP_ID=ww1234567890abcdef"
Environment="DROPLY_WEWORK_AGENT_ID=1000002"
Environment="DROPLY_WEWORK_SECRET=xxx"
Environment="DROPLY_WEWORK_REDIRECT_URI=https://login.example.com/_droply/wework/callback"

# CLI：为项目启用扫码登录（允许任何企业成员）
droply access set --subdomain alice --project docs --wework

# 或限定指定 user_id
droply access set --subdomain alice --project docs \
  --wework-user zhangsan --wework-user lisi
```

📖 **完整配置指南**：[docs/wework-zh-CN.md](docs/wework-zh-CN.md) 涵盖创建企业微信自建应用、配置可信域名、OAuth 校验文件、故障排查、已知限制。

## API

所有 API 通过 `api.droplydoc.com` 访问，JSON 格式。认证使用 `Authorization: Bearer <token>` 头。

| 方法 | 路径 | 说明 |
|------|------|------|
| POST | `/auth/register` | 注册 |
| POST | `/auth/login` | 登录 |
| POST | `/subdomains` | 创建子域名 |
| GET | `/subdomains` | 列出子域名 |
| DELETE | `/subdomains/:name` | 删除子域名 |
| GET | `/subdomains/:sub/projects` | 列出项目 |
| DELETE | `/subdomains/:sub/projects/:name` | 删除项目 |
| POST | `/subdomains/:sub/projects/:name/deploy` | 部署（multipart） |
| GET | `/subdomains/:sub/projects/:name/deployments` | 部署历史 |
| POST | `/subdomains/:sub/projects/:name/domains` | 添加自定义域名 |
| GET | `/subdomains/:sub/projects/:name/domains` | 列出自定义域名 |
| DELETE | `/subdomains/:sub/projects/:name/domains/:domain` | 删除自定义域名 |
| POST | `/subdomains/:sub/projects/:name/domains/:domain/verify` | 验证自定义域名 DNS |
| PUT | `/subdomains/:sub/access` | 设置子域名访问控制 |
| GET | `/subdomains/:sub/access` | 查看子域名访问控制 |
| DELETE | `/subdomains/:sub/access` | 移除子域名访问控制 |
| PUT | `/subdomains/:sub/projects/:name/access` | 设置项目访问控制 |
| GET | `/subdomains/:sub/projects/:name/access` | 查看项目访问控制 |
| DELETE | `/subdomains/:sub/projects/:name/access` | 移除项目访问控制 |

## 技术栈

| 组件 | 技术 |
|------|------|
| 语言 | Go |
| HTTP 路由 | [chi](https://github.com/go-chi/chi) |
| CLI 框架 | [cobra](https://github.com/spf13/cobra) |
| 数据库 | SQLite ([modernc.org/sqlite](https://pkg.go.dev/modernc.org/sqlite)) |
| 密码哈希 | bcrypt |
| Cookie 签名 | HMAC-SHA256 |
| 限流 | golang.org/x/time/rate |
| 配置文件 | TOML |
| HTTP/HTTPS | Go net/http + lego/ACME |

## License

MIT
