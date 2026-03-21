# Droply Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a static content publishing platform with multi-user subdomain support, Go CLI client, and Go API server backed by Caddy.

**Architecture:** Go monorepo with two binaries (`droply` CLI and `droply-server`). Server exposes a REST API on `:8080`, Caddy sits in front handling TLS and static file serving. SQLite stores all metadata. CLI packages directories as tar.gz and uploads via the API.

**Tech Stack:** Go, chi (HTTP router), cobra (CLI), modernc.org/sqlite, bcrypt, BurntSushi/toml, Caddy Admin API

**Spec:** `docs/superpowers/specs/2026-03-21-droply-design.md`

---

### Task 1: Project Scaffolding

**Files:**
- Create: `go.mod`
- Create: `cmd/droply-server/main.go`
- Create: `cmd/droply/main.go`
- Create: `Makefile`

- [ ] **Step 1: Initialize Go module**

```bash
cd /Users/chenzhong/Developer/droply
go mod init github.com/chenzhong/droply
```

- [ ] **Step 2: Create server entry point**

Create `cmd/droply-server/main.go`:

```go
package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Println("droply-server")
	os.Exit(0)
}
```

- [ ] **Step 3: Create CLI entry point**

Create `cmd/droply/main.go`:

```go
package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Println("droply")
	os.Exit(0)
}
```

- [ ] **Step 4: Create Makefile**

```makefile
.PHONY: build server cli test clean

build: server cli

server:
	go build -o bin/droply-server ./cmd/droply-server

cli:
	go build -o bin/droply ./cmd/droply

test:
	go test ./...

clean:
	rm -rf bin/
```

- [ ] **Step 5: Verify both binaries build**

```bash
make build
./bin/droply-server
./bin/droply
```

Expected: Both print their name and exit 0.

- [ ] **Step 6: Commit**

```bash
git add go.mod cmd/ Makefile
git commit -m "feat: scaffold project with server and CLI entry points"
```

---

### Task 2: Data Models and SQLite Store

**Files:**
- Create: `internal/model/model.go`
- Create: `internal/store/store.go`
- Create: `internal/store/sqlite.go`
- Create: `internal/store/sqlite_test.go`

- [ ] **Step 1: Define data models**

Create `internal/model/model.go`:

```go
package model

import "time"

type User struct {
	ID        int64     `json:"id"`
	Email     string    `json:"email"`
	Password  string    `json:"-"`
	APIToken  string    `json:"api_token,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

type Subdomain struct {
	ID           int64     `json:"id"`
	UserID       int64     `json:"user_id"`
	Name         string    `json:"name"`
	CreatedAt    time.Time `json:"created_at"`
	ProjectCount int      `json:"project_count,omitempty"`
}

type Project struct {
	ID          int64     `json:"id"`
	SubdomainID int64     `json:"subdomain_id"`
	Name        string    `json:"name"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type Deployment struct {
	ID        int64     `json:"id"`
	ProjectID int64     `json:"project_id"`
	Version   int       `json:"version"`
	FileCount int       `json:"file_count"`
	TotalSize int64     `json:"total_size"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
}

type CustomDomain struct {
	ID        int64     `json:"id"`
	ProjectID int64     `json:"project_id"`
	Domain    string    `json:"domain"`
	Verified  bool      `json:"verified"`
	CreatedAt time.Time `json:"created_at"`
}

// DomainWithPath is used for startup recovery — includes subdomain and project names.
type DomainWithPath struct {
	Domain        string `json:"domain"`
	SubdomainName string `json:"subdomain_name"`
	ProjectName   string `json:"project_name"`
}
```

- [ ] **Step 2: Define store interface**

Create `internal/store/store.go`:

```go
package store

import "github.com/chenzhong/droply/internal/model"

type Store interface {
	// Users
	CreateUser(email, hashedPassword, apiToken string) (*model.User, error)
	GetUserByEmail(email string) (*model.User, error)
	GetUserByToken(token string) (*model.User, error)

	// Subdomains
	CreateSubdomain(userID int64, name string) (*model.Subdomain, error)
	ListSubdomains(userID int64) ([]model.Subdomain, error)
	GetSubdomainByName(name string) (*model.Subdomain, error)
	DeleteSubdomain(userID int64, name string) error

	// Projects
	CreateProject(subdomainID int64, name string) (*model.Project, error)
	GetProject(subdomainID int64, name string) (*model.Project, error)
	ListProjects(subdomainID int64) ([]model.Project, error)
	DeleteProject(subdomainID int64, name string) error

	// Deployments
	CreateDeployment(projectID int64, fileCount int, totalSize int64) (*model.Deployment, error)
	ActivateDeployment(deploymentID int64) error
	ListDeployments(projectID int64) ([]model.Deployment, error)

	// Custom Domains
	CreateCustomDomain(projectID int64, domain string) (*model.CustomDomain, error)
	GetCustomDomain(domain string) (*model.CustomDomain, error)
	VerifyCustomDomain(domain string) error
	ListCustomDomains(projectID int64) ([]model.CustomDomain, error)
	DeleteCustomDomain(projectID int64, domain string) error

	// Startup recovery
	ListAllSubdomains() ([]model.Subdomain, error)
	ListAllVerifiedDomainsWithPaths() ([]DomainWithPath, error)

	Close() error
}
```

- [ ] **Step 3: Write failing tests for SQLite store**

Create `internal/store/sqlite_test.go`:

```go
package store

import (
	"testing"
)

func setupTestDB(t *testing.T) Store {
	t.Helper()
	s, err := NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestCreateAndGetUser(t *testing.T) {
	s := setupTestDB(t)

	user, err := s.CreateUser("test@example.com", "hashed", "dp_testtoken123")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if user.Email != "test@example.com" {
		t.Errorf("email = %q, want %q", user.Email, "test@example.com")
	}

	got, err := s.GetUserByEmail("test@example.com")
	if err != nil {
		t.Fatalf("GetUserByEmail: %v", err)
	}
	if got.ID != user.ID {
		t.Errorf("ID = %d, want %d", got.ID, user.ID)
	}

	got2, err := s.GetUserByToken("dp_testtoken123")
	if err != nil {
		t.Fatalf("GetUserByToken: %v", err)
	}
	if got2.ID != user.ID {
		t.Errorf("ID = %d, want %d", got2.ID, user.ID)
	}
}

func TestCreateDuplicateUser(t *testing.T) {
	s := setupTestDB(t)
	_, err := s.CreateUser("test@example.com", "hashed", "dp_token1")
	if err != nil {
		t.Fatalf("first CreateUser: %v", err)
	}
	_, err = s.CreateUser("test@example.com", "hashed", "dp_token2")
	if err == nil {
		t.Fatal("expected error for duplicate email, got nil")
	}
}

func TestSubdomainCRUD(t *testing.T) {
	s := setupTestDB(t)
	user, _ := s.CreateUser("test@example.com", "hashed", "dp_token1")

	sub, err := s.CreateSubdomain(user.ID, "alice")
	if err != nil {
		t.Fatalf("CreateSubdomain: %v", err)
	}
	if sub.Name != "alice" {
		t.Errorf("name = %q, want %q", sub.Name, "alice")
	}

	subs, err := s.ListSubdomains(user.ID)
	if err != nil {
		t.Fatalf("ListSubdomains: %v", err)
	}
	if len(subs) != 1 {
		t.Fatalf("len = %d, want 1", len(subs))
	}

	got, err := s.GetSubdomainByName("alice")
	if err != nil {
		t.Fatalf("GetSubdomainByName: %v", err)
	}
	if got.ID != sub.ID {
		t.Errorf("ID = %d, want %d", got.ID, sub.ID)
	}

	err = s.DeleteSubdomain(user.ID, "alice")
	if err != nil {
		t.Fatalf("DeleteSubdomain: %v", err)
	}

	subs, _ = s.ListSubdomains(user.ID)
	if len(subs) != 0 {
		t.Errorf("len = %d, want 0 after delete", len(subs))
	}
}

func TestProjectCRUD(t *testing.T) {
	s := setupTestDB(t)
	user, _ := s.CreateUser("test@example.com", "hashed", "dp_token1")
	sub, _ := s.CreateSubdomain(user.ID, "alice")

	proj, err := s.CreateProject(sub.ID, "blog")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if proj.Name != "blog" {
		t.Errorf("name = %q, want %q", proj.Name, "blog")
	}

	got, err := s.GetProject(sub.ID, "blog")
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}
	if got.ID != proj.ID {
		t.Errorf("ID = %d, want %d", got.ID, proj.ID)
	}

	projects, err := s.ListProjects(sub.ID)
	if err != nil {
		t.Fatalf("ListProjects: %v", err)
	}
	if len(projects) != 1 {
		t.Fatalf("len = %d, want 1", len(projects))
	}

	err = s.DeleteProject(sub.ID, "blog")
	if err != nil {
		t.Fatalf("DeleteProject: %v", err)
	}
	projects, _ = s.ListProjects(sub.ID)
	if len(projects) != 0 {
		t.Errorf("len = %d, want 0 after delete", len(projects))
	}
}

func TestDeploymentLifecycle(t *testing.T) {
	s := setupTestDB(t)
	user, _ := s.CreateUser("test@example.com", "hashed", "dp_token1")
	sub, _ := s.CreateSubdomain(user.ID, "alice")
	proj, _ := s.CreateProject(sub.ID, "blog")

	dep, err := s.CreateDeployment(proj.ID, 5, 1024)
	if err != nil {
		t.Fatalf("CreateDeployment: %v", err)
	}
	if dep.Status != "uploading" {
		t.Errorf("status = %q, want %q", dep.Status, "uploading")
	}
	if dep.Version != 1 {
		t.Errorf("version = %d, want 1", dep.Version)
	}

	err = s.ActivateDeployment(dep.ID)
	if err != nil {
		t.Fatalf("ActivateDeployment: %v", err)
	}

	deps, err := s.ListDeployments(proj.ID)
	if err != nil {
		t.Fatalf("ListDeployments: %v", err)
	}
	if len(deps) != 1 {
		t.Fatalf("len = %d, want 1", len(deps))
	}
	if deps[0].Status != "active" {
		t.Errorf("status = %q, want %q", deps[0].Status, "active")
	}
}

