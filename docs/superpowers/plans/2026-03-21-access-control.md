# Access Control Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add IP whitelist and password-based access control to Droply subdomains and projects.

**Architecture:** Access rules are stored in SQLite. When a subdomain has any access rule, its Caddy route switches from `file_server` to `reverse_proxy` pointing to a new site-serving HTTP server (port `:8081`). This server validates IP and cookie-based password sessions before serving static files via `http.FileServer`. A CLI command `droply access` manages rules via the existing API pattern.

**Tech Stack:** Go, SQLite, chi router, Caddy Admin API, bcrypt, HMAC-SHA256, `golang.org/x/time/rate`, `embed` (login page template)

**Spec:** `docs/superpowers/specs/2026-03-21-access-control-design.md`

**Spec deviations (deliberate improvements):**
- Store interface uses `CreateOrUpdateAccessRule` (upsert) instead of separate `CreateAccessRule`/`UpdateAccessRule` — simpler API matching the PUT upsert semantics
- `SetSubdomainUnprotected(name string)` omits `siteRoot` param — the Caddy client already knows `sitesDir` and derives the root internally
- `SetCustomDomainUnprotected(domain, subdomainName, projectName string)` takes name params instead of `siteRoot` — same reason, leverages `buildCustomDomainRoute`

---

## File Structure

| Action | File | Responsibility |
|--------|------|----------------|
| Modify | `internal/model/model.go` | Add `AccessRule` struct |
| Modify | `internal/store/store.go` | Add access rule interface methods |
| Modify | `internal/store/sqlite.go` | Add `access_rules` table migration + CRUD implementations |
| Create | `internal/store/sqlite_access_test.go` | Tests for access rule store operations |
| Modify | `internal/server/server.go` | Add `CaddyClient` interface methods, `Server` fields (`hmacKey`, `siteAddr`) |
| Create | `internal/server/access.go` | API handlers: PUT/GET/DELETE access rules (including custom domain route updates) |
| Create | `internal/server/access_test.go` | Tests for access rule API handlers |
| Create | `internal/server/site.go` | Site serving: `siteHandler`, `siteLoginHandler`, IP check, cookie check, login page template, rate limiter, custom domain resolution |
| Create | `internal/server/site_test.go` | Tests for site serving (IP, password, cookie, rate limiting, custom domains) |
| Modify | `internal/server/project.go` | After deleting project, check and update Caddy route |
| Modify | `internal/server/subdomain.go` | After deleting subdomain, no extra work needed (route already removed) |
| Modify | `internal/server/domain.go` | After creating/deleting custom domain, check access rules for route type |
| Modify | `internal/server/recovery.go` | Check access rules during route recovery |
| Modify | `internal/caddy/client.go` | Add `SetSubdomainProtected/Unprotected`, `SetCustomDomainProtected/Unprotected` |
| Create | `internal/caddy/client_access_test.go` | Tests for new Caddy client methods |
| Create | `internal/cli/access.go` | CLI commands: `droply access set/get/remove` |
| Modify | `internal/cli/root.go` | Register `access` command |
| Modify | `cmd/droply-server/main.go` | Add `--hmac-secret`, `--site-addr` flags, start site server |

---

### Task 1: Add AccessRule Model

**Files:**
- Modify: `internal/model/model.go:45-51`

- [ ] **Step 1: Add AccessRule struct to model.go**

Add after `DomainWithPath`:

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

- [ ] **Step 2: Verify it compiles**

Run: `cd /Users/chenzhong/Developer/droply && go build ./...`
Expected: no errors

- [ ] **Step 3: Commit**

```bash
git add internal/model/model.go
git commit -m "feat: add AccessRule model"
```

---

### Task 2: Add Store Interface + Implementation + Tests

This task combines interface definition and implementation into a single compilable commit.

**Files:**
- Modify: `internal/store/store.go:5-28`
- Modify: `internal/store/sqlite.go`
- Create: `internal/store/sqlite_access_test.go`

- [ ] **Step 1: Write failing tests for access rule store operations**

Create `internal/store/sqlite_access_test.go`:

```go
package store

import (
	"testing"
)

func TestCreateAndGetAccessRule(t *testing.T) {
	s := newTestStore(t)

	user, err := s.CreateUser("alice@example.com", "hash", "tok1")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	sub, err := s.CreateSubdomain(user.ID, "alice")
	if err != nil {
		t.Fatalf("CreateSubdomain: %v", err)
	}

	rule, err := s.CreateOrUpdateAccessRule(sub.ID, nil, []string{"10.0.0.0/8"}, "hashed_pw", 3600)
	if err != nil {
		t.Fatalf("CreateOrUpdateAccessRule: %v", err)
	}
	if rule.ID == 0 {
		t.Error("expected non-zero rule ID")
	}
	if rule.SessionTTL != 3600 {
		t.Errorf("expected TTL 3600, got %d", rule.SessionTTL)
	}
	if rule.ProjectID != nil {
		t.Error("expected nil ProjectID for subdomain-level rule")
	}

	got, err := s.GetAccessRule(sub.ID, nil)
	if err != nil {
		t.Fatalf("GetAccessRule: %v", err)
	}
	if got.ID != rule.ID {
		t.Errorf("ID mismatch: %d != %d", got.ID, rule.ID)
	}
	if len(got.AllowedIPs) != 1 || got.AllowedIPs[0] != "10.0.0.0/8" {
		t.Errorf("unexpected AllowedIPs: %v", got.AllowedIPs)
	}
	if !got.HasPassword {
		t.Error("expected HasPassword to be true")
	}
}

func TestCreateOrUpdateAccessRuleUpsert(t *testing.T) {
	s := newTestStore(t)
	user, _ := s.CreateUser("bob@example.com", "hash", "tok2")
	sub, _ := s.CreateSubdomain(user.ID, "bob")

	rule1, err := s.CreateOrUpdateAccessRule(sub.ID, nil, []string{"10.0.0.0/8"}, "pw1", 3600)
	if err != nil {
		t.Fatalf("first CreateOrUpdate: %v", err)
	}

	rule2, err := s.CreateOrUpdateAccessRule(sub.ID, nil, []string{"192.168.0.0/16"}, "pw2", 7200)
	if err != nil {
		t.Fatalf("second CreateOrUpdate: %v", err)
	}
	if rule2.ID != rule1.ID {
		t.Errorf("upsert should keep same ID: %d != %d", rule2.ID, rule1.ID)
	}
	if rule2.SessionTTL != 7200 {
		t.Errorf("expected updated TTL 7200, got %d", rule2.SessionTTL)
	}
}

func TestProjectLevelAccessRule(t *testing.T) {
	s := newTestStore(t)
	user, _ := s.CreateUser("carol@example.com", "hash", "tok3")
	sub, _ := s.CreateSubdomain(user.ID, "carol")
	proj, _ := s.CreateProject(sub.ID, "blog")

	projectID := proj.ID
	_, err := s.CreateOrUpdateAccessRule(sub.ID, &projectID, nil, "pw", 86400)
	if err != nil {
		t.Fatalf("CreateOrUpdateAccessRule with project: %v", err)
	}

	rule, err := s.GetAccessRule(sub.ID, &projectID)
	if err != nil {
		t.Fatalf("GetAccessRule with project: %v", err)
	}
	if rule.ProjectID == nil || *rule.ProjectID != projectID {
		t.Errorf("expected ProjectID %d, got %v", projectID, rule.ProjectID)
	}
}

func TestDeleteAccessRule(t *testing.T) {
	s := newTestStore(t)
	user, _ := s.CreateUser("dave@example.com", "hash", "tok4")
	sub, _ := s.CreateSubdomain(user.ID, "dave")

	_, err := s.CreateOrUpdateAccessRule(sub.ID, nil, []string{"10.0.0.0/8"}, "", 86400)
	if err != nil {
		t.Fatalf("CreateOrUpdateAccessRule: %v", err)
	}

	if err := s.DeleteAccessRule(sub.ID, nil); err != nil {
		t.Fatalf("DeleteAccessRule: %v", err)
	}

	_, err = s.GetAccessRule(sub.ID, nil)
	if err == nil {
		t.Error("expected error after delete, got nil")
	}
}

func TestFindAccessRuleForSite(t *testing.T) {
	s := newTestStore(t)
	user, _ := s.CreateUser("eve@example.com", "hash", "tok5")
	sub, _ := s.CreateSubdomain(user.ID, "eve")
	proj, _ := s.CreateProject(sub.ID, "blog")

	// Create subdomain-level rule
	_, err := s.CreateOrUpdateAccessRule(sub.ID, nil, nil, "sub_pw", 86400)
	if err != nil {
		t.Fatalf("subdomain rule: %v", err)
	}

	// Find for project without project-level rule → should get subdomain rule
	rule, err := s.FindAccessRuleForSite("eve", "blog")
	if err != nil {
		t.Fatalf("FindAccessRuleForSite: %v", err)
	}
	if rule.ProjectID != nil {
		t.Error("expected subdomain-level rule (nil ProjectID)")
	}

	// Create project-level rule → should override
	projectID := proj.ID
	_, err = s.CreateOrUpdateAccessRule(sub.ID, &projectID, nil, "proj_pw", 1800)
	if err != nil {
		t.Fatalf("project rule: %v", err)
	}

	rule, err = s.FindAccessRuleForSite("eve", "blog")
	if err != nil {
		t.Fatalf("FindAccessRuleForSite after project rule: %v", err)
	}
	if rule.ProjectID == nil {
		t.Error("expected project-level rule to override")
	}
	if rule.SessionTTL != 1800 {
		t.Errorf("expected project TTL 1800, got %d", rule.SessionTTL)
	}
}

func TestFindAccessRuleForSiteNoRule(t *testing.T) {
	s := newTestStore(t)
	user, _ := s.CreateUser("frank@example.com", "hash", "tok6")
	s.CreateSubdomain(user.ID, "frank")

	rule, err := s.FindAccessRuleForSite("frank", "blog")
	if err != nil {
		t.Fatalf("FindAccessRuleForSite: %v", err)
	}
	if rule != nil {
		t.Error("expected nil rule for unprotected site")
	}
}

func TestHasAccessRules(t *testing.T) {
	s := newTestStore(t)
	user, _ := s.CreateUser("grace@example.com", "hash", "tok7")
	sub, _ := s.CreateSubdomain(user.ID, "grace")

	has, err := s.HasAccessRules(sub.ID)
	if err != nil {
		t.Fatalf("HasAccessRules: %v", err)
	}
	if has {
		t.Error("expected no access rules")
	}

	_, _ = s.CreateOrUpdateAccessRule(sub.ID, nil, nil, "pw", 86400)

	has, err = s.HasAccessRules(sub.ID)
	if err != nil {
		t.Fatalf("HasAccessRules after create: %v", err)
	}
	if !has {
		t.Error("expected access rules to exist")
	}
}

func TestAccessRuleCascadeDeleteProject(t *testing.T) {
	s := newTestStore(t)
	user, _ := s.CreateUser("heidi@example.com", "hash", "tok8")
	sub, _ := s.CreateSubdomain(user.ID, "heidi")
	proj, _ := s.CreateProject(sub.ID, "blog")

	projectID := proj.ID
	_, _ = s.CreateOrUpdateAccessRule(sub.ID, &projectID, nil, "pw", 86400)

	_ = s.DeleteProject(sub.ID, "blog")

	_, err := s.GetAccessRule(sub.ID, &projectID)
	if err == nil {
		t.Error("expected rule to be deleted by cascade")
	}
}
```

