package store

import (
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestProjectReceivesStableHostLabel(t *testing.T) {
	s := newTestStore(t)
	projectID := deploymentProject(t, s)
	project, err := s.getProjectByID(projectID)
	if err != nil {
		t.Fatal(err)
	}
	if len(project.HostLabel) != 34 {
		t.Fatalf("host label=%q", project.HostLabel)
	}
	again, err := s.GetProject(project.SubdomainID, project.Name)
	if err != nil {
		t.Fatal(err)
	}
	if again.HostLabel != project.HostLabel {
		t.Fatal("project hostname changed")
	}
}

func TestPreviewCommitMaintainsProductionAndBranchAlias(t *testing.T) {
	s := newTestStore(t)
	project := deploymentProject(t, s)
	ctx := t.Context()
	production, err := s.BeginDeployment(ctx, project, "production")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.CommitDeployment(ctx, production.ID, 1, 1, "production"); err != nil {
		t.Fatal(err)
	}
	first, err := s.BeginDeploymentTarget(ctx, project, "preview-first", "preview", "feature/a", "commit-first")
	if err != nil {
		t.Fatal(err)
	}
	if first.Environment != "preview" || first.PreviewLabel == "" || first.BranchLabel == "" || first.Commit != "commit-first" {
		t.Fatalf("metadata=%+v", first)
	}
	if _, err := s.GetSiteTarget(ctx, first.PreviewLabel); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("uploading host resolved: %v", err)
	}
	if err := s.CommitDeployment(ctx, first.ID, 1, 2, "first"); err != nil {
		t.Fatal(err)
	}
	target, err := s.GetSiteTarget(ctx, first.BranchLabel)
	if err != nil || target.DeploymentID != first.ID || target.Kind != "branch" {
		t.Fatalf("branch=%+v %v", target, err)
	}
	if _, _, err := s.SwitchDeployment(ctx, project, first.Version); !errors.Is(err, ErrDeploymentState) {
		t.Fatalf("unpublished preview rolled back: %v", err)
	}
	failed, err := s.BeginDeploymentTarget(ctx, project, "preview-failed", "preview", "feature/a", "failed-commit")
	if err != nil {
		t.Fatal(err)
	}
	if failed.BranchLabel != first.BranchLabel {
		t.Fatal("branch hostname changed")
	}
	if err := s.FailDeployment(ctx, failed.ID, "upload failed"); err != nil {
		t.Fatal(err)
	}
	target, err = s.GetSiteTarget(ctx, first.BranchLabel)
	if err != nil || target.DeploymentID != first.ID {
		t.Fatalf("failed upload moved alias=%+v %v", target, err)
	}
	second, err := s.BeginDeploymentTarget(ctx, project, "preview-second", "preview", "feature/a", "commit-second")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.CommitDeployment(ctx, second.ID, 1, 3, "second"); err != nil {
		t.Fatal(err)
	}
	active, err := s.GetActiveDeployment(ctx, project)
	if err != nil || active.ID != production.ID {
		t.Fatalf("preview changed production=%+v %v", active, err)
	}
	target, err = s.GetSiteTarget(ctx, first.PreviewLabel)
	if err != nil || target.DeploymentID != first.ID || target.Kind != "preview" {
		t.Fatalf("immutable preview moved=%+v %v", target, err)
	}
	target, err = s.GetSiteTarget(ctx, first.BranchLabel)
	if err != nil || target.DeploymentID != second.ID {
		t.Fatalf("latest branch=%+v %v", target, err)
	}
	if err := s.MarkArtifactDeleting(ctx, second.ID); !errors.Is(err, ErrDeploymentState) {
		t.Fatalf("branch target collected=%v", err)
	}
	if err := s.MarkArtifactDeleting(ctx, first.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetSiteTarget(ctx, first.PreviewLabel); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("deleting preview resolved=%v", err)
	}
	if err := s.SetArtifactState(ctx, first.ID, "deleted"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetSiteTarget(ctx, first.PreviewLabel); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("deleted preview resolved=%v", err)
	}
}

