# Visit Analytics Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add page visit analytics to Droply — record complete access logs per page, aggregate daily PV/UV stats, and expose via API + CLI.

**Architecture:** Async channel-based recording in the site handler, SQLite storage with three new tables (page_visits, page_daily_stats, page_daily_ips), background cleanup goroutine, new API endpoints and CLI commands.

**Tech Stack:** Go, SQLite (modernc.org/sqlite), chi router, cobra CLI, existing store interface pattern.

---

### Task 1: Add model structs and store interface methods

**Files:**
- Modify: `internal/model/model.go`
- Modify: `internal/store/store.go`

- [ ] **Step 1: Add VisitLog and PageDailyStat to model**

Append to `internal/model/model.go`:

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

- [ ] **Step 2: Add interface methods to store**

Append to `internal/store/store.go` Store interface (before `Close() error`):

```go
RecordVisit(subdomainID int64, project, path, ip, referer, userAgent string) error
GetPageStats(subdomainID int64, project, period string) ([]model.PageDailyStat, error)
GetVisitLogs(subdomainID int64, project string, limit, offset int, pathFilter string) ([]model.VisitLog, int, error)
CleanupVisitLogs(retentionDays int) (int64, error)
```

- [ ] **Step 3: Verify compilation**

Run: `CGO_ENABLED=0 go build ./...`
Expected: compile error about missing SQLiteStore implementations — this is expected, we'll add them in Task 2.

- [ ] **Step 4: Commit**

```bash
git add internal/model/model.go internal/store/store.go
git commit -m "feat(analytics): add model structs and store interface for visit analytics"
```

---

### Task 2: Implement store methods and schema migration

**Files:**
- Modify: `internal/store/sqlite.go`

- [ ] **Step 1: Add tables to migrate() function**

Append to the `CREATE TABLE IF NOT EXISTS access_rules (...)` block inside `migrate()`, before the closing backtick:

```sql
CREATE TABLE IF NOT EXISTS page_visits (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    subdomain_id INTEGER NOT NULL REFERENCES subdomains(id) ON DELETE CASCADE,
    project TEXT NOT NULL,
    path TEXT NOT NULL,
    ip TEXT NOT NULL,
    referer TEXT DEFAULT '',
    user_agent TEXT DEFAULT '',
    visited_at DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%S', 'now'))
);

CREATE INDEX IF NOT EXISTS idx_page_visits_lookup ON page_visits(subdomain_id, project, visited_at);
CREATE INDEX IF NOT EXISTS idx_page_visits_path ON page_visits(subdomain_id, project, path, visited_at);
CREATE INDEX IF NOT EXISTS idx_page_visits_cleanup ON page_visits(visited_at);

CREATE TABLE IF NOT EXISTS page_daily_stats (
    subdomain_id INTEGER NOT NULL,
    project TEXT NOT NULL,
    path TEXT NOT NULL,
    date TEXT NOT NULL,
    pv INTEGER NOT NULL DEFAULT 0,
    uv INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (subdomain_id, project, path, date),
    FOREIGN KEY (subdomain_id) REFERENCES subdomains(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS page_daily_ips (
    subdomain_id INTEGER NOT NULL,
    project TEXT NOT NULL,
    path TEXT NOT NULL,
    date TEXT NOT NULL,
    ip TEXT NOT NULL,
    PRIMARY KEY (subdomain_id, project, path, date, ip),
    FOREIGN KEY (subdomain_id) REFERENCES subdomains(id) ON DELETE CASCADE
);
```

- [ ] **Step 2: Implement RecordVisit**

Append to `internal/store/sqlite.go`:

```go
// ---- Analytics ----

func (s *SQLiteStore) RecordVisit(subdomainID int64, project, path, ip, referer, userAgent string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	now := time.Now().UTC()
	visitedAt := now.Format(dtLayout)
	today := now.Format("2006-01-02")

	// Insert visit log
	_, err = tx.Exec(
		`INSERT INTO page_visits (subdomain_id, project, path, ip, referer, user_agent, visited_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		subdomainID, project, path, ip, referer, userAgent, visitedAt,
	)
	if err != nil {
		return fmt.Errorf("insert visit: %w", err)
	}

	// Upsert daily stats PV
	_, err = tx.Exec(
		`INSERT INTO page_daily_stats (subdomain_id, project, path, date, pv, uv)
		 VALUES (?, ?, ?, ?, 1, 0)
		 ON CONFLICT(subdomain_id, project, path, date)
		 DO UPDATE SET pv = pv + 1`,
		subdomainID, project, path, today,
	)
	if err != nil {
		return fmt.Errorf("upsert daily stats pv: %w", err)
	}

	// Deduplicate IP for UV via INSERT OR IGNORE
	res, err := tx.Exec(
		`INSERT OR IGNORE INTO page_daily_ips (subdomain_id, project, path, date, ip)
		 VALUES (?, ?, ?, ?, ?)`,
		subdomainID, project, path, today, ip,
	)
	if err != nil {
		return fmt.Errorf("insert daily ip: %w", err)
	}
	// If the IP was new (row was inserted), increment UV
	if n, _ := res.RowsAffected(); n > 0 {
		_, err = tx.Exec(
			`UPDATE page_daily_stats SET uv = uv + 1
			 WHERE subdomain_id = ? AND project = ? AND path = ? AND date = ?`,
			subdomainID, project, path, today,
		)
		if err != nil {
			return fmt.Errorf("increment uv: %w", err)
		}
	}

	return tx.Commit()
}

