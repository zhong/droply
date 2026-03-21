# Droply — 静态内容发布平台设计文档

## 概述

Droply 是一个支持多用户、多子域名的静态内容发布平台。用户通过 CLI 客户端快速发布静态网站，系统自动分配子域名并提供 HTTPS 访问。类似 Vercel 但只支持纯静态内容。

## 架构

### 整体架构

```
CLI Client (Go)                    Browser
     │                                │
     │ upload tar.gz                  │ HTTPS
     ▼                                ▼
┌─────────────────────────────────────────┐
│              Caddy (443/80)             │
│         Auto HTTPS + Wildcard TLS       │
├─────────────────┬───────────────────────┤
│ api.droplydoc.com  │  *.droplydoc.com         │
│ reverse_proxy   │  file_server          │
│    :8080        │  /data/droply/sites/  │
└────────┬────────┴───────────────────────┘
         │
         ▼
┌──────────────────┐    ┌─────────────────┐
│  Droply API (Go) │───→│     SQLite      │
│     :8080        │    │   droply.db     │
└──────────────────┘    └─────────────────┘
```

### 职责分离

- **Caddy**: TLS 终止、自动 HTTPS（通配符 + 自定义域名）、API 反代、静态文件服务
- **Go API**: 用户认证、上传处理、元数据管理、通过 Caddy Admin API 动态更新路由
- **SQLite**: 用户、子域名、项目、部署记录、自定义域名映射
- **磁盘**: `/data/droply/sites/{subdomain}/{project}/` 存储静态文件

## 数据模型

### users

| 字段 | 类型 | 说明 |
|------|------|------|
| id | INTEGER PK | 自增主键 |
| email | TEXT UNIQUE | 邮箱，登录凭证 |
| password | TEXT | bcrypt 哈希 |
| api_token | TEXT UNIQUE | CLI 认证 token |
| created_at | DATETIME | 创建时间 |

### subdomains

| 字段 | 类型 | 说明 |
|------|------|------|
| id | INTEGER PK | 自增主键 |
| user_id | INTEGER FK | 所属用户 |
| name | TEXT UNIQUE | 子域名名称，如 `alice` → `alice.droplydoc.com` |
| created_at | DATETIME | 创建时间 |

约束: name 格式为小写字母+数字+连字符，3-32 字符。

### projects

| 字段 | 类型 | 说明 |
|------|------|------|
| id | INTEGER PK | 自增主键 |
| subdomain_id | INTEGER FK | 所属子域名 |
| name | TEXT | 项目名，URL 路径 |
| created_at | DATETIME | 创建时间 |
| updated_at | DATETIME | 更新时间 |

约束: UNIQUE(subdomain_id, name)，name 格式同子域名。

### deployments

| 字段 | 类型 | 说明 |
|------|------|------|
| id | INTEGER PK | 自增主键 |
| project_id | INTEGER FK | 所属项目 |
| version | INTEGER | 自增版本号 |
| file_count | INTEGER | 文件数量 |
| total_size | INTEGER | 总大小(bytes) |
| status | TEXT | uploading/active/archived |
| created_at | DATETIME | 创建时间 |

### custom_domains

| 字段 | 类型 | 说明 |
|------|------|------|
| id | INTEGER PK | 自增主键 |
| project_id | INTEGER FK | 所属项目 |
| domain | TEXT UNIQUE | 自定义域名，如 `blog.alice.com` |
| verified | BOOLEAN | DNS 验证是否通过 |
| created_at | DATETIME | 创建时间 |

### 关系

```
users 1:N subdomains 1:N projects 1:N deployments
                                   1:N custom_domains
```

## API 设计

所有接口通过 `api.droplydoc.com`，JSON 格式。

### 认证（无需 token）

```
POST /auth/register    { email, password }           → { api_token }
POST /auth/login       { email, password }           → { api_token }
```

### 子域名管理（需 Bearer token）

```
POST   /subdomains          { name }                 → { subdomain }
GET    /subdomains                                   → [{ subdomain, project_count }]
DELETE /subdomains/:name                             → 204
```

### 项目管理（需 Bearer token）

```
GET    /subdomains/:sub/projects                     → [{ project }]
DELETE /subdomains/:sub/projects/:name               → 204
```

### 部署（需 Bearer token）

```
POST   /subdomains/:sub/projects/:name/deploy
       Content-Type: multipart/form-data
       Body: tar.gz 文件                              → { deployment_id, version, url }

GET    /subdomains/:sub/projects/:name/deployments   → [{ deployment }]
```

deploy 时如果项目不存在则自动创建。

### 自定义域名（需 Bearer token）

```
POST   /subdomains/:sub/projects/:name/domains
       { domain }                                     → { domain, verified, cname_target }
DELETE /subdomains/:sub/projects/:name/domains/:domain → 204
```

## CLI 设计

### 命令

