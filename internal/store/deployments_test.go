package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"github.com/zhong/droply/internal/model"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func deploymentProject(t *testing.T, s *SQLiteStore) int64 {
	t.Helper()
	u, err := s.CreateUser("deploy@example.com", "hash", "token")
	if err != nil {
		t.Fatal(err)
	}
	sub, err := s.CreateSubdomain(u.ID, "deploy")
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.CreateProject(sub.ID, "site")
	if err != nil {
		t.Fatal(err)
	}
	return p.ID
}

func TestLegacyDeploymentsRejectPublicationTransitions(t *testing.T) {
	s := newTestStore(t)
	project := deploymentProject(t, s)
	// Historical records have no immutable artifact. Do not construct them through
	// today's publication API, which would incorrectly claim artifact availability.
	if _, err := s.db.Exec(`INSERT INTO deployments(project_id,version,file_count,total_size,status) VALUES(?,1,1,10,'active'),(?,2,1,20,'archived')`, project, project); err != nil {
		t.Fatal(err)
	}
	for _, version := range []int{1, 2} {
		d, err := s.GetDeployment(t.Context(), project, version)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.CommitDeployment(t.Context(), d.ID, 1, 1, "checksum"); !errors.Is(err, ErrDeploymentState) {
			t.Fatalf("legacy commit accepted: %v", err)
		}
		if _, _, err := s.SwitchDeployment(t.Context(), project, version); !errors.Is(err, ErrDeploymentState) {
			t.Fatalf("legacy rollback accepted: %v", err)
		}
		if _, _, err := s.PromoteDeployment(t.Context(), project, version, 1); !errors.Is(err, ErrDeploymentState) {
			t.Fatalf("legacy promotion accepted: %v", err)
		}
	}
	active, err := s.GetActiveDeployment(t.Context(), project)
	if err != nil || active.Version != 1 || active.Available {
		t.Fatalf("legacy production changed: %+v %v", active, err)
	}
}

func TestDeploymentCommitAndSwitch(t *testing.T) {
	s := newTestStore(t)
	project := deploymentProject(t, s)
	ctx := t.Context()
	first, err := s.BeginDeployment(ctx, project, "artifact-first")
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.BeginDeployment(ctx, project, "artifact-second")
	if err != nil {
		t.Fatal(err)
	}
	if first.Available || first.ArtifactState != "pending" || first.Version != 1 || second.Version != 2 {
		t.Fatalf("bad reservation: %+v %+v", first, second)
	}
	// A later reservation finishes first; final successful completion wins.
	if err := s.CommitDeployment(ctx, second.ID, 2, 20, "second-checksum"); err != nil {
		t.Fatal(err)
	}
	if err := s.CommitDeployment(ctx, first.ID, 1, 10, "first-checksum"); err != nil {
		t.Fatal(err)
	}
	active, err := s.GetActiveDeployment(ctx, project)
	if err != nil {
		t.Fatal(err)
	}
	if active.ID != first.ID || !active.Available || !active.Production || active.Checksum != "first-checksum" {
		t.Fatalf("bad production: %+v", active)
	}
	switched, changed, err := s.SwitchDeployment(ctx, project, second.Version)
	if err != nil || !changed || switched.ID != second.ID {
		t.Fatalf("switch: %+v %v %v", switched, changed, err)
	}
	if _, changed, err := s.SwitchDeployment(ctx, project, second.Version); err != nil || changed {
		t.Fatalf("repeat: %v %v", changed, err)
	}
	if err := s.FailDeployment(ctx, second.ID, "late failure"); !errors.Is(err, ErrDeploymentState) {
		t.Fatalf("active failed: %v", err)
	}
	if err := s.CommitDeployment(ctx, first.ID, 0, 0, "changed"); !errors.Is(err, ErrDeploymentState) {
		t.Fatalf("immutable record recommitted: %v", err)
	}
	all, err := s.ListAllDeployments(ctx)
	if err != nil || len(all) != 2 {
		t.Fatalf("list: %+v %v", all, err)
	}
}

