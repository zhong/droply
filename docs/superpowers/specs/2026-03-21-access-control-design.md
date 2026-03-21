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

## Data Model

新增 `access_rules` 表：

```sql
CREATE TABLE access_rules (
    id           TEXT PRIMARY KEY,
    subdomain_id TEXT NOT NULL REFERENCES subdomains(id) ON DELETE CASCADE,
    project_id   TEXT NULL REFERENCES projects(id) ON DELETE CASCADE,
    allowed_ips  TEXT NULL,          -- JSON array, e.g. ["10.0.0.0/8", "192.168.1.100"]
    password_hash TEXT NULL,         -- bcrypt hash
    session_ttl  INTEGER NOT NULL DEFAULT 86400,  -- seconds
    created_at   DATETIME NOT NULL,
    updated_at   DATETIME NOT NULL,
    UNIQUE(subdomain_id, project_id)
);
```

- `project_id IS NULL` 表示 subdomain 级别规则
- `allowed_ips` 和 `password_hash` 至少有一个非 NULL（应用层校验）

Go model:

```go
type AccessRule struct {
    ID           string    `json:"id"`
    SubdomainID  string    `json:"subdomain_id"`
    ProjectID    *string   `json:"project_id,omitempty"`
    AllowedIPs   []string  `json:"allowed_ips,omitempty"`
    PasswordHash string    `json:"-"`
    SessionTTL   int       `json:"session_ttl"`
    CreatedAt    time.Time `json:"created_at"`
    UpdatedAt    time.Time `json:"updated_at"`
}
```

规则查找优先级：project 级精确匹配 > subdomain 级 (project_id IS NULL) > 无规则（放行）。

## Request Interception & Verification Flow

### Caddy Route Changes

设置访问控制时：将 Caddy 路由从 `file_server` 改为 `reverse_proxy` 到 Droply server。
移除访问控制时：改回 `file_server`。

### Droply Server Site Routes

新增站点服务路由（独立于 API 路由）：

```
GET  /site/{subdomain}/*           → siteHandler
GET  /site/{subdomain}/{project}/* → siteHandler
POST /site/_auth/login             → siteLoginHandler
```

### Verification Flow

```
请求进入
  ↓
解析 subdomain + project path
  ↓
查找 access_rule（project 级 > subdomain 级 > 无规则）
  ↓
无规则 → 直接返回文件
  ↓
有规则 → 检查 IP 白名单（如果配置了）
  ↓
  IP 不匹配 → 403
  IP 匹配或未配置 IP 规则 → 继续
  ↓
检查密码（如果配置了）
  ↓
  cookie 有效 → 返回文件
  无有效 cookie → 返回登录页
  ↓
POST 登录 → 密码正确 → Set-Cookie → 重定向回原始 URL
           → 密码错误 → 登录页 + 错误提示
```

### Cookie Design

- Cookie name: `_droply_access`
- Value: `{subdomain}:{project}:{expiry_timestamp}:{hmac_signature}`
- HMAC uses server-side secret key to prevent forgery
- `Path` set to project path or `/` (subdomain level)
- Flags: `HttpOnly`, `Secure`, `SameSite=Lax`

### IP Resolution

- Read from `X-Forwarded-For` or `X-Real-IP` headers (set by Caddy)
- Support CIDR matching (`net.ParseCIDR`) and exact IP matching

## API Endpoints

All endpoints require Bearer token authentication and subdomain ownership verification.

```
PUT    /subdomains/{sub}/access                       → set subdomain access rule
GET    /subdomains/{sub}/access                       → get subdomain access rule
DELETE /subdomains/{sub}/access                       → remove subdomain access rule

PUT    /subdomains/{sub}/projects/{project}/access     → set project access rule
GET    /subdomains/{sub}/projects/{project}/access     → get project access rule
DELETE /subdomains/{sub}/projects/{project}/access     → remove project access rule
```

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

### PUT Response (when auto_password=true)

```json
{
  "id": "uuid",
  "allowed_ips": ["10.0.0.0/8"],
  "has_password": true,
  "generated_password": "a1b2c3d4e5f6",
  "session_ttl": 3600
}
```

### Side Effects

- PUT: sync Caddy route from file_server to reverse_proxy
- DELETE: sync Caddy route from reverse_proxy back to file_server

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
- `--password auto`: auto-generate password, output to terminal
- `--password <value>`: custom password
- `--expire`: session TTL, supports `1h`, `24h`, `7d` format, default `24h`
- Without `--project`: operates at subdomain level

## Login Page

Embedded HTML template via Go `embed` package:

- Minimal centered form with password input and submit button
- Displays site name (subdomain/project)
- Red error message on incorrect password
- Mobile responsive
- No JavaScript dependency, pure HTML form POST

## Caddy Route Management

New methods in `internal/caddy/client.go`:

```go
func (c *Client) SetSubdomainProtected(name string, proxyAddr string) error
func (c *Client) SetSubdomainUnprotected(name string, siteRoot string) error
```

Switch logic: delete old route → add new route. Route ID stays the same (`subdomain-{name}`).

Startup recovery: `RecoverCaddyRoutes()` extended to check access_rules and configure reverse_proxy or file_server accordingly.
