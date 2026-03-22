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

### 编译

```bash
git clone https://github.com/zhong/droply.git
cd droply
make build
```

生成两个二进制文件：
- `bin/droply-server` — 服务端
- `bin/droply` — CLI 客户端

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

### 前置条件

- 一台 VPS（推荐 Ubuntu/Debian）
- 一个域名（如 `droplydoc.com`），DNS 已配置：
  - `A` 记录：`droplydoc.com` → 服务器 IP
  - `A` 记录：`*.droplydoc.com` → 服务器 IP
  - `A` 记录：`api.droplydoc.com` → 服务器 IP
- 安装 [Caddy](https://caddyserver.com/docs/install)（需要支持 DNS challenge 的版本以启用通配符证书）

### 1. 安装 Caddy

通配符证书需要 DNS challenge，需使用包含 DNS provider 模块的 Caddy。以 Cloudflare 为例：

```bash
# 使用 xcaddy 编译带 Cloudflare DNS 模块的 Caddy
# 注意：--replace 用于绕过 Cloudflare 新版 API Token（cfut_/cfat_ 前缀）的兼容性问题
# 参考：https://github.com/caddy-dns/cloudflare/issues/125
# 上游修复合并后可移除 --replace 行
xcaddy build \
  --with github.com/caddy-dns/cloudflare \
  --replace github.com/caddy-dns/cloudflare=github.com/ogerman/cloudflare@master
sudo mv caddy /usr/bin/caddy
```

### 2. 获取 Cloudflare API Token

Caddy 需要 Cloudflare API Token 来完成通配符证书（`*.droplydoc.com`）和 `api.droplydoc.com` 证书的 DNS challenge 验证。

1. 前往 [Cloudflare Dashboard](https://dash.cloudflare.com/profile/api-tokens)
2. 点击 **Create Token**
3. 使用 **Edit zone DNS** 模板，或手动创建 Token：
   - **Permissions**: Zone → DNS → Edit
   - **Zone Resources**: Include → Specific zone → `droplydoc.com`
4. 复制生成的 Token

将 Token 存储到服务器：

```bash
# 创建 Caddy 环境变量文件（仅 root 可读）
sudo tee /etc/caddy/env > /dev/null << 'EOF'
CLOUDFLARE_API_TOKEN=你的-cloudflare-api-token
EOF
sudo chmod 600 /etc/caddy/env
```

### 3. 部署 droply-server

```bash
# 创建数据目录
sudo mkdir -p /data/droply/sites

# 将编译好的二进制文件复制到服务器
scp bin/droply-server your-server:/usr/local/bin/

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

### 4. 配置 Caddy

创建 `/etc/caddy/Caddyfile`：

```caddyfile
{
    admin localhost:2019
}

# 通配符证书，通过 DNS challenge 签发
*.droplydoc.com {
    tls {
        dns cloudflare {env.CLOUDFLARE_API_TOKEN}
    }

    # 所有子域名请求代理到 droply-server 的站点处理器，
    # 由站点处理器提供文件服务并执行访问控制。
    reverse_proxy localhost:8081
}

# API 端点 — 自动获取独立证书
api.droplydoc.com {
    reverse_proxy localhost:8080
}
```

更新 Caddy 的 systemd 服务以加载环境变量文件：

```bash
# 编辑 Caddy 的 systemd override
sudo systemctl edit caddy
```

添加以下内容：

```ini
[Service]
EnvironmentFile=/etc/caddy/env
```

然后启动/重启：

```bash
sudo systemctl daemon-reload
sudo systemctl restart caddy
```

droply-server 启动时会通过 Caddy Admin API 自动注册子域名路由，无需手动配置。

### 5. 数据目录结构

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

将 `bin/droply` 复制到 `$PATH` 中的目录：

```bash
sudo cp bin/droply /usr/local/bin/
```

或者直接使用 `go install`：

```bash
go install github.com/zhong/droply/cmd/droply@latest
```

### 配置

CLI 配置文件位于 `~/.config/droply/config.toml`：

```toml
api_url = "https://api.droplydoc.com"
token = "dp_xxxxxxxxxxxx"
```

登录或注册时会自动创建和更新此文件。如果使用自部署实例，先手动创建配置文件并修改 `api_url`。

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
```

```bash
# 有 .droply.toml 后，直接运行即可
droply deploy
```

#### 打包排除规则

部署时自动排除以下文件和目录：

- `.git`
- `node_modules`
- `__pycache__`
- `.DS_Store`
- `.env`
- 所有隐藏目录（以 `.` 开头）

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
