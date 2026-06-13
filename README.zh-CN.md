# Droply

多用户、多子域名的静态内容发布平台。通过 CLI 快速发布静态网站，自动分配子域名并提供 HTTPS 访问。

[English](README.md)

## 架构

```
CLI (droply)                         Browser
     |                                  |
     | upload tar.gz                    | HTTPS
     v                                  v
+-------------------------------------------------+
|                Caddy (443/80)                    |
|          Auto HTTPS + Wildcard TLS               |
+-------------------+-----------------------------+
| api.droplydoc.com |  *.droplydoc.com            |
| reverse_proxy     |  file_server / reverse_proxy|
|    :8080          |  :8081 (受保护站点)          |
+--------+----------+-----------------------------+
         |
         v
+------------------+    +-----------------+
|  droply-server   |--->|     SQLite      |
|  API :8080       |    |   droply.db     |
|  Site :8081      |    +-----------------+
+------------------+
```

- **Caddy** — TLS 终止、自动 HTTPS（通配符 + 自定义域名）、API 反代、静态文件服务、受保护站点反代
- **droply-server** — 用户认证、上传处理、元数据管理、访问控制、通过 Caddy Admin API 动态更新路由
- **droply** — CLI 客户端，打包目录并上传

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

### 一键部署

在全新 VPS（Ubuntu/Debian）上一键部署完整的 droply 服务端：

```bash
curl -fsSL https://droplydoc.com/setup.sh | sudo bash
```

脚本会提示你选择 **TLS 模式**：

| 模式 | 适用场景 | 要求 |
|------|----------|------|
| **on-demand**（默认） | 大多数用户、新增子域 <50 张/周 | 80 + 443 端口可达、A 记录指向服务器 |
| **cloudflare** | 大量子域，或 80 端口不可用 | Cloudflare API Token |
| **manual** | 企业 PKI、自建 CA、隔离环境 | 自带证书文件 |

**on-demand 模式**（推荐）**无需任何 DNS API 配置** —— 只要把 A 记录指向服务器即可，无论 DNS 在哪家服务商（阿里云/腾讯云/GoDaddy/公司 DNS）。Caddy 在每个子域名首次访问时通过 HTTP-01 challenge 自动签发独立证书（首次访问延迟 2-5 秒，之后立即返回）。

非交互式部署：

```bash
# on-demand 模式（默认）
DOMAIN=example.com TLS_MODE=on-demand curl -fsSL https://droplydoc.com/setup.sh | sudo bash

# cloudflare 模式（通配符证书）
DOMAIN=example.com TLS_MODE=cloudflare CF_API_TOKEN=xxx curl -fsSL https://droplydoc.com/setup.sh | sudo bash

# manual 模式（自带证书）
DOMAIN=example.com TLS_MODE=manual CERT_PATH=/path/to/cert.pem KEY_PATH=/path/to/key.pem \
  curl -fsSL https://droplydoc.com/setup.sh | sudo bash
```

### TLS 模式对比

```
┌──────────────────┬──────────────┬────────────────┬─────────────────┐
│                  │ On-Demand    │ Cloudflare     │ Manual          │
├──────────────────┼──────────────┼────────────────┼─────────────────┤
│ 需要 DNS API     │ 否           │ 是             │ 否              │
│ 需要 80 端口     │ 是           │ 否             │ 否              │
│ 证书类型         │ 单子域名     │ 通配符         │ 用户自带        │
│ 首次访问延迟     │ 2-5 秒       │ 无             │ 无              │
│ LE 限流影响      │ 50/周/注册域 │ 不受影响       │ 不适用          │
│ 子域规模         │ 数百         │ 无限           │ 受证书限制      │
└──────────────────┴──────────────┴────────────────┴─────────────────┘
```

**如何选择：**

- **on-demand**：大多数部署的默认选择。和任何 DNS 服务商兼容（无需 API 集成），只需配置 A 记录即可使用。
- **cloudflare**：有大量子域（数百以上）或需要关闭 80 端口（如企业防火墙限制）时使用。需要 Cloudflare DNS 和 API Token。
- **manual**：使用企业内部 PKI、自定义证书颁发机构或隔离网络环境。

<details>
<summary>手动部署</summary>

### 前置条件

- 一台 VPS（推荐 Ubuntu/Debian）
- 一个域名（如 `droplydoc.com`），DNS 已配置：
  - `A` 记录：`droplydoc.com` → 服务器 IP
  - `A` 记录：`*.droplydoc.com` → 服务器 IP
  - `A` 记录：`api.droplydoc.com` → 服务器 IP
