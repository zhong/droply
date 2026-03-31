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

### 路径规范化

写入前对请求路径统一规范化，确保相同页面只产生一条记录：

- 去除尾部斜杠（`/about/` → `/about`，但 `/` 保持不变）
- `index.html` 解析为 `/`（项目根路径下的 `index.html`）
- 路径统一转小写

### page_visits（详细日志，自动过期）

```sql
CREATE TABLE page_visits (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    subdomain_id INTEGER NOT NULL,
    project TEXT NOT NULL,
    path TEXT NOT NULL,
    ip TEXT NOT NULL,
    referer TEXT DEFAULT '',
    user_agent TEXT DEFAULT '',
    visited_at DATETIME NOT NULL,
    FOREIGN KEY (subdomain_id) REFERENCES subdomains(id) ON DELETE CASCADE
);

CREATE INDEX idx_page_visits_lookup ON page_visits(subdomain_id, project, visited_at);
CREATE INDEX idx_page_visits_path ON page_visits(subdomain_id, project, path, visited_at);
CREATE INDEX idx_page_visits_cleanup ON page_visits(visited_at);
```

### page_daily_stats（日聚合，永久保留）

```sql
CREATE TABLE page_daily_stats (
    subdomain_id INTEGER NOT NULL,
    project TEXT NOT NULL,
    path TEXT NOT NULL,
    date TEXT NOT NULL,
    pv INTEGER DEFAULT 0,
    uv INTEGER DEFAULT 0,
    PRIMARY KEY (subdomain_id, project, path, date),
    FOREIGN KEY (subdomain_id) REFERENCES subdomains(id) ON DELETE CASCADE
);
```

**设计决策：** 使用 `subdomain_id INTEGER` 而非 `subdomain TEXT`，与现有 schema 的 FK 约定一致（`projects.subdomain_id`, `access_rules.subdomain_id` 等），确保级联删除可靠。

## 数据采集

在 `site.go` 的文件服务流程中，请求通过访问控制后、返回文件前，异步记录访问日志。

### 采集时机

```
请求进入 → resolveHost → 访问控制检查 → 记录访问日志(异步) → 返回文件
```

### 采集规则

- 只记录成功请求（文件存在且通过访问控制）
- 排除静态资源（定义在 `internal/server/analytics.go` 的常量 `skippedExtensions` 中，大小写不敏感）：
  `.css`, `.js`, `.map`, `.woff`, `.woff2`, `.ttf`, `.eot`, `.png`, `.jpg`, `.jpeg`, `.gif`, `.svg`, `.ico`, `.webp`, `.mp4`, `.webm`, `.mp3`
- 异步写入：通过带缓冲 channel（容量 1000）+ goroutine，不阻塞请求响应
- Channel 满时丢弃该条记录（不阻塞请求，通过 `select` + `default` 非阻塞发送）

### 异步写入与优雅关闭

```go
// Server 新增字段
type Server struct {
    // ... 现有字段
    visitCh    chan visitRecord
    done       chan struct{}   // 通知 analytics goroutine 停止
}

// 启动时
s.visitCh = make(chan visitRecord, 1000)
go s.processVisits()

// 关闭时（main.go 中在 st.Close() 前调用）
func (s *Server) ShutdownAnalytics() {
    close(s.visitCh)       // 停止接收新记录
    <-s.done               // 等待 goroutine 排空 channel
}
```

### 聚合更新

`processVisits` goroutine 从 channel 消费记录，在同一个事务中：

1. **INSERT** 到 `page_visits`
2. **UPSERT** `page_daily_stats` 的 PV+1：`ON CONFLICT(...) DO UPDATE SET pv = pv + 1`
3. **UV 计数**：`INSERT OR IGNORE` 到 `page_daily_ips` 去重表，如果插入成功（新 IP）则 UV+1

