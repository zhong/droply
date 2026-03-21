package store

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/zhong/droply/internal/model"
	_ "modernc.org/sqlite"
)

const dtLayout = "2006-01-02 15:04:05"

type SQLiteStore struct {
	db *sql.DB
}

func NewSQLiteStore(dsn string) (*SQLiteStore, error) {
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}

	if _, err := db.Exec(`PRAGMA journal_mode=WAL`); err != nil {
		db.Close()
		return nil, fmt.Errorf("set WAL mode: %w", err)
	}
	if _, err := db.Exec(`PRAGMA foreign_keys=ON`); err != nil {
		db.Close()
		return nil, fmt.Errorf("enable foreign keys: %w", err)
	}

	s := &SQLiteStore{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return s, nil
}

func (s *SQLiteStore) migrate() error {
	_, err := s.db.Exec(`
		CREATE TABLE IF NOT EXISTS users (
			id         INTEGER PRIMARY KEY AUTOINCREMENT,
			email      TEXT NOT NULL UNIQUE,
			password   TEXT NOT NULL,
			api_token  TEXT NOT NULL UNIQUE,
			created_at DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%S', 'now'))
		);

		CREATE TABLE IF NOT EXISTS subdomains (
			id         INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			name       TEXT NOT NULL UNIQUE,
			created_at DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%S', 'now'))
		);

		CREATE TABLE IF NOT EXISTS projects (
			id           INTEGER PRIMARY KEY AUTOINCREMENT,
			subdomain_id INTEGER NOT NULL REFERENCES subdomains(id) ON DELETE CASCADE,
			name         TEXT NOT NULL,
			created_at   DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%S', 'now')),
			updated_at   DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%S', 'now')),
			UNIQUE(subdomain_id, name)
		);

		CREATE TABLE IF NOT EXISTS deployments (
			id         INTEGER PRIMARY KEY AUTOINCREMENT,
			project_id INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
			version    INTEGER NOT NULL,
			file_count INTEGER NOT NULL DEFAULT 0,
			total_size INTEGER NOT NULL DEFAULT 0,
			status     TEXT NOT NULL DEFAULT 'uploading',
			created_at DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%S', 'now'))
		);

		CREATE TABLE IF NOT EXISTS custom_domains (
			id         INTEGER PRIMARY KEY AUTOINCREMENT,
			project_id INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
			domain     TEXT NOT NULL UNIQUE,
			verified   INTEGER NOT NULL DEFAULT 0,
			created_at DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%S', 'now'))
		);
	`)
	return err
}

func parseTime(s string) time.Time {
	t, _ := time.Parse(dtLayout, s)
	return t
}

// Close closes the underlying database connection.
func (s *SQLiteStore) Close() error {
	return s.db.Close()
}

// ---- Users ----

func (s *SQLiteStore) CreateUser(email, hashedPassword, apiToken string) (*model.User, error) {
	res, err := s.db.Exec(
		`INSERT INTO users (email, password, api_token) VALUES (?, ?, ?)`,
		email, hashedPassword, apiToken,
	)
	if err != nil {
		return nil, fmt.Errorf("create user: %w", err)
	}
	id, _ := res.LastInsertId()
	return s.getUserByID(id)
}

func (s *SQLiteStore) getUserByID(id int64) (*model.User, error) {
	row := s.db.QueryRow(
		`SELECT id, email, password, api_token, created_at FROM users WHERE id = ?`, id,
	)
	return scanUser(row)
}

func (s *SQLiteStore) GetUserByEmail(email string) (*model.User, error) {
	row := s.db.QueryRow(
		`SELECT id, email, password, api_token, created_at FROM users WHERE email = ?`, email,
	)
	return scanUser(row)
}

func (s *SQLiteStore) GetUserByToken(token string) (*model.User, error) {
	row := s.db.QueryRow(
		`SELECT id, email, password, api_token, created_at FROM users WHERE api_token = ?`, token,
	)
	return scanUser(row)
}

func scanUser(row *sql.Row) (*model.User, error) {
	var u model.User
	var createdAt string
	if err := row.Scan(&u.ID, &u.Email, &u.Password, &u.APIToken, &createdAt); err != nil {
		return nil, fmt.Errorf("scan user: %w", err)
	}
	u.CreatedAt = parseTime(createdAt)
	return &u, nil
}

// ---- Subdomains ----