- 安装 [Caddy](https://caddyserver.com/docs/install)

### 1. 安装 Caddy

**on-demand 或 manual 模式**（大多数用户）：

```bash
curl -fsSL "https://caddyserver.com/api/download?os=linux&arch=amd64" -o /tmp/caddy
sudo mv /tmp/caddy /usr/bin/caddy
sudo chmod +x /usr/bin/caddy
```

**cloudflare 模式**（通配符证书，需要 DNS-01）：

```bash
# 如未安装 Go
curl -fsSL https://go.dev/dl/go1.24.1.linux-amd64.tar.gz | sudo tar -C /usr/local -xz
export PATH="/usr/local/go/bin:$PATH"

# 安装 xcaddy 并编译带 Cloudflare DNS 模块的 Caddy
go install github.com/caddyserver/xcaddy/cmd/xcaddy@latest
~/go/bin/xcaddy build --with github.com/caddy-dns/cloudflare
sudo mv caddy /usr/bin/caddy
```

### 2. TLS 配置

从下面三种模式中选择一种。

#### 方案 A：On-Demand TLS（推荐）

Caddy 在子域名首次访问时自动签发证书。需要 80 + 443 端口可达。

创建 `/etc/caddy/Caddyfile`：

```caddyfile
{
    admin localhost:2019
    on_demand_tls {
        ask http://localhost:8080/_droply/tls-check
    }
}

*.droplydoc.com, droplydoc.com {
    tls {
        on_demand
    }
    reverse_proxy localhost:8081
}

api.droplydoc.com {
    tls {
        on_demand
    }
    reverse_proxy localhost:8080
}
```

#### 方案 B：Cloudflare DNS（通配符证书）

一张通配符证书覆盖所有子域。需要 Cloudflare API Token。

1. 在 [dash.cloudflare.com/profile/api-tokens](https://dash.cloudflare.com/profile/api-tokens) 创建 API Token
   - **Permissions**：Zone → DNS → Edit
   - **Zone Resources**：Include → Specific zone → `droplydoc.com`

2. 保存 Token：

```bash
sudo tee /etc/caddy/env > /dev/null << 'EOF'
CLOUDFLARE_API_TOKEN=你的-token
EOF
sudo chmod 600 /etc/caddy/env
```

3. 创建 `/etc/caddy/Caddyfile`：

```caddyfile
{
    admin localhost:2019
}

*.droplydoc.com {
    tls {
        dns cloudflare {env.CLOUDFLARE_API_TOKEN}
    }
    reverse_proxy localhost:8081
}

api.droplydoc.com {
    reverse_proxy localhost:8080
}
```

#### 方案 C：Manual（自带证书）

使用自己的证书文件（例如来自企业 PKI）。

```bash
# 将证书和私钥复制到 /etc/caddy/
sudo cp /path/to/cert.pem /etc/caddy/cert.pem
sudo cp /path/to/key.pem /etc/caddy/key.pem
sudo chmod 600 /etc/caddy/key.pem
```

创建 `/etc/caddy/Caddyfile`：

```caddyfile
{
    admin localhost:2019
}

*.droplydoc.com {
    tls /etc/caddy/cert.pem /etc/caddy/key.pem
    reverse_proxy localhost:8081
}

api.droplydoc.com {
    tls /etc/caddy/cert.pem /etc/caddy/key.pem
    reverse_proxy localhost:8080
}
```

### 3. 部署 droply-server

```bash
# 创建数据目录
sudo mkdir -p /data/droply/sites

# 下载最新版本
VERSION=$(curl -fsSL https://api.github.com/repos/zhong/droply/releases/latest | grep '"tag_name"' | sed -E 's/.*"([^"]+)".*/\1/')
curl -fsSL -o /tmp/droply-server "https://github.com/zhong/droply/releases/download/${VERSION}/droply-server-linux-amd64"
sudo mv /tmp/droply-server /usr/local/bin/droply-server
sudo chmod +x /usr/local/bin/droply-server

# 创建 systemd 服务
sudo tee /etc/systemd/system/droply.service > /dev/null << 'EOF'
[Unit]
Description=Droply Static Publishing Server
After=network.target caddy.service

[Service]
ExecStart=/usr/local/bin/droply-server \
  --addr :8080 \
  --site-addr :8081 \
  --data-dir /data/droply \
  --domain droplydoc.com \
  --caddy-admin http://localhost:2019
Restart=always
User=www-data

[Install]
WantedBy=multi-user.target
EOF

sudo systemctl daemon-reload
sudo systemctl enable --now droply
```

#### 服务端启动参数

| 参数 | 默认值 | 说明 |
|------|--------|------|
| `--addr` | `:8080` | API 监听地址 |
| `--site-addr` | `:8081` | 站点服务监听地址（受保护站点） |
| `--data-dir` | `/data/droply` | 数据目录（数据库 + 静态文件） |
| `--domain` | `droplydoc.com` | 基础域名 |
| `--caddy-admin` | `http://localhost:2019` | Caddy Admin API 地址 |
| `--hmac-secret` | （自动生成） | Cookie 签名密钥（留空则自动生成并持久化到 `hmac.key`） |
| `--wework-corp-id` | | 企业微信 Corp ID（可选，扫码登录） |
| `--wework-agent-id` | | 企业微信 Agent ID（可选） |
| `--wework-secret` | | 企业微信 Agent Secret（可选） |
| `--wework-redirect-uri` | | 企业微信 OAuth 回调 URL（可选） |

### 4. 启动 Caddy

```bash
# 创建 Caddy systemd 服务
sudo tee /etc/systemd/system/caddy.service > /dev/null << 'EOF'
[Unit]
Description=Caddy
After=network.target network-online.target
Requires=network-online.target

[Service]
Type=notify
ExecStart=/usr/bin/caddy run --environ --config /etc/caddy/Caddyfile
ExecReload=/usr/bin/caddy reload --config /etc/caddy/Caddyfile
TimeoutStopSec=5s
LimitNOFILE=1048576
PrivateTmp=true
ProtectSystem=full
AmbientCapabilities=CAP_NET_BIND_SERVICE
EnvironmentFile=-/etc/caddy/env

[Install]
WantedBy=multi-user.target
EOF

sudo systemctl daemon-reload
sudo systemctl enable --now caddy
```

### 5. 验证

```bash
# 检查服务状态
sudo systemctl status droply caddy

# 测试 API
curl https://api.droplydoc.com

# 查看日志
sudo journalctl -u droply -f
sudo journalctl -u caddy -f
```

droply-server 启动时会通过 Caddy Admin API 自动注册自定义域名路由。

</details>

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

[contexts.paratera]
api_url = "https://api.docs.paratera.co"
token = "dp_yyyyyyyyyyyy"
```

登录/注册时自动创建和更新此文件。旧的单服务器配置（顶层 `api_url` + `token`）会在首次使用时**静默迁移**为 `contexts.default`。

#### 同时使用多个服务器

```bash
# 登录自建服务器（自动创建 "paratera" context）
droply auth login --api-url https://api.docs.paratera.co

# 或显式命名 context
droply auth login --api-url https://api.docs.paratera.co --context corp

# 列出所有 context（* 标记当前激活的）
droply context list

# 切换服务器
droply context use paratera

# 仅添加 context，暂不认证
droply context add staging --api-url https://api.staging.example.com

# 删除 context
droply context remove staging

# 临时覆盖单次操作（不持久化）
droply --context paratera deploy
```

#### 项目级 context 绑定

项目目录下的 `.droply.toml` 可以绑定特定的 context：

```toml
context = "paratera"
subdomain = "alice"
project = "blog"
```

在该目录下执行 `droply` 命令时自动使用 `paratera` context。

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

添加自定义域名后，在 DNS 服务商处添加 CNAME 或 A 记录指向输出的目标地址，然后运行 `droply domain verify` 确认。Caddy 会自动为验证通过的自定义域名申请 HTTPS 证书。

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
| `--expire` | 会话过期时间（如 `1h`、`24h`、`7d`、`never`，默认 `24h`） |

#### 工作原理

- **IP 白名单**：只有来自指定 IP/子网的请求才能访问
- **密码保护**：访问者需要在登录页输入密码，通过后设置 cookie 保持会话
- **组合使用**：同时配置 IP 和密码时，两者都必须满足（AND 逻辑）
- **规则优先级**：项目级规则完全覆盖子域名级规则

受保护的站点会通过 Caddy 反代到 droply-server 的站点服务端口（`:8081`），由 server 处理验证逻辑。

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
| 反向代理/HTTPS | Caddy |

## License

MIT
