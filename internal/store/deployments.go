package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/zhong/droply/internal/model"
)

// ErrDeploymentState means a requested transition conflicts with the current
// deployment state or a live reference. The caller must not mutate its artifact.
var ErrDeploymentState = errors.New("deployment state or reference prevents operation")

const deploymentColumns = `id, project_id, version, file_count, total_size, status, created_at, artifact_id, artifact_state, failure_reason, checksum, environment, branch, commit_ref, preview_label, branch_label`

type deploymentScanner interface{ Scan(...any) error }

func scanDeployment(row deploymentScanner) (*model.Deployment, error) {
	var d model.Deployment
	var created string
	if err := row.Scan(&d.ID, &d.ProjectID, &d.Version, &d.FileCount, &d.TotalSize, &d.Status, &created, &d.ArtifactID, &d.ArtifactState, &d.FailureReason, &d.Checksum, &d.Environment, &d.Branch, &d.Commit, &d.PreviewLabel, &d.BranchLabel); err != nil {
		return nil, err
	}
	d.CreatedAt = parseTimeFlexible(created)
	d.Available = d.ArtifactState == "available" && d.ArtifactID != ""
	d.Production = d.Status == "active"
	return &d, nil
}

// migrateDeployments preserves legacy metadata without claiming historical files
// exist. Ambiguous old state requires operator repair rather than guessed history.
func (s *SQLiteStore) migrateDeployments() error {
	ctx := context.Background()
	cols, err := s.tableColumns("deployments")
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var duplicates int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM (SELECT project_id,version FROM deployments GROUP BY project_id,version HAVING COUNT(*)>1)`).Scan(&duplicates); err != nil {
		return err
	}
	if duplicates > 0 {
		return errors.New("duplicate deployment versions: restore a backup or explicitly repair project/version conflicts before retrying")
	}
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM (SELECT project_id FROM deployments WHERE status='active' GROUP BY project_id HAVING COUNT(*)>1)`).Scan(&duplicates); err != nil {
		return err
	}
	if duplicates > 0 {
		return errors.New("multiple active deployments: restore a backup or explicitly choose the correct production record before retrying")
	}
	for _, column := range []struct{ name, definition string }{
		{"artifact_id", `TEXT NOT NULL DEFAULT ''`},
		{"artifact_state", `TEXT NOT NULL DEFAULT 'legacy'`},
		{"failure_reason", `TEXT NOT NULL DEFAULT ''`},
		{"checksum", `TEXT NOT NULL DEFAULT ''`},
	} {
		if !cols[column.name] {
			if _, err := tx.ExecContext(ctx, `ALTER TABLE deployments ADD COLUMN `+column.name+` `+column.definition); err != nil {
				return err
			}
		}
	}
	if _, err := tx.ExecContext(ctx, `
 CREATE UNIQUE INDEX IF NOT EXISTS deployments_project_version ON deployments(project_id,version);
 CREATE UNIQUE INDEX IF NOT EXISTS deployments_active_project ON deployments(project_id) WHERE status='active';
 CREATE UNIQUE INDEX IF NOT EXISTS deployments_artifact_id ON deployments(artifact_id) WHERE artifact_id!='';
 CREATE UNIQUE INDEX IF NOT EXISTS deployments_project_id ON deployments(project_id,id);
 CREATE TABLE IF NOT EXISTS deployment_references (
  project_id INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  name TEXT NOT NULL,
  deployment_id INTEGER NOT NULL,
  PRIMARY KEY(project_id,name),
  FOREIGN KEY(project_id,deployment_id) REFERENCES deployments(project_id,id) ON DELETE CASCADE
 );
 CREATE INDEX IF NOT EXISTS deployment_references_target ON deployment_references(deployment_id);
 `); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *SQLiteStore) BeginDeployment(ctx context.Context, projectID int64, artifactID string) (*model.Deployment, error) {
	return s.BeginDeploymentTarget(ctx, projectID, artifactID, "production", "", "")
}

// CreateDeployment remains available for legacy fixtures/importers. It reserves
// a unique version but deliberately does not advertise an immutable artifact.
func (s *SQLiteStore) CreateDeployment(projectID int64, fileCount int, totalSize int64) (*model.Deployment, error) {
	return s.createDeployment(context.Background(), projectID, "", "legacy", fileCount, totalSize, "production", "", "")
}