- [ ] **Step 2: Add access rule methods to Store interface in store.go**

Add before `Close() error`:

```go
	CreateOrUpdateAccessRule(subdomainID int64, projectID *int64, allowedIPs []string, passwordHash string, sessionTTL int) (*model.AccessRule, error)
	GetAccessRule(subdomainID int64, projectID *int64) (*model.AccessRule, error)
	DeleteAccessRule(subdomainID int64, projectID *int64) error
	FindAccessRuleForSite(subdomainName string, projectName string) (*model.AccessRule, error)
	HasAccessRules(subdomainID int64) (bool, error)
```

- [ ] **Step 3: Add access_rules table to migration in sqlite.go**

Add to the `migrate()` method, after the `custom_domains` table creation:

```sql
		CREATE TABLE IF NOT EXISTS access_rules (
			id            INTEGER PRIMARY KEY AUTOINCREMENT,
			subdomain_id  INTEGER NOT NULL REFERENCES subdomains(id) ON DELETE CASCADE,
			project_id    INTEGER NULL REFERENCES projects(id) ON DELETE CASCADE,
			allowed_ips   TEXT NULL,
			password_hash TEXT NULL,
			session_ttl   INTEGER NOT NULL DEFAULT 86400,
			created_at    DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%S', 'now')),
			updated_at    DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%S', 'now')),
			UNIQUE(subdomain_id, project_id)
		);
```

- [ ] **Step 4: Implement access rule CRUD methods in sqlite.go**

Add `"encoding/json"` to the imports in `sqlite.go`. Then add at the end:

```go
// ---- Access Rules ----

func (s *SQLiteStore) CreateOrUpdateAccessRule(subdomainID int64, projectID *int64, allowedIPs []string, passwordHash string, sessionTTL int) (*model.AccessRule, error) {
	var ipsJSON *string
	if len(allowedIPs) > 0 {
		data, _ := json.Marshal(allowedIPs)
		str := string(data)
		ipsJSON = &str
	}

	var pwHash *string
	if passwordHash != "" {
		pwHash = &passwordHash
	}

	now := time.Now().UTC().Format(dtLayout)

	res, err := s.db.Exec(`
		INSERT INTO access_rules (subdomain_id, project_id, allowed_ips, password_hash, session_ttl, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(subdomain_id, project_id) DO UPDATE SET
			allowed_ips = excluded.allowed_ips,
			password_hash = excluded.password_hash,
			session_ttl = excluded.session_ttl,
			updated_at = excluded.updated_at
	`, subdomainID, projectID, ipsJSON, pwHash, sessionTTL, now, now)
	if err != nil {
		return nil, fmt.Errorf("upsert access rule: %w", err)
	}

	id, _ := res.LastInsertId()
	if id == 0 {
		// ON CONFLICT UPDATE doesn't set LastInsertId; query the row
		return s.GetAccessRule(subdomainID, projectID)
	}
	return s.getAccessRuleByID(id)
}

func (s *SQLiteStore) GetAccessRule(subdomainID int64, projectID *int64) (*model.AccessRule, error) {
	var row *sql.Row
	if projectID == nil {
		row = s.db.QueryRow(
			`SELECT id, subdomain_id, project_id, allowed_ips, password_hash, session_ttl, created_at, updated_at
			 FROM access_rules WHERE subdomain_id = ? AND project_id IS NULL`, subdomainID)
	} else {
		row = s.db.QueryRow(
			`SELECT id, subdomain_id, project_id, allowed_ips, password_hash, session_ttl, created_at, updated_at
			 FROM access_rules WHERE subdomain_id = ? AND project_id = ?`, subdomainID, *projectID)
	}
	return scanAccessRule(row)
}

func (s *SQLiteStore) getAccessRuleByID(id int64) (*model.AccessRule, error) {
	row := s.db.QueryRow(
		`SELECT id, subdomain_id, project_id, allowed_ips, password_hash, session_ttl, created_at, updated_at
		 FROM access_rules WHERE id = ?`, id)
	return scanAccessRule(row)
}

func (s *SQLiteStore) DeleteAccessRule(subdomainID int64, projectID *int64) error {
	if projectID == nil {
		_, err := s.db.Exec(
			`DELETE FROM access_rules WHERE subdomain_id = ? AND project_id IS NULL`, subdomainID)
		return err
	}
	_, err := s.db.Exec(
		`DELETE FROM access_rules WHERE subdomain_id = ? AND project_id = ?`, subdomainID, *projectID)
	return err
}

func (s *SQLiteStore) FindAccessRuleForSite(subdomainName string, projectName string) (*model.AccessRule, error) {
	// Try project-level rule first
	if projectName != "" {
		row := s.db.QueryRow(`
			SELECT ar.id, ar.subdomain_id, ar.project_id, ar.allowed_ips, ar.password_hash, ar.session_ttl, ar.created_at, ar.updated_at
			FROM access_rules ar
			JOIN projects p ON ar.project_id = p.id
			JOIN subdomains s ON ar.subdomain_id = s.id
			WHERE s.name = ? AND p.name = ? AND p.subdomain_id = s.id AND ar.project_id IS NOT NULL`,
			subdomainName, projectName)
		rule, err := scanAccessRule(row)
		if err == nil {
			return rule, nil
		}
	}

	// Fall back to subdomain-level rule
	row := s.db.QueryRow(`
		SELECT ar.id, ar.subdomain_id, ar.project_id, ar.allowed_ips, ar.password_hash, ar.session_ttl, ar.created_at, ar.updated_at
		FROM access_rules ar
		JOIN subdomains s ON ar.subdomain_id = s.id
		WHERE s.name = ? AND ar.project_id IS NULL`,
		subdomainName)
	rule, err := scanAccessRule(row)
	if err != nil {
		return nil, nil // No rule found — not an error
	}
	return rule, nil
}

func (s *SQLiteStore) HasAccessRules(subdomainID int64) (bool, error) {
	var count int
	err := s.db.QueryRow(
		`SELECT COUNT(*) FROM access_rules WHERE subdomain_id = ?`, subdomainID).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("count access rules: %w", err)
	}
	return count > 0, nil
}

func scanAccessRule(row *sql.Row) (*model.AccessRule, error) {
	var r model.AccessRule
	var projectID sql.NullInt64
	var allowedIPs sql.NullString
	var passwordHash sql.NullString
	var createdAt, updatedAt string

	if err := row.Scan(&r.ID, &r.SubdomainID, &projectID, &allowedIPs, &passwordHash, &r.SessionTTL, &createdAt, &updatedAt); err != nil {
		return nil, fmt.Errorf("scan access rule: %w", err)
	}

	if projectID.Valid {
		r.ProjectID = &projectID.Int64
	}
	if allowedIPs.Valid {
		json.Unmarshal([]byte(allowedIPs.String), &r.AllowedIPs)
	}
	if passwordHash.Valid {
		r.PasswordHash = passwordHash.String
		r.HasPassword = true
	}
	r.CreatedAt = parseTime(createdAt)
	r.UpdatedAt = parseTime(updatedAt)
	return &r, nil
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `cd /Users/chenzhong/Developer/droply && go test ./internal/store/ -v`
Expected: all tests pass

- [ ] **Step 6: Commit**

```bash
git add internal/store/store.go internal/store/sqlite.go internal/store/sqlite_access_test.go
git commit -m "feat: add access rule store interface and SQLite implementation"
```

---

### Task 3: Add Caddy Protected/Unprotected Route Methods

**Files:**
- Modify: `internal/caddy/client.go`
- Create: `internal/caddy/client_access_test.go`

- [ ] **Step 1: Write failing test for Caddy protected route methods**

Create `internal/caddy/client_access_test.go`:

```go
package caddy

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSetSubdomainProtected(t *testing.T) {
	var receivedRoutes []caddyRoute
	var deletedPaths []string

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodDelete:
			deletedPaths = append(deletedPaths, r.URL.Path)
			w.WriteHeader(http.StatusOK)
		case http.MethodPost:
			var route caddyRoute
			json.NewDecoder(r.Body).Decode(&route)
			receivedRoutes = append(receivedRoutes, route)
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer ts.Close()

	c := NewClient(ts.URL, "droplydoc.com", "/data/sites")
	if err := c.SetSubdomainProtected("alice", "localhost:8081"); err != nil {
		t.Fatalf("SetSubdomainProtected: %v", err)
	}

	if len(deletedPaths) != 1 || deletedPaths[0] != "/id/subdomain-alice" {
		t.Errorf("expected delete of /id/subdomain-alice, got %v", deletedPaths)
	}

	if len(receivedRoutes) != 1 {
		t.Fatalf("expected 1 route, got %d", len(receivedRoutes))
	}
	route := receivedRoutes[0]
	if route.ID != "subdomain-alice" {
		t.Errorf("unexpected route ID: %s", route.ID)
	}
	if route.Handle[0].Handler != "reverse_proxy" {
		t.Errorf("expected reverse_proxy handler, got %s", route.Handle[0].Handler)
	}
}

func TestSetSubdomainUnprotected(t *testing.T) {
	var receivedRoutes []caddyRoute
	var deletedPaths []string

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodDelete:
			deletedPaths = append(deletedPaths, r.URL.Path)
			w.WriteHeader(http.StatusOK)
		case http.MethodPost:
			var route caddyRoute
			json.NewDecoder(r.Body).Decode(&route)
			receivedRoutes = append(receivedRoutes, route)
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer ts.Close()

	c := NewClient(ts.URL, "droplydoc.com", "/data/sites")
	if err := c.SetSubdomainUnprotected("alice"); err != nil {
		t.Fatalf("SetSubdomainUnprotected: %v", err)
	}

	if len(deletedPaths) != 1 {
		t.Fatalf("expected 1 delete, got %d", len(deletedPaths))
	}
	if len(receivedRoutes) != 1 {
		t.Fatalf("expected 1 route, got %d", len(receivedRoutes))
	}
	if receivedRoutes[0].Handle[0].Handler != "file_server" {
		t.Errorf("expected file_server handler, got %s", receivedRoutes[0].Handle[0].Handler)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /Users/chenzhong/Developer/droply && go test ./internal/caddy/ -run TestSetSubdomain -v`
Expected: compile error (methods don't exist)

- [ ] **Step 3: Add caddyUpstream type and new methods to client.go**

Add `Upstreams` field to `caddyHandler` and add `caddyUpstream` type:

```go
type caddyHandler struct {
	Handler   string          `json:"handler"`
	Root      string          `json:"root,omitempty"`
	Upstreams []caddyUpstream `json:"upstreams,omitempty"`
}

type caddyUpstream struct {
	Dial string `json:"dial"`
}
```

Add new methods after `RemoveCustomDomainRoute`:

```go
// buildSubdomainProtectedRoute constructs a reverse_proxy Caddy route for a protected subdomain.
func (c *Client) buildSubdomainProtectedRoute(name string, proxyAddr string) caddyRoute {
	host := fmt.Sprintf("%s.%s", name, c.baseDomain)
	return caddyRoute{
		ID:    fmt.Sprintf("subdomain-%s", name),
		Match: []caddyMatch{{Host: []string{host}}},
		Handle: []caddyHandler{
			{Handler: "reverse_proxy", Upstreams: []caddyUpstream{{Dial: proxyAddr}}},
		},
		Terminal: true,
	}
}

// SetSubdomainProtected switches a subdomain route from file_server to reverse_proxy.
func (c *Client) SetSubdomainProtected(name string, proxyAddr string) error {
	_ = c.delete(fmt.Sprintf("/id/subdomain-%s", name))
	route := c.buildSubdomainProtectedRoute(name, proxyAddr)
	return c.postJSON("/config/apps/http/servers/main/routes", route)
}

// SetSubdomainUnprotected switches a subdomain route from reverse_proxy back to file_server.
func (c *Client) SetSubdomainUnprotected(name string) error {
	_ = c.delete(fmt.Sprintf("/id/subdomain-%s", name))
	route := c.buildSubdomainRoute(name)
	return c.postJSON("/config/apps/http/servers/main/routes", route)
}

// SetCustomDomainProtected switches a custom domain route to reverse_proxy.
func (c *Client) SetCustomDomainProtected(domain string, proxyAddr string) error {
	_ = c.delete(fmt.Sprintf("/id/domain-%s", domain))
	route := caddyRoute{
		ID:    fmt.Sprintf("domain-%s", domain),
		Match: []caddyMatch{{Host: []string{domain}}},
		Handle: []caddyHandler{
			{Handler: "reverse_proxy", Upstreams: []caddyUpstream{{Dial: proxyAddr}}},
		},
		Terminal: true,
	}
	return c.postJSON("/config/apps/http/servers/main/routes", route)
}

// SetCustomDomainUnprotected switches a custom domain route back to file_server.
func (c *Client) SetCustomDomainUnprotected(domain, subdomainName, projectName string) error {
	_ = c.delete(fmt.Sprintf("/id/domain-%s", domain))
	route := c.buildCustomDomainRoute(domain, subdomainName, projectName)
	return c.postJSON("/config/apps/http/servers/main/routes", route)
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /Users/chenzhong/Developer/droply && go test ./internal/caddy/ -v`
Expected: all tests pass

- [ ] **Step 5: Commit**

```bash
git add internal/caddy/client.go internal/caddy/client_access_test.go
git commit -m "feat: add Caddy protected/unprotected route switching methods"
```

---

### Task 4: Update Server, CaddyClient Interface, and API Handlers

This task combines server struct changes with handler implementation to keep every commit compilable.

**Files:**
- Modify: `internal/server/server.go`
- Modify: `internal/server/server_test.go`
- Create: `internal/server/access.go`
- Create: `internal/server/access_test.go`
- Modify: `cmd/droply-server/main.go`

- [ ] **Step 1: Write failing tests for access rule API handlers**

Create `internal/server/access_test.go`:

```go
package server_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func createSubdomain(t *testing.T, srv http.Handler, token, name string) {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"name": name})
	req := httptest.NewRequest(http.MethodPost, "/subdomains", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("createSubdomain: expected 201, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestSetSubdomainAccess(t *testing.T) {
	srv := newTestServer(t)
	token := registerAndGetToken(t, srv, "alice@test.com", "password123")
	createSubdomain(t, srv, token, "alice")

	body, _ := json.Marshal(map[string]any{
		"allowed_ips":   []string{"10.0.0.0/8"},
		"auto_password": true,
		"session_ttl":   3600,
	})
	req := httptest.NewRequest(http.MethodPut, "/subdomains/alice/access", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var resp map[string]any
	json.NewDecoder(rr.Body).Decode(&resp)
	if resp["has_password"] != true {
		t.Error("expected has_password to be true")
	}
	if resp["generated_password"] == nil || resp["generated_password"] == "" {
		t.Error("expected generated_password in response")
	}
	// Verify generated password is 16 alphanumeric chars
	gp := resp["generated_password"].(string)
	if len(gp) != 16 {
		t.Errorf("expected 16-char password, got %d chars: %s", len(gp), gp)
	}
}

func TestGetSubdomainAccess(t *testing.T) {
	srv := newTestServer(t)
	token := registerAndGetToken(t, srv, "bob@test.com", "password123")
	createSubdomain(t, srv, token, "bob")

	// Set access first
	body, _ := json.Marshal(map[string]any{
		"allowed_ips": []string{"192.168.0.0/16"},
		"password":    "secret123",
	})
	req := httptest.NewRequest(http.MethodPut, "/subdomains/bob/access", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)

	// Get access
	req = httptest.NewRequest(http.MethodGet, "/subdomains/bob/access", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr = httptest.NewRecorder()
	srv.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var resp map[string]any
	json.NewDecoder(rr.Body).Decode(&resp)
	if resp["has_password"] != true {
		t.Error("expected has_password true")
	}
	if resp["generated_password"] != nil {
		t.Error("GET should not expose generated_password")
	}
}

func TestDeleteSubdomainAccess(t *testing.T) {
	srv := newTestServer(t)
	token := registerAndGetToken(t, srv, "carol@test.com", "password123")
	createSubdomain(t, srv, token, "carol")

	// Set access
	body, _ := json.Marshal(map[string]any{"password": "secret123"})
	req := httptest.NewRequest(http.MethodPut, "/subdomains/carol/access", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)

	// Delete access
	req = httptest.NewRequest(http.MethodDelete, "/subdomains/carol/access", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr = httptest.NewRecorder()
	srv.ServeHTTP(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", rr.Code, rr.Body.String())
	}

	// Get should now 404
	req = httptest.NewRequest(http.MethodGet, "/subdomains/carol/access", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr = httptest.NewRecorder()
	srv.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404 after delete, got %d", rr.Code)
	}
}

func TestSetAccessForbiddenForNonOwner(t *testing.T) {
	srv := newTestServer(t)
	token1 := registerAndGetToken(t, srv, "owner@test.com", "password123")
	token2 := registerAndGetToken(t, srv, "mallory@test.com", "password123")
	createSubdomain(t, srv, token1, "alice")

	body, _ := json.Marshal(map[string]any{"password": "hackpass1"})
	req := httptest.NewRequest(http.MethodPut, "/subdomains/alice/access", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token2)
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", rr.Code)
	}
}

func TestSetAccessValidation(t *testing.T) {
	srv := newTestServer(t)
	token := registerAndGetToken(t, srv, "dave@test.com", "password123")
	createSubdomain(t, srv, token, "dave")

	// No IP and no password → should fail
	body, _ := json.Marshal(map[string]any{})
	req := httptest.NewRequest(http.MethodPut, "/subdomains/dave/access", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for empty rule, got %d: %s", rr.Code, rr.Body.String())
	}

	// Password too short
	body, _ = json.Marshal(map[string]any{"password": "short"})
	req = httptest.NewRequest(http.MethodPut, "/subdomains/dave/access", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	rr = httptest.NewRecorder()
	srv.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for short password, got %d: %s", rr.Code, rr.Body.String())
	}
}
```

- [ ] **Step 2: Update server.go**

Add to `CaddyClient` interface:

```go
	SetSubdomainProtected(name string, proxyAddr string) error
	SetSubdomainUnprotected(name string) error
	SetCustomDomainProtected(domain string, proxyAddr string) error
	SetCustomDomainUnprotected(domain, subdomainName, projectName string) error
```

Add fields to `Server` struct:

```go
type Server struct {
	store      store.Store
	sitesDir   string
	baseDomain string
	caddy      CaddyClient
	router     *chi.Mux
	hmacKey    []byte
	siteAddr   string // address for Caddy reverse_proxy, e.g. "localhost:8081"
}
```

Update `New` function signature:

```go
func New(s store.Store, sitesDir, baseDomain string, caddy CaddyClient, hmacKey []byte, siteAddr string) *Server {
	srv := &Server{
		store:      s,
		sitesDir:   sitesDir,
		baseDomain: baseDomain,
		caddy:      caddy,
		hmacKey:    hmacKey,
		siteAddr:   siteAddr,
	}
	srv.router = srv.buildRouter()
	return srv
}
```

Add new routes to `buildRouter()` in the authenticated group:

```go
		r.Put("/subdomains/{sub}/access", s.handleSetAccess)
		r.Get("/subdomains/{sub}/access", s.handleGetAccess)
		r.Delete("/subdomains/{sub}/access", s.handleDeleteAccess)

		r.Put("/subdomains/{sub}/projects/{project}/access", s.handleSetProjectAccess)
		r.Get("/subdomains/{sub}/projects/{project}/access", s.handleGetProjectAccess)
		r.Delete("/subdomains/{sub}/projects/{project}/access", s.handleDeleteProjectAccess)
```

- [ ] **Step 3: Update newTestServer in server_test.go**

```go
func newTestServer(t *testing.T) *server.Server {
	t.Helper()
	st, err := store.NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatalf("create test store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return server.New(st, "/tmp/sites", "droplydoc.com", nil, []byte("test-hmac-key-for-testing-1234"), "localhost:8081")
}
```

- [ ] **Step 4: Update cmd/droply-server/main.go**

Update the `server.New` call to pass the new parameters (placeholder — fully configured in Task 7):

```go
srv := server.New(st, sitesDir, *domain, caddyClient, []byte("placeholder"), "localhost:8081")
```

- [ ] **Step 5: Implement access.go**

Create `internal/server/access.go`:

```go
package server

import (
	"crypto/rand"
	"encoding/json"
	"math/big"
	"net"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"golang.org/x/crypto/bcrypt"
)

const alphanumChars = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

type setAccessRequest struct {
	AllowedIPs   []string `json:"allowed_ips"`
	Password     string   `json:"password"`
	AutoPassword bool     `json:"auto_password"`
	SessionTTL   int      `json:"session_ttl"`
}

type accessResponse struct {
	ID                int64    `json:"id"`
	AllowedIPs        []string `json:"allowed_ips,omitempty"`
	HasPassword       bool     `json:"has_password"`
	GeneratedPassword string   `json:"generated_password,omitempty"`
	SessionTTL        int      `json:"session_ttl"`
}

// generatePassword creates a 16-character alphanumeric password using crypto/rand.
func generatePassword() string {
	b := make([]byte, 16)
	for i := range b {
		n, _ := rand.Int(rand.Reader, big.NewInt(int64(len(alphanumChars))))
		b[i] = alphanumChars[n.Int64()]
	}
	return string(b)
}

func (s *Server) handleSetAccess(w http.ResponseWriter, r *http.Request) {
	s.setAccess(w, r, false)
}

func (s *Server) handleSetProjectAccess(w http.ResponseWriter, r *http.Request) {
	s.setAccess(w, r, true)
}

func (s *Server) setAccess(w http.ResponseWriter, r *http.Request, isProject bool) {
	user := userFromContext(r.Context())
	subName := chi.URLParam(r, "sub")

	sub, err := s.store.GetSubdomainByName(subName)
	if err != nil {
		jsonError(w, "subdomain not found", http.StatusNotFound)
		return
	}
	if sub.UserID != user.ID {
		jsonError(w, "forbidden", http.StatusForbidden)
		return
	}

	var projectID *int64
	if isProject {
		projName := chi.URLParam(r, "project")
		proj, err := s.store.GetProject(sub.ID, projName)
		if err != nil {
			jsonError(w, "project not found", http.StatusNotFound)
			return
		}
		projectID = &proj.ID
	}

	var req setAccessRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid request body", http.StatusBadRequest)
		return
	}

	// Validation
	if req.Password != "" && req.AutoPassword {
		jsonError(w, "password and auto_password are mutually exclusive", http.StatusBadRequest)
		return
	}
	if len(req.AllowedIPs) == 0 && req.Password == "" && !req.AutoPassword {
		jsonError(w, "at least one of allowed_ips or password must be provided", http.StatusBadRequest)
		return
	}
	if req.Password != "" && len(req.Password) < 8 {
		jsonError(w, "password must be at least 8 characters", http.StatusBadRequest)
		return
	}

	// Validate IPs/CIDRs
	for _, ip := range req.AllowedIPs {
		if strings.Contains(ip, "/") {
			if _, _, err := net.ParseCIDR(ip); err != nil {
				jsonError(w, "invalid CIDR: "+ip, http.StatusBadRequest)
				return
			}
		} else {
			if net.ParseIP(ip) == nil {
				jsonError(w, "invalid IP: "+ip, http.StatusBadRequest)
				return
			}
		}
	}

	// Session TTL
	if req.SessionTTL == 0 {
		req.SessionTTL = 86400
	}
	if req.SessionTTL < 300 {
		jsonError(w, "session_ttl must be at least 300 seconds", http.StatusBadRequest)
		return
	}
	if req.SessionTTL > 2592000 {
		jsonError(w, "session_ttl must be at most 2592000 seconds (30 days)", http.StatusBadRequest)
		return
	}

	// Generate or hash password
	var passwordHash string
	var generatedPassword string
	if req.AutoPassword {
		generatedPassword = generatePassword()
		hash, _ := bcrypt.GenerateFromPassword([]byte(generatedPassword), bcrypt.DefaultCost)
		passwordHash = string(hash)
	} else if req.Password != "" {
		hash, _ := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
		passwordHash = string(hash)
	}

	rule, err := s.store.CreateOrUpdateAccessRule(sub.ID, projectID, req.AllowedIPs, passwordHash, req.SessionTTL)
	if err != nil {
		jsonError(w, "internal server error", http.StatusInternalServerError)
		return
	}

	// Switch Caddy routes to protected
	if s.caddy != nil {
		_ = s.caddy.SetSubdomainProtected(subName, s.siteAddr)

		// Also update custom domain routes for affected project(s)
		s.updateCustomDomainRoutes(sub.ID, subName, true)
	}

	resp := accessResponse{
		ID:                rule.ID,
		AllowedIPs:        rule.AllowedIPs,
		HasPassword:       rule.HasPassword,
		GeneratedPassword: generatedPassword,
		SessionTTL:        rule.SessionTTL,
	}
	jsonResponse(w, resp, http.StatusOK)
}

func (s *Server) handleGetAccess(w http.ResponseWriter, r *http.Request) {
	s.getAccess(w, r, false)
}

func (s *Server) handleGetProjectAccess(w http.ResponseWriter, r *http.Request) {
	s.getAccess(w, r, true)
}

func (s *Server) getAccess(w http.ResponseWriter, r *http.Request, isProject bool) {
	user := userFromContext(r.Context())
	subName := chi.URLParam(r, "sub")

	sub, err := s.store.GetSubdomainByName(subName)
	if err != nil {
		jsonError(w, "subdomain not found", http.StatusNotFound)
		return
	}
	if sub.UserID != user.ID {
		jsonError(w, "forbidden", http.StatusForbidden)
		return
	}

	var projectID *int64
	if isProject {
		projName := chi.URLParam(r, "project")
		proj, err := s.store.GetProject(sub.ID, projName)
		if err != nil {
			jsonError(w, "project not found", http.StatusNotFound)
			return
		}
		projectID = &proj.ID
	}

	rule, err := s.store.GetAccessRule(sub.ID, projectID)
	if err != nil {
		jsonError(w, "access rule not found", http.StatusNotFound)
		return
	}

	resp := accessResponse{
		ID:          rule.ID,
		AllowedIPs:  rule.AllowedIPs,
		HasPassword: rule.HasPassword,
		SessionTTL:  rule.SessionTTL,
	}
	jsonResponse(w, resp, http.StatusOK)
}

func (s *Server) handleDeleteAccess(w http.ResponseWriter, r *http.Request) {
	s.deleteAccess(w, r, false)
}

func (s *Server) handleDeleteProjectAccess(w http.ResponseWriter, r *http.Request) {
	s.deleteAccess(w, r, true)
}

func (s *Server) deleteAccess(w http.ResponseWriter, r *http.Request, isProject bool) {
	user := userFromContext(r.Context())
	subName := chi.URLParam(r, "sub")

	sub, err := s.store.GetSubdomainByName(subName)
	if err != nil {
		jsonError(w, "subdomain not found", http.StatusNotFound)
		return
	}
	if sub.UserID != user.ID {
		jsonError(w, "forbidden", http.StatusForbidden)
		return
	}

	var projectID *int64
	if isProject {
		projName := chi.URLParam(r, "project")
		proj, err := s.store.GetProject(sub.ID, projName)
		if err != nil {
			jsonError(w, "project not found", http.StatusNotFound)
			return
		}
		projectID = &proj.ID
	}

	if err := s.store.DeleteAccessRule(sub.ID, projectID); err != nil {
		jsonError(w, "internal server error", http.StatusInternalServerError)
		return
	}

	// Check if subdomain still has any rules; if not, switch back to file_server
	if s.caddy != nil {
		hasRules, _ := s.store.HasAccessRules(sub.ID)
		if !hasRules {
			_ = s.caddy.SetSubdomainUnprotected(subName)
			// Also revert custom domain routes to file_server
			s.updateCustomDomainRoutes(sub.ID, subName, false)
		}
	}

	w.WriteHeader(http.StatusNoContent)
}

// updateCustomDomainRoutes updates all custom domain routes under a subdomain.
// If protect is true, switches to reverse_proxy; otherwise switches to file_server.
func (s *Server) updateCustomDomainRoutes(subdomainID int64, subdomainName string, protect bool) {
	projects, _ := s.store.ListProjects(subdomainID)
	for _, proj := range projects {
		domains, _ := s.store.ListCustomDomains(proj.ID)
		for _, d := range domains {
			if !d.Verified {
				continue
			}
			if protect {
				_ = s.caddy.SetCustomDomainProtected(d.Domain, s.siteAddr)
			} else {
				_ = s.caddy.SetCustomDomainUnprotected(d.Domain, subdomainName, proj.Name)
			}
		}
	}
}
```

- [ ] **Step 6: Run tests to verify they pass**

Run: `cd /Users/chenzhong/Developer/droply && go test ./internal/server/ -v`
Expected: all tests pass

- [ ] **Step 7: Commit**

```bash
git add internal/server/server.go internal/server/server_test.go internal/server/access.go internal/server/access_test.go cmd/droply-server/main.go
git commit -m "feat: add access rule API handlers with CaddyClient interface updates"
```

---

### Task 5: Implement Site Serving (IP check, cookie, login page, rate limiting, custom domains)

**Files:**
- Create: `internal/server/site.go`
- Create: `internal/server/site_test.go`

- [ ] **Step 1: Write failing tests for site handler**

Create `internal/server/site_test.go`:

```go
package server_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/zhong/droply/internal/server"
	"github.com/zhong/droply/internal/store"
)

func newTestSiteServer(t *testing.T) (*server.Server, string) {
	t.Helper()
	st, err := store.NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatalf("create test store: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	sitesDir := t.TempDir()
	srv := server.New(st, sitesDir, "droplydoc.com", nil, []byte("test-hmac-secret-key-1234567890"), "localhost:8081")
	return srv, sitesDir
}

func setupProtectedSite(t *testing.T, srv http.Handler, sitesDir string) string {
	t.Helper()

	token := registerAndGetToken(t, srv, "siteuser@test.com", "password123")
	createSubdomain(t, srv, token, "testsite")

	// Create site files on disk
	siteDir := filepath.Join(sitesDir, "testsite", "blog")
	os.MkdirAll(siteDir, 0755)
	os.WriteFile(filepath.Join(siteDir, "index.html"), []byte("<h1>Hello</h1>"), 0644)

	// Set access rule with password
	body, _ := json.Marshal(map[string]any{
		"password":    "secret123",
		"session_ttl": 3600,
	})
	req := httptest.NewRequest(http.MethodPut, "/subdomains/testsite/access", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("set access: expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	return token
}

func TestSiteHandlerNoRule(t *testing.T) {
	srv, sitesDir := newTestSiteServer(t)
	token := registerAndGetToken(t, srv, "noprotect@test.com", "password123")
	createSubdomain(t, srv, token, "open")

	siteDir := filepath.Join(sitesDir, "open")
	os.MkdirAll(siteDir, 0755)
	os.WriteFile(filepath.Join(siteDir, "index.html"), []byte("<h1>Open</h1>"), 0644)

	siteHandler := srv.NewSiteHandler()
	req := httptest.NewRequest(http.MethodGet, "/index.html", nil)
	req.Host = "open.droplydoc.com"
	rr := httptest.NewRecorder()
	siteHandler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if rr.Body.String() != "<h1>Open</h1>" {
		t.Errorf("unexpected body: %s", rr.Body.String())
	}
}

func TestSiteHandlerPasswordRequired(t *testing.T) {
	srv, sitesDir := newTestSiteServer(t)
	setupProtectedSite(t, srv, sitesDir)

	siteHandler := srv.NewSiteHandler()
	req := httptest.NewRequest(http.MethodGet, "/blog/index.html", nil)
	req.Host = "testsite.droplydoc.com"
	rr := httptest.NewRecorder()
	siteHandler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 (login page), got %d", rr.Code)
	}
	if !bytes.Contains(rr.Body.Bytes(), []byte("_droply/login")) {
		t.Error("expected login form in response")
	}
}

func TestSiteHandlerLoginSuccess(t *testing.T) {
	srv, sitesDir := newTestSiteServer(t)
	setupProtectedSite(t, srv, sitesDir)

	siteHandler := srv.NewSiteHandler()

	// POST login
	form := url.Values{}
	form.Set("password", "secret123")
	form.Set("redirect", "/blog/index.html")
	req := httptest.NewRequest(http.MethodPost, "/_droply/login", bytes.NewReader([]byte(form.Encode())))
	req.Host = "testsite.droplydoc.com"
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()
	siteHandler.ServeHTTP(rr, req)

	if rr.Code != http.StatusFound {
		t.Fatalf("expected 302, got %d: %s", rr.Code, rr.Body.String())
	}

	// Check cookie is set
	cookies := rr.Result().Cookies()
	var accessCookie *http.Cookie
	for _, c := range cookies {
		if c.Name == "_droply_access" {
			accessCookie = c
		}
	}
	if accessCookie == nil {
		t.Fatal("expected _droply_access cookie to be set")
	}

	// Use cookie to access the site
	req = httptest.NewRequest(http.MethodGet, "/blog/index.html", nil)
	req.Host = "testsite.droplydoc.com"
	req.AddCookie(accessCookie)
	rr = httptest.NewRecorder()
	siteHandler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 with valid cookie, got %d", rr.Code)
	}
	if !bytes.Contains(rr.Body.Bytes(), []byte("<h1>Hello</h1>")) {
		t.Errorf("expected site content, got: %s", rr.Body.String())
	}
}

func TestSiteHandlerLoginWrongPassword(t *testing.T) {
	srv, sitesDir := newTestSiteServer(t)
	setupProtectedSite(t, srv, sitesDir)

	siteHandler := srv.NewSiteHandler()
	form := url.Values{}
	form.Set("password", "wrongpassword")
	form.Set("redirect", "/blog/index.html")
	req := httptest.NewRequest(http.MethodPost, "/_droply/login", bytes.NewReader([]byte(form.Encode())))
	req.Host = "testsite.droplydoc.com"
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()
	siteHandler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 (login page with error), got %d", rr.Code)
	}
	if !bytes.Contains(rr.Body.Bytes(), []byte("Incorrect password")) {
		t.Error("expected error message in login page")
	}
}

func TestSiteHandlerIPBlocked(t *testing.T) {
	srv, sitesDir := newTestSiteServer(t)

	token := registerAndGetToken(t, srv, "iptest@test.com", "password123")
	createSubdomain(t, srv, token, "ipsite")

	// Set IP-only rule
	body, _ := json.Marshal(map[string]any{
		"allowed_ips": []string{"10.0.0.0/8"},
	})
	req := httptest.NewRequest(http.MethodPut, "/subdomains/ipsite/access", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)

	// Access from non-whitelisted IP
	siteHandler := srv.NewSiteHandler()
	req = httptest.NewRequest(http.MethodGet, "/", nil)
	req.Host = "ipsite.droplydoc.com"
	req.Header.Set("X-Real-IP", "192.168.1.1")
	rr = httptest.NewRecorder()
	siteHandler.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", rr.Code)
	}
}

func TestSiteHandlerIPAllowed(t *testing.T) {
	srv, sitesDir := newTestSiteServer(t)

	token := registerAndGetToken(t, srv, "ipallow@test.com", "password123")
	createSubdomain(t, srv, token, "ipallow")

	siteDir := filepath.Join(sitesDir, "ipallow")
	os.MkdirAll(siteDir, 0755)
	os.WriteFile(filepath.Join(siteDir, "index.html"), []byte("<h1>OK</h1>"), 0644)

	// Set IP-only rule
	body, _ := json.Marshal(map[string]any{
		"allowed_ips": []string{"10.0.0.0/8"},
	})
	req := httptest.NewRequest(http.MethodPut, "/subdomains/ipallow/access", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)

	// Access from whitelisted IP
	siteHandler := srv.NewSiteHandler()
	req = httptest.NewRequest(http.MethodGet, "/index.html", nil)
	req.Host = "ipallow.droplydoc.com"
	req.Header.Set("X-Real-IP", "10.1.2.3")
	rr = httptest.NewRecorder()
	siteHandler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
}

func TestSiteHandlerRateLimiting(t *testing.T) {
	srv, sitesDir := newTestSiteServer(t)
	setupProtectedSite(t, srv, sitesDir)

	siteHandler := srv.NewSiteHandler()

	// Send 11 login attempts (limit is 10 per minute)
	for i := 0; i < 11; i++ {
		form := url.Values{}
		form.Set("password", "wrongpassword")
		form.Set("redirect", "/blog/index.html")
		req := httptest.NewRequest(http.MethodPost, "/_droply/login", bytes.NewReader([]byte(form.Encode())))
		req.Host = "testsite.droplydoc.com"
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("X-Real-IP", "1.2.3.4")
		rr := httptest.NewRecorder()
		siteHandler.ServeHTTP(rr, req)

		if i < 10 {
			if rr.Code == http.StatusTooManyRequests {
				t.Fatalf("unexpected 429 on attempt %d", i+1)
			}
		} else {
			if rr.Code != http.StatusTooManyRequests {
				t.Fatalf("expected 429 on attempt %d, got %d", i+1, rr.Code)
			}
		}
	}
}

func TestSiteHandlerCustomDomain(t *testing.T) {
	srv, sitesDir := newTestSiteServer(t)

	token := registerAndGetToken(t, srv, "customdomain@test.com", "password123")
	createSubdomain(t, srv, token, "mysite")

	// Create project files
	siteDir := filepath.Join(sitesDir, "mysite", "docs")
	os.MkdirAll(siteDir, 0755)
	os.WriteFile(filepath.Join(siteDir, "index.html"), []byte("<h1>Docs</h1>"), 0644)

	// We can't easily test custom domain resolution without setting up the full
	// domain create flow. This test verifies that unknown hosts return 404.
	siteHandler := srv.NewSiteHandler()
	req := httptest.NewRequest(http.MethodGet, "/index.html", nil)
	req.Host = "docs.example.com"
	rr := httptest.NewRecorder()
	siteHandler.ServeHTTP(rr, req)

	// Unknown host → 404
	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for unknown host, got %d", rr.Code)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /Users/chenzhong/Developer/droply && go test ./internal/server/ -run TestSiteHandler -v`
Expected: compile error (NewSiteHandler doesn't exist)

- [ ] **Step 3: Implement site.go**

Create `internal/server/site.go`:

```go
package server

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"html/template"
	"net"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
	"golang.org/x/time/rate"
)

var loginPageTmpl = template.Must(template.New("login").Parse(`<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Access Protected - droply</title>
<style>
*{margin:0;padding:0;box-sizing:border-box}
body{font-family:-apple-system,BlinkMacSystemFont,"Segoe UI",Roboto,sans-serif;
background:#f5f5f5;display:flex;align-items:center;justify-content:center;min-height:100vh}
.card{background:#fff;border-radius:8px;box-shadow:0 2px 8px rgba(0,0,0,.1);
padding:2rem;width:100%;max-width:380px}
h1{font-size:1.1rem;color:#333;margin-bottom:.5rem;text-align:center}
.site{color:#666;font-size:.85rem;text-align:center;margin-bottom:1.5rem;word-break:break-all}
input[type=password]{width:100%;padding:.6rem .8rem;border:1px solid #ddd;border-radius:4px;
font-size:.95rem;margin-bottom:1rem}
button{width:100%;padding:.6rem;background:#333;color:#fff;border:none;border-radius:4px;
font-size:.95rem;cursor:pointer}
button:hover{background:#555}
.error{color:#d32f2f;font-size:.85rem;margin-bottom:1rem;text-align:center}
</style>
</head>
<body>
<div class="card">
<h1>This site is protected</h1>
<p class="site">{{.SiteName}}</p>
{{if .Error}}<p class="error">{{.Error}}</p>{{end}}
<form method="POST" action="/_droply/login">
<input type="hidden" name="redirect" value="{{.Redirect}}">
<input type="password" name="password" placeholder="Enter password..." autofocus required>
<button type="submit">Continue</button>
</form>
</div>
</body>
</html>`))

type loginPageData struct {
	SiteName string
	Redirect string
	Error    string
}

// rateLimiter tracks per-IP login attempt rate limits.
type rateLimiter struct {
	mu       sync.Mutex
	limiters map[string]*rate.Limiter
}

func newRateLimiter() *rateLimiter {
	rl := &rateLimiter{limiters: make(map[string]*rate.Limiter)}
	go rl.cleanup()
	return rl
}

func (rl *rateLimiter) allow(ip string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	l, ok := rl.limiters[ip]
	if !ok {
		// 10 requests per minute
		l = rate.NewLimiter(rate.Every(6*time.Second), 10)
		rl.limiters[ip] = l
	}
	return l.Allow()
}

func (rl *rateLimiter) cleanup() {
	for {
		time.Sleep(10 * time.Minute)
		rl.mu.Lock()
		rl.limiters = make(map[string]*rate.Limiter)
		rl.mu.Unlock()
	}
}

// NewSiteHandler creates an http.Handler for serving protected sites.
func (s *Server) NewSiteHandler() http.Handler {
	rl := newRateLimiter()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/_droply/login" {
			s.siteLoginHandler(w, r, rl)
			return
		}
		s.siteHandler(w, r)
	})
}

// resolveHost resolves the Host header to a subdomain name and optional project override.
// For subdomain hosts (alice.droplydoc.com): returns (alice, "", true)
// For custom domains (docs.example.com): looks up in DB, returns (subdomainName, projectName, true)
// For unknown hosts: returns ("", "", false)
func (s *Server) resolveHost(host string) (subdomainName, projectName string, ok bool) {
	// Strip port
	h := host
	if colonIdx := strings.LastIndex(h, ":"); colonIdx != -1 {
		h = h[:colonIdx]
	}

	// Check if it's a subdomain of the base domain
	suffix := "." + s.baseDomain
	if strings.HasSuffix(h, suffix) {
		subName := strings.TrimSuffix(h, suffix)
		return subName, "", true
	}

	// Check if it's a custom domain
	domains, err := s.store.ListAllVerifiedDomainsWithPaths()
	if err != nil {
		return "", "", false
	}
	for _, d := range domains {
		if d.Domain == h {
			return d.SubdomainName, d.ProjectName, true
		}
	}

	return "", "", false
}

func (s *Server) siteHandler(w http.ResponseWriter, r *http.Request) {
	subdomainName, customProjectName, ok := s.resolveHost(r.Host)
	if !ok || subdomainName == "" {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}

	// For custom domains, the project is determined by the domain mapping, not the URL path
	isCustomDomain := customProjectName != ""

	// Extract project name from path (first segment) — only for subdomain hosts
	projectName := customProjectName
	if !isCustomDomain {
		path := strings.TrimPrefix(r.URL.Path, "/")
		if idx := strings.Index(path, "/"); idx > 0 {
			projectName = path[:idx]
		} else if path != "" {
			projectName = path
		}
	}

	// Find applicable access rule
	rule, err := s.store.FindAccessRuleForSite(subdomainName, projectName)
	if err != nil {
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	if rule != nil {
		// Check IP whitelist
		if len(rule.AllowedIPs) > 0 {
			clientIP := getClientIP(r)
			if !isIPAllowed(clientIP, rule.AllowedIPs) {
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}
		}

		// Check password via cookie
		if rule.HasPassword {
			if !s.isValidAccessCookie(r, subdomainName, projectName) {
				siteName := r.Host
				if !isCustomDomain && projectName != "" {
					siteName += "/" + projectName
				}
				loginPageTmpl.Execute(w, loginPageData{
					SiteName: siteName,
					Redirect: r.URL.Path,
				})
				return
			}
		}
	}

	// Serve the file
	var root string
	if isCustomDomain {
		root = filepath.Join(s.sitesDir, subdomainName, customProjectName)
	} else {
		root = filepath.Join(s.sitesDir, subdomainName)
	}
	http.FileServer(http.Dir(root)).ServeHTTP(w, r)
}

func (s *Server) siteLoginHandler(w http.ResponseWriter, r *http.Request, rl *rateLimiter) {
	subdomainName, customProjectName, ok := s.resolveHost(r.Host)
	if !ok || subdomainName == "" {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}

	clientIP := getClientIP(r)
	if !rl.allow(clientIP) {
		http.Error(w, "too many attempts, please try again later", http.StatusTooManyRequests)
		return
	}

	r.ParseForm()
	password := r.FormValue("password")
	redirect := r.FormValue("redirect")
	if redirect == "" {
		redirect = "/"
	}

	isCustomDomain := customProjectName != ""

	// Extract project from redirect path or custom domain
	projectName := customProjectName
	if !isCustomDomain {
		trimmed := strings.TrimPrefix(redirect, "/")
		if idx := strings.Index(trimmed, "/"); idx > 0 {
			projectName = trimmed[:idx]
		} else if trimmed != "" {
			projectName = trimmed
		}
	}

	rule, err := s.store.FindAccessRuleForSite(subdomainName, projectName)
	if err != nil || rule == nil || !rule.HasPassword {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}

	if err := bcrypt.CompareHashAndPassword([]byte(rule.PasswordHash), []byte(password)); err != nil {
		siteName := r.Host
		if !isCustomDomain && projectName != "" {
			siteName += "/" + projectName
		}
		loginPageTmpl.Execute(w, loginPageData{
			SiteName: siteName,
			Redirect: redirect,
			Error:    "Incorrect password",
		})
		return
	}

	// Set cookie
	expiry := time.Now().Add(time.Duration(rule.SessionTTL) * time.Second)
	cookieProject := projectName
	if rule.ProjectID == nil {
		cookieProject = "" // subdomain-level cookie
	}
	cookieValue := s.signCookie(subdomainName, cookieProject, expiry)

	cookiePath := "/"
	if rule.ProjectID != nil && projectName != "" && !isCustomDomain {
		cookiePath = "/" + projectName + "/"
	}

	http.SetCookie(w, &http.Cookie{
		Name:     "_droply_access",
		Value:    cookieValue,
		Path:     cookiePath,
		Expires:  expiry,
		HttpOnly: true,
		Secure:   true, // Always true — Caddy terminates TLS in production
		SameSite: http.SameSiteLaxMode,
	})

	http.Redirect(w, r, redirect, http.StatusFound)
}

func getClientIP(r *http.Request) string {
	if ip := r.Header.Get("X-Real-IP"); ip != "" {
		return ip
	}
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.Split(xff, ",")
		return strings.TrimSpace(parts[0])
	}
	host, _, _ := net.SplitHostPort(r.RemoteAddr)
	return host
}

func isIPAllowed(clientIP string, allowedIPs []string) bool {
	ip := net.ParseIP(clientIP)
	if ip == nil {
		return false
	}
	for _, allowed := range allowedIPs {
		if strings.Contains(allowed, "/") {
			_, cidr, err := net.ParseCIDR(allowed)
			if err == nil && cidr.Contains(ip) {
				return true
			}
		} else {
			if allowedIP := net.ParseIP(allowed); allowedIP != nil && allowedIP.Equal(ip) {
				return true
			}
		}
	}
	return false
}

func (s *Server) signCookie(subdomain, project string, expiry time.Time) string {
	expiryStr := strconv.FormatInt(expiry.Unix(), 10)
	payload := subdomain + ":" + project + ":" + expiryStr
	mac := hmac.New(sha256.New, s.hmacKey)
	mac.Write([]byte(payload))
	sig := hex.EncodeToString(mac.Sum(nil))
	return payload + ":" + sig
}

func (s *Server) isValidAccessCookie(r *http.Request, subdomain, project string) bool {
	cookie, err := r.Cookie("_droply_access")
	if err != nil {
		return false
	}

	parts := strings.SplitN(cookie.Value, ":", 4)
	if len(parts) != 4 {
		return false
	}

	cookieSub := parts[0]
	cookieProj := parts[1]
	expiryStr := parts[2]
	sig := parts[3]

	// Verify HMAC
	payload := cookieSub + ":" + cookieProj + ":" + expiryStr
	mac := hmac.New(sha256.New, s.hmacKey)
	mac.Write([]byte(payload))
	expectedSig := hex.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(sig), []byte(expectedSig)) {
		return false
	}

	// Check expiry
	expiryUnix, err := strconv.ParseInt(expiryStr, 10, 64)
	if err != nil {
		return false
	}
	if time.Now().Unix() > expiryUnix {
		return false
	}

	// Check subdomain match
	if cookieSub != subdomain {
		return false
	}

	// For subdomain-level cookies (empty project), allow access to any project
	// For project-level cookies, must match the project
	if cookieProj != "" && cookieProj != project {
		return false
	}

	return true
}
```

- [ ] **Step 4: Add `golang.org/x/time` dependency**

Run: `cd /Users/chenzhong/Developer/droply && go get golang.org/x/time/rate`

- [ ] **Step 5: Run tests to verify they pass**

Run: `cd /Users/chenzhong/Developer/droply && go test ./internal/server/ -v`
Expected: all tests pass

- [ ] **Step 6: Commit**

```bash
git add internal/server/site.go internal/server/site_test.go go.mod go.sum
git commit -m "feat: implement site serving with IP check, password login, cookie sessions, rate limiting"
```

---

### Task 6: Update Project/Domain Deletion and Route Recovery

**Files:**
- Modify: `internal/server/project.go:41-64`
- Modify: `internal/server/domain.go` (handleCreateDomain needs access-aware routing)
- Modify: `internal/server/recovery.go`

- [ ] **Step 1: Read domain.go to understand the existing handleCreateDomain**

Run: Read `internal/server/domain.go` to find the existing handleCreateDomain implementation and identify where to add access-rule-aware route creation.

- [ ] **Step 2: Update handleDeleteProject to check access rules after deletion**

In `internal/server/project.go`, after the existing `os.RemoveAll` line, add:

```go
	// After project deletion, ON DELETE CASCADE removes its access rule.
	// Check if subdomain still has any rules; if not, switch back to file_server.
	if s.caddy != nil {
		hasRules, _ := s.store.HasAccessRules(sub.ID)
		if !hasRules {
			_ = s.caddy.SetSubdomainUnprotected(subName)
		}
	}
```

- [ ] **Step 3: Update handleCreateDomain (in domain.go) to use protected route if project has access rules**

In the existing `handleCreateDomain`, after the Caddy `AddCustomDomainRoute` call, add a check: if the project has access rules, switch the newly added custom domain route to `reverse_proxy` instead.

Find the code that calls `s.caddy.AddCustomDomainRoute(...)` and replace with:

```go
	if s.caddy != nil {
		// Check if the project has access rules — if so, use protected route
		rule, _ := s.store.FindAccessRuleForSite(subName, projName)
		if rule != nil {
			_ = s.caddy.SetCustomDomainProtected(req.Domain, s.siteAddr)
		} else {
			_ = s.caddy.AddCustomDomainRoute(req.Domain, subName, projName)
		}
	}
```

Note: `handleDeleteSubdomain` does not need changes — it already calls `RemoveSubdomainRoute` which removes the route entirely, and `ON DELETE CASCADE` cleans up access rules.

- [ ] **Step 4: Update RecoverCaddyRoutes**

Replace the content of `internal/server/recovery.go`:

```go
package server

import (
	"fmt"
	"log"
)

func (s *Server) RecoverCaddyRoutes() error {
	if s.caddy == nil {
		log.Println("No Caddy client configured, skipping route recovery")
		return nil
	}

	subs, err := s.store.ListAllSubdomains()
	if err != nil {
		return fmt.Errorf("list subdomains: %w", err)
	}

	protectedCount := 0
	for _, sub := range subs {
		hasRules, _ := s.store.HasAccessRules(sub.ID)
		if hasRules {
			if err := s.caddy.SetSubdomainProtected(sub.Name, s.siteAddr); err != nil {
				log.Printf("Warning: failed to set protected route for subdomain %s: %v", sub.Name, err)
			}
			protectedCount++
		} else {
			if err := s.caddy.AddSubdomainRoute(sub.Name); err != nil {
				log.Printf("Warning: failed to add route for subdomain %s: %v", sub.Name, err)
			}
		}
	}

	domains, err := s.store.ListAllVerifiedDomainsWithPaths()
	if err != nil {
		return fmt.Errorf("list domains: %w", err)
	}
	for _, d := range domains {
		rule, _ := s.store.FindAccessRuleForSite(d.SubdomainName, d.ProjectName)
		if rule != nil {
			if err := s.caddy.SetCustomDomainProtected(d.Domain, s.siteAddr); err != nil {
				log.Printf("Warning: failed to set protected route for domain %s: %v", d.Domain, err)
			}
		} else {
			if err := s.caddy.AddCustomDomainRoute(d.Domain, d.SubdomainName, d.ProjectName); err != nil {
				log.Printf("Warning: failed to add route for domain %s: %v", d.Domain, err)
			}
		}
	}

	log.Printf("Recovered %d subdomain routes (%d protected) and %d custom domain routes", len(subs), protectedCount, len(domains))
	return nil
}
```

- [ ] **Step 5: Verify it compiles and run all tests**

Run: `cd /Users/chenzhong/Developer/droply && go build ./... && go test ./... -v`
Expected: all tests pass

- [ ] **Step 6: Commit**

```bash
git add internal/server/project.go internal/server/domain.go internal/server/recovery.go
git commit -m "feat: update project/domain deletion and route recovery for access control"
```

---

### Task 7: Update Server Main with HMAC Key and Site Server

**Files:**
- Modify: `cmd/droply-server/main.go`

- [ ] **Step 1: Implement HMAC key management and site server startup**

Replace `cmd/droply-server/main.go`:

```go
package main

import (
	"crypto/rand"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"

	"github.com/zhong/droply/internal/caddy"
	"github.com/zhong/droply/internal/server"
	"github.com/zhong/droply/internal/store"
)

func main() {
	addr := flag.String("addr", ":8080", "API listen address")
	siteAddr := flag.String("site-addr", ":8081", "site serving listen address")
	dataDir := flag.String("data-dir", "/data/droply", "directory for SQLite database and site files")
	domain := flag.String("domain", "droplydoc.com", "base domain for subdomains")
	caddyAddr := flag.String("caddy-admin", "http://localhost:2019", "Caddy admin API address")
	hmacSecret := flag.String("hmac-secret", "", "HMAC secret for cookie signing (auto-generated if empty)")
	flag.Parse()

	dsn := fmt.Sprintf("%s/droply.db", *dataDir)
	if err := os.MkdirAll(*dataDir, 0755); err != nil {
		log.Fatalf("create data dir: %v", err)
	}

	st, err := store.NewSQLiteStore(dsn)
	if err != nil {
		log.Fatalf("open store: %v", err)
	}
	defer st.Close()

	hmacKey, err := loadOrGenerateHMACKey(*hmacSecret, *dataDir)
	if err != nil {
		log.Fatalf("HMAC key: %v", err)
	}

	sitesDir := fmt.Sprintf("%s/sites", *dataDir)
	caddyClient := caddy.NewClient(*caddyAddr, *domain, sitesDir)

	// siteAddr for Caddy proxy: strip leading colon and prepend localhost
	siteProxyAddr := "localhost" + *siteAddr
	srv := server.New(st, sitesDir, *domain, caddyClient, hmacKey, siteProxyAddr)

	if err := srv.RecoverCaddyRoutes(); err != nil {
		log.Printf("Warning: route recovery failed: %v", err)
	}

	// Start site serving server in background
	siteHandler := srv.NewSiteHandler()
	go func() {
		log.Printf("site server listening on %s", *siteAddr)
		if err := http.ListenAndServe(*siteAddr, siteHandler); err != nil {
			log.Fatalf("site server error: %v", err)
		}
	}()

	log.Printf("droply-server listening on %s (domain=%s, data=%s)", *addr, *domain, *dataDir)
	if err := http.ListenAndServe(*addr, srv); err != nil {
		log.Fatalf("server error: %v", err)
	}
}

func loadOrGenerateHMACKey(secret, dataDir string) ([]byte, error) {
	if secret != "" {
		return []byte(secret), nil
	}

	keyPath := filepath.Join(dataDir, "hmac.key")

	if data, err := os.ReadFile(keyPath); err == nil && len(data) == 32 {
		return data, nil
	}

	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("generate HMAC key: %w", err)
	}

	if err := os.WriteFile(keyPath, key, 0600); err != nil {
		return nil, fmt.Errorf("write HMAC key: %w", err)
	}

	log.Printf("Generated new HMAC key at %s", keyPath)
	return key, nil
}
```

- [ ] **Step 2: Verify it compiles and run all tests**

Run: `cd /Users/chenzhong/Developer/droply && go build ./... && go test ./... -v`
Expected: all tests pass

- [ ] **Step 3: Commit**

```bash
git add cmd/droply-server/main.go
git commit -m "feat: add HMAC key management and site server startup"
```

---

### Task 8: Implement CLI Access Command

**Files:**
- Create: `internal/cli/access.go`
- Modify: `internal/cli/root.go`

- [ ] **Step 1: Implement access CLI commands**

Create `internal/cli/access.go`:

```go
package cli

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

func newAccessCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "access",
		Short: "Manage access control for subdomains and projects",
	}
	cmd.AddCommand(newAccessSetCmd())
	cmd.AddCommand(newAccessGetCmd())
	cmd.AddCommand(newAccessRemoveCmd())
	return cmd
}

func newAccessSetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "set",
		Short: "Set access control rules",
		RunE: func(cmd *cobra.Command, args []string) error {
			sub, _ := cmd.Flags().GetString("subdomain")
			project, _ := cmd.Flags().GetString("project")
			ips, _ := cmd.Flags().GetStringSlice("ip")
			password, _ := cmd.Flags().GetString("password")
			expire, _ := cmd.Flags().GetString("expire")

			if sub == "" {
				return fmt.Errorf("--subdomain is required")
			}

			reqBody := map[string]any{}
			if len(ips) > 0 {
				reqBody["allowed_ips"] = ips
			}
			if password == "auto" {
				reqBody["auto_password"] = true
			} else if password != "" {
				reqBody["password"] = password
			}

			if expire != "" {
				ttl, err := parseDuration(expire)
				if err != nil {
					return fmt.Errorf("invalid --expire value: %w", err)
				}
				reqBody["session_ttl"] = int(ttl.Seconds())
			}

			cfg := LoadConfig()
			client := NewAPIClient(cfg)

			apiPath := fmt.Sprintf("/subdomains/%s/access", sub)
			if project != "" {
				apiPath = fmt.Sprintf("/subdomains/%s/projects/%s/access", sub, project)
			}

			var result map[string]any
			if err := client.doJSON("PUT", apiPath, reqBody, &result); err != nil {
				return err
			}

			fmt.Println("Access control updated.")
			if ips := result["allowed_ips"]; ips != nil {
				fmt.Printf("  IP whitelist: %v\n", ips)
			}
			if result["has_password"] == true {
				if gp, ok := result["generated_password"].(string); ok && gp != "" {
					fmt.Printf("  Password: %s\n", gp)
				} else {
					fmt.Println("  Password: (set)")
				}
			}
			if ttl, ok := result["session_ttl"].(float64); ok {
				fmt.Printf("  Session TTL: %s\n", (time.Duration(ttl) * time.Second).String())
			}

			return nil
		},
	}

	cmd.Flags().String("subdomain", "", "Subdomain name (required)")
	cmd.Flags().String("project", "", "Project name (optional, for project-level rules)")
	cmd.Flags().StringSlice("ip", nil, "Allowed IP or CIDR (repeatable)")
	cmd.Flags().String("password", "", "Password ('auto' to generate, or a custom value)")
	cmd.Flags().String("expire", "24h", "Session expiry duration (e.g. 1h, 24h, 7d)")

	return cmd
}

func newAccessGetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "get",
		Short: "Show access control rules",
		RunE: func(cmd *cobra.Command, args []string) error {
			sub, _ := cmd.Flags().GetString("subdomain")
			project, _ := cmd.Flags().GetString("project")

			if sub == "" {
				return fmt.Errorf("--subdomain is required")
			}

			cfg := LoadConfig()
			client := NewAPIClient(cfg)

			apiPath := fmt.Sprintf("/subdomains/%s/access", sub)
			if project != "" {
				apiPath = fmt.Sprintf("/subdomains/%s/projects/%s/access", sub, project)
			}

			var result map[string]any
			if err := client.doJSON("GET", apiPath, nil, &result); err != nil {
				return err
			}

			target := sub
			if project != "" {
				target = sub + "/" + project
			}
			fmt.Printf("Access control for %s:\n", target)
			if ips := result["allowed_ips"]; ips != nil {
				fmt.Printf("  IP whitelist: %v\n", ips)
			}
			if result["has_password"] == true {
				fmt.Println("  Password: (set)")
			}
			if ttl, ok := result["session_ttl"].(float64); ok {
				fmt.Printf("  Session TTL: %s\n", (time.Duration(ttl) * time.Second).String())
			}
			return nil
		},
	}

	cmd.Flags().String("subdomain", "", "Subdomain name (required)")
	cmd.Flags().String("project", "", "Project name (optional)")

	return cmd
}

func newAccessRemoveCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "remove",
		Short: "Remove access control rules",
		RunE: func(cmd *cobra.Command, args []string) error {
			sub, _ := cmd.Flags().GetString("subdomain")
			project, _ := cmd.Flags().GetString("project")

			if sub == "" {
				return fmt.Errorf("--subdomain is required")
			}

			cfg := LoadConfig()
			client := NewAPIClient(cfg)

			apiPath := fmt.Sprintf("/subdomains/%s/access", sub)
			if project != "" {
				apiPath = fmt.Sprintf("/subdomains/%s/projects/%s/access", sub, project)
			}

			if err := client.doJSON("DELETE", apiPath, nil, nil); err != nil {
				return err
			}

			target := sub
			if project != "" {
				target = sub + "/" + project
			}
			fmt.Printf("Access control removed for %s.\n", target)
			return nil
		},
	}

	cmd.Flags().String("subdomain", "", "Subdomain name (required)")
	cmd.Flags().String("project", "", "Project name (optional)")

	return cmd
}

// parseDuration parses duration strings like "1h", "24h", "7d".
func parseDuration(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if strings.HasSuffix(s, "d") {
		days := strings.TrimSuffix(s, "d")
		var d int
		if _, err := fmt.Sscanf(days, "%d", &d); err != nil {
			return 0, fmt.Errorf("invalid duration: %s", s)
		}
		return time.Duration(d) * 24 * time.Hour, nil
	}
	return time.ParseDuration(s)
}
```

- [ ] **Step 2: Register access command in root.go**

Add to `NewRootCmd()`:

```go
	root.AddCommand(newAccessCmd())
```

- [ ] **Step 3: Verify it compiles**

Run: `cd /Users/chenzhong/Developer/droply && go build ./...`
Expected: no errors

- [ ] **Step 4: Verify CLI help output**

Run: `cd /Users/chenzhong/Developer/droply && go run ./cmd/droply/main.go access --help`
Expected: shows access subcommands (set, get, remove)

Run: `cd /Users/chenzhong/Developer/droply && go run ./cmd/droply/main.go access set --help`
Expected: shows flags (--subdomain, --project, --ip, --password, --expire)

- [ ] **Step 5: Commit**

```bash
git add internal/cli/access.go internal/cli/root.go
git commit -m "feat: add droply access CLI commands"
```

---

### Task 9: Final Integration Verification

**Files:**
- All files from previous tasks

- [ ] **Step 1: Run all tests**

Run: `cd /Users/chenzhong/Developer/droply && go test ./... -v`
Expected: all tests pass

- [ ] **Step 2: Run go vet**

Run: `cd /Users/chenzhong/Developer/droply && go vet ./...`
Expected: no issues

- [ ] **Step 3: Build all binaries**

Run: `cd /Users/chenzhong/Developer/droply && go build ./cmd/droply && go build ./cmd/droply-server`
Expected: both compile successfully

- [ ] **Step 4: Commit any remaining changes**

If any loose changes exist:

```bash
git add -A
git commit -m "chore: final cleanup for access control feature"
```