func (s *SQLiteStore) GetPageStats(subdomainID int64, project, period string) ([]model.PageDailyStat, error) {
	query := `SELECT path, SUM(pv) as pv, SUM(uv) as uv
			  FROM page_daily_stats
			  WHERE subdomain_id = ? AND project = ?`
	args := []any{subdomainID, project}

	switch period {
	case "7d":
		query += ` AND date >= date('now', '-7 days')`
	case "30d":
		query += ` AND date >= date('now', '-30 days')`
	}
	// "all" or default: no date filter

	query += ` GROUP BY path ORDER BY pv DESC`

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("get page stats: %w", err)
	}
	defer rows.Close()

	var result []model.PageDailyStat
	for rows.Next() {
		var s model.PageDailyStat
		if err := rows.Scan(&s.Path, &s.PV, &s.UV); err != nil {
			return nil, fmt.Errorf("scan page stat: %w", err)
		}
		result = append(result, s)
	}
	return result, rows.Err()
}

func (s *SQLiteStore) GetVisitLogs(subdomainID int64, project string, limit, offset int, pathFilter string) ([]model.VisitLog, int, error) {
	// Count query
	countQuery := `SELECT COUNT(*) FROM page_visits WHERE subdomain_id = ? AND project = ?`
	dataQuery := `SELECT path, ip, referer, user_agent, visited_at
				  FROM page_visits WHERE subdomain_id = ? AND project = ?`
	args := []any{subdomainID, project}

	if pathFilter != "" {
		countQuery += ` AND path LIKE ?`
		dataQuery += ` AND path LIKE ?`
		args = append(args, pathFilter+"%")
	}

	var total int
	if err := s.db.QueryRow(countQuery, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count visit logs: %w", err)
	}

	dataQuery += ` ORDER BY visited_at DESC LIMIT ? OFFSET ?`
	queryArgs := append(args, limit, offset)

	rows, err := s.db.Query(dataQuery, queryArgs...)
	if err != nil {
		return nil, 0, fmt.Errorf("get visit logs: %w", err)
	}
	defer rows.Close()

	var result []model.VisitLog
	for rows.Next() {
		var l model.VisitLog
		if err := rows.Scan(&l.Path, &l.IP, &l.Referer, &l.UserAgent, &l.VisitedAt); err != nil {
			return nil, 0, fmt.Errorf("scan visit log: %w", err)
		}
		result = append(result, l)
	}
	return result, total, rows.Err()
}

func (s *SQLiteStore) CleanupVisitLogs(retentionDays int) (int64, error) {
	res, err := s.db.Exec(
		`DELETE FROM page_visits WHERE visited_at < strftime('%Y-%m-%d %H:%M:%S', 'now', ? || ' days')`,
		fmt.Sprintf("-%d", retentionDays),
	)
	if err != nil {
		return 0, fmt.Errorf("cleanup visit logs: %w", err)
	}
	affected, _ := res.RowsAffected()

	// Also cleanup old daily IPs
	s.db.Exec(
		`DELETE FROM page_daily_ips WHERE date < date('now', ? || ' days')`,
		fmt.Sprintf("-%d", retentionDays),
	)

	return affected, nil
}
```

- [ ] **Step 3: Verify compilation**

Run: `CGO_ENABLED=0 go build ./...`
Expected: compiles successfully.

- [ ] **Step 4: Run existing tests**

Run: `CGO_ENABLED=0 go test ./...`
Expected: all tests pass (no regressions).

- [ ] **Step 5: Commit**

```bash
git add internal/store/sqlite.go
git commit -m "feat(analytics): implement store methods and schema for visit analytics"
```

---

### Task 3: Add analytics handler and server integration

**Files:**
- Create: `internal/server/analytics.go`
- Modify: `internal/server/server.go` (add fields + routes)

- [ ] **Step 1: Create analytics.go with handler + goroutine**

Create `internal/server/analytics.go`:

```go
package server

import (
	"log"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/zhong/droply/internal/model"
)

