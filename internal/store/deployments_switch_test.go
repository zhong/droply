package store

import (
	"sync"
	"sync/atomic"
	"testing"
)

func TestProductionSwitchTimestampFailureRollsBack(t *testing.T) {
	for _, action := range []string{"promote", "rollback"} {
		t.Run(action, func(t *testing.T) {
			s := newTestStore(t)
			project := deploymentProject(t, s)
			actor, err := s.GetUserByToken("token")
			if err != nil {
				t.Fatal(err)
			}
			production, err := s.BeginDeployment(t.Context(), project, "production")
			if err != nil {
				t.Fatal(err)
			}
			if err := s.CommitDeployment(t.Context(), production.ID, 1, 1, "production"); err != nil {
				t.Fatal(err)
			}
			preview, err := s.BeginDeploymentTarget(t.Context(), project, "preview", "preview", "feature", "commit")
			if err != nil {
				t.Fatal(err)
			}
			if err := s.CommitDeployment(t.Context(), preview.ID, 1, 1, "preview"); err != nil {
				t.Fatal(err)
			}
			activeID, targetVersion := production.ID, preview.Version
			eventCount := 0
			if action == "rollback" {
				if _, _, err := s.PromoteDeployment(t.Context(), project, preview.Version, actor.ID); err != nil {
					t.Fatal(err)
				}
				activeID, targetVersion, eventCount = preview.ID, production.Version, 1
			}
			if _, err := s.db.Exec(`UPDATE projects SET updated_at='2000-01-01 00:00:00' WHERE id=?`, project); err != nil {
				t.Fatal(err)
			}
			if _, err := s.db.Exec(`CREATE TRIGGER reject_publication_time BEFORE UPDATE OF updated_at ON projects BEGIN SELECT RAISE(ABORT,'timestamp failure'); END`); err != nil {
				t.Fatal(err)
			}
			if action == "promote" {
				_, _, err = s.PromoteDeployment(t.Context(), project, targetVersion, actor.ID)
			} else {
				_, _, err = s.SwitchDeployment(t.Context(), project, targetVersion)
			}
			if err == nil {
				t.Fatal("injected timestamp failure was ignored")
			}
			active, err := s.GetActiveDeployment(t.Context(), project)
			if err != nil || active.ID != activeID {
				t.Fatalf("production changed: %+v %v", active, err)
			}
			events, err := s.ListPublicationEvents(t.Context(), project)
			if err != nil || len(events) != eventCount {
				t.Fatalf("events changed: %+v %v", events, err)
			}
			var timestamp string
			if err := s.db.QueryRow(`SELECT CAST(updated_at AS TEXT) FROM projects WHERE id=?`, project).Scan(&timestamp); err != nil {
				t.Fatal(err)
			}
			if timestamp != "2000-01-01 00:00:00" {
				t.Fatalf("timestamp changed: %s", timestamp)
			}
		})
	}
}

func TestConcurrentPromotionIsIdempotent(t *testing.T) {
	s := newTestStore(t)
	project := deploymentProject(t, s)
	actor, err := s.GetUserByToken("token")
	if err != nil {
		t.Fatal(err)
	}
	preview, err := s.BeginDeploymentTarget(t.Context(), project, "preview", "preview", "feature", "commit")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.CommitDeployment(t.Context(), preview.ID, 1, 1, "preview"); err != nil {
		t.Fatal(err)
	}
	var changes atomic.Int32
	var workers sync.WaitGroup
	for range 8 {
		workers.Go(func() {
			d, changed, err := s.PromoteDeployment(t.Context(), project, preview.Version, actor.ID)
			if err != nil {
				t.Error(err)
				return
			}
			if d.ID != preview.ID || !d.Production {
				t.Errorf("unexpected production: %+v", d)
			}
			if changed {
				changes.Add(1)
			}
		})
	}
	workers.Wait()
	if changes.Load() != 1 {
		t.Fatalf("changed %d times", changes.Load())
	}
	events, err := s.ListPublicationEvents(t.Context(), project)
	if err != nil || len(events) != 1 {
		t.Fatalf("events: %+v %v", events, err)
	}
}