func (s *SQLiteStore) CreateSubdomain(userID int64, name string) (*model.Subdomain, error) {
	res, err := s.db.Exec(
		`INSERT INTO subdomains (user_id, name) VALUES (?, ?)`, userID, name,
	)
	if err != nil {
		return nil, fmt.Errorf("create subdomain: %w", err)
	}
	id, _ := res.LastInsertId()
	return s.getSubdomainByID(id)
}

func (s *SQLiteStore) getSubdomainByID(id int64) (*model.Subdomain, error) {
	row := s.db.QueryRow(
		`SELECT id, user_id, name, created_at,
			(SELECT COUNT(*) FROM projects WHERE subdomain_id = subdomains.id) AS project_count
		 FROM subdomains WHERE id = ?`, id,
	)
	return scanSubdomain(row)
}

func (s *SQLiteStore) GetSubdomainByName(name string) (*model.Subdomain, error) {
	row := s.db.QueryRow(
		`SELECT id, user_id, name, created_at,
			(SELECT COUNT(*) FROM projects WHERE subdomain_id = subdomains.id) AS project_count
		 FROM subdomains WHERE name = ?`, name,
	)
	return scanSubdomain(row)
}

func (s *SQLiteStore) ListSubdomains(userID int64) ([]model.Subdomain, error) {
	rows, err := s.db.Query(
		`SELECT id, user_id, name, created_at,
			(SELECT COUNT(*) FROM projects WHERE subdomain_id = subdomains.id) AS project_count
		 FROM subdomains WHERE user_id = ? ORDER BY created_at DESC`, userID,
	)
	if err != nil {
		return nil, fmt.Errorf("list subdomains: %w", err)
	}
	defer rows.Close()
	return scanSubdomains(rows)
}

func (s *SQLiteStore) ListAllSubdomains() ([]model.Subdomain, error) {
	rows, err := s.db.Query(
		`SELECT id, user_id, name, created_at,
			(SELECT COUNT(*) FROM projects WHERE subdomain_id = subdomains.id) AS project_count
		 FROM subdomains ORDER BY created_at DESC`,
	)
	if err != nil {
		return nil, fmt.Errorf("list all subdomains: %w", err)
	}
	defer rows.Close()
	return scanSubdomains(rows)
}

func (s *SQLiteStore) DeleteSubdomain(userID int64, name string) error {
	_, err := s.db.Exec(
		`DELETE FROM subdomains WHERE user_id = ? AND name = ?`, userID, name,
	)
	return err
}

func scanSubdomain(row *sql.Row) (*model.Subdomain, error) {
	var sd model.Subdomain
	var createdAt string
	if err := row.Scan(&sd.ID, &sd.UserID, &sd.Name, &createdAt, &sd.ProjectCount); err != nil {
		return nil, fmt.Errorf("scan subdomain: %w", err)
	}
	sd.CreatedAt = parseTime(createdAt)
	return &sd, nil
}

func scanSubdomains(rows *sql.Rows) ([]model.Subdomain, error) {
	var result []model.Subdomain
	for rows.Next() {
		var sd model.Subdomain
		var createdAt string
		if err := rows.Scan(&sd.ID, &sd.UserID, &sd.Name, &createdAt, &sd.ProjectCount); err != nil {
			return nil, fmt.Errorf("scan subdomain row: %w", err)
		}
		sd.CreatedAt = parseTime(createdAt)
		result = append(result, sd)
	}
	return result, rows.Err()
}

// ---- Projects ----

func (s *SQLiteStore) CreateProject(subdomainID int64, name string) (*model.Project, error) {
	res, err := s.db.Exec(
		`INSERT INTO projects (subdomain_id, name) VALUES (?, ?)`, subdomainID, name,
	)
	if err != nil {
		return nil, fmt.Errorf("create project: %w", err)
	}
	id, _ := res.LastInsertId()
	return s.getProjectByID(id)
}

func (s *SQLiteStore) getProjectByID(id int64) (*model.Project, error) {
	row := s.db.QueryRow(
		`SELECT id, subdomain_id, name, created_at, updated_at FROM projects WHERE id = ?`, id,
	)
	return scanProject(row)
}

func (s *SQLiteStore) GetProject(subdomainID int64, name string) (*model.Project, error) {
	row := s.db.QueryRow(
		`SELECT id, subdomain_id, name, created_at, updated_at FROM projects WHERE subdomain_id = ? AND name = ?`,
		subdomainID, name,
	)
	return scanProject(row)
}