// skippedExtensions are file extensions excluded from visit tracking.
var skippedExtensions = map[string]bool{
	".css": true, ".js": true, ".map": true,
	".woff": true, ".woff2": true, ".ttf": true, ".eot": true,
	".png": true, ".jpg": true, ".jpeg": true, ".gif": true,
	".svg": true, ".ico": true, ".webp": true,
	".mp4": true, ".webm": true, ".mp3": true,
}

// visitRecord is a single visit to record asynchronously.
type visitRecord struct {
	SubdomainID int64
	Project     string
	Path        string
	IP          string
	Referer     string
	UserAgent   string
}

// normalizePath canonicalizes a request path for consistent analytics.
func normalizePath(p string) string {
	p = strings.ToLower(p)
	// Strip trailing slash (but keep "/" as-is)
	if len(p) > 1 {
		p = strings.TrimRight(p, "/")
	}
	// Resolve index.html at root
	if p == "/index.html" {
		p = "/"
	}
	return p
}

// shouldTrack returns true if the path should be tracked for analytics.
func shouldTrack(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	return !skippedExtensions[ext]
}

// StartAnalytics initializes the async visit processing goroutine.
func (s *Server) StartAnalytics() {
	go s.processVisits()
}

// ShutdownAnalytics drains the visit channel and waits for processing to complete.
func (s *Server) ShutdownAnalytics() {
	close(s.visitCh)
	<-s.done
}

// processVisits consumes visit records from the channel and writes them to the store.
func (s *Server) processVisits() {
	for rec := range s.visitCh {
		if err := s.store.RecordVisit(rec.SubdomainID, rec.Project, rec.Path, rec.IP, rec.Referer, rec.UserAgent); err != nil {
			log.Printf("analytics: failed to record visit: %v", err)
		}
	}
	s.done <- struct{}{}
}

// recordVisit enqueues a visit record for async processing.
// Uses non-blocking send — drops the record if the channel is full.
func (s *Server) recordVisit(subdomainID int64, project, path, ip, referer, userAgent string) {
	select {
	case s.visitCh <- visitRecord{
		SubdomainID: subdomainID,
		Project:     project,
		Path:        path,
		IP:          ip,
		Referer:     referer,
		UserAgent:   userAgent,
	}:
	default:
		// Channel full, drop the visit.
	}
}

type statsResponse struct {
	TotalPV int               `json:"total_pv"`
	TotalUV int               `json:"total_uv"`
	Pages   []model.PageDailyStat `json:"pages"`
}

type logsResponse struct {
	Logs  []model.VisitLog `json:"logs"`
	Total int              `json:"total"`
}

// handleGetStats returns aggregated page view statistics for a project.
func (s *Server) handleGetStats(w http.ResponseWriter, r *http.Request) {
	user := userFromContext(r.Context())
	subName := chi.URLParam(r, "sub")
	projName := chi.URLParam(r, "project")

	sub, err := s.store.GetSubdomainByName(subName)
	if err != nil {
		jsonError(w, "subdomain not found", http.StatusNotFound)
		return
	}
	if sub.UserID != user.ID {
		jsonError(w, "forbidden", http.StatusForbidden)
		return
	}

	period := r.URL.Query().Get("period")
	if period == "" {
		period = "30d"
	}
	if period != "7d" && period != "30d" && period != "all" {
		jsonError(w, "invalid period, use 7d, 30d, or all", http.StatusBadRequest)
		return
	}

	pages, err := s.store.GetPageStats(sub.ID, projName, period)
	if err != nil {
		jsonError(w, "internal server error", http.StatusInternalServerError)
		return
	}
	if pages == nil {
		pages = []model.PageDailyStat{}
	}

	var totalPV, totalUV int
	for _, p := range pages {
		totalPV += p.PV
		totalUV += p.UV
	}

	jsonResponse(w, statsResponse{
		TotalPV: totalPV,
		TotalUV: totalUV,
		Pages:   pages,
	}, http.StatusOK)
}