```sql
-- UV 去重辅助表
CREATE TABLE page_daily_ips (
    subdomain_id INTEGER NOT NULL,
    project TEXT NOT NULL,
    path TEXT NOT NULL,
    date TEXT NOT NULL,
    ip TEXT NOT NULL,
    PRIMARY KEY (subdomain_id, project, path, date, ip),
    FOREIGN KEY (subdomain_id) REFERENCES subdomains(id) ON DELETE CASCADE
);
```

UV 通过 `page_daily_ips` 表的 UNIQUE 约束原子去重，避免并发竞态导致 UV 多算。

## API 端点

所有端点需 Bearer token 鉴权 + subdomain 所有权校验，与现有 API 权限模型一致。

### GET /subdomains/{sub}/projects/{project}/stats

查询聚合统计。

参数：
- `period`: `7d` | `30d` | `all`（默认 `30d`）

SQL 映射：
- `7d` → `WHERE date >= date('now', '-7 days')`
- `30d` → `WHERE date >= date('now', '-30 days')`
- `all` → 无日期过滤

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
- `--sub` 或从 `.droply.toml` 读取

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
- `--sub` 或从 `.droply.toml` 读取

## 日志清理

服务端后台 goroutine 定期清理过期日志：

- 默认保留 30 天，通过 CLI flag `--log-retention-days` 配置
- 启动时立即执行一次，之后每 24 小时执行一次
- 删除 subdomain/project 时通过外键级联清理（包括 `page_visits`、`page_daily_stats`、`page_daily_ips`）
- 清理 `page_visits` 时同步清理 `page_daily_ips` 中对应日期之前的记录

## 权限控制

复用现有权限模型：
- API: Bearer token 鉴别用户 + 查询 subdomains 表验证所有权（`sub.UserID != user.ID` → 403）
- CLI: 使用 `~/.config/droply/config.toml` 中的 API token
- 只有 subdomain owner 才能查看其下项目的 stats 和 logs

## Store 层变更

### 新增接口方法

```go
RecordVisit(subdomainID int64, project, path, ip, referer, userAgent string) error
GetPageStats(subdomainID int64, project, period string) ([]PageDailyStat, error)
GetVisitLogs(subdomainID int64, project string, limit, offset int, pathFilter string) ([]VisitLog, int, error)
CleanupVisitLogs(retentionDays int) (int64, error)
```

注意：`RecordVisit` 在 `processVisits` goroutine 中调用，不在请求热路径上。

### 新增 model 结构体（internal/model/model.go）

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
```

### 响应结构体（internal/server/analytics.go）

```go
type statsResponse struct {
    TotalPV int             `json:"total_pv"`
    TotalUV int             `json:"total_uv"`
    Pages   []PageDailyStat `json:"pages"`
}
```

**设计决策：** `StatsResponse` 放在 handler 文件而非 model，与现有模式一致（`setAccessResponse` 在 `access.go`，不在 model）。

## 迁移策略

新增 3 张表通过 `CREATE TABLE IF NOT EXISTS` 添加到现有 `migrate()` 函数末尾。纯增量、非破坏性，与现有迁移模式一致。

## 文件变更清单

| 文件 | 变更 |
|------|------|
| `internal/model/model.go` | 新增 VisitLog, PageDailyStat |
| `internal/store/store.go` | 新增 4 个接口方法 |
| `internal/store/sqlite.go` | 新增 3 张表 + 索引 + 4 个方法实现 |
| `internal/server/site.go` | 文件服务时向 channel 发送访问记录 |
| `internal/server/analytics.go` | 新增 stats/logs handler + 常量 + processVisits goroutine + 响应结构体 |
| `internal/server/server.go` | Server 新增 visitCh/done 字段 + 注册路由 |
| `internal/cli/stats.go` | 新增 stats 命令 |
| `internal/cli/logs.go` | 新增 logs 命令 |
| `internal/cli/root.go` | 注册 stats 和 logs 命令 |
| `cmd/droply-server/main.go` | 启动 analytics goroutine + 清理 goroutine + 优雅关闭 + `--log-retention-days` flag |
| `internal/server/analytics_test.go` | handler 和统计逻辑的测试 |
| `internal/store/sqlite_test.go` | 新增 store 方法的测试 |