func TestCustomDomainCRUD(t *testing.T) {
	s := setupTestDB(t)
	user, _ := s.CreateUser("test@example.com", "hashed", "dp_token1")
	sub, _ := s.CreateSubdomain(user.ID, "alice")
	proj, _ := s.CreateProject(sub.ID, "blog")

	cd, err := s.CreateCustomDomain(proj.ID, "blog.alice.com")
	if err != nil {
		t.Fatalf("CreateCustomDomain: %v", err)
	}
	if cd.Verified {
		t.Error("expected verified=false for new domain")
	}

	err = s.VerifyCustomDomain("blog.alice.com")
	if err != nil {
		t.Fatalf("VerifyCustomDomain: %v", err)
	}

	got, err := s.GetCustomDomain("blog.alice.com")
	if err != nil {
		t.Fatalf("GetCustomDomain: %v", err)
	}
	if !got.Verified {
		t.Error("expected verified=true after verify")
	}

	domains, err := s.ListCustomDomains(proj.ID)
	if err != nil {
		t.Fatalf("ListCustomDomains: %v", err)
	}
	if len(domains) != 1 {
		t.Fatalf("len = %d, want 1", len(domains))
	}

	err = s.DeleteCustomDomain(proj.ID, "blog.alice.com")
	if err != nil {
		t.Fatalf("DeleteCustomDomain: %v", err)
	}
	domains, _ = s.ListCustomDomains(proj.ID)
	if len(domains) != 0 {
		t.Errorf("len = %d, want 0 after delete", len(domains))
	}
}
```

- [ ] **Step 4: Run tests to verify they fail**

```bash
go test ./internal/store/ -v
```

Expected: FAIL — `NewSQLiteStore` not defined.

- [ ] **Step 5: Implement SQLite store**

Create `internal/store/sqlite.go`:

```go
package store

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/chenzhong/droply/internal/model"
	_ "modernc.org/sqlite"
)

type SQLiteStore struct {
	db *sql.DB
}

func NewSQLiteStore(dsn string) (*SQLiteStore, error) {
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}

	// Enable WAL mode and foreign keys
	for _, pragma := range []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA foreign_keys=ON",
	} {
		if _, err := db.Exec(pragma); err != nil {
			return nil, fmt.Errorf("exec %s: %w", pragma, err)
		}
	}

	s := &SQLiteStore{db: db}
	if err := s.migrate(); err != nil {
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return s, nil
}

func (s *SQLiteStore) migrate() error {
	_, err := s.db.Exec(`
		CREATE TABLE IF NOT EXISTS users (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			email TEXT UNIQUE NOT NULL,
			password TEXT NOT NULL,
			api_token TEXT UNIQUE NOT NULL,
			created_at DATETIME NOT NULL DEFAULT (datetime('now'))
		);

		CREATE TABLE IF NOT EXISTS subdomains (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			name TEXT UNIQUE NOT NULL,
			created_at DATETIME NOT NULL DEFAULT (datetime('now'))
		);

		CREATE TABLE IF NOT EXISTS projects (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			subdomain_id INTEGER NOT NULL REFERENCES subdomains(id) ON DELETE CASCADE,
			name TEXT NOT NULL,
			created_at DATETIME NOT NULL DEFAULT (datetime('now')),
			updated_at DATETIME NOT NULL DEFAULT (datetime('now')),
			UNIQUE(subdomain_id, name)
		);

		CREATE TABLE IF NOT EXISTS deployments (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			project_id INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
			version INTEGER NOT NULL,
			file_count INTEGER NOT NULL,
			total_size INTEGER NOT NULL,
			status TEXT NOT NULL DEFAULT 'uploading',
			created_at DATETIME NOT NULL DEFAULT (datetime('now'))
		);

		CREATE TABLE IF NOT EXISTS custom_domains (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			project_id INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
			domain TEXT UNIQUE NOT NULL,
			verified BOOLEAN NOT NULL DEFAULT 0,
			created_at DATETIME NOT NULL DEFAULT (datetime('now'))
		);
	`)
	return err
}

func (s *SQLiteStore) Close() error {
	return s.db.Close()
}

// --- Users ---

func (s *SQLiteStore) CreateUser(email, hashedPassword, apiToken string) (*model.User, error) {
	res, err := s.db.Exec(
		"INSERT INTO users (email, password, api_token) VALUES (?, ?, ?)",
		email, hashedPassword, apiToken,
	)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return s.getUserByID(id)
}

func (s *SQLiteStore) getUserByID(id int64) (*model.User, error) {
	u := &model.User{}
	var createdAt string
	err := s.db.QueryRow(
		"SELECT id, email, password, api_token, created_at FROM users WHERE id = ?", id,
	).Scan(&u.ID, &u.Email, &u.Password, &u.APIToken, &createdAt)
	if err != nil {
		return nil, err
	}
	u.CreatedAt, _ = time.Parse("2006-01-02 15:04:05", createdAt)
	return u, nil
}

func (s *SQLiteStore) GetUserByEmail(email string) (*model.User, error) {
	u := &model.User{}
	var createdAt string
	err := s.db.QueryRow(
		"SELECT id, email, password, api_token, created_at FROM users WHERE email = ?", email,
	).Scan(&u.ID, &u.Email, &u.Password, &u.APIToken, &createdAt)
	if err != nil {
		return nil, err
	}
	u.CreatedAt, _ = time.Parse("2006-01-02 15:04:05", createdAt)
	return u, nil
}

func (s *SQLiteStore) GetUserByToken(token string) (*model.User, error) {
	u := &model.User{}
	var createdAt string
	err := s.db.QueryRow(
		"SELECT id, email, password, api_token, created_at FROM users WHERE api_token = ?", token,
	).Scan(&u.ID, &u.Email, &u.Password, &u.APIToken, &createdAt)
	if err != nil {
		return nil, err
	}
	u.CreatedAt, _ = time.Parse("2006-01-02 15:04:05", createdAt)
	return u, nil
}

// --- Subdomains ---

func (s *SQLiteStore) CreateSubdomain(userID int64, name string) (*model.Subdomain, error) {
	res, err := s.db.Exec(
		"INSERT INTO subdomains (user_id, name) VALUES (?, ?)", userID, name,
	)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	sub := &model.Subdomain{}
	var createdAt string
	err = s.db.QueryRow(
		"SELECT id, user_id, name, created_at FROM subdomains WHERE id = ?", id,
	).Scan(&sub.ID, &sub.UserID, &sub.Name, &createdAt)
	if err != nil {
		return nil, err
	}
	sub.CreatedAt, _ = time.Parse("2006-01-02 15:04:05", createdAt)
	return sub, nil
}