// handleGetLogs returns detailed visit logs for a project.
func (s *Server) handleGetLogs(w http.ResponseWriter, r *http.Request) {
	user := userFromContext(r.Context())
	subName := chi.URLParam(r, "sub")
	projName := chi.URLParam(r, "project")

	sub, err := s.store.GetSubdomainByName(subName)
	if err != nil {
		jsonError(w, "subdomain not found", http.StatusNotFound)
		return
	}
	if sub.UserID != user.ID {
		jsonError(w, "forbidden", http.StatusForbidden)
		return
	}

	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 500 {
			limit = n
		}
	}

	offset := 0
	if v := r.URL.Query().Get("offset"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			offset = n
		}
	}

	pathFilter := r.URL.Query().Get("path")

	logs, total, err := s.store.GetVisitLogs(sub.ID, projName, limit, offset, pathFilter)
	if err != nil {
		jsonError(w, "internal server error", http.StatusInternalServerError)
		return
	}
	if logs == nil {
		logs = []model.VisitLog{}
	}

	jsonResponse(w, logsResponse{
		Logs:  logs,
		Total: total,
	}, http.StatusOK)
}
```

- [ ] **Step 2: Add visitCh and done fields to Server struct**

In `internal/server/server.go`, update the Server struct to add two fields:

```go
type Server struct {
	store      store.Store
	sitesDir   string
	baseDomain string
	caddy      CaddyClient
	router     *chi.Mux
	hmacKey    []byte
	siteAddr   string
	visitCh    chan visitRecord
	done       chan struct{}
}
```

Update `New()` to initialize the channels:

```go
func New(s store.Store, sitesDir, baseDomain string, caddy CaddyClient, hmacKey []byte, siteAddr string) *Server {
	srv := &Server{
		store:      s,
		sitesDir:   sitesDir,
		baseDomain: baseDomain,
		caddy:      caddy,
		hmacKey:    hmacKey,
		siteAddr:   siteAddr,
		visitCh:    make(chan visitRecord, 1000),
		done:       make(chan struct{}),
	}
	srv.router = srv.buildRouter()
	return srv
}
```

- [ ] **Step 3: Register analytics routes**

In `internal/server/server.go`, add inside the authenticated route group in `buildRouter()`:

```go
r.Get("/subdomains/{sub}/projects/{project}/stats", s.handleGetStats)
r.Get("/subdomains/{sub}/projects/{project}/logs", s.handleGetLogs)
```

Place these right after the existing access routes (after line 93 `r.Delete(...s.handleDeleteProjectAccess)`).

- [ ] **Step 4: Verify compilation**

Run: `CGO_ENABLED=0 go build ./...`
Expected: compiles successfully.

- [ ] **Step 5: Commit**

```bash
git add internal/server/analytics.go internal/server/server.go
git commit -m "feat(analytics): add analytics handler and async recording goroutine"
```

---

### Task 4: Integrate visit recording into site handler

**Files:**
- Modify: `internal/server/site.go`

- [ ] **Step 1: Add visit recording to serveFile**

In `internal/server/site.go`, modify the `serveFile` method to record visits. The key insight: we need the subdomain's integer ID, which we already have from the earlier `GetSubdomainByName` call in `siteHandler`. We'll pass it through.

First, change the `serveFile` signature to accept subdomainID:

```go
func (s *Server) serveFile(w http.ResponseWriter, r *http.Request, subdomainID int64, subdomain, project, servePath string) {
	root := filepath.Join(s.sitesDir, subdomain, project)
	fs := http.FileServer(http.Dir(root))
	r.URL.Path = servePath
	fs.ServeHTTP(w, r)

	// Record visit asynchronously after serving the file.
	if shouldTrack(servePath) {
		normalizedPath := normalizePath(servePath)
		s.recordVisit(subdomainID, project, normalizedPath, getClientIP(r), r.Referer(), r.UserAgent())
	}
}
```

- [ ] **Step 2: Update all serveFile call sites**

In `siteHandler`, store the subdomain object and pass its ID:

Near the top of `siteHandler`, change the subdomain verification to save the object:

```go
sub, err := s.store.GetSubdomainByName(subdomainName)
if err != nil {
    http.NotFound(w, r)
    return
}
```

Then update every call to `serveFile` — there are 3 call sites in `siteHandler`:

1. IP whitelisted path (around line 202):
```go
s.serveFile(w, r, sub.ID, subdomainName, projectName, servePath)
```

2. Valid cookie path (around line 215):
```go
s.serveFile(w, r, sub.ID, subdomainName, projectName, servePath)
```

3. No rule path (around line 229):
```go
s.serveFile(w, r, sub.ID, subdomainName, projectName, servePath)
```

Each call gains `sub.ID` as the first argument after `w, r`.

- [ ] **Step 3: Verify compilation**

Run: `CGO_ENABLED=0 go build ./...`
Expected: compiles successfully.

- [ ] **Step 4: Run existing tests**

Run: `CGO_ENABLED=0 go test ./...`
Expected: all tests pass.

- [ ] **Step 5: Commit**

```bash
git add internal/server/site.go
git commit -m "feat(analytics): record page visits in site handler"
```

---

### Task 5: Add CLI stats and logs commands

**Files:**
- Create: `internal/cli/stats.go`
- Create: `internal/cli/logs.go`
- Modify: `internal/cli/root.go`

- [ ] **Step 1: Create stats.go**

Create `internal/cli/stats.go`:

```go
package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

