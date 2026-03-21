# Access Control for Subdomain/Project

## Overview

为 Droply 的 subdomain 和 project 增加访问控制功能，支持 IP 白名单和密码保护两种方式。访问者必须满足所有已配置的规则才能访问受保护的站点。

## Requirements

- **粒度**: 支持 subdomain 级别和 project 级别的访问控制
- **覆盖策略**: project 级别规则完全覆盖 subdomain 级别规则
- **IP 白名单**: 支持单 IP 和 CIDR 子网
- **密码保护**: 支持自动生成和自定义密码，通过自定义登录页输入，cookie 保持会话
- **组合逻辑**: 同时配置时 AND（都必须满足）；只配置一种时只验证该种
- **会话过期**: 可配置，默认 24 小时
- **实现层**: 受保护站点通过 Caddy reverse_proxy 转发到 Droply server 处理
- **自定义域名**: 通过自定义域名访问的受保护站点同样适用访问控制

## Data Model

新增 `access_rules` 表（ID 类型与现有表一致使用 INTEGER）：

```sql
CREATE TABLE access_rules (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    subdomain_id  INTEGER NOT NULL REFERENCES subdomains(id) ON DELETE CASCADE,
    project_id    INTEGER NULL REFERENCES projects(id) ON DELETE CASCADE,
    allowed_ips   TEXT NULL,          -- JSON array, e.g. ["10.0.0.0/8", "192.168.1.100"]
    password_hash TEXT NULL,          -- bcrypt hash
    session_ttl   INTEGER NOT NULL DEFAULT 86400,  -- seconds
    created_at    DATETIME NOT NULL,
    updated_at    DATETIME NOT NULL,
    UNIQUE(subdomain_id, project_id)
);
```

- `project_id IS NULL` 表示 subdomain 级别规则
- `allowed_ips` 和 `password_hash` 至少有一个非 NULL（应用层校验）
- `ON DELETE CASCADE` 确保 subdomain 或 project 被删除时自动清理规则

Go model:

```go
type AccessRule struct {
    ID           int64     `json:"id"`
    SubdomainID  int64     `json:"subdomain_id"`
    ProjectID    *int64    `json:"project_id,omitempty"`
    AllowedIPs   []string  `json:"allowed_ips,omitempty"`
    PasswordHash string    `json:"-"`
    HasPassword  bool      `json:"has_password"`
    SessionTTL   int       `json:"session_ttl"`
    CreatedAt    time.Time `json:"created_at"`
    UpdatedAt    time.Time `json:"updated_at"`
}
```

规则查找优先级：project 级精确匹配 > subdomain 级 (project_id IS NULL) > 无规则（放行）。

### Store Interface Methods

```go
// Access rule CRUD
CreateAccessRule(rule *AccessRule) error
UpdateAccessRule(rule *AccessRule) error
GetAccessRule(subdomainID int64, projectID *int64) (*AccessRule, error)
DeleteAccessRule(subdomainID int64, projectID *int64) error

// For site serving: find applicable rule for a request
FindAccessRule(subdomainName string, projectName string) (*AccessRule, error)

// Check if a subdomain has any access rules (subdomain-level or any project-level)
HasAccessRules(subdomainID int64) (bool, error)
```

## Request Interception & Verification Flow

### Caddy Route Changes

**策略：只要一个 subdomain 下存在任何访问控制规则（subdomain 级或任一 project 级），整个 subdomain 的 Caddy 路由就切换为 reverse_proxy。** 这样简化了 Caddy 配置——不需要为每个 project 单独配置路由。Droply server 在收到请求后根据具体路径决定是否需要验证。

- 设置第一条规则时：将 subdomain 的 Caddy 路由从 `file_server` 改为 `reverse_proxy`
- 移除最后一条规则时：改回 `file_server`
- 自定义域名：同理，受保护 project 的自定义域名路由也切换为 `reverse_proxy`

### Droply Server Site Serving

不使用独立的 `/site/` 路由前缀。Caddy 的 reverse_proxy 直接将原始请求转发到 Droply server，由 Droply server 根据 `Host` header 识别 subdomain，根据 URL path 识别 project。

新增一个独立的 HTTP handler 用于站点服务：

```go
// siteHandler 处理受保护站点的访问请求
// Caddy reverse_proxy 将请求原样转发，保留原始 Host header
// 例如: Host: alice.droplydoc.com, Path: /blog/index.html
func (s *Server) siteHandler(w http.ResponseWriter, r *http.Request)

// siteLoginHandler 处理登录表单提交
// POST 表单字段: password, redirect (原始 URL)
// 表单 action: /_droply/login
func (s *Server) siteLoginHandler(w http.ResponseWriter, r *http.Request)
```