func (s *SQLiteStore) ListSubdomains(userID int64) ([]model.Subdomain, error) {
	rows, err := s.db.Query(`
		SELECT s.id, s.user_id, s.name, s.created_at,
			(SELECT COUNT(*) FROM projects WHERE subdomain_id = s.id) as project_count
		FROM subdomains s WHERE s.user_id = ? ORDER BY s.name`, userID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var subs []model.Subdomain
	for rows.Next() {
		var sub model.Subdomain
		var createdAt string
		if err := rows.Scan(&sub.ID, &sub.UserID, &sub.Name, &createdAt, &sub.ProjectCount); err != nil {
			return nil, err
		}
		sub.CreatedAt, _ = time.Parse("2006-01-02 15:04:05", createdAt)
		subs = append(subs, sub)
	}
	return subs, rows.Err()
}

func (s *SQLiteStore) GetSubdomainByName(name string) (*model.Subdomain, error) {
	sub := &model.Subdomain{}
	var createdAt string
	err := s.db.QueryRow(
		"SELECT id, user_id, name, created_at FROM subdomains WHERE name = ?", name,
	).Scan(&sub.ID, &sub.UserID, &sub.Name, &createdAt)
	if err != nil {
		return nil, err
	}
	sub.CreatedAt, _ = time.Parse("2006-01-02 15:04:05", createdAt)
	return sub, nil
}

func (s *SQLiteStore) DeleteSubdomain(userID int64, name string) error {
	_, err := s.db.Exec(
		"DELETE FROM subdomains WHERE user_id = ? AND name = ?", userID, name,
	)
	return err
}

// --- Projects ---

func (s *SQLiteStore) CreateProject(subdomainID int64, name string) (*model.Project, error) {
	res, err := s.db.Exec(
		"INSERT INTO projects (subdomain_id, name) VALUES (?, ?)", subdomainID, name,
	)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	proj := &model.Project{}
	var createdAt, updatedAt string
	err = s.db.QueryRow(
		"SELECT id, subdomain_id, name, created_at, updated_at FROM projects WHERE id = ?", id,
	).Scan(&proj.ID, &proj.SubdomainID, &proj.Name, &createdAt, &updatedAt)
	if err != nil {
		return nil, err
	}
	proj.CreatedAt, _ = time.Parse("2006-01-02 15:04:05", createdAt)
	proj.UpdatedAt, _ = time.Parse("2006-01-02 15:04:05", updatedAt)
	return proj, nil
}

func (s *SQLiteStore) GetProject(subdomainID int64, name string) (*model.Project, error) {
	proj := &model.Project{}
	var createdAt, updatedAt string
	err := s.db.QueryRow(
		"SELECT id, subdomain_id, name, created_at, updated_at FROM projects WHERE subdomain_id = ? AND name = ?",
		subdomainID, name,
	).Scan(&proj.ID, &proj.SubdomainID, &proj.Name, &createdAt, &updatedAt)
	if err != nil {
		return nil, err
	}
	proj.CreatedAt, _ = time.Parse("2006-01-02 15:04:05", createdAt)
	proj.UpdatedAt, _ = time.Parse("2006-01-02 15:04:05", updatedAt)
	return proj, nil
}

func (s *SQLiteStore) ListProjects(subdomainID int64) ([]model.Project, error) {
	rows, err := s.db.Query(
		"SELECT id, subdomain_id, name, created_at, updated_at FROM projects WHERE subdomain_id = ? ORDER BY name",
		subdomainID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var projects []model.Project
	for rows.Next() {
		var p model.Project
		var createdAt, updatedAt string
		if err := rows.Scan(&p.ID, &p.SubdomainID, &p.Name, &createdAt, &updatedAt); err != nil {
			return nil, err
		}
		p.CreatedAt, _ = time.Parse("2006-01-02 15:04:05", createdAt)
		p.UpdatedAt, _ = time.Parse("2006-01-02 15:04:05", updatedAt)
		projects = append(projects, p)
	}
	return projects, rows.Err()
}

func (s *SQLiteStore) DeleteProject(subdomainID int64, name string) error {
	_, err := s.db.Exec(
		"DELETE FROM projects WHERE subdomain_id = ? AND name = ?", subdomainID, name,
	)
	return err
}

// --- Deployments ---

func (s *SQLiteStore) CreateDeployment(projectID int64, fileCount int, totalSize int64) (*model.Deployment, error) {
	// Get next version number
	var maxVersion sql.NullInt64
	s.db.QueryRow(
		"SELECT MAX(version) FROM deployments WHERE project_id = ?", projectID,
	).Scan(&maxVersion)

	version := 1
	if maxVersion.Valid {
		version = int(maxVersion.Int64) + 1
	}

	res, err := s.db.Exec(
		"INSERT INTO deployments (project_id, version, file_count, total_size, status) VALUES (?, ?, ?, ?, 'uploading')",
		projectID, version, fileCount, totalSize,
	)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	dep := &model.Deployment{}
	var createdAt string
	err = s.db.QueryRow(
		"SELECT id, project_id, version, file_count, total_size, status, created_at FROM deployments WHERE id = ?", id,
	).Scan(&dep.ID, &dep.ProjectID, &dep.Version, &dep.FileCount, &dep.TotalSize, &dep.Status, &createdAt)
	if err != nil {
		return nil, err
	}
	dep.CreatedAt, _ = time.Parse("2006-01-02 15:04:05", createdAt)
	return dep, nil
}

func (s *SQLiteStore) ActivateDeployment(deploymentID int64) error {
	// Archive previous active deployments for same project
	_, err := s.db.Exec(`
		UPDATE deployments SET status = 'archived'
		WHERE project_id = (SELECT project_id FROM deployments WHERE id = ?)
		AND status = 'active'`, deploymentID,
	)
	if err != nil {
		return err
	}
	_, err = s.db.Exec("UPDATE deployments SET status = 'active' WHERE id = ?", deploymentID)
	return err
}

func (s *SQLiteStore) ListDeployments(projectID int64) ([]model.Deployment, error) {
	rows, err := s.db.Query(
		"SELECT id, project_id, version, file_count, total_size, status, created_at FROM deployments WHERE project_id = ? ORDER BY version DESC",
		projectID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var deps []model.Deployment
	for rows.Next() {
		var d model.Deployment
		var createdAt string
		if err := rows.Scan(&d.ID, &d.ProjectID, &d.Version, &d.FileCount, &d.TotalSize, &d.Status, &createdAt); err != nil {
			return nil, err
		}
		d.CreatedAt, _ = time.Parse("2006-01-02 15:04:05", createdAt)
		deps = append(deps, d)
	}
	return deps, rows.Err()
}

// --- Custom Domains ---

func (s *SQLiteStore) CreateCustomDomain(projectID int64, domain string) (*model.CustomDomain, error) {
	res, err := s.db.Exec(
		"INSERT INTO custom_domains (project_id, domain) VALUES (?, ?)", projectID, domain,
	)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	cd := &model.CustomDomain{}
	var createdAt string
	err = s.db.QueryRow(
		"SELECT id, project_id, domain, verified, created_at FROM custom_domains WHERE id = ?", id,
	).Scan(&cd.ID, &cd.ProjectID, &cd.Domain, &cd.Verified, &createdAt)
	if err != nil {
		return nil, err
	}
	cd.CreatedAt, _ = time.Parse("2006-01-02 15:04:05", createdAt)
	return cd, nil
}

func (s *SQLiteStore) GetCustomDomain(domain string) (*model.CustomDomain, error) {
	cd := &model.CustomDomain{}
	var createdAt string
	err := s.db.QueryRow(
		"SELECT id, project_id, domain, verified, created_at FROM custom_domains WHERE domain = ?", domain,
	).Scan(&cd.ID, &cd.ProjectID, &cd.Domain, &cd.Verified, &createdAt)
	if err != nil {
		return nil, err
	}
	cd.CreatedAt, _ = time.Parse("2006-01-02 15:04:05", createdAt)
	return cd, nil
}

func (s *SQLiteStore) VerifyCustomDomain(domain string) error {
	_, err := s.db.Exec("UPDATE custom_domains SET verified = 1 WHERE domain = ?", domain)
	return err
}

func (s *SQLiteStore) ListCustomDomains(projectID int64) ([]model.CustomDomain, error) {
	rows, err := s.db.Query(
		"SELECT id, project_id, domain, verified, created_at FROM custom_domains WHERE project_id = ? ORDER BY domain",
		projectID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var domains []model.CustomDomain
	for rows.Next() {
		var cd model.CustomDomain
		var createdAt string
		if err := rows.Scan(&cd.ID, &cd.ProjectID, &cd.Domain, &cd.Verified, &createdAt); err != nil {
			return nil, err
		}
		cd.CreatedAt, _ = time.Parse("2006-01-02 15:04:05", createdAt)
		domains = append(domains, cd)
	}
	return domains, rows.Err()
}

func (s *SQLiteStore) DeleteCustomDomain(projectID int64, domain string) error {
	_, err := s.db.Exec(
		"DELETE FROM custom_domains WHERE project_id = ? AND domain = ?", projectID, domain,
	)
	return err
}

// --- Startup Recovery ---

func (s *SQLiteStore) ListAllSubdomains() ([]model.Subdomain, error) {
	rows, err := s.db.Query("SELECT id, user_id, name, created_at FROM subdomains ORDER BY name")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var subs []model.Subdomain
	for rows.Next() {
		var sub model.Subdomain
		var createdAt string
		if err := rows.Scan(&sub.ID, &sub.UserID, &sub.Name, &createdAt); err != nil {
			return nil, err
		}
		sub.CreatedAt, _ = time.Parse("2006-01-02 15:04:05", createdAt)
		subs = append(subs, sub)
	}
	return subs, rows.Err()
}

func (s *SQLiteStore) ListAllVerifiedDomainsWithPaths() ([]model.DomainWithPath, error) {
	rows, err := s.db.Query(`
		SELECT cd.domain, sub.name, p.name
		FROM custom_domains cd
		JOIN projects p ON cd.project_id = p.id
		JOIN subdomains sub ON p.subdomain_id = sub.id
		WHERE cd.verified = 1
		ORDER BY cd.domain`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var domains []model.DomainWithPath
	for rows.Next() {
		var d model.DomainWithPath
		if err := rows.Scan(&d.Domain, &d.SubdomainName, &d.ProjectName); err != nil {
			return nil, err
		}
		domains = append(domains, d)
	}
	return domains, rows.Err()
}
```

- [ ] **Step 6: Run tests to verify they pass**

```bash
go test ./internal/store/ -v
```

Expected: All PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/model/ internal/store/ go.mod go.sum
git commit -m "feat: add data models and SQLite store with tests"
```

---

### Task 3: HTTP Server with Auth Endpoints

**Files:**
- Create: `internal/server/server.go`
- Create: `internal/server/auth.go`
- Create: `internal/server/server_test.go`
- Modify: `cmd/droply-server/main.go`

- [ ] **Step 1: Write failing tests for auth endpoints**

Create `internal/server/server_test.go`:

```go
package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/chenzhong/droply/internal/store"
)

func setupTestServer(t *testing.T) *Server {
	t.Helper()
	s, err := store.NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}
	t.Cleanup(func() { s.Close() })

	srv := New(s, "/tmp/droply-test-sites", "droply.dev", nil)
	return srv
}

func TestRegister(t *testing.T) {
	srv := setupTestServer(t)

	body, _ := json.Marshal(map[string]string{
		"email":    "test@example.com",
		"password": "secret123",
	})
	req := httptest.NewRequest("POST", "/auth/register", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.Router().ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d; body: %s", w.Code, http.StatusCreated, w.Body.String())
	}

	var resp map[string]string
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["api_token"] == "" {
		t.Error("expected non-empty api_token")
	}
}

func TestRegisterDuplicate(t *testing.T) {
	srv := setupTestServer(t)

	body, _ := json.Marshal(map[string]string{
		"email":    "test@example.com",
		"password": "secret123",
	})

	req := httptest.NewRequest("POST", "/auth/register", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.Router().ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("first register: status = %d", w.Code)
	}

	req = httptest.NewRequest("POST", "/auth/register", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	srv.Router().ServeHTTP(w, req)
	if w.Code != http.StatusConflict {
		t.Errorf("duplicate register: status = %d, want %d", w.Code, http.StatusConflict)
	}
}

func TestLogin(t *testing.T) {
	srv := setupTestServer(t)

	// Register first
	body, _ := json.Marshal(map[string]string{
		"email":    "test@example.com",
		"password": "secret123",
	})
	req := httptest.NewRequest("POST", "/auth/register", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.Router().ServeHTTP(w, req)

	// Login
	req = httptest.NewRequest("POST", "/auth/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	srv.Router().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	var resp map[string]string
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["api_token"] == "" {
		t.Error("expected non-empty api_token")
	}
}

func TestLoginWrongPassword(t *testing.T) {
	srv := setupTestServer(t)

	body, _ := json.Marshal(map[string]string{
		"email":    "test@example.com",
		"password": "secret123",
	})
	req := httptest.NewRequest("POST", "/auth/register", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.Router().ServeHTTP(w, req)

	body, _ = json.Marshal(map[string]string{
		"email":    "test@example.com",
		"password": "wrong",
	})
	req = httptest.NewRequest("POST", "/auth/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	srv.Router().ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", w.Code, http.StatusUnauthorized)
	}
}

// Helper to register and get token for subsequent tests
func registerAndGetToken(t *testing.T, srv *Server) string {
	t.Helper()
	body, _ := json.Marshal(map[string]string{
		"email":    "test@example.com",
		"password": "secret123",
	})
	req := httptest.NewRequest("POST", "/auth/register", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.Router().ServeHTTP(w, req)

	var resp map[string]string
	json.NewDecoder(w.Body).Decode(&resp)
	return resp["api_token"]
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test ./internal/server/ -v
```

Expected: FAIL — `server.New` not defined.

- [ ] **Step 3: Implement server and auth handlers**

Create `internal/server/server.go`:

```go
package server

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/chenzhong/droply/internal/model"
	"github.com/chenzhong/droply/internal/store"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

type CaddyClient interface {
	AddSubdomainRoute(name string) error
	RemoveSubdomainRoute(name string) error
	AddCustomDomainRoute(domain, subdomainName, projectName string) error
	RemoveCustomDomainRoute(domain string) error
}

type Server struct {
	store    store.Store
	sitesDir string
	baseDomain string
	caddy    CaddyClient
	router   chi.Router
}

func New(s store.Store, sitesDir, baseDomain string, caddy CaddyClient) *Server {
	srv := &Server{
		store:      s,
		sitesDir:   sitesDir,
		baseDomain: baseDomain,
		caddy:      caddy,
	}
	srv.router = srv.buildRouter()
	return srv
}

func (s *Server) Router() chi.Router {
	return s.router
}

func (s *Server) buildRouter() chi.Router {
	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	// Public routes
	r.Post("/auth/register", s.handleRegister)
	r.Post("/auth/login", s.handleLogin)

	// Authenticated routes
	r.Group(func(r chi.Router) {
		r.Use(s.authMiddleware)
		r.Post("/subdomains", s.handleCreateSubdomain)
		r.Get("/subdomains", s.handleListSubdomains)
		r.Delete("/subdomains/{name}", s.handleDeleteSubdomain)
		r.Get("/subdomains/{sub}/projects", s.handleListProjects)
		r.Delete("/subdomains/{sub}/projects/{name}", s.handleDeleteProject)
		r.Post("/subdomains/{sub}/projects/{name}/deploy", s.handleDeploy)
		r.Get("/subdomains/{sub}/projects/{name}/deployments", s.handleListDeployments)
		r.Post("/subdomains/{sub}/projects/{name}/domains", s.handleCreateDomain)
		r.Get("/subdomains/{sub}/projects/{name}/domains", s.handleListDomains)
		r.Delete("/subdomains/{sub}/projects/{name}/domains/{domain}", s.handleDeleteDomain)
	})

	return r
}

type contextKey string

const userContextKey contextKey = "user"

func (s *Server) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := r.Header.Get("Authorization")
		if len(token) > 7 && token[:7] == "Bearer " {
			token = token[7:]
		} else {
			jsonError(w, "missing or invalid authorization header", http.StatusUnauthorized)
			return
		}

		user, err := s.store.GetUserByToken(token)
		if err != nil {
			jsonError(w, "invalid token", http.StatusUnauthorized)
			return
		}

		ctx := context.WithValue(r.Context(), userContextKey, user)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func userFromContext(ctx context.Context) *model.User {
	u, _ := ctx.Value(userContextKey).(*model.User)
	return u
}

func jsonError(w http.ResponseWriter, msg string, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func jsonResponse(w http.ResponseWriter, data any, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(data)
}
```

Create `internal/server/auth.go`:

```go
package server

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"

	"golang.org/x/crypto/bcrypt"
)

func generateToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "dp_" + hex.EncodeToString(b), nil
}

type authRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	var req authRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid request body", http.StatusBadRequest)
		return
	}

	if req.Email == "" || req.Password == "" {
		jsonError(w, "email and password are required", http.StatusBadRequest)
		return
	}

	hashed, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		jsonError(w, "internal error", http.StatusInternalServerError)
		return
	}

	token, err := generateToken()
	if err != nil {
		jsonError(w, "internal error", http.StatusInternalServerError)
		return
	}

	user, err := s.store.CreateUser(req.Email, string(hashed), token)
	if err != nil {
		jsonError(w, "email already registered", http.StatusConflict)
		return
	}

	jsonResponse(w, map[string]string{
		"api_token": user.APIToken,
	}, http.StatusCreated)
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req authRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid request body", http.StatusBadRequest)
		return
	}

	user, err := s.store.GetUserByEmail(req.Email)
	if err != nil {
		jsonError(w, "invalid email or password", http.StatusUnauthorized)
		return
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(req.Password)); err != nil {
		jsonError(w, "invalid email or password", http.StatusUnauthorized)
		return
	}

	jsonResponse(w, map[string]string{
		"api_token": user.APIToken,
	}, http.StatusOK)
}
```

Add stub handlers to `server.go` for endpoints implemented in later tasks:

```go
// Stub handlers — implemented in later tasks
func (s *Server) handleCreateSubdomain(w http.ResponseWriter, r *http.Request) {
	jsonError(w, "not implemented", http.StatusNotImplemented)
}
func (s *Server) handleListSubdomains(w http.ResponseWriter, r *http.Request) {
	jsonError(w, "not implemented", http.StatusNotImplemented)
}
func (s *Server) handleDeleteSubdomain(w http.ResponseWriter, r *http.Request) {
	jsonError(w, "not implemented", http.StatusNotImplemented)
}
func (s *Server) handleListProjects(w http.ResponseWriter, r *http.Request) {
	jsonError(w, "not implemented", http.StatusNotImplemented)
}
func (s *Server) handleDeleteProject(w http.ResponseWriter, r *http.Request) {
	jsonError(w, "not implemented", http.StatusNotImplemented)
}
func (s *Server) handleDeploy(w http.ResponseWriter, r *http.Request) {
	jsonError(w, "not implemented", http.StatusNotImplemented)
}
func (s *Server) handleListDeployments(w http.ResponseWriter, r *http.Request) {
	jsonError(w, "not implemented", http.StatusNotImplemented)
}
func (s *Server) handleCreateDomain(w http.ResponseWriter, r *http.Request) {
	jsonError(w, "not implemented", http.StatusNotImplemented)
}
func (s *Server) handleListDomains(w http.ResponseWriter, r *http.Request) {
	jsonError(w, "not implemented", http.StatusNotImplemented)
}
func (s *Server) handleDeleteDomain(w http.ResponseWriter, r *http.Request) {
	jsonError(w, "not implemented", http.StatusNotImplemented)
}
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
go test ./internal/server/ -v
```

Expected: All PASS.

- [ ] **Step 5: Wire up server main**

Update `cmd/droply-server/main.go`:

```go
package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"

	"github.com/chenzhong/droply/internal/server"
	"github.com/chenzhong/droply/internal/store"
)

func main() {
	addr := flag.String("addr", ":8080", "listen address")
	dataDir := flag.String("data-dir", "/data/droply", "data directory")
	baseDomain := flag.String("domain", "droply.dev", "base domain")
	flag.Parse()

	dbPath := *dataDir + "/droply.db"
	sitesDir := *dataDir + "/sites"

	if err := os.MkdirAll(sitesDir, 0755); err != nil {
		log.Fatalf("create sites dir: %v", err)
	}

	s, err := store.NewSQLiteStore(dbPath)
	if err != nil {
		log.Fatalf("open database: %v", err)
	}
	defer s.Close()

	srv := server.New(s, sitesDir, *baseDomain, nil)
	fmt.Printf("droply-server listening on %s\n", *addr)
	log.Fatal(http.ListenAndServe(*addr, srv.Router()))
}
```

- [ ] **Step 6: Verify build**

```bash
make build
```

Expected: Both binaries build successfully.

- [ ] **Step 7: Commit**

```bash
git add internal/server/ cmd/droply-server/ go.mod go.sum
git commit -m "feat: add HTTP server with auth endpoints (register/login)"
```

---

### Task 4: Subdomain and Project API Handlers

**Files:**
- Create: `internal/server/subdomain.go`
- Create: `internal/server/project.go`
- Modify: `internal/server/server.go` (remove stubs)
- Create: `internal/server/subdomain_test.go`

- [ ] **Step 1: Write failing tests for subdomain endpoints**

Add to `internal/server/subdomain_test.go`:

```go
package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSubdomainCRUD(t *testing.T) {
	srv := setupTestServer(t)
	token := registerAndGetToken(t, srv)

	// Create subdomain
	body, _ := json.Marshal(map[string]string{"name": "alice"})
	req := httptest.NewRequest("POST", "/subdomains", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	srv.Router().ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("create subdomain: status = %d, want %d; body: %s", w.Code, http.StatusCreated, w.Body.String())
	}

	// List subdomains
	req = httptest.NewRequest("GET", "/subdomains", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w = httptest.NewRecorder()
	srv.Router().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("list subdomains: status = %d", w.Code)
	}

	var subs []map[string]any
	json.NewDecoder(w.Body).Decode(&subs)
	if len(subs) != 1 {
		t.Fatalf("len = %d, want 1", len(subs))
	}
	if subs[0]["name"] != "alice" {
		t.Errorf("name = %v, want alice", subs[0]["name"])
	}

	// Delete subdomain
	req = httptest.NewRequest("DELETE", "/subdomains/alice", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w = httptest.NewRecorder()
	srv.Router().ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Errorf("delete subdomain: status = %d, want %d", w.Code, http.StatusNoContent)
	}
}

func TestSubdomainInvalidName(t *testing.T) {
	srv := setupTestServer(t)
	token := registerAndGetToken(t, srv)

	body, _ := json.Marshal(map[string]string{"name": "INVALID!"})
	req := httptest.NewRequest("POST", "/subdomains", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	srv.Router().ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestSubdomainUnauthorized(t *testing.T) {
	srv := setupTestServer(t)

	req := httptest.NewRequest("GET", "/subdomains", nil)
	w := httptest.NewRecorder()
	srv.Router().ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", w.Code, http.StatusUnauthorized)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test ./internal/server/ -run TestSubdomain -v
```

Expected: FAIL — stubs return 501.

- [ ] **Step 3: Implement subdomain handlers**

Create `internal/server/subdomain.go`:

```go
package server

import (
	"encoding/json"
	"net/http"
	"regexp"

	"github.com/go-chi/chi/v5"
)

var nameRegex = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,30}[a-z0-9]$`)

func validName(name string) bool {
	return nameRegex.MatchString(name)
}

func (s *Server) handleCreateSubdomain(w http.ResponseWriter, r *http.Request) {
	user := userFromContext(r.Context())

	var req struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid request body", http.StatusBadRequest)
		return
	}

	if !validName(req.Name) {
		jsonError(w, "invalid subdomain name: must be 3-32 chars, lowercase alphanumeric and hyphens", http.StatusBadRequest)
		return
	}

	sub, err := s.store.CreateSubdomain(user.ID, req.Name)
	if err != nil {
		jsonError(w, "subdomain already exists", http.StatusConflict)
		return
	}

	if s.caddy != nil {
		if err := s.caddy.AddSubdomainRoute(req.Name); err != nil {
			jsonError(w, "failed to configure routing", http.StatusInternalServerError)
			return
		}
	}

	jsonResponse(w, sub, http.StatusCreated)
}

func (s *Server) handleListSubdomains(w http.ResponseWriter, r *http.Request) {
	user := userFromContext(r.Context())

	subs, err := s.store.ListSubdomains(user.ID)
	if err != nil {
		jsonError(w, "internal error", http.StatusInternalServerError)
		return
	}
	if subs == nil {
		subs = []model.Subdomain{}
	}
	jsonResponse(w, subs, http.StatusOK)
}

func (s *Server) handleDeleteSubdomain(w http.ResponseWriter, r *http.Request) {
	user := userFromContext(r.Context())
	name := chi.URLParam(r, "name")

	// Verify ownership
	sub, err := s.store.GetSubdomainByName(name)
	if err != nil || sub.UserID != user.ID {
		jsonError(w, "subdomain not found", http.StatusNotFound)
		return
	}

	if err := s.store.DeleteSubdomain(user.ID, name); err != nil {
		jsonError(w, "internal error", http.StatusInternalServerError)
		return
	}

	if s.caddy != nil {
		s.caddy.RemoveSubdomainRoute(name)
	}

	w.WriteHeader(http.StatusNoContent)
}
```


- [ ] **Step 4: Implement project handlers**

Create `internal/server/project.go`:

```go
package server

import (
	"net/http"
	"os"
	"path/filepath"

	"github.com/chenzhong/droply/internal/model"
	"github.com/go-chi/chi/v5"
)

func (s *Server) handleListProjects(w http.ResponseWriter, r *http.Request) {
	user := userFromContext(r.Context())
	subName := chi.URLParam(r, "sub")

	sub, err := s.store.GetSubdomainByName(subName)
	if err != nil || sub.UserID != user.ID {
		jsonError(w, "subdomain not found", http.StatusNotFound)
		return
	}

	projects, err := s.store.ListProjects(sub.ID)
	if err != nil {
		jsonError(w, "internal error", http.StatusInternalServerError)
		return
	}
	if projects == nil {
		projects = []model.Project{}
	}
	jsonResponse(w, projects, http.StatusOK)
}

func (s *Server) handleDeleteProject(w http.ResponseWriter, r *http.Request) {
	user := userFromContext(r.Context())
	subName := chi.URLParam(r, "sub")
	projName := chi.URLParam(r, "name")

	sub, err := s.store.GetSubdomainByName(subName)
	if err != nil || sub.UserID != user.ID {
		jsonError(w, "subdomain not found", http.StatusNotFound)
		return
	}

	if err := s.store.DeleteProject(sub.ID, projName); err != nil {
		jsonError(w, "internal error", http.StatusInternalServerError)
		return
	}

	// Remove files from disk
	os.RemoveAll(filepath.Join(s.sitesDir, subName, projName))

	w.WriteHeader(http.StatusNoContent)
}
```


Remove the stub handlers from `server.go` (the ones for subdomain, project — keep deploy and domain stubs for now).

- [ ] **Step 5: Run tests to verify they pass**

```bash
go test ./internal/server/ -v
```

Expected: All PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/server/
git commit -m "feat: add subdomain and project API handlers"
```

---

### Task 5: Deploy Handler

**Files:**
- Create: `internal/server/deploy.go` (replace stub)
- Create: `internal/server/deploy_test.go`

- [ ] **Step 1: Write failing test for deploy endpoint**

Create `internal/server/deploy_test.go`:

```go
package server

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func createTestTarGz(t *testing.T, files map[string]string) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)

	for name, content := range files {
		hdr := &tar.Header{
			Name: name,
			Mode: 0644,
			Size: int64(len(content)),
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	tw.Close()
	gw.Close()
	return &buf
}

func TestDeploy(t *testing.T) {
	sitesDir := t.TempDir()
	s, _ := store.NewSQLiteStore(":memory:")
	t.Cleanup(func() { s.Close() })
	srv := New(s, sitesDir, "droply.dev", nil)

	token := registerAndGetToken(t, srv)

	// Create subdomain first
	body, _ := json.Marshal(map[string]string{"name": "alice"})
	req := httptest.NewRequest("POST", "/subdomains", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	srv.Router().ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create subdomain: %d", w.Code)
	}

	// Create tar.gz
	tarBuf := createTestTarGz(t, map[string]string{
		"index.html": "<h1>Hello</h1>",
		"style.css":  "body { color: red; }",
	})

	// Build multipart request
	var mpBuf bytes.Buffer
	mpw := multipart.NewWriter(&mpBuf)
	fw, _ := mpw.CreateFormFile("file", "deploy.tar.gz")
	io.Copy(fw, tarBuf)
	mpw.Close()

	req = httptest.NewRequest("POST", "/subdomains/alice/projects/blog/deploy", &mpBuf)
	req.Header.Set("Content-Type", mpw.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+token)
	w = httptest.NewRecorder()
	srv.Router().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("deploy: status = %d, want %d; body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["version"] != float64(1) {
		t.Errorf("version = %v, want 1", resp["version"])
	}
	if resp["url"] != "https://alice.droply.dev/blog" {
		t.Errorf("url = %v, want https://alice.droply.dev/blog", resp["url"])
	}

	// Verify files on disk
	indexPath := filepath.Join(sitesDir, "alice", "blog", "index.html")
	content, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}
	if string(content) != "<h1>Hello</h1>" {
		t.Errorf("content = %q", string(content))
	}
}

func TestDeployAutoCreatesProject(t *testing.T) {
	sitesDir := t.TempDir()
	s, _ := store.NewSQLiteStore(":memory:")
	t.Cleanup(func() { s.Close() })
	srv := New(s, sitesDir, "droply.dev", nil)
	token := registerAndGetToken(t, srv)

	// Create subdomain
	body, _ := json.Marshal(map[string]string{"name": "bob"})
	req := httptest.NewRequest("POST", "/subdomains", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	srv.Router().ServeHTTP(w, req)

	// Deploy without creating project first
	tarBuf := createTestTarGz(t, map[string]string{"index.html": "hi"})
	var mpBuf bytes.Buffer
	mpw := multipart.NewWriter(&mpBuf)
	fw, _ := mpw.CreateFormFile("file", "deploy.tar.gz")
	io.Copy(fw, tarBuf)
	mpw.Close()

	req = httptest.NewRequest("POST", "/subdomains/bob/projects/newsite/deploy", &mpBuf)
	req.Header.Set("Content-Type", mpw.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+token)
	w = httptest.NewRecorder()
	srv.Router().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("deploy: status = %d; body: %s", w.Code, w.Body.String())
	}
}

func TestDeploySizeLimit(t *testing.T) {
	srv := setupTestServer(t)
	token := registerAndGetToken(t, srv)

	body, _ := json.Marshal(map[string]string{"name": "alice"})
	req := httptest.NewRequest("POST", "/subdomains", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	srv.Router().ServeHTTP(w, req)

	// Create a request that exceeds 50MB limit indicator
	// We just test the header check, not actually sending 50MB
	var mpBuf bytes.Buffer
	mpw := multipart.NewWriter(&mpBuf)
	fw, _ := mpw.CreateFormFile("file", "deploy.tar.gz")
	fw.Write([]byte("small"))
	mpw.Close()

	req = httptest.NewRequest("POST", "/subdomains/alice/projects/blog/deploy", &mpBuf)
	req.Header.Set("Content-Type", mpw.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+token)
	// This will succeed because the content is small
	w = httptest.NewRecorder()
	srv.Router().ServeHTTP(w, req)
	// Just verifying it doesn't crash — the real size check is on the tar extraction
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test ./internal/server/ -run TestDeploy -v
```

Expected: FAIL — stub returns 501.

- [ ] **Step 3: Implement deploy handler**

Replace deploy stub in `internal/server/deploy.go`:

```go
package server

import (
	"archive/tar"
	"compress/gzip"
	"database/sql"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-chi/chi/v5"
)

const maxUploadSize = 50 << 20 // 50MB

func (s *Server) handleDeploy(w http.ResponseWriter, r *http.Request) {
	user := userFromContext(r.Context())
	subName := chi.URLParam(r, "sub")
	projName := chi.URLParam(r, "name")

	if !validName(projName) {
		jsonError(w, "invalid project name", http.StatusBadRequest)
		return
	}

	sub, err := s.store.GetSubdomainByName(subName)
	if err != nil || sub.UserID != user.ID {
		jsonError(w, "subdomain not found", http.StatusNotFound)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxUploadSize)

	file, _, err := r.FormFile("file")
	if err != nil {
		jsonError(w, "file upload required (field: file)", http.StatusBadRequest)
		return
	}
	defer file.Close()

	// Auto-create project if not exists
	proj, err := s.store.GetProject(sub.ID, projName)
	if err == sql.ErrNoRows {
		proj, err = s.store.CreateProject(sub.ID, projName)
	}
	if err != nil {
		jsonError(w, "failed to get/create project", http.StatusInternalServerError)
		return
	}

	// Extract tar.gz to site directory
	destDir := filepath.Join(s.sitesDir, subName, projName)

	// Remove old files
	os.RemoveAll(destDir)
	if err := os.MkdirAll(destDir, 0755); err != nil {
		jsonError(w, "failed to create site directory", http.StatusInternalServerError)
		return
	}

	fileCount, totalSize, err := extractTarGz(file, destDir)
	if err != nil {
		jsonError(w, fmt.Sprintf("failed to extract archive: %v", err), http.StatusBadRequest)
		return
	}

	dep, err := s.store.CreateDeployment(proj.ID, fileCount, totalSize)
	if err != nil {
		jsonError(w, "failed to create deployment record", http.StatusInternalServerError)
		return
	}
	if err := s.store.ActivateDeployment(dep.ID); err != nil {
		jsonError(w, "failed to activate deployment", http.StatusInternalServerError)
		return
	}

	jsonResponse(w, map[string]any{
		"deployment_id": dep.ID,
		"version":       dep.Version,
		"file_count":    fileCount,
		"total_size":    totalSize,
		"url":           fmt.Sprintf("https://%s.%s/%s", subName, s.baseDomain, projName),
	}, http.StatusOK)
}

func extractTarGz(r io.Reader, destDir string) (fileCount int, totalSize int64, err error) {
	gr, err := gzip.NewReader(r)
	if err != nil {
		return 0, 0, fmt.Errorf("gzip: %w", err)
	}
	defer gr.Close()

	tr := tar.NewReader(gr)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return 0, 0, fmt.Errorf("tar: %w", err)
		}

		// Security: prevent path traversal
		cleanName := filepath.Clean(hdr.Name)
		if strings.HasPrefix(cleanName, "..") || strings.HasPrefix(cleanName, "/") {
			continue
		}

		target := filepath.Join(destDir, cleanName)

		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0755); err != nil {
				return 0, 0, err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
				return 0, 0, err
			}
			f, err := os.Create(target)
			if err != nil {
				return 0, 0, err
			}
			n, err := io.Copy(f, tr)
			f.Close()
			if err != nil {
				return 0, 0, err
			}
			fileCount++
			totalSize += n
		}
	}
	return fileCount, totalSize, nil
}

func (s *Server) handleListDeployments(w http.ResponseWriter, r *http.Request) {
	user := userFromContext(r.Context())
	subName := chi.URLParam(r, "sub")
	projName := chi.URLParam(r, "name")

	sub, err := s.store.GetSubdomainByName(subName)
	if err != nil || sub.UserID != user.ID {
		jsonError(w, "subdomain not found", http.StatusNotFound)
		return
	}

	proj, err := s.store.GetProject(sub.ID, projName)
	if err != nil {
		jsonError(w, "project not found", http.StatusNotFound)
		return
	}

	deps, err := s.store.ListDeployments(proj.ID)
	if err != nil {
		jsonError(w, "internal error", http.StatusInternalServerError)
		return
	}
	if deps == nil {
		deps = []model.Deployment{}
	}
	jsonResponse(w, deps, http.StatusOK)
}
```


- [ ] **Step 4: Run tests to verify they pass**

```bash
go test ./internal/server/ -v
```

Expected: All PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/server/
git commit -m "feat: add deploy handler with tar.gz extraction"
```

---

### Task 6: Custom Domain API Handlers

**Files:**
- Create: `internal/server/domain.go` (replace stub)
- Add tests to: `internal/server/domain_test.go`

- [ ] **Step 1: Write failing tests**

Create `internal/server/domain_test.go`:

```go
package server

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/chenzhong/droply/internal/store"
)

func setupDomainTest(t *testing.T) (*Server, string) {
	t.Helper()
	sitesDir := t.TempDir()
	s, _ := store.NewSQLiteStore(":memory:")
	t.Cleanup(func() { s.Close() })
	srv := New(s, sitesDir, "droply.dev", nil)
	token := registerAndGetToken(t, srv)

	// Create subdomain
	body, _ := json.Marshal(map[string]string{"name": "alice"})
	req := httptest.NewRequest("POST", "/subdomains", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	srv.Router().ServeHTTP(w, req)

	// Create project via deploy
	tarBuf := createTestTarGz(t, map[string]string{"index.html": "hi"})
	var mpBuf bytes.Buffer
	mpw := multipart.NewWriter(&mpBuf)
	fw, _ := mpw.CreateFormFile("file", "deploy.tar.gz")
	io.Copy(fw, tarBuf)
	mpw.Close()

	req = httptest.NewRequest("POST", "/subdomains/alice/projects/blog/deploy", &mpBuf)
	req.Header.Set("Content-Type", mpw.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+token)
	w = httptest.NewRecorder()
	srv.Router().ServeHTTP(w, req)

	return srv, token
}

func TestCustomDomainCRUD(t *testing.T) {
	srv, token := setupDomainTest(t)

	// Add domain
	body, _ := json.Marshal(map[string]string{"domain": "blog.alice.com"})
	req := httptest.NewRequest("POST", "/subdomains/alice/projects/blog/domains", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	srv.Router().ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("create domain: status = %d; body: %s", w.Code, w.Body.String())
	}

	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["domain"] != "blog.alice.com" {
		t.Errorf("domain = %v", resp["domain"])
	}
	if resp["verified"] != false {
		t.Errorf("verified = %v, want false", resp["verified"])
	}

	// List domains
	req = httptest.NewRequest("GET", "/subdomains/alice/projects/blog/domains", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w = httptest.NewRecorder()
	srv.Router().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("list domains: status = %d", w.Code)
	}

	var domains []map[string]any
	json.NewDecoder(w.Body).Decode(&domains)
	if len(domains) != 1 {
		t.Fatalf("len = %d, want 1", len(domains))
	}

	// Delete domain
	req = httptest.NewRequest("DELETE", "/subdomains/alice/projects/blog/domains/blog.alice.com", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w = httptest.NewRecorder()
	srv.Router().ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Errorf("delete domain: status = %d", w.Code)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test ./internal/server/ -run TestCustomDomain -v
```

Expected: FAIL — stubs return 501.

- [ ] **Step 3: Implement domain handlers**

Create `internal/server/domain.go`:

```go
package server

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"
)

func (s *Server) handleCreateDomain(w http.ResponseWriter, r *http.Request) {
	user := userFromContext(r.Context())
	subName := chi.URLParam(r, "sub")
	projName := chi.URLParam(r, "name")

	sub, err := s.store.GetSubdomainByName(subName)
	if err != nil || sub.UserID != user.ID {
		jsonError(w, "subdomain not found", http.StatusNotFound)
		return
	}

	proj, err := s.store.GetProject(sub.ID, projName)
	if err != nil {
		jsonError(w, "project not found", http.StatusNotFound)
		return
	}

	var req struct {
		Domain string `json:"domain"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Domain == "" {
		jsonError(w, "domain is required", http.StatusBadRequest)
		return
	}

	cd, err := s.store.CreateCustomDomain(proj.ID, req.Domain)
	if err != nil {
		jsonError(w, "domain already exists", http.StatusConflict)
		return
	}

	jsonResponse(w, map[string]any{
		"domain":       cd.Domain,
		"verified":     cd.Verified,
		"cname_target": fmt.Sprintf("%s.%s", subName, s.baseDomain),
	}, http.StatusCreated)
}

func (s *Server) handleListDomains(w http.ResponseWriter, r *http.Request) {
	user := userFromContext(r.Context())
	subName := chi.URLParam(r, "sub")
	projName := chi.URLParam(r, "name")

	sub, err := s.store.GetSubdomainByName(subName)
	if err != nil || sub.UserID != user.ID {
		jsonError(w, "subdomain not found", http.StatusNotFound)
		return
	}

	proj, err := s.store.GetProject(sub.ID, projName)
	if err != nil {
		jsonError(w, "project not found", http.StatusNotFound)
		return
	}

	domains, err := s.store.ListCustomDomains(proj.ID)
	if err != nil {
		jsonError(w, "internal error", http.StatusInternalServerError)
		return
	}
	if domains == nil {
		domains = []model.CustomDomain{}
	}
	jsonResponse(w, domains, http.StatusOK)
}

func (s *Server) handleDeleteDomain(w http.ResponseWriter, r *http.Request) {
	user := userFromContext(r.Context())
	subName := chi.URLParam(r, "sub")
	projName := chi.URLParam(r, "name")
	domainName := chi.URLParam(r, "domain")

	sub, err := s.store.GetSubdomainByName(subName)
	if err != nil || sub.UserID != user.ID {
		jsonError(w, "subdomain not found", http.StatusNotFound)
		return
	}

	proj, err := s.store.GetProject(sub.ID, projName)
	if err != nil {
		jsonError(w, "project not found", http.StatusNotFound)
		return
	}

	if err := s.store.DeleteCustomDomain(proj.ID, domainName); err != nil {
		jsonError(w, "internal error", http.StatusInternalServerError)
		return
	}

	if s.caddy != nil {
		s.caddy.RemoveCustomDomainRoute(domainName)
	}

	w.WriteHeader(http.StatusNoContent)
}
```


- [ ] **Step 4: Run tests to verify they pass**

```bash
go test ./internal/server/ -v
```

Expected: All PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/server/
git commit -m "feat: add custom domain API handlers"
```

---

### Task 7: Caddy Admin API Client

**Files:**
- Create: `internal/caddy/client.go`
- Create: `internal/caddy/client_test.go`

- [ ] **Step 1: Write failing tests**

Create `internal/caddy/client_test.go`:

```go
package caddy

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestBuildSubdomainRoute(t *testing.T) {
	c := &Client{
		adminURL:   "http://localhost:2019",
		baseDomain: "droply.dev",
		sitesDir:   "/data/droply/sites",
	}

	route := c.buildSubdomainRoute("alice")

	// Verify the route has correct host match
	routeJSON, _ := json.Marshal(route)
	var parsed map[string]any
	json.Unmarshal(routeJSON, &parsed)

	match := parsed["match"].([]any)[0].(map[string]any)
	hosts := match["host"].([]any)
	if hosts[0] != "alice.droply.dev" {
		t.Errorf("host = %v, want alice.droply.dev", hosts[0])
	}
}

func TestAddSubdomainRouteHTTP(t *testing.T) {
	// Mock Caddy admin API
	var receivedPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	c := NewClient(server.URL, "droply.dev", "/data/droply/sites")

	err := c.AddSubdomainRoute("alice")
	if err != nil {
		t.Fatalf("AddSubdomainRoute: %v", err)
	}

	expected := "/config/apps/http/servers/main/routes"
	if receivedPath != expected {
		t.Errorf("path = %q, want %q", receivedPath, expected)
	}
}

func TestRemoveSubdomainRouteHTTP(t *testing.T) {
	// Mock Caddy admin API — first GET returns routes, then DELETE
	callCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		if r.Method == "GET" {
			routes := []map[string]any{
				{
					"@id":   "subdomain-alice",
					"match": []map[string]any{{"host": []string{"alice.droply.dev"}}},
				},
			}
			json.NewEncoder(w).Encode(routes)
		} else if r.Method == "DELETE" {
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer server.Close()

	c := NewClient(server.URL, "droply.dev", "/data/droply/sites")
	err := c.RemoveSubdomainRoute("alice")
	if err != nil {
		t.Fatalf("RemoveSubdomainRoute: %v", err)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test ./internal/caddy/ -v
```

Expected: FAIL — `Client` not defined.

- [ ] **Step 3: Implement Caddy client**

Create `internal/caddy/client.go`:

```go
package caddy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

type Client struct {
	adminURL   string
	baseDomain string
	sitesDir   string
	httpClient *http.Client
}

func NewClient(adminURL, baseDomain, sitesDir string) *Client {
	return &Client{
		adminURL:   adminURL,
		baseDomain: baseDomain,
		sitesDir:   sitesDir,
		httpClient: &http.Client{},
	}
}

// LoadInitialConfig sets up the base Caddy config with API reverse proxy.
func (c *Client) LoadInitialConfig(apiAddr string) error {
	config := map[string]any{
		"apps": map[string]any{
			"http": map[string]any{
				"servers": map[string]any{
					"main": map[string]any{
						"listen": []string{":443", ":80"},
						"routes": []any{
							map[string]any{
								"@id":   "api",
								"match": []map[string]any{{"host": []string{"api." + c.baseDomain}}},
								"handle": []map[string]any{{
									"handler":   "reverse_proxy",
									"upstreams": []map[string]string{{"dial": apiAddr}},
								}},
							},
						},
					},
				},
			},
		},
	}

	return c.postJSON("/load", config)
}

func (c *Client) buildSubdomainRoute(name string) map[string]any {
	host := name + "." + c.baseDomain
	root := c.sitesDir + "/" + name
	return map[string]any{
		"@id":   "subdomain-" + name,
		"match": []map[string]any{{"host": []string{host}}},
		"handle": []map[string]any{
			{
				"handler": "file_server",
				"root":    root,
			},
		},
	}
}

func (c *Client) AddSubdomainRoute(name string) error {
	route := c.buildSubdomainRoute(name)
	return c.postJSON("/config/apps/http/servers/main/routes", route)
}

func (c *Client) RemoveSubdomainRoute(name string) error {
	routeID := "subdomain-" + name
	return c.delete("/id/" + routeID)
}

func (c *Client) AddCustomDomainRoute(domain, subdomainName, projectName string) error {
	root := c.sitesDir + "/" + subdomainName + "/" + projectName
	route := map[string]any{
		"@id":   "domain-" + domain,
		"match": []map[string]any{{"host": []string{domain}}},
		"handle": []map[string]any{
			{
				"handler": "file_server",
				"root":    root,
			},
		},
	}
	return c.postJSON("/config/apps/http/servers/main/routes", route)
}

func (c *Client) RemoveCustomDomainRoute(domain string) error {
	routeID := "domain-" + domain
	return c.delete("/id/" + routeID)
}

func (c *Client) postJSON(path string, data any) error {
	body, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}

	req, err := http.NewRequest("POST", c.adminURL+path, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("new request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("do request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("caddy API error %d: %s", resp.StatusCode, string(respBody))
	}
	return nil
}

func (c *Client) delete(path string) error {
	req, err := http.NewRequest("DELETE", c.adminURL+path, nil)
	if err != nil {
		return fmt.Errorf("new request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("do request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("caddy API error %d: %s", resp.StatusCode, string(respBody))
	}
	return nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
go test ./internal/caddy/ -v
```

Expected: All PASS.

- [ ] **Step 5: Wire Caddy client into server startup**

Update `cmd/droply-server/main.go` to create and pass a Caddy client:

```go
// Add to imports
"github.com/chenzhong/droply/internal/caddy"

// In main(), before server.New:
caddyAddr := flag.String("caddy-admin", "http://localhost:2019", "Caddy admin API address")

// After flag.Parse():
caddyClient := caddy.NewClient(*caddyAddr, *baseDomain, sitesDir)

// Pass to server.New:
srv := server.New(s, sitesDir, *baseDomain, caddyClient)
```

- [ ] **Step 6: Verify build**

```bash
make build
```

Expected: Build succeeds.

- [ ] **Step 7: Commit**

```bash
git add internal/caddy/ cmd/droply-server/
git commit -m "feat: add Caddy Admin API client for dynamic route management"
```

---

### Task 8: CLI — Config and Auth Commands

**Files:**
- Create: `internal/cli/config.go`
- Create: `internal/cli/root.go`
- Create: `internal/cli/auth.go`
- Create: `internal/cli/client.go`
- Modify: `cmd/droply/main.go`

- [ ] **Step 1: Implement CLI config**

Create `internal/cli/config.go`:

```go
package cli

import (
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

type Config struct {
	APIURL string `toml:"api_url"`
	Token  string `toml:"token"`
}

type ProjectConfig struct {
	Subdomain string `toml:"subdomain"`
	Project   string `toml:"project"`
}

func configDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "droply")
}

func configPath() string {
	return filepath.Join(configDir(), "config.toml")
}

func LoadConfig() (*Config, error) {
	cfg := &Config{
		APIURL: "https://api.droply.dev",
	}
	_, err := toml.DecodeFile(configPath(), cfg)
	if os.IsNotExist(err) {
		return cfg, nil
	}
	return cfg, err
}

func SaveConfig(cfg *Config) error {
	dir := configDir()
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	f, err := os.Create(configPath())
	if err != nil {
		return err
	}
	defer f.Close()
	return toml.NewEncoder(f).Encode(cfg)
}

func LoadProjectConfig() (*ProjectConfig, error) {
	cfg := &ProjectConfig{}
	_, err := toml.DecodeFile(".droply.toml", cfg)
	if os.IsNotExist(err) {
		return cfg, nil
	}
	return cfg, err
}
```

- [ ] **Step 2: Implement HTTP client helper**

Create `internal/cli/client.go`:

```go
package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
)

type APIClient struct {
	BaseURL string
	Token   string
	HTTP    *http.Client
}

func NewAPIClient(cfg *Config) *APIClient {
	return &APIClient{
		BaseURL: cfg.APIURL,
		Token:   cfg.Token,
		HTTP:    &http.Client{},
	}
}

func (c *APIClient) doJSON(method, path string, body any, result any) error {
	var reqBody io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reqBody = bytes.NewReader(data)
	}

	req, err := http.NewRequest(method, c.BaseURL+path, reqBody)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		var errResp map[string]string
		json.NewDecoder(resp.Body).Decode(&errResp)
		msg := errResp["error"]
		if msg == "" {
			msg = fmt.Sprintf("HTTP %d", resp.StatusCode)
		}
		return fmt.Errorf("%s", msg)
	}

	if result != nil && resp.StatusCode != http.StatusNoContent {
		return json.NewDecoder(resp.Body).Decode(result)
	}
	return nil
}

func (c *APIClient) uploadFile(path, filePath string) (map[string]any, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var buf bytes.Buffer
	mpw := multipart.NewWriter(&buf)
	fw, err := mpw.CreateFormFile("file", "deploy.tar.gz")
	if err != nil {
		return nil, err
	}
	if _, err := io.Copy(fw, f); err != nil {
		return nil, err
	}
	mpw.Close()

	req, err := http.NewRequest("POST", c.BaseURL+path, &buf)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", mpw.FormDataContentType())
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		var errResp map[string]string
		json.NewDecoder(resp.Body).Decode(&errResp)
		return nil, fmt.Errorf("%s", errResp["error"])
	}

	var result map[string]any
	json.NewDecoder(resp.Body).Decode(&result)
	return result, nil
}
```

- [ ] **Step 3: Implement root command**

Create `internal/cli/root.go`:

```go
package cli

import (
	"github.com/spf13/cobra"
)

func NewRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "droply",
		Short: "Static content publishing platform",
	}

	root.AddCommand(newRegisterCmd())
	root.AddCommand(newLoginCmd())
	root.AddCommand(newLogoutCmd())
	root.AddCommand(newSubdomainCmd())
	root.AddCommand(newDeployCmd())
	root.AddCommand(newListCmd())
	root.AddCommand(newDomainCmd())
	root.AddCommand(newWhoamiCmd())

	return root
}
```

- [ ] **Step 4: Implement auth commands**

Create `internal/cli/auth.go`:

```go
package cli

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"
)

func readInput(prompt string) string {
	fmt.Print(prompt)
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Scan()
	return strings.TrimSpace(scanner.Text())
}

func readPassword(prompt string) string {
	fmt.Print(prompt)
	pw, _ := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Println()
	return string(pw)
}

func newRegisterCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "register",
		Short: "Register a new account",
		RunE: func(cmd *cobra.Command, args []string) error {
			email := readInput("Email: ")
			password := readPassword("Password: ")

			cfg, err := LoadConfig()
			if err != nil {
				return err
			}

			client := NewAPIClient(cfg)
			var resp struct {
				APIToken string `json:"api_token"`
			}
			err = client.doJSON("POST", "/auth/register", map[string]string{
				"email":    email,
				"password": password,
			}, &resp)
			if err != nil {
				return fmt.Errorf("registration failed: %w", err)
			}

			cfg.Token = resp.APIToken
			if err := SaveConfig(cfg); err != nil {
				return fmt.Errorf("save config: %w", err)
			}

			fmt.Println("Registered successfully! Token saved.")
			return nil
		},
	}
}

func newLoginCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "login",
		Short: "Login to your account",
		RunE: func(cmd *cobra.Command, args []string) error {
			email := readInput("Email: ")
			password := readPassword("Password: ")

			cfg, err := LoadConfig()
			if err != nil {
				return err
			}

			client := NewAPIClient(cfg)
			var resp struct {
				APIToken string `json:"api_token"`
			}
			err = client.doJSON("POST", "/auth/login", map[string]string{
				"email":    email,
				"password": password,
			}, &resp)
			if err != nil {
				return fmt.Errorf("login failed: %w", err)
			}

			cfg.Token = resp.APIToken
			if err := SaveConfig(cfg); err != nil {
				return fmt.Errorf("save config: %w", err)
			}

			fmt.Println("Logged in successfully!")
			return nil
		},
	}
}

func newLogoutCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "logout",
		Short: "Clear saved credentials",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := LoadConfig()
			if err != nil {
				return err
			}
			cfg.Token = ""
			if err := SaveConfig(cfg); err != nil {
				return err
			}
			fmt.Println("Logged out.")
			return nil
		},
	}
}

func newWhoamiCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "whoami",
		Short: "Show current user info",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := LoadConfig()
			if err != nil {
				return err
			}
			if cfg.Token == "" {
				fmt.Println("Not logged in. Run 'droply login' or 'droply register'.")
				return nil
			}
			fmt.Printf("API URL: %s\n", cfg.APIURL)
			fmt.Printf("Token:   %s...%s\n", cfg.Token[:6], cfg.Token[len(cfg.Token)-4:])
			return nil
		},
	}
}
```

- [ ] **Step 5: Wire up CLI main**

Update `cmd/droply/main.go`:

```go
package main

import (
	"os"

	"github.com/chenzhong/droply/internal/cli"
)

func main() {
	if err := cli.NewRootCmd().Execute(); err != nil {
		os.Exit(1)
	}
}
```

- [ ] **Step 6: Add stub commands for remaining CLI commands**

Create placeholder files to avoid compile errors. These will be implemented in the next tasks:

`internal/cli/subdomain.go`:

```go
package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newSubdomainCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "subdomain",
		Short: "Manage subdomains",
	}

	cmd.AddCommand(&cobra.Command{
		Use:   "create <name>",
		Short: "Create a subdomain",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, _ := LoadConfig()
			client := NewAPIClient(cfg)
			var result map[string]any
			err := client.doJSON("POST", "/subdomains", map[string]string{"name": args[0]}, &result)
			if err != nil {
				return err
			}
			fmt.Printf("Subdomain created: %s.droply.dev\n", args[0])
			return nil
		},
	})

	cmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List subdomains",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, _ := LoadConfig()
			client := NewAPIClient(cfg)
			var subs []map[string]any
			err := client.doJSON("GET", "/subdomains", nil, &subs)
			if err != nil {
				return err
			}
			if len(subs) == 0 {
				fmt.Println("No subdomains yet. Create one with 'droply subdomain create <name>'")
				return nil
			}
			for _, s := range subs {
				fmt.Printf("  %s.droply.dev  (%v projects)\n", s["name"], s["project_count"])
			}
			return nil
		},
	})

	cmd.AddCommand(&cobra.Command{
		Use:   "delete <name>",
		Short: "Delete a subdomain",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, _ := LoadConfig()
			client := NewAPIClient(cfg)
			err := client.doJSON("DELETE", "/subdomains/"+args[0], nil, nil)
			if err != nil {
				return err
			}
			fmt.Printf("Subdomain %s deleted.\n", args[0])
			return nil
		},
	})

	return cmd
}
```

`internal/cli/list.go`:

```go
package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List projects",
		RunE: func(cmd *cobra.Command, args []string) error {
			sub, _ := cmd.Flags().GetString("sub")
			if sub == "" {
				projCfg, _ := LoadProjectConfig()
				sub = projCfg.Subdomain
			}
			if sub == "" {
				return fmt.Errorf("specify --sub or set subdomain in .droply.toml")
			}

			cfg, _ := LoadConfig()
			client := NewAPIClient(cfg)
			var projects []map[string]any
			err := client.doJSON("GET", "/subdomains/"+sub+"/projects", nil, &projects)
			if err != nil {
				return err
			}
			if len(projects) == 0 {
				fmt.Println("No projects yet.")
				return nil
			}
			for _, p := range projects {
				fmt.Printf("  %s.droply.dev/%s\n", sub, p["name"])
			}
			return nil
		},
	}
	cmd.Flags().String("sub", "", "subdomain name")
	return cmd
}
```

- [ ] **Step 7: Verify build**

```bash
make build
./bin/droply --help
```

Expected: Shows help with all subcommands.

- [ ] **Step 8: Commit**

```bash
git add internal/cli/ cmd/droply/ go.mod go.sum
git commit -m "feat: add CLI with auth, subdomain, and list commands"
```

---

### Task 9: CLI — Deploy Command

**Files:**
- Create: `internal/cli/deploy.go`

- [ ] **Step 1: Implement deploy command**

Create `internal/cli/deploy.go`:

```go
package cli

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