func TestDeploymentTransactionFailureRollsBack(t *testing.T) {
	s := newTestStore(t)
	project := deploymentProject(t, s)
	ctx := t.Context()
	first, err := s.BeginDeployment(ctx, project, "first")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.CommitDeployment(ctx, first.ID, 1, 10, "first"); err != nil {
		t.Fatal(err)
	}
	next, err := s.BeginDeployment(ctx, project, "next")
	if err != nil {
		t.Fatal(err)
	}
	// The trigger fails the final write after previous production was archived.
	if _, err := s.db.Exec(`CREATE TRIGGER reject_commit BEFORE UPDATE OF artifact_state ON deployments WHEN NEW.artifact_state='available' BEGIN SELECT RAISE(ABORT,'injected metadata failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := s.CommitDeployment(ctx, next.ID, 2, 20, "next"); err == nil {
		t.Fatal("injected failure succeeded")
	}
	active, err := s.GetActiveDeployment(ctx, project)
	if err != nil || active.ID != first.ID {
		t.Fatalf("production changed: %+v %v", active, err)
	}
	pending, err := s.GetDeployment(ctx, project, next.Version)
	if err != nil || pending.Status != "uploading" || pending.Available || pending.Checksum != "" {
		t.Fatalf("partial commit: %+v %v", pending, err)
	}
	if err := s.FailDeployment(ctx, next.ID, "metadata commit failed"); err != nil {
		t.Fatal(err)
	}
	failed, err := s.GetDeployment(ctx, project, next.Version)
	if err != nil || failed.FailureReason != "metadata commit failed" || failed.Status != "failed" {
		t.Fatalf("failed: %+v %v", failed, err)
	}
	if _, _, err := s.SwitchDeployment(ctx, project, next.Version); !errors.Is(err, ErrDeploymentState) {
		t.Fatalf("failed switched: %v", err)
	}
}

func TestConcurrentDeploymentVersions(t *testing.T) {
	s := newTestStore(t)
	project := deploymentProject(t, s)
	const count = 24
	results := make(chan int, count)
	errs := make(chan error, count)
	var wg sync.WaitGroup
	for i := range count {
		wg.Go(func() {
			d, err := s.BeginDeployment(t.Context(), project, fmt.Sprintf("artifact-%d", i))
			if err != nil {
				errs <- err
				return
			}
			results <- d.Version
		})
	}
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	seen := map[int]bool{}
	for v := range results {
		if seen[v] {
			t.Fatalf("duplicate version %d", v)
		}
		seen[v] = true
	}
	if len(seen) != count {
		t.Fatalf("versions=%d", len(seen))
	}
	for i := 1; i <= count; i++ {
		if !seen[i] {
			t.Fatalf("missing reservation %d", i)
		}
	}
}

func TestDeploymentReferenceAndCollectionGuards(t *testing.T) {
	s := newTestStore(t)
	project := deploymentProject(t, s)
	ctx := t.Context()
	first, err := s.BeginDeployment(ctx, project, "first")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.MarkArtifactDeleting(ctx, first.ID); !errors.Is(err, ErrDeploymentState) {
		t.Fatalf("upload collected: %v", err)
	}
	if err := s.CommitDeployment(ctx, first.ID, 1, 1, "first"); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkArtifactDeleting(ctx, first.ID); !errors.Is(err, ErrDeploymentState) {
		t.Fatalf("production collected: %v", err)
	}
	next, err := s.BeginDeployment(ctx, project, "next")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.CommitDeployment(ctx, next.ID, 1, 1, "next"); err != nil {
		t.Fatal(err)
	}
	if err := s.PutDeploymentReference(ctx, project, "branch/main", first.ID); err != nil {
		t.Fatal(err)
	}
	if referenced, err := s.DeploymentReferenced(ctx, first.ID); err != nil || !referenced {
		t.Fatalf("reference=%v %v", referenced, err)
	}
	if err := s.MarkArtifactDeleting(ctx, first.ID); !errors.Is(err, ErrDeploymentState) {
		t.Fatalf("alias collected: %v", err)
	}
	sub, err := s.GetSubdomainByName("deploy")
	if err != nil {
		t.Fatal(err)
	}
	other, err := s.CreateProject(sub.ID, "other")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.PutDeploymentReference(ctx, other.ID, "main", first.ID); !errors.Is(err, ErrDeploymentState) {
		t.Fatalf("cross-project reference: %v", err)
	}
	if _, _, err := s.SwitchDeployment(ctx, other.ID, first.Version); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("cross-project switch: %v", err)
	}
	if err := s.PutDeploymentReference(ctx, project, "branch/main", next.ID); err != nil {
		t.Fatal(err)
	}
	if referenced, err := s.DeploymentReferenced(ctx, first.ID); err != nil || referenced {
		t.Fatalf("reference=%v %v", referenced, err)
	}
	if err := s.MarkArtifactDeleting(ctx, first.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.PutDeploymentReference(ctx, project, "retained", first.ID); !errors.Is(err, ErrDeploymentState) {
		t.Fatalf("reference deleting: %v", err)
	}
	if _, _, err := s.SwitchDeployment(ctx, project, first.Version); !errors.Is(err, ErrDeploymentState) {
		t.Fatalf("switch deleting: %v", err)
	}
	if err := s.SetArtifactState(ctx, first.ID, "deleted"); err != nil {
		t.Fatal(err)
	}
	deleted, err := s.GetDeployment(ctx, project, first.Version)
	if err != nil || deleted.Available || deleted.ArtifactState != "deleted" {
		t.Fatalf("deleted: %+v %v", deleted, err)
	}
	if err := s.SetArtifactState(ctx, next.ID, "deleted"); !errors.Is(err, ErrDeploymentState) {
		t.Fatalf("active deleted: %v", err)
	}
	if err := s.SetArtifactState(ctx, next.ID, "missing"); err != nil {
		t.Fatal(err)
	}
	missing, err := s.GetActiveDeployment(ctx, project)
	if err != nil || missing.Available || !missing.Production {
		t.Fatalf("missing: %+v %v", missing, err)
	}
}

func TestDeferredCommitFailureRetainsProduction(t *testing.T) {
	s := newTestStore(t)
	project := deploymentProject(t, s)
	ctx := t.Context()
	first, err := s.BeginDeployment(ctx, project, "first")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.CommitDeployment(ctx, first.ID, 1, 1, "first"); err != nil {
		t.Fatal(err)
	}
	next, err := s.BeginDeployment(ctx, project, "next")
	if err != nil {
		t.Fatal(err)
	}
	// Defer FK checks so all UPDATEs succeed and only COMMIT itself fails.
	if _, err := s.db.Exec(`CREATE TRIGGER fail_at_commit AFTER UPDATE OF artifact_state ON deployments WHEN NEW.id=2 AND NEW.artifact_state='available' BEGIN INSERT INTO deployment_references(project_id,name,deployment_id) VALUES(NEW.project_id,'invalid',999999); END; PRAGMA defer_foreign_keys=ON;`); err != nil {
		t.Fatal(err)
	}
	if err := s.CommitDeployment(ctx, next.ID, 1, 1, "next"); err == nil {
		t.Fatal("commit should fail")
	}
	active, err := s.GetActiveDeployment(ctx, project)
	if err != nil || active.ID != first.ID {
		t.Fatalf("failed COMMIT exposed production: %+v %v", active, err)
	}
	// The connection must not retain the failed open transaction.
	if _, err := s.BeginDeployment(ctx, project, "third"); err != nil {
		t.Fatalf("connection unusable after failed commit: %v", err)
	}
}

func openLegacyDeploymentFixture(t *testing.T, path string) (*SQLiteStore, int64) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	s := &SQLiteStore{db: db}
	if err := s.migrate(); err != nil {
		s.Close()
		t.Fatal(err)
	}
	return s, deploymentProject(t, s)
}

func TestLegacyDeploymentMigrationAndRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	old, project := openLegacyDeploymentFixture(t, path)
	for _, entry := range []struct {
		version int
		status  string
	}{{1, "archived"}, {2, "active"}} {
		if _, err := old.db.Exec(`INSERT INTO deployments(project_id,version,file_count,total_size,status) VALUES(?,?,3,100,?)`, project, entry.version, entry.status); err != nil {
			t.Fatal(err)
		}
	}
	if err := old.Close(); err != nil {
		t.Fatal(err)
	}
	s, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	active, err := s.GetActiveDeployment(ctx, project)
	if err != nil {
		t.Fatal(err)
	}
	if active.Version != 2 || active.ArtifactState != "legacy" || active.Available || !active.Production {
		t.Fatalf("legacy active: %+v", active)
	}
	history, err := s.GetDeployment(ctx, project, 1)
	if err != nil {
		t.Fatal(err)
	}
	if history.Available || history.ArtifactState != "legacy" || history.Status != "archived" {
		t.Fatalf("legacy history: %+v", history)
	}
	if _, _, err := s.SwitchDeployment(ctx, project, history.Version); !errors.Is(err, ErrDeploymentState) {
		t.Fatalf("legacy rollback: %v", err)
	}
	if err := s.AttachLegacyArtifact(ctx, history.ID, "fake-history", 3, 100, "checksum"); !errors.Is(err, ErrDeploymentState) {
		t.Fatalf("historical files invented: %v", err)
	}
	if err := s.AttachLegacyArtifact(ctx, active.ID, "migrated-current", 3, 100, "checksum"); err != nil {
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
	active, err = s.GetActiveDeployment(ctx, project)
	if err != nil || !active.Available || active.ArtifactID != "migrated-current" || active.Checksum != "checksum" {
		t.Fatalf("restart: %+v %v", active, err)
	}
	next, err := s.BeginDeployment(ctx, project, "new-artifact")
	if err != nil || next.Version != 3 {
		t.Fatalf("version after migration: %+v %v", next, err)
	}
	user, err := s.GetUserByToken("token")
	if err != nil || user.Email != "deploy@example.com" {
		t.Fatalf("user/token changed: %+v %v", user, err)
	}
}

func TestDeploymentMigrationRejectsAmbiguousHistory(t *testing.T) {
	for _, kind := range []string{"versions", "active"} {
		t.Run(kind, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "legacy.db")
			old, project := openLegacyDeploymentFixture(t, path)
			firstStatus, secondStatus := "archived", "archived"
			version := 1
			if kind == "active" {
				firstStatus = "active"
				secondStatus = "active"
				version = 2
			}
			if _, err := old.db.Exec(`INSERT INTO deployments(project_id,version,status) VALUES(?,1,?),(?,?,?)`, project, firstStatus, project, version, secondStatus); err != nil {
				t.Fatal(err)
			}
			if err := old.Close(); err != nil {
				t.Fatal(err)
			}
			s, err := NewSQLiteStore(path)
			if err == nil {
				s.Close()
				t.Fatal("ambiguous history migrated")
			}
			if !strings.Contains(err.Error(), "restore a backup") {
				t.Fatalf("unhelpful migration error: %v", err)
			}
			db, err := sql.Open("sqlite", path)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			inspect := &SQLiteStore{db: db}
			cols, err := inspect.tableColumns("deployments")
			if err != nil {
				t.Fatal(err)
			}
			if cols["artifact_state"] {
				t.Fatal("failed migration partially modified schema")
			}
		})
	}
}

func TestSwitchFailureRollsBackProduction(t *testing.T) {
	s := newTestStore(t)
	project := deploymentProject(t, s)
	ctx := t.Context()
	var versions []*model.Deployment
	for i := range 2 {
		d, err := s.BeginDeployment(ctx, project, fmt.Sprintf("version-%d", i))
		if err != nil {
			t.Fatal(err)
		}
		if err := s.CommitDeployment(ctx, d.ID, 1, 1, "hash"); err != nil {
			t.Fatal(err)
		}
		versions = append(versions, d)
	}
	if _, err := s.db.Exec(`CREATE TRIGGER reject_switch BEFORE UPDATE OF status ON deployments WHEN NEW.id=1 AND NEW.status='active' BEGIN SELECT RAISE(ABORT,'switch failed'); END`); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.SwitchDeployment(ctx, project, versions[0].Version); err == nil {
		t.Fatal("switch succeeded")
	}
	active, err := s.GetActiveDeployment(ctx, project)
	if err != nil || active.ID != versions[1].ID {
		t.Fatalf("failed switch changed production: %+v %v", active, err)
	}
}

func TestDeploymentReservationRejectsInvalidAndCanceledWrites(t *testing.T) {
	s := newTestStore(t)
	project := deploymentProject(t, s)
	ctx := t.Context()
	if _, err := s.BeginDeployment(ctx, project, ""); !errors.Is(err, ErrDeploymentState) {
		t.Fatalf("empty artifact: %v", err)
	}
	if _, err := s.BeginDeployment(ctx, 999999, "orphan"); err == nil {
		t.Fatal("missing project accepted")
	}
	first, err := s.BeginDeployment(ctx, project, "same-artifact")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.BeginDeployment(ctx, project, first.ArtifactID); err == nil {
		t.Fatal("artifact reused")
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := s.BeginDeployment(canceled, project, "canceled"); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled request: %v", err)
	}
	next, err := s.BeginDeployment(ctx, project, "next")
	if err != nil || next.Version != 2 {
		t.Fatalf("failed reservation consumed version: %+v %v", next, err)
	}
}