路由注册：在 Droply server 中为站点服务创建独立的 HTTP server 或监听独立端口（如 `:8081`），避免与 API 路由冲突。Caddy reverse_proxy 指向此端口。

### Route Disambiguation

由于站点服务使用独立端口，不存在与 API 路由的歧义。站点服务 handler 的路径解析逻辑：

1. 从 `Host` header 提取 subdomain 名称（去掉域名后缀）
2. 从 URL path 提取第一段作为 project 名称（如果存在）
3. 查找 access rule：先查 project 级，再查 subdomain 级
4. 如果 path 为 `/_droply/login`，路由到 siteLoginHandler

### Verification Flow

```
请求进入（Host: alice.droplydoc.com, Path: /blog/index.html）
  ↓
从 Host 提取 subdomain = "alice"
从 Path 提取 project = "blog", file = "index.html"
  ↓
查找 access_rule（project "blog" 级 > subdomain "alice" 级 > 无规则）
  ↓
无规则 → 直接返回文件（http.FileServer）
  ↓
有规则 → 检查 IP 白名单（如果配置了）
  ↓
  IP 不匹配 → 403（不暴露具体原因）
  IP 匹配或未配置 IP 规则 → 继续
  ↓
检查密码（如果配置了）
  ↓
  cookie 有效 → 返回文件（http.FileServer）
  无有效 cookie → 返回登录页
  ↓
POST /_droply/login
  form fields: password, redirect
  ↓
  密码正确 → Set-Cookie → 302 重定向到 redirect URL
  密码错误 → 返回登录页 + 错误提示
```

### Static File Serving

受保护站点的文件通过 Go `http.FileServer` 返回。文件根目录与 Caddy file_server 相同：`{dataDir}/sites/{subdomain}/`。

```go
// 使用 http.StripPrefix + http.FileServer 组合
fileServer := http.FileServer(http.Dir(filepath.Join(s.dataDir, "sites", subdomain)))
fileServer.ServeHTTP(w, r)
```

### Cookie Design

- Cookie name: `_droply_access`
- Value: `{subdomain}:{project_or_empty}:{expiry_unix}:{hmac_sha256}`
- HMAC 输入: `{subdomain}:{project_or_empty}:{expiry_unix}`
- HMAC key: 服务端密钥（见下文 Secret Key Management）
- `Path`: project 级设为 `/{project}/`，subdomain 级设为 `/`
- Flags: `HttpOnly`, `Secure`, `SameSite=Lax`
- Cookie 值中的 subdomain/project/expiry 为明文，但 HMAC 签名防止篡改。这对于此场景可接受——这些信息本身不敏感（从 URL 即可获知），HMAC 确保完整性。

### Secret Key Management

- HMAC 签名使用的密钥通过 server 启动参数 `--hmac-secret` 指定
- 如果未指定，启动时自动生成 32 字节随机密钥并写入 `{dataDir}/hmac.key` 文件
- 后续启动自动从文件读取，确保重启后已有 session 仍然有效
- 密钥变更会使所有现有 session 失效（可接受，用户重新输入密码即可）

### IP Resolution

- 从 Caddy 设置的 `X-Real-IP` header 获取客户端 IP（Caddy 默认会设置此 header）
- **Caddy 配置要求**: Caddy 必须覆写（而非追加）`X-Real-IP` header，确保客户端无法通过伪造 header 绕过 IP 限制
- 支持 CIDR 匹配（`net.ParseCIDR` + `Contains`）和精确 IP 匹配
- 如果 header 中无法获取 IP，fallback 到 `r.RemoteAddr`

### Rate Limiting

对 `POST /_droply/login` 端点实施简单的内存 rate limiting：

- 基于客户端 IP，使用 `golang.org/x/time/rate` 限流器
- 每个 IP 每分钟最多 10 次登录尝试
- 超出限制返回 429 Too Many Requests
- 使用 `sync.Map` 存储限流器，定期清理过期条目

## API Endpoints

All endpoints require Bearer token authentication and subdomain ownership verification.

```
PUT    /subdomains/{sub}/access                       → set subdomain access rule (upsert)
GET    /subdomains/{sub}/access                       → get subdomain access rule
DELETE /subdomains/{sub}/access                       → remove subdomain access rule

PUT    /subdomains/{sub}/projects/{project}/access     → set project access rule (upsert)
GET    /subdomains/{sub}/projects/{project}/access     → get project access rule
DELETE /subdomains/{sub}/projects/{project}/access     → remove project access rule
```