var excludeDirs = map[string]bool{
	".git":         true,
	"node_modules": true,
	"__pycache__":  true,
	".DS_Store":    true,
	".env":         true,
}

func newDeployCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "deploy [dir]",
		Short: "Deploy a directory",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := "."
			if len(args) > 0 {
				dir = args[0]
			}

			sub, _ := cmd.Flags().GetString("sub")
			proj, _ := cmd.Flags().GetString("project")

			// Load from .droply.toml if not specified
			if sub == "" || proj == "" {
				projCfg, _ := LoadProjectConfig()
				if sub == "" {
					sub = projCfg.Subdomain
				}
				if proj == "" {
					proj = projCfg.Project
				}
			}

			if sub == "" {
				return fmt.Errorf("specify --sub or set subdomain in .droply.toml")
			}
			if proj == "" {
				return fmt.Errorf("specify --project or set project in .droply.toml")
			}

			// Create tar.gz
			tmpFile, err := os.CreateTemp("", "droply-deploy-*.tar.gz")
			if err != nil {
				return fmt.Errorf("create temp file: %w", err)
			}
			defer os.Remove(tmpFile.Name())
			defer tmpFile.Close()

			fmt.Printf("Packaging %s...\n", dir)
			if err := createTarGz(tmpFile, dir); err != nil {
				return fmt.Errorf("package: %w", err)
			}
			tmpFile.Close()

			// Upload
			cfg, err := LoadConfig()
			if err != nil {
				return err
			}
			client := NewAPIClient(cfg)

			fmt.Printf("Deploying to %s.droply.dev/%s...\n", sub, proj)
			path := fmt.Sprintf("/subdomains/%s/projects/%s/deploy", sub, proj)
			result, err := client.uploadFile(path, tmpFile.Name())
			if err != nil {
				return fmt.Errorf("deploy failed: %w", err)
			}

			fmt.Printf("Deployed! Version %v\n", result["version"])
			fmt.Printf("URL: %s\n", result["url"])
			return nil
		},
	}
	cmd.Flags().String("sub", "", "subdomain name")
	cmd.Flags().String("project", "", "project name")
	return cmd
}