func (s *SQLiteStore) createDeployment(ctx context.Context, projectID int64, artifactID, state string, count int, size int64, environment, branch, commit string) (*model.Deployment, error) {
	if count < 0 || size < 0 {
		return nil, ErrDeploymentState
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	previewLabel, branchLabel := "", ""
	if environment == "preview" {
		previewLabel = newHostLabel("d-")
		if branch != "" {
			branchLabel = branchHostLabel(projectID, branch)
		}
	}
	res, err := tx.ExecContext(ctx, `INSERT INTO deployments(project_id,version,file_count,total_size,status,artifact_id,artifact_state,environment,branch,commit_ref,preview_label,branch_label)
 SELECT ?,COALESCE(MAX(version),0)+1,?,?,'uploading',?,?,?,?,?,?,? FROM deployments WHERE project_id=?`, projectID, count, size, artifactID, state, environment, branch, commit, previewLabel, branchLabel, projectID)
	if err != nil {
		return nil, fmt.Errorf("reserve deployment version: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	d, err := scanDeployment(tx.QueryRowContext(ctx, `SELECT `+deploymentColumns+` FROM deployments WHERE id=?`, id))
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return d, nil
}

// CommitDeployment activates the complete artifact in the same transaction that
// archives the prior production row. Successful completion order decides production.
func (s *SQLiteStore) CommitDeployment(ctx context.Context, id int64, count int, size int64, checksum string) error {
	if count < 0 || size < 0 || checksum == "" {
		return ErrDeploymentState
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	d, err := scanDeployment(tx.QueryRowContext(ctx, `SELECT `+deploymentColumns+` FROM deployments WHERE id=?`, id))
	if err != nil {
		return err
	}
	if d.Status != "uploading" || d.ArtifactState != "pending" || d.ArtifactID == "" {
		return ErrDeploymentState
	}
	status := "active"
	if d.Environment == "preview" {
		status = "preview"
	} else {
		if _, err := tx.ExecContext(ctx, `UPDATE deployments SET status='archived' WHERE project_id=? AND status='active'`, d.ProjectID); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE deployments SET status=?,artifact_state='available',file_count=?,total_size=?,checksum=?,failure_reason='' WHERE id=?`, status, count, size, checksum, id); err != nil {
		return err
	}
	if d.Environment == "preview" && d.BranchLabel != "" {
		if _, err := tx.ExecContext(ctx, `INSERT INTO deployment_references(project_id,name,deployment_id) VALUES(?,?,?) ON CONFLICT(project_id,name) DO UPDATE SET deployment_id=excluded.deployment_id`, d.ProjectID, d.BranchLabel, id); err != nil {
			return err
		}
	}

	if _, err := tx.ExecContext(ctx, `UPDATE projects SET updated_at=strftime('%Y-%m-%d %H:%M:%S','now') WHERE id=?`, d.ProjectID); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *SQLiteStore) FailDeployment(ctx context.Context, id int64, reason string) error {
	res, err := s.db.ExecContext(ctx, `UPDATE deployments SET status='failed',failure_reason=? WHERE id=? AND status='uploading'`, reason, id)
	return deploymentTransition(res, err)
}

func (s *SQLiteStore) GetActiveDeployment(ctx context.Context, projectID int64) (*model.Deployment, error) {
	return scanDeployment(s.db.QueryRowContext(ctx, `SELECT `+deploymentColumns+` FROM deployments WHERE project_id=? AND status='active'`, projectID))
}
func (s *SQLiteStore) GetDeployment(ctx context.Context, projectID int64, version int) (*model.Deployment, error) {
	return scanDeployment(s.db.QueryRowContext(ctx, `SELECT `+deploymentColumns+` FROM deployments WHERE project_id=? AND version=?`, projectID, version))
}
func (s *SQLiteStore) getDeploymentByID(id int64) (*model.Deployment, error) {
	return s.GetDeploymentByID(context.Background(), id)
}
func (s *SQLiteStore) GetDeploymentByID(ctx context.Context, id int64) (*model.Deployment, error) {
	return scanDeployment(s.db.QueryRowContext(ctx, `SELECT `+deploymentColumns+` FROM deployments WHERE id=?`, id))
}
func (s *SQLiteStore) ListDeployments(projectID int64) ([]model.Deployment, error) {
	return s.listDeployments(context.Background(), ` WHERE project_id=? ORDER BY version DESC`, projectID)
}
func (s *SQLiteStore) ListAllDeployments(ctx context.Context) ([]model.Deployment, error) {
	return s.listDeployments(ctx, ` ORDER BY project_id,version`)
}
func (s *SQLiteStore) listDeployments(ctx context.Context, suffix string, args ...any) ([]model.Deployment, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+deploymentColumns+` FROM deployments`+suffix, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []model.Deployment{}
	for rows.Next() {
		d, err := scanDeployment(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, *d)
	}
	return result, rows.Err()
}

func (s *SQLiteStore) AttachLegacyArtifact(ctx context.Context, id int64, artifactID string, count int, size int64, checksum string) error {
	if strings.TrimSpace(artifactID) == "" || count < 0 || size < 0 || checksum == "" {
		return ErrDeploymentState
	}
	res, err := s.db.ExecContext(ctx, `UPDATE deployments SET artifact_id=?,artifact_state='available',file_count=?,total_size=?,checksum=? WHERE id=? AND status='active' AND artifact_state='legacy' AND artifact_id=''`, artifactID, count, size, checksum, id)
	return deploymentTransition(res, err)
}

func (s *SQLiteStore) SwitchDeployment(ctx context.Context, projectID int64, version int) (*model.Deployment, bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, false, err
	}
	defer tx.Rollback()
	d, err := scanDeployment(tx.QueryRowContext(ctx, `SELECT `+deploymentColumns+` FROM deployments WHERE project_id=? AND version=?`, projectID, version))
	if err != nil {
		return nil, false, err
	}
	if !d.Available || (d.Status != "active" && d.Status != "archived") {
		return nil, false, ErrDeploymentState
	}
	if d.Status == "active" {
		return d, false, nil
	}
	if _, err := tx.ExecContext(ctx, `UPDATE deployments SET status='archived' WHERE project_id=? AND status='active'`, projectID); err != nil {
		return nil, false, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE deployments SET status='active' WHERE id=?`, d.ID); err != nil {
		return nil, false, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE projects SET updated_at=strftime('%Y-%m-%d %H:%M:%S','now') WHERE id=?`, projectID); err != nil {
		return nil, false, err
	}
	if err := tx.Commit(); err != nil {
		return nil, false, err
	}
	d.Status = "active"
	d.Production = true
	return d, true, nil
}

// ActivateDeployment supports old fixture creation without making metadata-only
// deployments available for rollback. New serving code uses Commit/Switch instead.
func (s *SQLiteStore) ActivateDeployment(id int64) error {
	ctx := context.Background()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	d, err := scanDeployment(tx.QueryRowContext(ctx, `SELECT `+deploymentColumns+` FROM deployments WHERE id=?`, id))
	if err != nil {
		return err
	}
	if d.ArtifactState != "legacy" || (d.Status != "uploading" && d.Status != "active" && d.Status != "archived") {
		return ErrDeploymentState
	}
	if _, err := tx.ExecContext(ctx, `UPDATE deployments SET status='archived' WHERE project_id=? AND status='active' AND id!=?`, d.ProjectID, id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE deployments SET status='active' WHERE id=?`, id); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *SQLiteStore) MarkArtifactDeleting(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, `UPDATE deployments SET artifact_state='deleting' WHERE id=? AND status NOT IN ('active','uploading') AND artifact_id!='' AND artifact_state IN ('available','pending','missing','deleting') AND NOT EXISTS (SELECT 1 FROM deployment_references WHERE deployment_id=deployments.id)`, id)
	return deploymentTransition(res, err)
}
func (s *SQLiteStore) SetArtifactState(ctx context.Context, id int64, state string) error {
	var res sql.Result
	var err error
	switch state {
	case "deleted":
		res, err = s.db.ExecContext(ctx, `UPDATE deployments SET artifact_state='deleted' WHERE id=? AND artifact_state IN ('deleting','deleted') AND status NOT IN ('active','uploading') AND NOT EXISTS(SELECT 1 FROM deployment_references WHERE deployment_id=deployments.id)`, id)
	case "missing":
		res, err = s.db.ExecContext(ctx, `UPDATE deployments SET artifact_state='missing' WHERE id=? AND artifact_id!='' AND artifact_state IN ('available','pending','missing')`, id)
	default:
		return ErrDeploymentState
	}
	return deploymentTransition(res, err)
}

// PutDeploymentReference reserves an available same-project artifact against
// collection. It is an internal storage primitive, not a preview API.
func (s *SQLiteStore) PutDeploymentReference(ctx context.Context, projectID int64, name string, deploymentID int64) error {
	if strings.TrimSpace(name) == "" {
		return ErrDeploymentState
	}
	res, err := s.db.ExecContext(ctx, `INSERT INTO deployment_references(project_id,name,deployment_id)
 SELECT ?,?,id FROM deployments WHERE project_id=? AND id=? AND artifact_state='available' AND artifact_id!='' AND status IN ('preview','active','archived')
 ON CONFLICT(project_id,name) DO UPDATE SET deployment_id=excluded.deployment_id`, projectID, name, projectID, deploymentID)
	return deploymentTransition(res, err)
}

func deploymentTransition(res sql.Result, err error) error {
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrDeploymentState
	}
	return nil
}

// DeploymentReferenced reports whether production, an upload, or an alias pins
// the artifact. MarkArtifactDeleting rechecks this atomically before collection.
func (s *SQLiteStore) DeploymentReferenced(ctx context.Context, id int64) (bool, error) {
	var pinned bool
	err := s.db.QueryRowContext(ctx, `SELECT status IN ('active','uploading') OR EXISTS(SELECT 1 FROM deployment_references WHERE deployment_id=deployments.id) FROM deployments WHERE id=?`, id).Scan(&pinned)
	return pinned, err
}
