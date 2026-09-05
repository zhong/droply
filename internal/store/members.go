package store

import (
	"context"
	"database/sql"
	"errors"
	"github.com/zhong/droply/internal/model"
	"strings"
	"time"
)

var ErrMembership = errors.New("invalid member role or immutable owner")

type MembershipStore interface {
	ProjectRole(context.Context, int64, int64) (string, error)
	ListAccessibleProjects(context.Context, int64) ([]model.AuthorizedProject, error)
	ListProjectMembers(context.Context, int64) ([]model.ProjectMember, error)
	PutProjectMember(context.Context, int64, string, string) (*model.ProjectMember, error)
	RemoveProjectMember(context.Context, int64, int64) error
}

func (s *SQLiteStore) migrateMembers() error {
	columns, err := s.tableColumns("project_tokens")
	if err != nil {
		return err
	}
	if !columns["issuer_id"] {
		if _, err := s.db.Exec(`ALTER TABLE project_tokens ADD COLUMN issuer_id INTEGER REFERENCES users(id)`); err != nil {
			return err
		}
	}
	if _, err := s.db.Exec(`UPDATE project_tokens SET issuer_id=(SELECT s.user_id FROM projects p JOIN subdomains s ON s.id=p.subdomain_id WHERE p.id=project_tokens.project_id) WHERE issuer_id IS NULL`); err != nil {
		return err
	}

	_, err = s.db.Exec(`CREATE TABLE IF NOT EXISTS project_members(
 project_id INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
 user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 role TEXT NOT NULL CHECK(role IN ('deployer','viewer')),
 PRIMARY KEY(project_id,user_id)
 );CREATE INDEX IF NOT EXISTS project_members_user ON project_members(user_id,project_id);`)
	return err
}
func (s *SQLiteStore) ProjectRole(ctx context.Context, projectID, userID int64) (string, error) {
	var role string
	err := s.db.QueryRowContext(ctx, `SELECT CASE WHEN s.user_id=? THEN 'owner' ELSE COALESCE(m.role,'') END FROM projects p JOIN subdomains s ON s.id=p.subdomain_id LEFT JOIN project_members m ON m.project_id=p.id AND m.user_id=? WHERE p.id=?`, userID, userID, projectID).Scan(&role)
	return role, err
}
func (s *SQLiteStore) ListAccessibleProjects(ctx context.Context, userID int64) ([]model.AuthorizedProject, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT p.id,s.name,p.name,p.host_label,CASE WHEN s.user_id=? THEN 'owner' ELSE m.role END FROM projects p JOIN subdomains s ON s.id=p.subdomain_id LEFT JOIN project_members m ON m.project_id=p.id AND m.user_id=? WHERE s.user_id=? OR m.user_id IS NOT NULL ORDER BY s.name,p.name`, userID, userID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []model.AuthorizedProject{}
	for rows.Next() {
		var p model.AuthorizedProject
		if err := rows.Scan(&p.ProjectID, &p.SubdomainName, &p.Project, &p.HostLabel, &p.Role); err != nil {
			return nil, err
		}
		result = append(result, p)
	}
	return result, rows.Err()
}
func (s *SQLiteStore) ListProjectMembers(ctx context.Context, projectID int64) ([]model.ProjectMember, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT u.id,u.email,'owner' FROM projects p JOIN subdomains s ON s.id=p.subdomain_id JOIN users u ON u.id=s.user_id WHERE p.id=? UNION ALL SELECT u.id,u.email,m.role FROM project_members m JOIN users u ON u.id=m.user_id JOIN projects p ON p.id=m.project_id JOIN subdomains s ON s.id=p.subdomain_id WHERE m.project_id=? AND u.id<>s.user_id`, projectID, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []model.ProjectMember{}
	for rows.Next() {
		var m model.ProjectMember
		if err := rows.Scan(&m.UserID, &m.Email, &m.Role); err != nil {
			return nil, err
		}
		result = append(result, m)
	}
	return result, rows.Err()
}
func (s *SQLiteStore) PutProjectMember(ctx context.Context, projectID int64, email, role string) (*model.ProjectMember, error) {
	if role != "deployer" && role != "viewer" {
		return nil, ErrMembership
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var userID, ownerID int64
	email = strings.TrimSpace(email)
	if err := tx.QueryRowContext(ctx, `SELECT id FROM users WHERE email=?`, email).Scan(&userID); err != nil {
		return nil, err
	}
	if err := tx.QueryRowContext(ctx, `SELECT s.user_id FROM projects p JOIN subdomains s ON s.id=p.subdomain_id WHERE p.id=?`, projectID).Scan(&ownerID); err != nil {
		return nil, err
	}
	if userID == ownerID {
		return nil, ErrMembership
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO project_members(project_id,user_id,role) VALUES(?,?,?) ON CONFLICT(project_id,user_id) DO UPDATE SET role=excluded.role`, projectID, userID, role); err != nil {
		return nil, err
	}
	if role == "viewer" {
		if _, err := tx.ExecContext(ctx, `UPDATE project_tokens SET revoked_at=COALESCE(revoked_at,?) WHERE project_id=? AND issuer_id=?`, time.Now().UTC().Format(time.RFC3339Nano), projectID, userID); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &model.ProjectMember{UserID: userID, Email: email, Role: role}, nil
}
func (s *SQLiteStore) RemoveProjectMember(ctx context.Context, projectID, userID int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var ownerID int64
	if err := tx.QueryRowContext(ctx, `SELECT s.user_id FROM projects p JOIN subdomains s ON s.id=p.subdomain_id WHERE p.id=?`, projectID).Scan(&ownerID); err != nil {
		return err
	}
	if ownerID == userID {
		return ErrMembership
	}
	res, err := tx.ExecContext(ctx, `DELETE FROM project_members WHERE project_id=? AND user_id=?`, projectID, userID)
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
	if _, err := tx.ExecContext(ctx, `UPDATE project_tokens SET revoked_at=COALESCE(revoked_at,?) WHERE project_id=? AND issuer_id=?`, time.Now().UTC().Format(time.RFC3339Nano), projectID, userID); err != nil {
		return err
	}
	return tx.Commit()
}
