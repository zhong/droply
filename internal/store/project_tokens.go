package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/zhong/droply/internal/model"
)

const ProjectTokenPrefix = "dply_p_"
const MaxProjectTokenLifetime = 365 * 24 * time.Hour

var ErrProjectTokenInput = errors.New("invalid project token name, scopes, or expiration")

type ProjectTokenStore interface {
	CreateProjectToken(context.Context, int64, int64, string, []string, time.Time) (*model.ProjectToken, string, error)
	ListProjectTokens(context.Context, int64, int64) ([]model.ProjectToken, error)
	RevokeProjectToken(context.Context, int64, int64, int64) error
	AuthenticateProjectToken(context.Context, string) (*model.ProjectToken, error)
}

func (s *SQLiteStore) migrateProjectTokens() error {
	_, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS project_tokens (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 project_id INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
 digest TEXT NOT NULL UNIQUE,
 name TEXT NOT NULL,
 scopes TEXT NOT NULL,
 expires_at TEXT NOT NULL,
 revoked_at TEXT,
 created_at TEXT NOT NULL
 ); CREATE INDEX IF NOT EXISTS project_tokens_project ON project_tokens(project_id);`)
	return err
}

func tokenDigest(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

func (s *SQLiteStore) CreateProjectToken(ctx context.Context, projectID, ownerID int64, name string, scopes []string, expiresAt time.Time) (*model.ProjectToken, string, error) {
	now := time.Now().UTC()
	if expiresAt.IsZero() {
		expiresAt = now.Add(30 * 24 * time.Hour)
	}
	if len(scopes) == 0 {
		scopes = []string{"preview"}
	}
	if strings.TrimSpace(name) == "" {
		name = "ci"
	}
	if len(name) > 100 || !expiresAt.After(now) || expiresAt.After(now.Add(MaxProjectTokenLifetime)) {
		return nil, "", ErrProjectTokenInput
	}
	seen := map[string]bool{}
	normalized := []string{}
	for _, scope := range scopes {
		if scope != "preview" && scope != "production" {
			return nil, "", ErrProjectTokenInput
		}
		if !seen[scope] {
			normalized = append(normalized, scope)
			seen[scope] = true
		}
	}
	scopeJSON, err := json.Marshal(normalized)
	if err != nil {
		return nil, "", err
	}
	raw := ProjectTokenPrefix + rand.Text() + rand.Text()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, "", err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `INSERT INTO project_tokens(project_id,digest,name,scopes,expires_at,created_at,issuer_id)
 SELECT p.id,?,?,?,?,?,? FROM projects p JOIN subdomains s ON s.id=p.subdomain_id WHERE p.id=? AND (s.user_id=? OR EXISTS(SELECT 1 FROM project_members m WHERE m.project_id=p.id AND m.user_id=? AND m.role='deployer'))`, tokenDigest(raw), name, string(scopeJSON), expiresAt.UTC().Format(time.RFC3339Nano), now.Format(time.RFC3339Nano), ownerID, projectID, ownerID, ownerID)
	if err != nil {
		return nil, "", err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return nil, "", err
	}
	if n != 1 {
		return nil, "", sql.ErrNoRows
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, "", err
	}
	if err = tx.Commit(); err != nil {
		return nil, "", err
	}
	var projectOwnerID int64
	if err := s.db.QueryRowContext(ctx, `SELECT s.user_id FROM projects p JOIN subdomains s ON s.id=p.subdomain_id WHERE p.id=?`, projectID).Scan(&projectOwnerID); err != nil {
		return nil, "", err
	}
	return &model.ProjectToken{ID: id, ProjectID: projectID, OwnerID: projectOwnerID, IssuerID: ownerID, Name: name, Scopes: normalized, ExpiresAt: expiresAt.UTC(), CreatedAt: now}, raw, nil
}

const projectTokenColumns = `t.id,t.project_id,s.user_id,t.issuer_id,t.name,t.scopes,t.expires_at,t.revoked_at,t.created_at`

func scanProjectToken(row interface{ Scan(...any) error }) (*model.ProjectToken, error) {
	var t model.ProjectToken
	var scopes, expires, created string
	var revoked sql.NullString
	if err := row.Scan(&t.ID, &t.ProjectID, &t.OwnerID, &t.IssuerID, &t.Name, &scopes, &expires, &revoked, &created); err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(scopes), &t.Scopes); err != nil {
		return nil, err
	}
	var err error
	t.ExpiresAt, err = time.Parse(time.RFC3339Nano, expires)
	if err != nil {
		return nil, err
	}
	t.CreatedAt, err = time.Parse(time.RFC3339Nano, created)
	if err != nil {
		return nil, err
	}
	if revoked.Valid {
		parsed, err := time.Parse(time.RFC3339Nano, revoked.String)
		if err != nil {
			return nil, err
		}
		t.RevokedAt = &parsed
	}
	return &t, nil
}
func (s *SQLiteStore) ListProjectTokens(ctx context.Context, projectID, ownerID int64) ([]model.ProjectToken, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+projectTokenColumns+` FROM project_tokens t JOIN projects p ON p.id=t.project_id JOIN subdomains s ON s.id=p.subdomain_id WHERE p.id=? AND (s.user_id=? OR (t.issuer_id=? AND EXISTS(SELECT 1 FROM project_members m WHERE m.project_id=p.id AND m.user_id=? AND m.role='deployer'))) ORDER BY t.id`, projectID, ownerID, ownerID, ownerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []model.ProjectToken{}
	for rows.Next() {
		t, err := scanProjectToken(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, *t)
	}
	return result, rows.Err()
}
func (s *SQLiteStore) RevokeProjectToken(ctx context.Context, projectID, ownerID, tokenID int64) error {
	res, err := s.db.ExecContext(ctx, `UPDATE project_tokens SET revoked_at=COALESCE(revoked_at,?) WHERE id=? AND project_id=? AND EXISTS(SELECT 1 FROM projects p JOIN subdomains s ON s.id=p.subdomain_id WHERE p.id=project_tokens.project_id AND (s.user_id=? OR (project_tokens.issuer_id=? AND EXISTS(SELECT 1 FROM project_members m WHERE m.project_id=p.id AND m.user_id=? AND m.role='deployer'))))`, time.Now().UTC().Format(time.RFC3339Nano), tokenID, projectID, ownerID, ownerID, ownerID)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return sql.ErrNoRows
	}
	return nil
}
func (s *SQLiteStore) AuthenticateProjectToken(ctx context.Context, raw string) (*model.ProjectToken, error) {
	if !strings.HasPrefix(raw, ProjectTokenPrefix) || len(raw) != len(ProjectTokenPrefix)+52 {
		return nil, sql.ErrNoRows
	}
	t, err := scanProjectToken(s.db.QueryRowContext(ctx, `SELECT `+projectTokenColumns+` FROM project_tokens t JOIN projects p ON p.id=t.project_id JOIN subdomains s ON s.id=p.subdomain_id WHERE t.digest=?`, tokenDigest(raw)))
	if err != nil {
		return nil, err
	}
	if t.RevokedAt != nil || !time.Now().Before(t.ExpiresAt) {
		return nil, sql.ErrNoRows
	}
	role, err := s.ProjectRole(ctx, t.ProjectID, t.IssuerID)
	if err != nil {
		return nil, err
	}
	if role != "owner" && role != "deployer" {
		return nil, sql.ErrNoRows
	}
	return t, nil
}