### PUT Semantics (Upsert)

PUT 是幂等的 upsert 操作：
- 如果规则不存在，创建新规则
- 如果规则已存在，**完全替换**（不是部分更新）。调用者必须提供完整的规则定义。
- SQL: `INSERT INTO access_rules ... ON CONFLICT(subdomain_id, project_id) DO UPDATE SET ...`

### PUT Request Body

```json
{
  "allowed_ips": ["10.0.0.0/8", "192.168.1.100"],
  "password": "my-secret",
  "auto_password": true,
  "session_ttl": 3600
}
```

- `password` and `auto_password` are mutually exclusive
- `allowed_ips` and password (`password` or `auto_password`) must have at least one
- `password` minimum length: 8 characters
- `auto_password` generates 16 character alphanumeric string using `crypto/rand`
- `session_ttl` optional, default 86400 (24h), min 300 (5min), max 2592000 (30 days)

### PUT Response

```json
{
  "id": 1,
  "allowed_ips": ["10.0.0.0/8"],
  "has_password": true,
  "generated_password": "a1b2c3d4e5f6g7h8",
  "session_ttl": 3600
}
```

- `generated_password` 仅在 `auto_password=true` 时返回，且仅在创建/更新时返回一次

### Side Effects

- PUT: 检查该 subdomain 是否已有 reverse_proxy 路由，没有则切换 Caddy 路由
- DELETE: 检查该 subdomain 下是否还有其他 access_rule，没有则切换回 file_server
- DELETE project 或 subdomain 时（现有 handler）：由于 `ON DELETE CASCADE`，数据库自动清理规则。handler 中需额外检查并更新 Caddy 路由。

## CLI Commands

```bash
# Subdomain level
droply access set --subdomain alice --ip 10.0.0.0/8 --ip 192.168.1.100 --password auto --expire 24h
droply access set --subdomain alice --password "my-secret" --expire 7d
droply access get --subdomain alice
droply access remove --subdomain alice

# Project level
droply access set --subdomain alice --project blog --ip 10.0.0.0/8 --password auto
droply access get --subdomain alice --project blog
droply access remove --subdomain alice --project blog
```

Parameters:
- `--ip`: repeatable, IP address or CIDR notation
- `--password auto`: auto-generate 16-char alphanumeric password, output to terminal
- `--password <value>`: custom password (minimum 8 characters)
- `--expire`: session TTL, supports `1h`, `24h`, `7d` format, default `24h`
- Without `--project`: operates at subdomain level

## Login Page

Embedded HTML template via Go `embed` package:

- Minimal centered form with password input and submit button
- Displays site name (subdomain/project)
- Red error message on incorrect password
- Mobile responsive
- No JavaScript dependency, pure HTML form POST
- Form action: `/_droply/login`
- Form fields: `password` (text input), `redirect` (hidden, original URL)
- On 429 rate limit: display "Too many attempts, please try again later"

## Caddy Route Management

New methods in `internal/caddy/client.go`:

```go
// SetSubdomainProtected switches a subdomain's Caddy route from file_server
// to reverse_proxy pointing to the site serving port.
func (c *Client) SetSubdomainProtected(name string, proxyAddr string) error

// SetSubdomainUnprotected switches a subdomain's Caddy route from reverse_proxy
// back to file_server.
func (c *Client) SetSubdomainUnprotected(name string, siteRoot string) error

// SetCustomDomainProtected switches a custom domain's route to reverse_proxy.
func (c *Client) SetCustomDomainProtected(domain string, proxyAddr string) error

// SetCustomDomainUnprotected switches a custom domain's route back to file_server.
func (c *Client) SetCustomDomainUnprotected(domain string, siteRoot string) error
```

Switch logic: delete old route → add new route. Route ID stays the same (`subdomain-{name}` or `domain-{domain}`).

Startup recovery: `RecoverCaddyRoutes()` extended to check whether each subdomain has any access_rules. If yes, configure reverse_proxy; otherwise configure file_server. Same for custom domains.

## Deletion Cascading

When a project or subdomain is deleted through existing API endpoints:

1. `ON DELETE CASCADE` removes associated access_rules from the database
2. The existing delete handlers must additionally check if the subdomain still has remaining access_rules
3. If no rules remain, switch Caddy route back to file_server
4. If the subdomain itself is deleted, the Caddy route is already removed by existing logic
