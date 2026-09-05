package store

import (
	"crypto/rand"
	"database/sql"
	"errors"
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

	// Keep one persistent connection: SQLite PRAGMAs and in-memory databases are connection-local.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	if err := prepareSchemaUpgrade(db); err != nil {
		db.Close()
		return nil, err
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
	if err := s.runMigrations(); err != nil {
		db.Close()
		return nil, err
	}
	if _, err := db.Exec(fmt.Sprintf("PRAGMA user_version=%d", SchemaVersion)); err != nil {
		db.Close()
		return nil, fmt.Errorf("record schema version: %w", err)
	}
	return s, nil
}

func (s *SQLiteStore) migrate() error {
	_, err := s.db.Exec(`
		CREATE TABLE IF NOT EXISTS users (
			id         INTEGER PRIMARY KEY AUTOINCREMENT,
			email      TEXT NOT NULL UNIQUE,
 is_admin INTEGER NOT NULL DEFAULT 0,
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
		`SELECT id, email, password, api_token, created_at, is_admin FROM users WHERE id = ?`, id,
	)
	return scanUser(row)
}

func (s *SQLiteStore) GetUserByEmail(email string) (*model.User, error) {
	row := s.db.QueryRow(
		`SELECT id, email, password, api_token, created_at, is_admin FROM users WHERE email = ?`, email,
	)
	return scanUser(row)
}

func (s *SQLiteStore) GetUserByToken(token string) (*model.User, error) {
	row := s.db.QueryRow(
		`SELECT id, email, password, api_token, created_at, is_admin FROM users WHERE api_token = ?`, token,
	)
	return scanUser(row)
}

func scanUser(row *sql.Row) (*model.User, error) {
	var u model.User
	var createdAt string
	if err := row.Scan(&u.ID, &u.Email, &u.Password, &u.APIToken, &createdAt, &u.IsAdmin); err != nil {
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

func parseTimeFlexible(s string) time.Time {
	if t, err := time.Parse(dtLayout, s); err == nil {
		return t
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t
	}
	return time.Time{}
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