func newStatsCmd() *cobra.Command {
	var sub string
	var period string

	cmd := &cobra.Command{
		Use:   "stats [project]",
		Short: "Show page view statistics for a project",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			projectName := ""
			if len(args) > 0 {
				projectName = args[0]
			}

			if sub == "" || projectName == "" {
				pc, err := LoadProjectConfig()
				if err != nil {
					return fmt.Errorf("--sub and project name required (or set .droply.toml): %w", err)
				}
				if sub == "" {
					sub = pc.Subdomain
				}
				if projectName == "" {
					projectName = pc.Project
				}
			}
			if sub == "" || projectName == "" {
				return fmt.Errorf("subdomain and project name required: use --sub and argument, or set .droply.toml")
			}

			cfg := LoadConfig()
			client := NewAPIClient(cfg)

			path := fmt.Sprintf("/subdomains/%s/projects/%s/stats?period=%s", sub, projectName, period)

			var resp struct {
				TotalPV int `json:"total_pv"`
				TotalUV int `json:"total_uv"`
				Pages   []struct {
					Path string `json:"path"`
					PV   int    `json:"pv"`
					UV   int    `json:"uv"`
				} `json:"pages"`
			}
			if err := client.doJSON("GET", path, nil, &resp); err != nil {
				return err
			}

			periodLabel := "all time"
			if period != "all" {
				periodLabel = fmt.Sprintf("last %s", strings.TrimSuffix(period, "d"))
				if !strings.Contains(periodLabel, "days") {
					periodLabel += " days"
				}
			}

			fmt.Printf("Project: %s/%s  |  Period: %s\n\n", sub, projectName, periodLabel)
			fmt.Printf("Total PV: %s  |  Total UV: %s\n\n", formatNum(resp.TotalPV), formatNum(resp.TotalUV))

			if len(resp.Pages) == 0 {
				fmt.Println("No page views recorded yet.")
				return nil
			}

			fmt.Println("Top Pages:")
			for _, p := range resp.Pages {
				fmt.Printf("  %-30s %s PV   %s UV\n", p.Path, formatNum(p.PV), formatNum(p.UV))
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&sub, "sub", "", "Subdomain name")
	cmd.Flags().StringVar(&period, "period", "30d", "Time period: 7d, 30d, all")
	return cmd
}

func formatNum(n int) string {
	s := fmt.Sprintf("%d", n)
	if len(s) <= 3 {
		return s
	}
	result := ""
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			result += ","
		}
		result += string(c)
	}
	return result
}
```

- [ ] **Step 2: Create logs.go**

Create `internal/cli/logs.go`:

```go
package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newLogsCmd() *cobra.Command {
	var sub string
	var limit int
	var pathFilter string

	cmd := &cobra.Command{
		Use:   "logs [project]",
		Short: "Show detailed visit logs for a project",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			projectName := ""
			if len(args) > 0 {
				projectName = args[0]
			}

			if sub == "" || projectName == "" {
				pc, err := LoadProjectConfig()
				if err != nil {
					return fmt.Errorf("--sub and project name required (or set .droply.toml): %w", err)
				}
				if sub == "" {
					sub = pc.Subdomain
				}
				if projectName == "" {
					projectName = pc.Project
				}
			}
			if sub == "" || projectName == "" {
				return fmt.Errorf("subdomain and project name required: use --sub and argument, or set .droply.toml")
			}

			cfg := LoadConfig()
			client := NewAPIClient(cfg)

			apiPath := fmt.Sprintf("/subdomains/%s/projects/%s/logs?limit=%d", sub, projectName, limit)
			if pathFilter != "" {
				apiPath += fmt.Sprintf("&path=%s", pathFilter)
			}

			var resp struct {
				Logs  []struct {
					Path      string `json:"path"`
					IP        string `json:"ip"`
					Referer   string `json:"referer"`
					UserAgent string `json:"user_agent"`
					VisitedAt string `json:"visited_at"`
				} `json:"logs"`
				Total int `json:"total"`
			}
			if err := client.doJSON("GET", apiPath, nil, &resp); err != nil {
				return err
			}

			fmt.Printf("Project: %s/%s  |  Showing %d of %d logs\n\n", sub, projectName, len(resp.Logs), resp.Total)

			if len(resp.Logs) == 0 {
				fmt.Println("No visit logs found.")
				return nil
			}

			for _, l := range resp.Logs {
				fmt.Printf("  %s  %s  %s  %s\n", l.VisitedAt, l.Path, l.IP, truncate(l.Referer, 40))
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&sub, "sub", "", "Subdomain name")
	cmd.Flags().IntVar(&limit, "limit", 50, "Number of logs to show (max 500)")
	cmd.Flags().StringVar(&pathFilter, "path", "", "Filter by path prefix")
	return cmd
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}
```

- [ ] **Step 3: Register commands in root.go**

In `internal/cli/root.go`, add after `root.AddCommand(newProjectCmd())`:

```go
root.AddCommand(newStatsCmd())
root.AddCommand(newLogsCmd())
```

- [ ] **Step 4: Verify compilation**

Run: `CGO_ENABLED=0 go build ./...`
Expected: compiles successfully.

- [ ] **Step 5: Commit**

```bash
git add internal/cli/stats.go internal/cli/logs.go internal/cli/root.go
git commit -m "feat(analytics): add CLI stats and logs commands"
```

---

### Task 6: Wire up analytics lifecycle in main.go

**Files:**
- Modify: `cmd/droply-server/main.go`

- [ ] **Step 1: Start analytics goroutine and add cleanup**

In `cmd/droply-server/main.go`, update the import block to add `"os/signal"`, `"os"`, and `"time"`:

```go
import (
	"crypto/rand"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"time"

	"github.com/zhong/droply/internal/caddy"
	"github.com/zhong/droply/internal/server"
	"github.com/zhong/droply/internal/store"
)
```

Add the `--log-retention-days` flag after the existing flags (after line 29):

```go
logRetention := flag.Int("log-retention-days", 30, "days to retain detailed visit logs")
```

Then after the `srv.RecoverCaddyRoutes()` block (after line 56), add:

```go
srv.StartAnalytics()

// Start cleanup goroutine
go func() {
	if n, err := st.CleanupVisitLogs(*logRetention); err == nil && n > 0 {
		log.Printf("Cleaned up %d expired visit logs", n)
	}
	for {
		time.Sleep(24 * time.Hour)
		if n, err := st.CleanupVisitLogs(*logRetention); err == nil && n > 0 {
			log.Printf("Cleaned up %d expired visit logs", n)
		}
	}
}()

// Graceful shutdown: drain analytics channel on SIGINT/SIGTERM
go func() {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt)
	<-sigCh
	log.Println("Shutting down analytics...")
	srv.ShutdownAnalytics()
	st.Close()
	os.Exit(0)
}()
```

- [ ] **Step 2: Verify compilation**

Run: `CGO_ENABLED=0 go build ./...`
Expected: compiles successfully.

- [ ] **Step 3: Commit**

```bash
git add cmd/droply-server/main.go
git commit -m "feat(analytics): start analytics goroutine and log cleanup in server"
```

---

### Task 7: Add tests for analytics store methods

**Files:**
- Create: `internal/store/analytics_test.go`

- [ ] **Step 1: Write store tests**

Create `internal/store/analytics_test.go`. Note: `newTestStore` is already defined in `sqlite_test.go` in the same package — we only add `createTestSubdomain` as a new helper.

```go
package store