func (s *SQLiteStore) ListProjects(subdomainID int64) ([]model.Project, error) {
	rows, err := s.db.Query(
		`SELECT id, subdomain_id, name, created_at, updated_at FROM projects WHERE subdomain_id = ? ORDER BY created_at DESC`,
		subdomainID,
	)
	if err != nil {
		return nil, fmt.Errorf("list projects: %w", err)
	}
	defer rows.Close()

	var result []model.Project
	for rows.Next() {
		p, err := scanProjectRow(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, *p)
	}
	return result, rows.Err()
}

func (s *SQLiteStore) DeleteProject(subdomainID int64, name string) error {
	_, err := s.db.Exec(
		`DELETE FROM projects WHERE subdomain_id = ? AND name = ?`, subdomainID, name,
	)
	return err
}

func scanProject(row *sql.Row) (*model.Project, error) {
	var p model.Project
	var createdAt, updatedAt string
	if err := row.Scan(&p.ID, &p.SubdomainID, &p.Name, &createdAt, &updatedAt); err != nil {
		return nil, fmt.Errorf("scan project: %w", err)
	}
	p.CreatedAt = parseTime(createdAt)
	p.UpdatedAt = parseTime(updatedAt)
	return &p, nil
}

func scanProjectRow(rows *sql.Rows) (*model.Project, error) {
	var p model.Project
	var createdAt, updatedAt string
	if err := rows.Scan(&p.ID, &p.SubdomainID, &p.Name, &createdAt, &updatedAt); err != nil {
		return nil, fmt.Errorf("scan project row: %w", err)
	}
	p.CreatedAt = parseTime(createdAt)
	p.UpdatedAt = parseTime(updatedAt)
	return &p, nil
}

// ---- Deployments ----

func (s *SQLiteStore) CreateDeployment(projectID int64, fileCount int, totalSize int64) (*model.Deployment, error) {
	var maxVersion sql.NullInt64
	err := s.db.QueryRow(
		`SELECT MAX(version) FROM deployments WHERE project_id = ?`, projectID,
	).Scan(&maxVersion)
	if err != nil {
		return nil, fmt.Errorf("get max version: %w", err)
	}
	version := 1
	if maxVersion.Valid {
		version = int(maxVersion.Int64) + 1
	}

	res, err := s.db.Exec(
		`INSERT INTO deployments (project_id, version, file_count, total_size, status) VALUES (?, ?, ?, ?, 'uploading')`,
		projectID, version, fileCount, totalSize,
	)
	if err != nil {
		return nil, fmt.Errorf("create deployment: %w", err)
	}
	id, _ := res.LastInsertId()
	return s.getDeploymentByID(id)
}

func (s *SQLiteStore) ActivateDeployment(deploymentID int64) error {
	// Get the project_id for the deployment
	var projectID int64
	err := s.db.QueryRow(
		`SELECT project_id FROM deployments WHERE id = ?`, deploymentID,
	).Scan(&projectID)
	if err != nil {
		return fmt.Errorf("get deployment project: %w", err)
	}

	// Archive previously active deployments
	if _, err := s.db.Exec(
		`UPDATE deployments SET status = 'archived' WHERE project_id = ? AND status = 'active'`,
		projectID,
	); err != nil {
		return fmt.Errorf("archive active deployments: %w", err)
	}

	// Activate the new deployment
	if _, err := s.db.Exec(
		`UPDATE deployments SET status = 'active' WHERE id = ?`, deploymentID,
	); err != nil {
		return fmt.Errorf("activate deployment: %w", err)
	}
	return nil
}

func (s *SQLiteStore) ListDeployments(projectID int64) ([]model.Deployment, error) {
	rows, err := s.db.Query(
		`SELECT id, project_id, version, file_count, total_size, status, created_at
		 FROM deployments WHERE project_id = ? ORDER BY version DESC`,
		projectID,
	)
	if err != nil {
		return nil, fmt.Errorf("list deployments: %w", err)
	}
	defer rows.Close()

	var result []model.Deployment
	for rows.Next() {
		var d model.Deployment
		var createdAt string
		if err := rows.Scan(&d.ID, &d.ProjectID, &d.Version, &d.FileCount, &d.TotalSize, &d.Status, &createdAt); err != nil {
			return nil, fmt.Errorf("scan deployment: %w", err)
		}
		d.CreatedAt = parseTime(createdAt)
		result = append(result, d)
	}
	return result, rows.Err()
}

