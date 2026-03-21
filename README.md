# Droply

多用户、多子域名的静态内容发布平台。通过 CLI 快速发布静态网站，自动分配子域名并提供 HTTPS 访问。

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
| api.droplydoc.com    |  *.droplydoc.com               |
| reverse_proxy     |  file_server                |
|    :8080          |  /data/droply/sites/         |
+--------+----------+-----------------------------+
         |
         v
+------------------+    +-----------------+
|  droply-server   |--->|     SQLite      |
|     :8080        |    |   droply.db     |
+------------------+    +-----------------+
```

- **Caddy** — TLS 终止、自动 HTTPS（通配符 + 自定义域名）、API 反代、静态文件服务
- **droply-server** — 用户认证、上传处理、元数据管理、通过 Caddy Admin API 动态更新路由
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
xcaddy build --with github.com/caddy-dns/cloudflare
sudo mv caddy /usr/bin/caddy
```

### 2. 部署 droply-server

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
| `--addr` | `:8080` | 监听地址 |
| `--data-dir` | `/data/droply` | 数据目录（数据库 + 静态文件） |
| `--domain` | `droplydoc.com` | 基础域名 |
| `--caddy-admin` | `http://localhost:2019` | Caddy Admin API 地址 |

### 3. 配置 Caddy

创建 `/etc/caddy/Caddyfile`：

```caddyfile
{
    admin localhost:2019
}

api.droplydoc.com {
    reverse_proxy localhost:8080
}
```

droply-server 启动时会通过 Caddy Admin API 自动注册子域名路由，无需手动配置。

```bash
sudo systemctl restart caddy
```

### 4. 数据目录结构

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

### 查看项目列表

```bash
droply list --sub alice
```

### 自定义域名

```bash
# 为项目添加自定义域名
droply domain add blog.example.com --sub alice --project blog
# 输出 CNAME 记录目标，按提示在 DNS 中添加

# 查看自定义域名
droply domain list --sub alice --project blog

# 移除自定义域名
droply domain remove blog.example.com --sub alice --project blog
```

添加自定义域名后，需要在 DNS 服务商处添加 CNAME 记录，指向输出的目标地址。Caddy 会自动为验证通过的自定义域名申请 HTTPS 证书。

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

## 技术栈

| 组件 | 技术 |
|------|------|
| 语言 | Go |
| HTTP 路由 | [chi](https://github.com/go-chi/chi) |
| CLI 框架 | [cobra](https://github.com/spf13/cobra) |
| 数据库 | SQLite ([modernc.org/sqlite](https://pkg.go.dev/modernc.org/sqlite)) |
| 密码哈希 | bcrypt |
| 配置文件 | TOML |
| 反向代理/HTTPS | Caddy |

## License

MIT
