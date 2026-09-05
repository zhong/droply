package server

import (
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zhong/droply/internal/store"
)

type publicationRoleBarrier struct {
	store.Store
	calls atomic.Int32
	at    int32
	ready chan struct{}
}

func (s *publicationRoleBarrier) ProjectRole(ctx context.Context, projectID, userID int64) (string, error) {
	role, err := s.Store.ProjectRole(ctx, projectID, userID)
	if s.calls.Add(1) == s.at {
		close(s.ready)
	}
	return role, err
}

func TestPublicationRechecksPermissionAfterWaitingForLock(t *testing.T) {
	for _, tc := range []struct {
		action, change string
		token          bool
	}{{"rollback/1", "remove", false}, {"promote/2", "downgrade", false}, {"promote/2", "revoke", true}, {"promote/2", "downgrade", true}, {"deploy", "revoke", true}} {
		t.Run(tc.action+"-"+tc.change, func(t *testing.T) {
			st, err := store.NewSQLiteStore(":memory:")
			if err != nil {
				t.Fatal(err)
			}
			defer st.Close()
			owner, err := st.CreateUser("owner@test", "hash", "owner")
			if err != nil {
				t.Fatal(err)
			}
			member, err := st.CreateUser("member@test", "hash", "member")
			if err != nil {
				t.Fatal(err)
			}
			sub, err := st.CreateSubdomain(owner.ID, "team")
			if err != nil {
				t.Fatal(err)
			}
			project, err := st.CreateProject(sub.ID, "site")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := st.PutProjectMember(t.Context(), project.ID, member.Email, "deployer"); err != nil {
				t.Fatal(err)
			}
			barrier := &publicationRoleBarrier{Store: st, at: 1, ready: make(chan struct{})}
			srv := New(barrier, t.TempDir(), "example.test", []byte("publication-test-key"))
			if err := srv.PrepareDeployments(t.Context()); err != nil {
				t.Fatal(err)
			}
			for _, env := range []string{"production", "preview"} {
				files := t.TempDir()
				if err := os.WriteFile(filepath.Join(files, "index.html"), []byte(env), 0600); err != nil {
					t.Fatal(err)
				}
				d, err := st.BeginDeploymentTarget(t.Context(), project.ID, env, env, "", "")
				if err != nil {
					t.Fatal(err)
				}
				info, err := srv.artifacts.Import(t.Context(), env, files, srv.artifactLimits())
				if err != nil {
					t.Fatal(err)
				}
				if err := srv.artifacts.Publish(env); err != nil {
					t.Fatal(err)
				}
				if err := st.CommitDeployment(t.Context(), d.ID, info.FileCount, info.TotalSize, info.Checksum); err != nil {
					t.Fatal(err)
				}
			}
			raw := "member"
			var credentialID int64
			if tc.token {
				credential, value, err := st.CreateProjectToken(t.Context(), project.ID, member.ID, "ci", []string{"production"}, time.Time{})
				if err != nil {
					t.Fatal(err)
				}
				credentialID = credential.ID
				raw = value
				barrier.at = 2
			}
			srv.deploymentMu.Lock()
			locked := true
			defer func() {
				if locked {
					srv.deploymentMu.Unlock()
				}
			}()
			result := make(chan *httptest.ResponseRecorder, 1)
			go func() {
				r := httptest.NewRequest("POST", "/subdomains/team/projects/site/"+tc.action, nil)
				r.Host = "api.example.test"
				r.Header.Set("Authorization", "Bearer "+raw)
				w := httptest.NewRecorder()
				srv.ServeHTTP(w, r)
				result <- w
			}()
			select {
			case <-barrier.ready:
			case <-time.After(5 * time.Second):
				t.Fatal("authorization did not reach barrier")
			}
			// The actor was authorized, but publication must wait behind this ownership mutation.
			switch tc.change {
			case "remove":
				err = st.RemoveProjectMember(t.Context(), project.ID, member.ID)
			case "downgrade":
				_, err = st.PutProjectMember(t.Context(), project.ID, member.Email, "viewer")
			case "revoke":
				err = st.RevokeProjectToken(t.Context(), project.ID, owner.ID, credentialID)
			}
			if err != nil {
				t.Fatal(err)
			}
			srv.deploymentMu.Unlock()
			locked = false
			select {
			case w := <-result:
				if w.Code != 403 {
					t.Fatalf("stale permission published: %d %s", w.Code, w.Body.String())
				}
			case <-time.After(5 * time.Second):
				t.Fatal("publication did not finish")
			}
			active, err := st.GetActiveDeployment(t.Context(), project.ID)
			if err != nil || active.Version != 1 {
				t.Fatalf("production changed: %+v %v", active, err)
			}
			events, err := st.ListPublicationEvents(t.Context(), project.ID)
			if err != nil || len(events) != 0 {
				t.Fatal("revoked promotion created an event")
			}
		})
	}
}