import (
	"testing"
)

func createTestSubdomain(t *testing.T, st *SQLiteStore) int64 {
	t.Helper()
	user, err := st.CreateUser("test@example.com", "hash", "dp_test_token")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	sub, err := st.CreateSubdomain(user.ID, "mysub")
	if err != nil {
		t.Fatalf("create subdomain: %v", err)
	}
	return sub.ID
}

func TestRecordVisit(t *testing.T) {
	st := newTestStore(t)
	subID := createTestSubdomain(t, st)

	err := st.RecordVisit(subID, "blog", "/hello", "1.2.3.4", "https://google.com", "Mozilla/5.0")
	if err != nil {
		t.Fatalf("RecordVisit: %v", err)
	}

	// Verify the visit was recorded
	logs, total, err := st.GetVisitLogs(subID, "blog", 10, 0, "")
	if err != nil {
		t.Fatalf("GetVisitLogs: %v", err)
	}
	if total != 1 {
		t.Fatalf("expected 1 log, got %d", total)
	}
	if len(logs) != 1 {
		t.Fatalf("expected 1 log entry, got %d", len(logs))
	}
	if logs[0].Path != "/hello" {
		t.Fatalf("expected path /hello, got %s", logs[0].Path)
	}
	if logs[0].IP != "1.2.3.4" {
		t.Fatalf("expected IP 1.2.3.4, got %s", logs[0].IP)
	}
}

func TestRecordVisitUV(t *testing.T) {
	st := newTestStore(t)
	subID := createTestSubdomain(t, st)

	// Same IP, same path — UV should be 1
	st.RecordVisit(subID, "blog", "/hello", "1.2.3.4", "", "")
	st.RecordVisit(subID, "blog", "/hello", "1.2.3.4", "", "")
	// Different IP — UV should be 2
	st.RecordVisit(subID, "blog", "/hello", "5.6.7.8", "", "")

	stats, err := st.GetPageStats(subID, "blog", "all")
	if err != nil {
		t.Fatalf("GetPageStats: %v", err)
	}
	if len(stats) != 1 {
		t.Fatalf("expected 1 page, got %d", len(stats))
	}
	if stats[0].PV != 3 {
		t.Fatalf("expected PV=3, got %d", stats[0].PV)
	}
	if stats[0].UV != 2 {
		t.Fatalf("expected UV=2, got %d", stats[0].UV)
	}
}