func TestBranchNamesHaveDistinctBoundedLabels(t *testing.T) {
	s := newTestStore(t)
	project := deploymentProject(t, s)
	ctx := t.Context()
	p, err := s.getProjectByID(project)
	if err != nil {
		t.Fatal(err)
	}
	other, err := s.CreateProject(p.SubdomainID, "second")
	if err != nil {
		t.Fatal(err)
	}
	labels := map[string]bool{p.HostLabel: true, other.HostLabel: true}
	for i, branch := range []string{"feature/a", "feature-a", "A", "a", "分支/测试", strings.Repeat("long/", 100)} {
		for _, projectID := range []int64{project, other.ID} {
			d, err := s.BeginDeploymentTarget(ctx, projectID, fmt.Sprintf("artifact-%d-%d", projectID, i), "preview", branch, "")
			if err != nil {
				t.Fatal(err)
			}
			for _, label := range []string{d.PreviewLabel, d.BranchLabel} {
				if labels[label] {
					t.Fatalf("label collision=%s", label)
				}
				labels[label] = true
				if len(label) <= 32 || len(label) > 63 || !regexp.MustCompile(`^[a-z0-9-]+$`).MatchString(label) {
					t.Fatalf("unsafe label=%q", label)
				}
			}
		}
	}
	for label := range labels {
		if label == "api" || label == "www" {
			t.Fatalf("reserved label=%s", label)
		}
	}
	// SQL uniqueness protects against a caller accidentally assigning an existing
	// production hostname; it cannot silently shadow another project's origin.
	if _, err := s.db.Exec(`UPDATE projects SET host_label=? WHERE id=?`, p.HostLabel, other.ID); err == nil {
		t.Fatal("duplicate production hostname accepted")
	}
}