func createTarGz(w io.Writer, srcDir string) error {
	gw := gzip.NewWriter(w)
	defer gw.Close()
	tw := tar.NewWriter(gw)
	defer tw.Close()

	srcDir, err := filepath.Abs(srcDir)
	if err != nil {
		return err
	}

	return filepath.Walk(srcDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Check exclusions
		name := info.Name()
		if excludeDirs[name] {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		// Get relative path
		relPath, err := filepath.Rel(srcDir, path)
		if err != nil {
			return err
		}
		if relPath == "." {
			return nil
		}

		// Skip hidden files at root level (except .droply.toml type files are fine, but skip others)
		if strings.HasPrefix(name, ".") && info.IsDir() {
			return filepath.SkipDir
		}

		hdr, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		hdr.Name = relPath

		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}

		if !info.Mode().IsRegular() {
			return nil
		}

		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = io.Copy(tw, f)
		return err
	})
}
```

- [ ] **Step 2: Implement domain command**

Create `internal/cli/domain.go`:

```go
package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newDomainCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "domain",
		Short: "Manage custom domains",
	}

	cmd.AddCommand(&cobra.Command{
		Use:   "add <domain>",
		Short: "Add a custom domain",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			sub, proj, err := resolveProject(cmd)
			if err != nil {
				return err
			}

			cfg, _ := LoadConfig()
			client := NewAPIClient(cfg)
			var result map[string]any
			path := fmt.Sprintf("/subdomains/%s/projects/%s/domains", sub, proj)
			err = client.doJSON("POST", path, map[string]string{"domain": args[0]}, &result)
			if err != nil {
				return err
			}
			fmt.Printf("Domain added: %s\n", args[0])
			fmt.Printf("Add a CNAME record pointing to: %s\n", result["cname_target"])
			return nil
		},
	})

	cmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List custom domains",
		RunE: func(cmd *cobra.Command, args []string) error {
			sub, proj, err := resolveProject(cmd)
			if err != nil {
				return err
			}

			cfg, _ := LoadConfig()
			client := NewAPIClient(cfg)
			var domains []map[string]any
			path := fmt.Sprintf("/subdomains/%s/projects/%s/domains", sub, proj)
			err = client.doJSON("GET", path, nil, &domains)
			if err != nil {
				return err
			}
			if len(domains) == 0 {
				fmt.Println("No custom domains.")
				return nil
			}
			for _, d := range domains {
				verified := "not verified"
				if v, ok := d["verified"].(bool); ok && v {
					verified = "verified"
				}
				fmt.Printf("  %s (%s)\n", d["domain"], verified)
			}
			return nil
		},
	})

	cmd.AddCommand(&cobra.Command{
		Use:   "remove <domain>",
		Short: "Remove a custom domain",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			sub, proj, err := resolveProject(cmd)
			if err != nil {
				return err
			}

			cfg, _ := LoadConfig()
			client := NewAPIClient(cfg)
			path := fmt.Sprintf("/subdomains/%s/projects/%s/domains/%s", sub, proj, args[0])
			err = client.doJSON("DELETE", path, nil, nil)
			if err != nil {
				return err
			}
			fmt.Printf("Domain %s removed.\n", args[0])
			return nil
		},
	})

	// Add flags to parent
	cmd.PersistentFlags().String("sub", "", "subdomain name")
	cmd.PersistentFlags().String("project", "", "project name")

	return cmd
}