func TestGetPageStatsPeriod(t *testing.T) {
	st := newTestStore(t)
	subID := createTestSubdomain(t, st)

	st.RecordVisit(subID, "blog", "/", "1.2.3.4", "", "")
	st.RecordVisit(subID, "blog", "/about", "1.2.3.4", "", "")

	stats, err := st.GetPageStats(subID, "blog", "7d")
	if err != nil {
		t.Fatalf("GetPageStats: %v", err)
	}
	if len(stats) != 2 {
		t.Fatalf("expected 2 pages, got %d", len(stats))
	}
}

func TestGetPageStatsEmpty(t *testing.T) {
	st := newTestStore(t)
	subID := createTestSubdomain(t, st)

	stats, err := st.GetPageStats(subID, "blog", "all")
	if err != nil {
		t.Fatalf("GetPageStats: %v", err)
	}
	if len(stats) != 0 {
		t.Fatalf("expected 0 pages for unvisited project, got %d", len(stats))
	}
}

func TestGetVisitLogsWithPathFilter(t *testing.T) {
	st := newTestStore(t)
	subID := createTestSubdomain(t, st)

	st.RecordVisit(subID, "blog", "/hello", "1.2.3.4", "", "")
	st.RecordVisit(subID, "blog", "/world", "1.2.3.4", "", "")
	st.RecordVisit(subID, "blog", "/hello/world", "1.2.3.4", "", "")

	logs, total, err := st.GetVisitLogs(subID, "blog", 10, 0, "/hello")
	if err != nil {
		t.Fatalf("GetVisitLogs: %v", err)
	}
	if total != 2 {
		t.Fatalf("expected 2 logs matching /hello, got %d", total)
	}
	if len(logs) != 2 {
		t.Fatalf("expected 2 log entries, got %d", len(logs))
	}
}

func TestGetVisitLogsPagination(t *testing.T) {
	st := newTestStore(t)
	subID := createTestSubdomain(t, st)

	for i := 0; i < 5; i++ {
		st.RecordVisit(subID, "blog", "/page", "1.2.3.4", "", "")
	}

	logs, total, err := st.GetVisitLogs(subID, "blog", 2, 0, "")
	if err != nil {
		t.Fatalf("GetVisitLogs: %v", err)
	}
	if total != 5 {
		t.Fatalf("expected total=5, got %d", total)
	}
	if len(logs) != 2 {
		t.Fatalf("expected 2 log entries (limit), got %d", len(logs))
	}
}

func TestCleanupVisitLogs(t *testing.T) {
	st := newTestStore(t)
	subID := createTestSubdomain(t, st)

	st.RecordVisit(subID, "blog", "/hello", "1.2.3.4", "", "")

	// Cleanup with 0 days retention should remove everything
	n, err := st.CleanupVisitLogs(0)
	if err != nil {
		t.Fatalf("CleanupVisitLogs: %v", err)
	}
	if n != 1 {
		t.Fatalf("expected 1 deleted, got %d", n)
	}

	// Verify logs are gone
	_, total, err := st.GetVisitLogs(subID, "blog", 10, 0, "")
	if err != nil {
		t.Fatalf("GetVisitLogs: %v", err)
	}
	if total != 0 {
		t.Fatalf("expected 0 logs after cleanup, got %d", total)
	}
}

func TestRecordVisitDifferentProjects(t *testing.T) {
	st := newTestStore(t)
	subID := createTestSubdomain(t, st)

	st.RecordVisit(subID, "blog", "/", "1.2.3.4", "", "")
	st.RecordVisit(subID, "docs", "/", "1.2.3.4", "", "")

	blogStats, _ := st.GetPageStats(subID, "blog", "all")
	if len(blogStats) != 1 || blogStats[0].PV != 1 {
		t.Fatalf("expected blog PV=1, got %v", blogStats)
	}

	docsStats, _ := st.GetPageStats(subID, "docs", "all")
	if len(docsStats) != 1 || docsStats[0].PV != 1 {
		t.Fatalf("expected docs PV=1, got %v", docsStats)
	}
}
```

- [ ] **Step 2: Run store tests**

Run: `CGO_ENABLED=0 go test ./internal/store -v`
Expected: all tests pass.

- [ ] **Step 3: Commit**

```bash
git add internal/store/analytics_test.go
git commit -m "test(analytics): add store tests for visit analytics"
```

---

### Task 8: Add tests for analytics HTTP handlers

**Files:**
- Create: `internal/server/analytics_test.go`

- [ ] **Step 1: Write handler tests**

Create `internal/server/analytics_test.go`:

```go
package server_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/zhong/droply/internal/store"
)