func TestPromotionIsAtomicAndKeepsPreviewIdentity(t *testing.T) {
	s := newTestStore(t)
	project := deploymentProject(t, s)
	ctx := t.Context()
	actor, err := s.GetUserByToken("token")
	if err != nil {
		t.Fatal(err)
	}
	production, err := s.BeginDeployment(ctx, project, "production")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.CommitDeployment(ctx, production.ID, 1, 1, "production"); err != nil {
		t.Fatal(err)
	}
	preview, err := s.BeginDeploymentTarget(ctx, project, "preview", "preview", "review", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.CommitDeployment(ctx, preview.ID, 2, 2, "preview"); err != nil {
		t.Fatal(err)
	}
	// Foreign-key failure while recording the actor must roll back the pointer.
	if _, _, err := s.PromoteDeployment(ctx, project, preview.Version, 999999); err == nil {
		t.Fatal("invalid actor promoted")
	}
	active, err := s.GetActiveDeployment(ctx, project)
	if err != nil || active.ID != production.ID {
		t.Fatalf("failed promotion changed active=%+v %v", active, err)
	}
	for round := range 2 {
		promoted, changed, err := s.PromoteDeployment(ctx, project, preview.Version, actor.ID)
		if err != nil {
			t.Fatal(err)
		}
		if changed != (round == 0) || promoted.ArtifactID != preview.ArtifactID || promoted.PreviewLabel != preview.PreviewLabel || promoted.Environment != "preview" || !promoted.Production {
			t.Fatalf("promotion=%+v %v", promoted, changed)
		}
	}
	events, err := s.ListPublicationEvents(ctx, project)
	if err != nil || len(events) != 1 {
		t.Fatalf("events=%+v %v", events, err)
	}
	if events[0].ActorID != actor.ID || events[0].SourceVersion != preview.Version || events[0].Action != "promote" || events[0].CreatedAt.IsZero() {
		t.Fatalf("event=%+v", events[0])
	}
	target, err := s.GetSiteTarget(ctx, preview.PreviewLabel)
	if err != nil || target.DeploymentID != preview.ID {
		t.Fatalf("promotion lost preview=%+v %v", target, err)
	}
	if _, _, err := s.SwitchDeployment(ctx, project, production.Version); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.SwitchDeployment(ctx, project, preview.Version); err != nil {
		t.Fatalf("published preview cannot rollback=%v", err)
	}
}

func TestBranchCommitFailurePreservesBothTargets(t *testing.T) {
	s := newTestStore(t)
	project := deploymentProject(t, s)
	ctx := t.Context()
	first, err := s.BeginDeploymentTarget(ctx, project, "first", "preview", "branch", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.CommitDeployment(ctx, first.ID, 1, 1, "first"); err != nil {
		t.Fatal(err)
	}
	next, err := s.BeginDeploymentTarget(ctx, project, "next", "preview", "branch", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`CREATE TRIGGER fail_branch_update BEFORE UPDATE OF deployment_id ON deployment_references BEGIN SELECT RAISE(ABORT,'branch update failed'); END`); err != nil {
		t.Fatal(err)
	}
	if err := s.CommitDeployment(ctx, next.ID, 1, 1, "next"); err == nil {
		t.Fatal("branch commit succeeded")
	}
	target, err := s.GetSiteTarget(ctx, first.BranchLabel)
	if err != nil || target.DeploymentID != first.ID {
		t.Fatalf("failed branch moved=%+v %v", target, err)
	}
	if _, err := s.GetSiteTarget(ctx, next.PreviewLabel); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("failed preview became available=%v", err)
	}
}

func TestProjectAndPreviewHostsSurviveRestartAndDeletion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pages.db")
	s, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	project := deploymentProject(t, s)
	ctx := t.Context()
	p, err := s.getProjectByID(project)
	if err != nil {
		t.Fatal(err)
	}
	target, err := s.GetSiteTarget(ctx, p.HostLabel)
	if err != nil || target.ProjectID != project || target.DeploymentID != 0 || target.Kind != "production" {
		t.Fatalf("empty project target=%+v %v", target, err)
	}
	d, err := s.BeginDeploymentTarget(ctx, project, "preview", "preview", "stable", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.CommitDeployment(ctx, d.ID, 1, 1, "preview"); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, label := range []string{p.HostLabel, d.PreviewLabel, d.BranchLabel} {
		if _, err := s.GetSiteTarget(ctx, label); err != nil {
			t.Fatalf("restart lost %s: %v", label, err)
		}
	}
	again, err := s.GetProject(p.SubdomainID, p.Name)
	if err != nil || again.HostLabel != p.HostLabel {
		t.Fatalf("hostname changed=%+v %v", again, err)
	}
	if err := s.DeleteProject(p.SubdomainID, p.Name); err != nil {
		t.Fatal(err)
	}
	for _, label := range []string{p.HostLabel, d.PreviewLabel, d.BranchLabel} {
		if _, err := s.GetSiteTarget(ctx, label); !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("deleted project resolved %s: %v", label, err)
		}
	}
}

func TestPagesMigrationBackfillsM1ProjectsIdempotently(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	old := openHistoricalSchema(t, path, "m1")
	db := old.db
	if _, err := db.Exec(`
 INSERT INTO users(id,email,password,api_token) VALUES(1,'legacy@test','hash','legacy-token');
 INSERT INTO subdomains(id,user_id,name) VALUES(1,1,'legacy');
 INSERT INTO projects(id,subdomain_id,name) VALUES(1,1,'site');
 INSERT INTO deployments(project_id,version,file_count,total_size,status) VALUES(1,1,1,7,'active');`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	s, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.GetProject(1, "site")
	if err != nil || len(p.HostLabel) != 34 {
		t.Fatalf("backfill: %+v %v", p, err)
	}
	d, err := s.GetActiveDeployment(t.Context(), 1)
	if err != nil || d.Environment != "production" || d.Available || d.ArtifactState != "legacy" {
		t.Fatalf("legacy metadata: %+v %v", d, err)
	}
	label := p.HostLabel
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	p, err = s.GetProject(1, "site")
	if err != nil || p.HostLabel != label {
		t.Fatalf("unstable backfill: %+v %v", p, err)
	}
	target, err := s.GetSiteTarget(t.Context(), label)
	if err != nil || target.ProjectID != 1 {
		t.Fatalf("legacy host: %+v %v", target, err)
	}
}