```
droply register                     交互式注册，保存 token
droply login                        交互式登录，保存 token
droply logout                       清除本地 token

droply subdomain create <name>      创建子域名
droply subdomain list               列出所有子域名
droply subdomain delete <name>      删除子域名及所有项目

droply deploy [dir]                 部署当前目录或指定目录
  --sub <name>                      目标子域名
  --project <name>                  项目名

droply list [--sub <name>]          列出项目和部署信息
droply domain add <domain>          添加自定义域名
droply domain list                  列出自定义域名
droply domain remove <domain>       移除自定义域名
droply whoami                       显示当前用户信息
```

### 项目配置 `.droply.toml`

```toml
subdomain = "alice"
project = "blog"
```

放在项目根目录，deploy 时自动读取，免去每次传参。

### 用户配置 `~/.config/droply/config.toml`

```toml
api_url = "https://api.droplydoc.com"
token = "dp_xxxxxxxxxxxx"
```

### Deploy 流程

1. 读取 `.droply.toml` 或命令行参数确定 subdomain + project
2. 打包目录为 tar.gz（排除 `.git`、`node_modules` 等常见目录）
3. 上传到 `POST /subdomains/:sub/projects/:name/deploy`
4. 打印部署结果和访问 URL

## Caddy 配置策略

### 动态配置

通过 Caddy Admin API (`localhost:2019`) 管理路由：

- **启动时**: 加载基础配置（API 反代 + 通配符子域名处理）
- **新建子域名时**: 添加路由 `{name}.droplydoc.com` → `file_server /data/droply/sites/{name}/`
- **添加自定义域名时**: 添加路由 + 自动申请证书
- **删除时**: 移除对应路由

### TLS

- 通配符 `*.droplydoc.com`: DNS challenge（需配置 DNS provider API key，如 Cloudflare）
- 自定义域名: 标准 HTTP challenge，Caddy 自动处理

## 磁盘布局

```
/data/droply/
├── droply.db
├── sites/
│   ├── alice/
│   │   ├── blog/
│   │   │   ├── index.html
│   │   │   └── style.css
│   │   └── portfolio/
│   │       └── index.html
│   └── bob/
│       └── docs/
│           └── index.html
└── caddy/
    └── config.json
```

## 项目结构

```
droply/
├── cmd/
│   ├── droply/                  CLI 客户端
│   │   └── main.go
│   └── droply-server/           服务端
│       └── main.go
├── internal/
│   ├── server/
│   │   ├── server.go            HTTP server + chi 路由
│   │   ├── auth.go              注册/登录/认证中间件
│   │   ├── subdomain.go         子域名 CRUD
│   │   ├── deploy.go            部署处理
│   │   └── domain.go            自定义域名管理
│   ├── caddy/
│   │   └── client.go            Caddy Admin API 客户端
│   ├── store/
│   │   ├── store.go             数据库接口
│   │   └── sqlite.go            SQLite 实现
│   └── model/
│       └── model.go             数据模型
├── internal/cli/
│   ├── root.go                  CLI 根命令
│   ├── auth.go                  register/login/logout
│   ├── subdomain.go             subdomain 管理
│   ├── deploy.go                打包上传
│   ├── domain.go                自定义域名
│   └── config.go                配置文件读写
├── go.mod
├── go.sum
└── Makefile
```

## 技术选型

| 用途 | 库 | 理由 |
|------|-----|------|
| HTTP 路由 | `github.com/go-chi/chi/v5` | 轻量，兼容标准库，中间件生态好 |
| CLI 框架 | `github.com/spf13/cobra` | Go CLI 事实标准 |
| SQLite | `modernc.org/sqlite` | 纯 Go，无需 CGO |
| 密码哈希 | `golang.org/x/crypto/bcrypt` | 标准选择 |
| 配置文件 | `github.com/BurntSushi/toml` | TOML 解析 |

## 实现细节备注

- **上传大小限制**: 默认最大 50MB per deploy
- **打包排除列表**: `.git`, `node_modules`, `.DS_Store`, `__pycache__`, `.env`
- **Token 生成**: `dp_` 前缀 + 32 字节 `crypto/rand` hex 字符串
- **自定义域名 DNS 验证**: 用户通过 `droply domain add` 添加后，手动执行 `droply domain verify <domain>` 触发服务端 DNS 检查；或在 deploy 时自动检查未验证的域名
- **Caddy 启动恢复**: Go API 启动时从 SQLite 重建所有 Caddy 路由，不依赖 Caddy 持久化配置

## MVP 范围

### 包含

- 用户注册/登录（CLI）
- 子域名创建/管理
- 项目部署（tar.gz 上传 + 解压）
- Caddy 动态路由配置
- 通配符子域名 HTTPS
- 自定义域名（含 DNS 验证）

### 不包含（后续迭代）

- 部署回滚
- 团队协作/权限管理
- 构建流程（CI/CD）
- Web 管理界面
- 用量统计/限制
- CDN 分发
