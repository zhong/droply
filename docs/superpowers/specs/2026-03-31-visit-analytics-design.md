# Visit Analytics Design

Droply 的页面访问统计功能，支持记录完整访问日志和长期聚合统计。

## 需求

- 记录每个项目内具体页面的完整访问日志（路径、IP、Referer、User-Agent、时间）
- 按天聚合 PV/UV 统计，永久保留
- 详细日志自动过期（默认 30 天）
- CLI 命令查看统计和日志
- 后续可扩展 Web Dashboard
- 所有接口在用户权限控制之下

## 数据模型

### page_visits（详细日志，自动过期）

```sql
CREATE TABLE page_visits (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    subdomain TEXT NOT NULL,
    project TEXT NOT NULL,
    path TEXT NOT NULL,
    ip TEXT NOT NULL,
    referer TEXT DEFAULT '',
    user_agent TEXT DEFAULT '',
    visited_at TEXT NOT NULL,
    FOREIGN KEY (subdomain) REFERENCES subdomains(name) ON DELETE CASCADE
);

CREATE INDEX idx_page_visits_project ON page_visits(subdomain, project, visited_at);
CREATE INDEX idx_page_visits_path ON page_visits(subdomain, project, path, visited_at);
```

### page_daily_stats（日聚合，永久保留）

```sql
CREATE TABLE page_daily_stats (
    subdomain TEXT NOT NULL,
    project TEXT NOT NULL,
    path TEXT NOT NULL,
    date TEXT NOT NULL,
    pv INTEGER DEFAULT 0,
    uv INTEGER DEFAULT 0,
    PRIMARY KEY (subdomain, project, path, date),
    FOREIGN KEY (subdomain) REFERENCES subdomains(name) ON DELETE CASCADE
);
```

## 数据采集

在 `site.go` 的文件服务流程中，请求通过访问控制后、返回文件前，异步记录访问日志。

### 采集时机

```
请求进入 → resolveHost → 访问控制检查 → 记录访问日志(异步) → 返回文件
```

### 采集规则

- 只记录成功请求（文件存在且通过访问控制）
- 排除静态资源：`.css`, `.js`, `.map`, `.woff`, `.woff2`, `.ttf`, `.eot`, `.png`, `.jpg`, `.jpeg`, `.gif`, `.svg`, `.ico`, `.webp`, `.mp4`, `.webm`, `.mp3`
- 异步写入：通过 channel + goroutine，不阻塞请求响应

### 聚合更新

写入 `page_visits` 时同步 upsert `page_daily_stats`：
- PV: 直接 +1
- UV: 查询当天该 IP 是否首次访问该路径，是则 +1

## API 端点

所有端点需 Bearer token 鉴权 + subdomain 所有权校验，与现有 API 权限模型一致。

### GET /subdomains/{sub}/projects/{project}/stats

查询聚合统计。

参数：
- `period`: `7d` | `30d` | `all`（默认 `30d`）

响应：
```json
{
  "total_pv": 1234,
  "total_uv": 456,
  "pages": [
    {"path": "/", "pv": 500, "uv": 200},
    {"path": "/about.html", "pv": 300, "uv": 150}
  ]
}
```

### GET /subdomains/{sub}/projects/{project}/logs

查询详细访问日志。

参数：
- `limit`: 默认 50，最大 500
- `path`: 可选，按路径前缀过滤
- `offset`: 可选，分页偏移

响应：
```json
{
  "logs": [
    {
      "path": "/about.html",
      "ip": "1.2.3.4",
      "referer": "https://google.com",
      "user_agent": "Mozilla/5.0 ...",
      "visited_at": "2026-03-31T10:30:00Z"
    }
  ],
  "total": 200
}
```

## CLI 命令

### droply stats [project]

显示聚合统计表格。

参数：
- `--period 7d|30d|all`（默认 `30d`）
- `--subdomain` 或从 `.droply.toml` 读取

输出示例：
```
Project: mysite  |  Period: last 30 days

Total PV: 1,234  |  Total UV: 456

Top Pages:
  /                500 PV   200 UV
  /about.html      300 PV   150 UV
  /contact         200 PV    80 UV
```

### droply logs [project]

显示详细访问日志。

参数：
- `--limit 50`（默认 50）
- `--path /about`（按路径过滤）
- `--subdomain` 或从 `.droply.toml` 读取

## 日志清理

服务端后台 goroutine 定期清理过期日志：

- 默认保留 30 天，通过环境变量 `DROPY_LOG_RETENTION_DAYS` 配置
- 启动时立即执行一次，之后每 24 小时执行一次
- 删除 subdomain/project 时通过外键级联清理

## 权限控制

复用现有权限模型：
- API: Bearer token 鉴别用户 + 查询 subdomains 表验证所有权
- CLI: 使用 `~/.config/droply/config.toml` 中的 API token
- 只有 subdomain owner 才能查看其下项目的 stats 和 logs

## Store 层变更

### 新增接口方法

```go
RecordVisit(subdomain, project, path, ip, referer, userAgent string) error
GetPageStats(subdomain, project, period string) ([]PageDailyStat, error)
GetVisitLogs(subdomain, project string, limit, offset int, pathFilter string) ([]VisitLog, int, error)
CleanupVisitLogs(retentionDays int) (int64, error)
```

### 新增 model 结构体

```go
type VisitLog struct {
    Path      string `json:"path"`
    IP        string `json:"ip"`
    Referer   string `json:"referer"`
    UserAgent string `json:"user_agent"`
    VisitedAt string `json:"visited_at"`
}

type PageDailyStat struct {
    Path string `json:"path"`
    PV   int    `json:"pv"`
    UV   int    `json:"uv"`
}

type StatsResponse struct {
    TotalPV int             `json:"total_pv"`
    TotalUV int             `json:"total_uv"`
    Pages   []PageDailyStat `json:"pages"`
}
```

## 文件变更清单

| 文件 | 变更 |
|------|------|
| `internal/model/model.go` | 新增 VisitLog, PageDailyStat, StatsResponse |
| `internal/store/store.go` | 新增 4 个接口方法 |
| `internal/store/sqlite.go` | 新增 2 张表 + 索引 + 4 个方法实现 + 自动建表 |
| `internal/server/site.go` | 文件服务时异步记录访问日志 |
| `internal/server/analytics.go` | 新增 stats/logs 两个 HTTP handler |
| `internal/server/server.go` | 注册新路由 |
| `internal/cli/stats.go` | 新增 stats 命令 |
| `internal/cli/logs.go` | 新增 logs 命令 |
| `cmd/droply-server/main.go` | 启动清理 goroutine |
