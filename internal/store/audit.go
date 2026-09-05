package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/zhong/droply/internal/model"
)

type AuditStore interface {
	BeginAuditEvent(context.Context, model.AuditEvent) (int64, error)
	FinishAuditEvent(context.Context, int64, int64, string, int, string) error
	ListAuditEvents(context.Context, int64, int64, int64, int) ([]model.AuditEvent, error)
	CleanupAuditEvents(context.Context, int) (int64, error)
}

func (s *SQLiteStore) migrateAudit() error {
	_, err := s.db.ExecContext(context.Background(), `CREATE TABLE IF NOT EXISTS audit_events(
 id INTEGER PRIMARY KEY AUTOINCREMENT, project_id INTEGER NOT NULL DEFAULT 0,
 subdomain_id INTEGER NOT NULL DEFAULT 0, actor_kind TEXT NOT NULL,actor_id INTEGER NOT NULL,user_id INTEGER NOT NULL,
 action TEXT NOT NULL,target TEXT NOT NULL,result TEXT NOT NULL DEFAULT 'pending',status_code INTEGER NOT NULL DEFAULT 0,
 created_at TEXT NOT NULL DEFAULT(strftime('%Y-%m-%d %H:%M:%S','now')));
 CREATE INDEX IF NOT EXISTS audit_project_cursor ON audit_events(project_id,id);
 CREATE INDEX IF NOT EXISTS audit_subdomain_cursor ON audit_events(subdomain_id,id);
 CREATE INDEX IF NOT EXISTS audit_retention ON audit_events(created_at);`)
	return err
}

func (s *SQLiteStore) BeginAuditEvent(ctx context.Context, event model.AuditEvent) (int64, error) {
	if (event.ActorKind != "user" && event.ActorKind != "project_token") || event.ActorID <= 0 || event.UserID <= 0 || event.Action == "" || len(event.Target) > 512 {
		return 0, errors.New("invalid audit event")
	}
	result, err := s.db.ExecContext(ctx, `INSERT INTO audit_events(project_id,subdomain_id,actor_kind,actor_id,user_id,action,target) VALUES(?,?,?,?,?,?,?)`, event.ProjectID, event.SubdomainID, event.ActorKind, event.ActorID, event.UserID, event.Action, event.Target)
	if err != nil {
		return 0, err
	}
	return result.LastInsertId()
}
func (s *SQLiteStore) FinishAuditEvent(ctx context.Context, id, projectID int64, target string, status int, outcome string) error {
	if status < 100 || status > 599 || len(target) > 512 || (outcome != "success" && outcome != "failure") {
		return errors.New("invalid audit outcome")
	}
	result, err := s.db.ExecContext(ctx, `UPDATE audit_events SET project_id=?,target=?,result=?,status_code=? WHERE id=? AND result='pending'`, projectID, target, outcome, status, id)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return errors.New("audit event is not pending")
	}
	return nil
}
func (s *SQLiteStore) ListAuditEvents(ctx context.Context, projectID, subdomainID, before int64, limit int) ([]model.AuditEvent, error) {
	if limit < 1 || limit > 100 || before < 0 || projectID < 0 || subdomainID < 0 {
		return nil, errors.New("invalid audit pagination")
	}
	clauses := []string{"1=1"}
	args := []any{}
	if projectID > 0 {
		clauses = append(clauses, "(project_id=? OR (project_id=0 AND subdomain_id=?))")
		args = append(args, projectID, subdomainID)
	}
	if before > 0 {
		clauses = append(clauses, "id<?")
		args = append(args, before)
	}
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, `SELECT id,project_id,subdomain_id,actor_kind,actor_id,user_id,action,target,result,status_code,created_at FROM audit_events WHERE `+strings.Join(clauses, " AND ")+` ORDER BY id DESC LIMIT ?`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	events := []model.AuditEvent{}
	for rows.Next() {
		var e model.AuditEvent
		var created string
		if err := rows.Scan(&e.ID, &e.ProjectID, &e.SubdomainID, &e.ActorKind, &e.ActorID, &e.UserID, &e.Action, &e.Target, &e.Result, &e.StatusCode, &created); err != nil {
			return nil, err
		}
		e.CreatedAt = parseTimeFlexible(created)
		events = append(events, e)
	}
	return events, rows.Err()
}
func (s *SQLiteStore) CleanupAuditEvents(ctx context.Context, days int) (int64, error) {
	if days < 1 {
		return 0, fmt.Errorf("audit retention must be positive")
	}
	result, err := s.db.ExecContext(ctx, `DELETE FROM audit_events WHERE created_at < ?`, time.Now().UTC().AddDate(0, 0, -days).Format("2006-01-02 15:04:05"))
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}