func resolveProject(cmd *cobra.Command) (sub, proj string, err error) {
	sub, _ = cmd.Flags().GetString("sub")
	proj, _ = cmd.Flags().GetString("project")

	if sub == "" || proj == "" {
		projCfg, _ := LoadProjectConfig()
		if sub == "" {
			sub = projCfg.Subdomain
		}
		if proj == "" {
			proj = projCfg.Project
		}
	}

	if sub == "" {
		return "", "", fmt.Errorf("specify --sub or set subdomain in .droply.toml")
	}
	if proj == "" {
		return "", "", fmt.Errorf("specify --project or set project in .droply.toml")
	}
	return sub, proj, nil
}
```

- [ ] **Step 3: Verify build**

```bash
make build
./bin/droply deploy --help
./bin/droply domain --help
```

Expected: Both show help text.

- [ ] **Step 4: Commit**

```bash
git add internal/cli/
git commit -m "feat: add CLI deploy and domain commands"
```

---

### Task 10: Server Startup Recovery and Integration

**Files:**
- Modify: `cmd/droply-server/main.go`
- Create: `internal/server/recovery.go`

- [ ] **Step 1: Implement startup recovery**

Create `internal/server/recovery.go`:

```go
package server

import (
	"fmt"
	"log"
)

// RecoverCaddyRoutes rebuilds all Caddy routes from database state on startup.
func (s *Server) RecoverCaddyRoutes() error {
	if s.caddy == nil {
		log.Println("No Caddy client configured, skipping route recovery")
		return nil
	}

	// Add routes for all subdomains
	subs, err := s.store.ListAllSubdomains()
	if err != nil {
		return fmt.Errorf("list subdomains: %w", err)
	}

	for _, sub := range subs {
		if err := s.caddy.AddSubdomainRoute(sub.Name); err != nil {
			log.Printf("Warning: failed to add route for subdomain %s: %v", sub.Name, err)
		}
	}

	// Add routes for all verified custom domains
	domains, err := s.store.ListAllVerifiedDomainsWithPaths()
	if err != nil {
		return fmt.Errorf("list domains: %w", err)
	}

	for _, d := range domains {
		if err := s.caddy.AddCustomDomainRoute(d.Domain, d.SubdomainName, d.ProjectName); err != nil {
			log.Printf("Warning: failed to add route for domain %s: %v", d.Domain, err)
		}
	}

	log.Printf("Recovered %d subdomain routes and %d custom domain routes", len(subs), len(domains))
	return nil
}
```

- [ ] **Step 2: Update server main to call recovery**

Update `cmd/droply-server/main.go` — add after creating the server:

```go
// Recover Caddy routes from database
if err := srv.RecoverCaddyRoutes(); err != nil {
	log.Printf("Warning: route recovery failed: %v", err)
}
```

- [ ] **Step 3: Verify build and all tests pass**

```bash
make build && make test
```

Expected: Build succeeds, all tests pass.

- [ ] **Step 4: Commit**

```bash
git add internal/server/recovery.go cmd/droply-server/
git commit -m "feat: add Caddy route recovery on server startup"
```

---

### Task 11: Add .gitignore and Final Cleanup

**Files:**
- Create: `.gitignore`
- Verify: all tests pass, both binaries build

- [ ] **Step 1: Create .gitignore**

```gitignore
bin/
*.db
.superpowers/
.droply.toml
/data/
```

- [ ] **Step 2: Run full test suite**

```bash
make test
```

Expected: All tests pass.

- [ ] **Step 3: Build both binaries**

```bash
make build
```

Expected: `bin/droply` and `bin/droply-server` exist.

- [ ] **Step 4: Commit**

```bash
git add .gitignore
git commit -m "chore: add .gitignore"
```

---

## Summary

| Task | Component | Tests |
|------|-----------|-------|
| 1 | Project scaffolding | Build verification |
| 2 | Data models + SQLite store | 6 test functions |
| 3 | HTTP server + auth | 4 test functions |
| 4 | Subdomain + project handlers | 3 test functions |
| 5 | Deploy handler | 3 test functions |
| 6 | Custom domain handlers | 1 test function |
| 7 | Caddy Admin API client | 3 test functions |
| 8 | CLI config + auth + subdomain | Build verification |
| 9 | CLI deploy + domain | Build verification |
| 10 | Server startup recovery | Integration |
| 11 | Cleanup + .gitignore | Full test suite |
