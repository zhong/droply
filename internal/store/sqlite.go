package store

import (
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/zhong/droply/internal/model"
	"modernc.org/sqlite"
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

	// Keep one persistent connection: SQLite PRAGMAs and in-memory databases are connection-local.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

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
	if err := s.migrateWeWork(); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate wework: %w", err)
	}
	if err := s.migrateDomainVerification(); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate domain verification: %w", err)
	}
	if err := s.migrateDeployments(); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate deployments: %w", err)
	}
	if err := s.migratePages(); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate pages: %w", err)
	}
	if err := s.migrateProjectTokens(); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate project tokens: %w", err)
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
 host_label TEXT NOT NULL DEFAULT '',
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
	`)
	return err
}

func parseTime(s string) time.Time {
	t, _ := time.Parse(dtLayout, s)
	return t
}

// migrateWeWork adds WeWork-related columns to access_rules. Idempotent via PRAGMA check.
func (s *SQLiteStore) migrateWeWork() error {
	cols, err := s.tableColumns("access_rules")
	if err != nil {
		return err
	}
	if !cols["wework_enabled"] {
		if _, err := s.db.Exec(`ALTER TABLE access_rules ADD COLUMN wework_enabled INTEGER NOT NULL DEFAULT 0`); err != nil {
			return fmt.Errorf("add wework_enabled: %w", err)
		}
	}
	if !cols["allowed_wework_users"] {
		if _, err := s.db.Exec(`ALTER TABLE access_rules ADD COLUMN allowed_wework_users TEXT`); err != nil {
			return fmt.Errorf("add allowed_wework_users: %w", err)
		}
	}
	return nil
}

func (s *SQLiteStore) tableColumns(table string) (map[string]bool, error) {
	rows, err := s.db.Query(fmt.Sprintf(`PRAGMA table_info(%s)`, table))
	if err != nil {
		return nil, fmt.Errorf("pragma table_info: %w", err)
	}
	defer rows.Close()
	cols := make(map[string]bool)
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull, pk int
		var dfltValue sql.NullString
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dfltValue, &pk); err != nil {
			return nil, fmt.Errorf("scan table_info: %w", err)
		}
		cols[name] = true
	}
	return cols, rows.Err()
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
		`INSERT INTO projects (subdomain_id, name, host_label) VALUES (?, ?, ?)`, subdomainID, name, newHostLabel("p-"),
	)
	if err != nil {
		return nil, fmt.Errorf("create project: %w", err)
	}
	id, _ := res.LastInsertId()
	return s.getProjectByID(id)
}

func (s *SQLiteStore) getProjectByID(id int64) (*model.Project, error) {
	row := s.db.QueryRow(
		`SELECT id, subdomain_id, name, created_at, updated_at, host_label FROM projects WHERE id = ?`, id,
	)
	return scanProject(row)
}

func (s *SQLiteStore) GetProject(subdomainID int64, name string) (*model.Project, error) {
	row := s.db.QueryRow(
		`SELECT id, subdomain_id, name, created_at, updated_at, host_label FROM projects WHERE subdomain_id = ? AND name = ?`,
		subdomainID, name,
	)
	return scanProject(row)
}

func (s *SQLiteStore) ListProjects(subdomainID int64) ([]model.Project, error) {
	rows, err := s.db.Query(
		`SELECT id, subdomain_id, name, created_at, updated_at, host_label FROM projects WHERE subdomain_id = ? ORDER BY created_at DESC`,
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
	if err := row.Scan(&p.ID, &p.SubdomainID, &p.Name, &createdAt, &updatedAt, &p.HostLabel); err != nil {
		return nil, fmt.Errorf("scan project: %w", err)
	}
	p.CreatedAt = parseTime(createdAt)
	p.UpdatedAt = parseTime(updatedAt)
	return &p, nil
}

func scanProjectRow(rows *sql.Rows) (*model.Project, error) {
	var p model.Project
	var createdAt, updatedAt string
	if err := rows.Scan(&p.ID, &p.SubdomainID, &p.Name, &createdAt, &updatedAt, &p.HostLabel); err != nil {
		return nil, fmt.Errorf("scan project row: %w", err)
	}
	p.CreatedAt = parseTime(createdAt)
	p.UpdatedAt = parseTime(updatedAt)
	return &p, nil
}

// ---- Custom Domains ----

func (s *SQLiteStore) CreateCustomDomain(projectID int64, domain string) (*model.CustomDomain, error) {
	res, err := s.db.Exec(
		`INSERT INTO custom_domains (project_id, domain, verification_token) VALUES (?, ?, ?)`, projectID, strings.ToLower(strings.TrimSuffix(domain, ".")), rand.Text(),
	)
	if err != nil {
		if sqliteErr, ok := errors.AsType[*sqlite.Error](err); ok && sqliteErr.Code() == 2067 {
			return nil, ErrDomainTaken
		}
		return nil, fmt.Errorf("create custom domain: %w", err)
	}
	id, _ := res.LastInsertId()
	return s.getCustomDomainByID(id)
}

func (s *SQLiteStore) GetCustomDomain(domain string) (*model.CustomDomain, error) {
	row := s.db.QueryRow(
		`SELECT id, project_id, domain, verified, created_at, verification_token FROM custom_domains WHERE domain = ?`, domain,
	)
	return scanCustomDomain(row)
}

func (s *SQLiteStore) VerifyCustomDomain(domain string) error {
	res, err := s.db.Exec(
		`UPDATE custom_domains SET verified = 1 WHERE domain = ?`, domain,
	)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *SQLiteStore) ListCustomDomains(projectID int64) ([]model.CustomDomain, error) {
	rows, err := s.db.Query(
		`SELECT id, project_id, domain, verified, created_at, verification_token FROM custom_domains WHERE project_id = ? ORDER BY created_at DESC`,
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
		if err := rows.Scan(&cd.ID, &cd.ProjectID, &cd.Domain, &verified, &createdAt, &cd.VerificationToken); err != nil {
			return nil, fmt.Errorf("scan custom domain: %w", err)
		}
		cd.Verified = verified == 1
		cd.Status = "pending"
		if cd.Verified {
			cd.Status = "verified"
		}
		cd.VerificationRecord = "_droply-verification." + cd.Domain
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
		`SELECT id, project_id, domain, verified, created_at, verification_token FROM custom_domains WHERE id = ?`, id,
	)
	return scanCustomDomain(row)
}

func scanCustomDomain(row *sql.Row) (*model.CustomDomain, error) {
	var cd model.CustomDomain
	var createdAt string
	var verified int
	if err := row.Scan(&cd.ID, &cd.ProjectID, &cd.Domain, &verified, &createdAt, &cd.VerificationToken); err != nil {
		return nil, fmt.Errorf("scan custom domain: %w", err)
	}
	cd.Verified = verified == 1
	cd.Status = "pending"
	if cd.Verified {
		cd.Status = "verified"
	}
	cd.VerificationRecord = "_droply-verification." + cd.Domain
	cd.CreatedAt = parseTime(createdAt)
	return &cd, nil
}

// ---- Access Rules ----

func (s *SQLiteStore) CreateOrUpdateAccessRule(subdomainID int64, projectID *int64, allowedIPs []string, passwordHash string, sessionTTL int, weWorkEnabled bool, allowedWeWorkUsers []string) (*model.AccessRule, error) {
	var ipsJSON interface{}
	if len(allowedIPs) > 0 {
		b, err := json.Marshal(allowedIPs)
		if err != nil {
			return nil, fmt.Errorf("marshal allowed_ips: %w", err)
		}
		ipsJSON = string(b)
	}

	var pwHash interface{}
	if passwordHash != "" {
		pwHash = passwordHash
	}

	var weWorkUsersJSON interface{}
	if len(allowedWeWorkUsers) > 0 {
		b, err := json.Marshal(allowedWeWorkUsers)
		if err != nil {
			return nil, fmt.Errorf("marshal allowed_wework_users: %w", err)
		}
		weWorkUsersJSON = string(b)
	}

	weWorkInt := 0
	if weWorkEnabled {
		weWorkInt = 1
	}

	// Check if rule already exists (needed because UNIQUE doesn't match NULL=NULL)
	existing, err := s.GetAccessRule(subdomainID, projectID)
	if err != nil {
		return nil, fmt.Errorf("check existing access rule: %w", err)
	}

	if existing != nil {
		// Update existing rule
		_, err = s.db.Exec(
			`UPDATE access_rules SET allowed_ips = ?, password_hash = ?, session_ttl = ?,
				wework_enabled = ?, allowed_wework_users = ?,
				updated_at = strftime('%Y-%m-%d %H:%M:%S', 'now')
			 WHERE id = ?`,
			ipsJSON, pwHash, sessionTTL, weWorkInt, weWorkUsersJSON, existing.ID,
		)
		if err != nil {
			return nil, fmt.Errorf("update access rule: %w", err)
		}
	} else {
		// Insert new rule
		_, err = s.db.Exec(
			`INSERT INTO access_rules (subdomain_id, project_id, allowed_ips, password_hash, session_ttl, wework_enabled, allowed_wework_users)
			 VALUES (?, ?, ?, ?, ?, ?, ?)`,
			subdomainID, projectID, ipsJSON, pwHash, sessionTTL, weWorkInt, weWorkUsersJSON,
		)
		if err != nil {
			return nil, fmt.Errorf("create access rule: %w", err)
		}
	}

	return s.GetAccessRule(subdomainID, projectID)
}

func (s *SQLiteStore) GetAccessRule(subdomainID int64, projectID *int64) (*model.AccessRule, error) {
	var row *sql.Row
	if projectID == nil {
		row = s.db.QueryRow(
			`SELECT id, subdomain_id, project_id, allowed_ips, password_hash, session_ttl, wework_enabled, allowed_wework_users, created_at, updated_at
			 FROM access_rules WHERE subdomain_id = ? AND project_id IS NULL`, subdomainID,
		)
	} else {
		row = s.db.QueryRow(
			`SELECT id, subdomain_id, project_id, allowed_ips, password_hash, session_ttl, wework_enabled, allowed_wework_users, created_at, updated_at
			 FROM access_rules WHERE subdomain_id = ? AND project_id = ?`, subdomainID, *projectID,
		)
	}
	rule, err := scanAccessRule(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get access rule: %w", err)
	}
	return rule, nil
}

func (s *SQLiteStore) getAccessRuleByID(id int64) (*model.AccessRule, error) {
	row := s.db.QueryRow(
		`SELECT id, subdomain_id, project_id, allowed_ips, password_hash, session_ttl, wework_enabled, allowed_wework_users, created_at, updated_at
		 FROM access_rules WHERE id = ?`, id,
	)
	rule, err := scanAccessRule(row)
	if err != nil {
		return nil, fmt.Errorf("get access rule by id: %w", err)
	}
	return rule, nil
}

func (s *SQLiteStore) DeleteAccessRule(subdomainID int64, projectID *int64) error {
	if projectID == nil {
		_, err := s.db.Exec(
			`DELETE FROM access_rules WHERE subdomain_id = ? AND project_id IS NULL`, subdomainID,
		)
		return err
	}
	_, err := s.db.Exec(
		`DELETE FROM access_rules WHERE subdomain_id = ? AND project_id = ?`, subdomainID, *projectID,
	)
	return err
}

func (s *SQLiteStore) FindAccessRuleForSite(subdomainName string, projectName string) (*model.AccessRule, error) {
	// Try project-level first
	if projectName != "" {
		row := s.db.QueryRow(
			`SELECT ar.id, ar.subdomain_id, ar.project_id, ar.allowed_ips, ar.password_hash, ar.session_ttl, ar.wework_enabled, ar.allowed_wework_users, ar.created_at, ar.updated_at
			 FROM access_rules ar
			 JOIN subdomains s ON ar.subdomain_id = s.id
			 JOIN projects p ON ar.project_id = p.id AND p.subdomain_id = s.id
			 WHERE s.name = ? AND p.name = ?`,
			subdomainName, projectName,
		)
		rule, err := scanAccessRule(row)
		if err == nil {
			return rule, nil
		}
		if err != sql.ErrNoRows {
			return nil, fmt.Errorf("find project access rule: %w", err)
		}
	}

	// Fall back to subdomain-level
	row := s.db.QueryRow(
		`SELECT ar.id, ar.subdomain_id, ar.project_id, ar.allowed_ips, ar.password_hash, ar.session_ttl, ar.wework_enabled, ar.allowed_wework_users, ar.created_at, ar.updated_at
		 FROM access_rules ar
		 JOIN subdomains s ON ar.subdomain_id = s.id
		 WHERE s.name = ? AND ar.project_id IS NULL`,
		subdomainName,
	)
	rule, err := scanAccessRule(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("find subdomain access rule: %w", err)
	}
	return rule, nil
}

func (s *SQLiteStore) HasAccessRules(subdomainID int64) (bool, error) {
	var count int
	err := s.db.QueryRow(
		`SELECT COUNT(*) FROM access_rules WHERE subdomain_id = ?`, subdomainID,
	).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("has access rules: %w", err)
	}
	return count > 0, nil
}

func parseTimeFlexible(s string) time.Time {
	if t, err := time.Parse(dtLayout, s); err == nil {
		return t
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t
	}
	return time.Time{}
}

func scanAccessRule(row *sql.Row) (*model.AccessRule, error) {
	var r model.AccessRule
	var projectID sql.NullInt64
	var allowedIPs sql.NullString
	var passwordHash sql.NullString
	var weWorkEnabled int
	var allowedWeWorkUsers sql.NullString
	var createdAt, updatedAt string

	if err := row.Scan(&r.ID, &r.SubdomainID, &projectID, &allowedIPs, &passwordHash, &r.SessionTTL, &weWorkEnabled, &allowedWeWorkUsers, &createdAt, &updatedAt); err != nil {
		return nil, err
	}

	if projectID.Valid {
		r.ProjectID = &projectID.Int64
	}
	if allowedIPs.Valid {
		if err := json.Unmarshal([]byte(allowedIPs.String), &r.AllowedIPs); err != nil {
			return nil, fmt.Errorf("unmarshal allowed_ips: %w", err)
		}
	}
	if passwordHash.Valid {
		r.PasswordHash = passwordHash.String
		r.HasPassword = true
	}
	r.WeWorkEnabled = weWorkEnabled != 0
	if allowedWeWorkUsers.Valid && allowedWeWorkUsers.String != "" {
		if err := json.Unmarshal([]byte(allowedWeWorkUsers.String), &r.AllowedWeWorkUsers); err != nil {
			return nil, fmt.Errorf("unmarshal allowed_wework_users: %w", err)
		}
	}
	r.CreatedAt = parseTimeFlexible(createdAt)
	r.UpdatedAt = parseTimeFlexible(updatedAt)
	return &r, nil
}

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

// ErrDomainTaken indicates that another binding already owns the hostname.
var ErrDomainTaken = errors.New("domain already bound")

// Existing verified bindings remain trusted. Pending bindings gain a persistent challenge.
func (s *SQLiteStore) migrateDomainVerification() error {
	// Canonicalize legacy names too; uniqueness conflicts stop startup rather than
	// assigning an ambiguous hostname to an arbitrary project.
	if _, err := s.db.Exec(`UPDATE custom_domains SET domain = lower(rtrim(trim(domain), '.'))`); err != nil {
		return fmt.Errorf("normalize legacy domains: %w", err)
	}
	cols, err := s.tableColumns("custom_domains")
	if err != nil {
		return err
	}
	if !cols["verification_token"] {
		if _, err := s.db.Exec(`ALTER TABLE custom_domains ADD COLUMN verification_token TEXT NOT NULL DEFAULT ''`); err != nil {
			return err
		}
	}
	rows, err := s.db.Query(`SELECT id FROM custom_domains WHERE verification_token = ''`)
	if err != nil {
		return err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, id := range ids {
		if _, err := s.db.Exec(`UPDATE custom_domains SET verification_token = ? WHERE id = ?`, rand.Text(), id); err != nil {
			return err
		}
	}
	return nil
}

// VerifyCustomDomainChallenge atomically binds ownership proof to the current binding.
func (s *SQLiteStore) VerifyCustomDomainChallenge(domain, token string) error {
	res, err := s.db.Exec(`UPDATE custom_domains SET verified = 1 WHERE domain = ? AND verification_token = ? AND verification_token != ''`, domain, token)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}
