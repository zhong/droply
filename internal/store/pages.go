package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base32"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/zhong/droply/internal/model"
)

func newHostLabel(prefix string) string {
	var random [16]byte
	_, _ = rand.Read(random[:])
	return prefix + hex.EncodeToString(random[:])
}

// Hash the exact branch bytes and project ID. DNS normalization must not merge
// branch names such as feature/a, feature-a, A, or a into one mutable alias.
func branchHostLabel(projectID int64, branch string) string {
	sum := sha256.Sum256([]byte(strconv.FormatInt(projectID, 10) + "\x00" + branch))
	return "b-" + strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(sum[:]))
}

func (s *SQLiteStore) migratePages() error {
	ctx := context.Background()
	projectColumns, err := s.tableColumns("projects")
	if err != nil {
		return err
	}
	columns, err := s.tableColumns("deployments")
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if !projectColumns["host_label"] {
		if _, err := tx.ExecContext(ctx, `ALTER TABLE projects ADD COLUMN host_label TEXT NOT NULL DEFAULT ''`); err != nil {
			return err
		}
	}
	rows, err := tx.QueryContext(ctx, `SELECT id FROM projects WHERE host_label=''`)
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
		if _, err := tx.ExecContext(ctx, `UPDATE projects SET host_label=? WHERE id=?`, newHostLabel("p-"), id); err != nil {
			return err
		}
	}
	for _, column := range []struct{ name, definition string }{
		{"environment", `TEXT NOT NULL DEFAULT 'production'`},
		{"branch", `TEXT NOT NULL DEFAULT ''`},
		{"commit_ref", `TEXT NOT NULL DEFAULT ''`},
		{"preview_label", `TEXT NOT NULL DEFAULT ''`},
		{"branch_label", `TEXT NOT NULL DEFAULT ''`},
	} {
		if !columns[column.name] {
			if _, err := tx.ExecContext(ctx, `ALTER TABLE deployments ADD COLUMN `+column.name+` `+column.definition); err != nil {
				return err
			}
		}
	}
	if _, err := tx.ExecContext(ctx, `
 CREATE UNIQUE INDEX IF NOT EXISTS projects_host_label ON projects(host_label) WHERE host_label!='';
 CREATE UNIQUE INDEX IF NOT EXISTS deployments_preview_label ON deployments(preview_label) WHERE preview_label!='';
 CREATE TABLE IF NOT EXISTS publication_events(
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  project_id INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  deployment_id INTEGER NOT NULL,
  source_version INTEGER NOT NULL,
  actor_id INTEGER NOT NULL REFERENCES users(id),
  action TEXT NOT NULL,
  created_at DATETIME NOT NULL DEFAULT(strftime('%Y-%m-%d %H:%M:%S','now')),
  FOREIGN KEY(project_id,deployment_id) REFERENCES deployments(project_id,id) ON DELETE CASCADE
 );
 CREATE INDEX IF NOT EXISTS publication_events_project ON publication_events(project_id,id);
 `); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *SQLiteStore) BeginDeploymentTarget(ctx context.Context, projectID int64, artifactID, environment, branch, commit string) (*model.Deployment, error) {
	if environment == "" {
		environment = "production"
	}
	if environment != "production" && environment != "preview" {
		return nil, fmt.Errorf("unknown deployment environment: %w", ErrDeploymentState)
	}
	if strings.TrimSpace(artifactID) == "" {
		return nil, fmt.Errorf("artifact ID is required: %w", ErrDeploymentState)
	}
	return s.createDeployment(ctx, projectID, artifactID, "pending", 0, 0, environment, branch, commit)
}

func (s *SQLiteStore) GetSiteTarget(ctx context.Context, label string) (*model.SiteTarget, error) {
	label = strings.ToLower(strings.TrimSuffix(label, "."))
	target := &model.SiteTarget{}
	// A project owns its production hostname even before its first publication.
	err := s.db.QueryRowContext(ctx, `SELECT s.name,p.name,p.id FROM projects p JOIN subdomains s ON s.id=p.subdomain_id WHERE p.host_label=?`, label).Scan(&target.SubdomainName, &target.ProjectName, &target.ProjectID)
	if err == nil {
		target.Kind = "production"
		return target, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	err = s.db.QueryRowContext(ctx, `SELECT s.name,p.name,p.id,d.id FROM deployments d JOIN projects p ON p.id=d.project_id JOIN subdomains s ON s.id=p.subdomain_id WHERE d.preview_label=? AND d.preview_label!='' AND d.artifact_state='available' AND d.status IN ('preview','active','archived')`, label).Scan(&target.SubdomainName, &target.ProjectName, &target.ProjectID, &target.DeploymentID)
	if err == nil {
		target.Kind = "preview"
		return target, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	err = s.db.QueryRowContext(ctx, `SELECT s.name,p.name,p.id,d.id FROM deployment_references r JOIN deployments d ON d.id=r.deployment_id AND d.project_id=r.project_id JOIN projects p ON p.id=d.project_id JOIN subdomains s ON s.id=p.subdomain_id WHERE r.name=? AND d.branch_label=r.name AND d.branch_label!='' AND d.artifact_state='available' AND d.status IN ('preview','active','archived')`, label).Scan(&target.SubdomainName, &target.ProjectName, &target.ProjectID, &target.DeploymentID)
	if err != nil {
		return nil, err
	}
	target.Kind = "branch"
	return target, nil
}

// PromoteDeployment switches production to the existing preview artifact and
// records its actor/source in the same transaction. No files are copied or rebuilt.
func (s *SQLiteStore) PromoteDeployment(ctx context.Context, projectID int64, version int, actorID int64) (*model.Deployment, bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, false, err
	}
	defer tx.Rollback()
	d, err := scanDeployment(tx.QueryRowContext(ctx, `SELECT `+deploymentColumns+` FROM deployments WHERE project_id=? AND version=?`, projectID, version))
	if err != nil {
		return nil, false, err
	}
	if !d.Available || d.Environment != "preview" || (d.Status != "preview" && d.Status != "active" && d.Status != "archived") {
		return nil, false, ErrDeploymentState
	}
	if d.Status == "active" {
		return d, false, nil
	}
	if err := switchProductionTx(ctx, tx, projectID, d.ID); err != nil {
		return nil, false, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO publication_events(project_id,deployment_id,source_version,actor_id,action) VALUES(?,?,?,?,'promote')`, projectID, d.ID, d.Version, actorID); err != nil {
		return nil, false, err
	}
	if err := touchPublishedProjectTx(ctx, tx, projectID); err != nil {
		return nil, false, err
	}
	if err := tx.Commit(); err != nil {
		return nil, false, err
	}
	d.Status = "active"
	d.Production = true
	return d, true, nil
}

func (s *SQLiteStore) ListPublicationEvents(ctx context.Context, projectID int64) ([]model.PublicationEvent, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,project_id,deployment_id,source_version,actor_id,action,created_at FROM publication_events WHERE project_id=? ORDER BY id`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	events := []model.PublicationEvent{}
	for rows.Next() {
		var event model.PublicationEvent
		var created string
		if err := rows.Scan(&event.ID, &event.ProjectID, &event.DeploymentID, &event.SourceVersion, &event.ActorID, &event.Action, &created); err != nil {
			return nil, err
		}
		event.CreatedAt = parseTimeFlexible(created)
		events = append(events, event)
	}
	return events, rows.Err()
}