func (s *SQLiteStore) getDeploymentByID(id int64) (*model.Deployment, error) {
	row := s.db.QueryRow(
		`SELECT id, project_id, version, file_count, total_size, status, created_at
		 FROM deployments WHERE id = ?`, id,
	)
	var d model.Deployment
	var createdAt string
	if err := row.Scan(&d.ID, &d.ProjectID, &d.Version, &d.FileCount, &d.TotalSize, &d.Status, &createdAt); err != nil {
		return nil, fmt.Errorf("scan deployment by id: %w", err)
	}
	d.CreatedAt = parseTime(createdAt)
	return &d, nil
}

// ---- Custom Domains ----

func (s *SQLiteStore) CreateCustomDomain(projectID int64, domain string) (*model.CustomDomain, error) {
	res, err := s.db.Exec(
		`INSERT INTO custom_domains (project_id, domain) VALUES (?, ?)`, projectID, domain,
	)
	if err != nil {
		return nil, fmt.Errorf("create custom domain: %w", err)
	}
	id, _ := res.LastInsertId()
	return s.getCustomDomainByID(id)
}

func (s *SQLiteStore) GetCustomDomain(domain string) (*model.CustomDomain, error) {
	row := s.db.QueryRow(
		`SELECT id, project_id, domain, verified, created_at FROM custom_domains WHERE domain = ?`, domain,
	)
	return scanCustomDomain(row)
}

func (s *SQLiteStore) VerifyCustomDomain(domain string) error {
	_, err := s.db.Exec(
		`UPDATE custom_domains SET verified = 1 WHERE domain = ?`, domain,
	)
	return err
}

func (s *SQLiteStore) ListCustomDomains(projectID int64) ([]model.CustomDomain, error) {
	rows, err := s.db.Query(
		`SELECT id, project_id, domain, verified, created_at FROM custom_domains WHERE project_id = ? ORDER BY created_at DESC`,
		projectID,
	)
	if err != nil {
		return nil, fmt.Errorf("list custom domains: %w", err)
	}
	defer rows.Close()

	var result []model.CustomDomain
	for rows.Next() {
		var cd model.CustomDomain
		var createdAt string
		var verified int
		if err := rows.Scan(&cd.ID, &cd.ProjectID, &cd.Domain, &verified, &createdAt); err != nil {
			return nil, fmt.Errorf("scan custom domain: %w", err)
		}
		cd.Verified = verified == 1
		cd.CreatedAt = parseTime(createdAt)
		result = append(result, cd)
	}
	return result, rows.Err()
}

func (s *SQLiteStore) DeleteCustomDomain(projectID int64, domain string) error {
	_, err := s.db.Exec(
		`DELETE FROM custom_domains WHERE project_id = ? AND domain = ?`, projectID, domain,
	)
	return err
}

func (s *SQLiteStore) ListAllVerifiedDomainsWithPaths() ([]model.DomainWithPath, error) {
	rows, err := s.db.Query(
		`SELECT cd.domain, s.name AS subdomain_name, p.name AS project_name
		 FROM custom_domains cd
		 JOIN projects p ON cd.project_id = p.id
		 JOIN subdomains s ON p.subdomain_id = s.id
		 WHERE cd.verified = 1
		 ORDER BY cd.domain`,
	)
	if err != nil {
		return nil, fmt.Errorf("list verified domains: %w", err)
	}
	defer rows.Close()

	var result []model.DomainWithPath
	for rows.Next() {
		var d model.DomainWithPath
		if err := rows.Scan(&d.Domain, &d.SubdomainName, &d.ProjectName); err != nil {
			return nil, fmt.Errorf("scan domain with path: %w", err)
		}
		result = append(result, d)
	}
	return result, rows.Err()
}

func (s *SQLiteStore) getCustomDomainByID(id int64) (*model.CustomDomain, error) {
	row := s.db.QueryRow(
		`SELECT id, project_id, domain, verified, created_at FROM custom_domains WHERE id = ?`, id,
	)
	return scanCustomDomain(row)
}

func scanCustomDomain(row *sql.Row) (*model.CustomDomain, error) {
	var cd model.CustomDomain
	var createdAt string
	var verified int
	if err := row.Scan(&cd.ID, &cd.ProjectID, &cd.Domain, &verified, &createdAt); err != nil {
		return nil, fmt.Errorf("scan custom domain: %w", err)
	}
	cd.Verified = verified == 1
	cd.CreatedAt = parseTime(createdAt)
	return &cd, nil
}