// setupAnalyticsEnv creates a test user + subdomain and returns (token, subdomainName).
// Uses the shared newTestServer helper from server_test.go.
func setupAnalyticsEnv(t *testing.T, srv http.Handler) (token, subName string, st *store.SQLiteStore) {
	t.Helper()
	token = registerAndGetToken(t, srv, "analytics@example.com", "password123")

	body, _ := json.Marshal(map[string]string{"name": "mysite"})
	req := httptest.NewRequest(http.MethodPost, "/subdomains", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("create subdomain: expected 201, got %d", rr.Code)
	}

	return token, "mysite", nil
}

// newAnalyticsServer creates a test server and exposes the store for direct data insertion.
func newAnalyticsServer(t *testing.T) (*server.Server, *store.SQLiteStore) {
	t.Helper()
	st, err := store.NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	srv := server.New(st, "/tmp/sites", "droplydoc.com", nil, []byte("test-hmac-key-for-testing-1234"), "localhost:8081")
	return srv, st
}

func TestGetStats(t *testing.T) {
	srv, st := newAnalyticsServer(t)
	token, subName, _ := setupAnalyticsEnv(t, srv)

	// Get the subdomain ID (first subdomain = ID 1 in fresh DB)
	st.RecordVisit(1, "blog", "/", "1.2.3.4", "", "")
	st.RecordVisit(1, "blog", "/", "5.6.7.8", "", "")
	st.RecordVisit(1, "blog", "/about", "1.2.3.4", "", "")

	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/subdomains/%s/projects/blog/stats?period=7d", subName), nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var resp struct {
		TotalPV int `json:"total_pv"`
		TotalUV int `json:"total_uv"`
		Pages   []struct {
			Path string `json:"path"`
			PV   int    `json:"pv"`
			UV   int    `json:"uv"`
		} `json:"pages"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.TotalPV != 3 {
		t.Fatalf("expected total_pv=3, got %d", resp.TotalPV)
	}
	if resp.TotalUV != 2 {
		t.Fatalf("expected total_uv=2, got %d", resp.TotalUV)
	}
	if len(resp.Pages) != 2 {
		t.Fatalf("expected 2 pages, got %d", len(resp.Pages))
	}
}

func TestGetStatsForbidden(t *testing.T) {
	srv, _ := newAnalyticsServer(t)
	_, _, _ = setupAnalyticsEnv(t, srv)

	otherToken := registerAndGetToken(t, srv, "other@example.com", "password456")

	req := httptest.NewRequest(http.MethodGet, "/subdomains/mysite/projects/blog/stats", nil)
	req.Header.Set("Authorization", "Bearer "+otherToken)
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", rr.Code)
	}
}

func TestGetLogs(t *testing.T) {
	srv, st := newAnalyticsServer(t)
	token, subName, _ := setupAnalyticsEnv(t, srv)

	st.RecordVisit(1, "blog", "/hello", "1.2.3.4", "https://google.com", "Mozilla/5.0")

	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/subdomains/%s/projects/blog/logs?limit=10", subName), nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var resp struct {
		Logs  []json.RawMessage `json:"logs"`
		Total int               `json:"total"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Total != 1 {
		t.Fatalf("expected total=1, got %d", resp.Total)
	}
	if len(resp.Logs) != 1 {
		t.Fatalf("expected 1 log, got %d", len(resp.Logs))
	}
}

func TestGetStatsInvalidPeriod(t *testing.T) {
	srv, _ := newAnalyticsServer(t)
	token, subName, _ := setupAnalyticsEnv(t, srv)

	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/subdomains/%s/projects/blog/stats?period=invalid", subName), nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
}

func TestGetLogsWithPathFilter(t *testing.T) {
	srv, st := newAnalyticsServer(t)
	token, subName, _ := setupAnalyticsEnv(t, srv)

	st.RecordVisit(1, "blog", "/hello", "1.2.3.4", "", "")
	st.RecordVisit(1, "blog", "/world", "1.2.3.4", "", "")

	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/subdomains/%s/projects/blog/logs?path=/hello", subName), nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var resp struct {
		Logs  []json.RawMessage `json:"logs"`
		Total int               `json:"total"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Total != 1 {
		t.Fatalf("expected total=1 (path filter), got %d", resp.Total)
	}
}
```

- [ ] **Step 2: Run handler tests**

Run: `CGO_ENABLED=0 go test ./internal/server -v -run "TestGet(Stats|Logs)"`
Expected: all tests pass.

- [ ] **Step 3: Commit**

```bash
git add internal/server/analytics_test.go
git commit -m "test(analytics): add handler tests for stats and logs endpoints"
```

---

### Task 9: Final verification

- [ ] **Step 1: Run full build**

Run: `make build`
Expected: compiles without errors.

- [ ] **Step 2: Run all tests**

Run: `make test`
Expected: all tests pass.

- [ ] **Step 3: Run go vet**

Run: `go vet ./...`
Expected: no warnings.

- [ ] **Step 4: Final commit (if any fixes needed)**

```bash
git add -A
git commit -m "fix(analytics): address final verification issues"
```
