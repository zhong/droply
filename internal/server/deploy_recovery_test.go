//go:build integration

package server_test

import (
	"bytes"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/zhong/droply/internal/artifacts"
	"github.com/zhong/droply/internal/model"
	"github.com/zhong/droply/internal/server"
	"github.com/zhong/droply/internal/store"
)

type recoveryFixture struct {
	srv                  *server.Server
	st                   *store.SQLiteStore
	dbPath, sites, token string
}

func newRecoveryFixture(t *testing.T) *recoveryFixture {
	t.Helper()
	root := t.TempDir()
	f := &recoveryFixture{dbPath: filepath.Join(root, "droply.db"), sites: filepath.Join(root, "sites")}
	var err error
	f.st, err = store.NewSQLiteStore(f.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.st.Close() })
	f.srv = server.New(f.st, f.sites, "droplydoc.com", []byte("recovery-test-signing-key"))
	f.token = registerAndGetToken(t, f.srv, "recovery@example.test", "password123")
	createSubdomain(t, f.srv, f.token, "recovery")
	return f
}

func (f *recoveryFixture) upload(t *testing.T, content string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	f.srv.ServeHTTP(w, buildDeployRequest(t, "/subdomains/recovery/projects/site/deploy", createTestTarGz(t, map[string]string{"index.html": content}), f.token))
	return w
}

func (f *recoveryFixture) history(t *testing.T) []model.Deployment {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, "/subdomains/recovery/projects/site/deployments", nil)
	r.Host = "api.droplydoc.com"
	r.Header.Set("Authorization", "Bearer "+f.token)
	w := httptest.NewRecorder()
	f.srv.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("history: %d %s", w.Code, w.Body.String())
	}
	var ds []model.Deployment
	if err := json.Unmarshal(w.Body.Bytes(), &ds); err != nil {
		t.Fatal(err)
	}
	return ds
}

func (f *recoveryFixture) assertContent(t *testing.T, expected string) {
	t.Helper()
	w := siteResponse(f.srv.NewSiteHandler(), "recovery.droplydoc.com", "/site/")
	if w.Code != 200 || w.Body.String() != expected {
		t.Fatalf("production: %d %q, want %q", w.Code, w.Body.String(), expected)
	}
}

func TestDeployPublicationDatabaseFailurePreservesProduction(t *testing.T) {
	for _, test := range []struct{ name, ddl string }{
		{"statement abort", `CREATE TRIGGER reject_publication BEFORE UPDATE OF status ON deployments WHEN NEW.status='active' AND NEW.version=2 BEGIN SELECT RAISE(ABORT,'publication unavailable'); END;`},
		{"deferred commit failure", `CREATE TABLE publication_parent(id INTEGER PRIMARY KEY); CREATE TABLE publication_child(parent_id INTEGER REFERENCES publication_parent(id) DEFERRABLE INITIALLY DEFERRED); CREATE TRIGGER reject_publication AFTER UPDATE OF status ON deployments WHEN NEW.status='active' AND NEW.version=2 BEGIN INSERT INTO publication_child VALUES(999); END;`},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newRecoveryFixture(t)
			if w := f.upload(t, "old"); w.Code != 200 {
				t.Fatal(w.Body.String())
			}
			db, err := sql.Open("sqlite", f.dbPath)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			if _, err = db.Exec(test.ddl); err != nil {
				t.Fatal(err)
			}
			if w := f.upload(t, "new must stay unpublished"); w.Code != 500 {
				t.Fatalf("failed publication: %d %s", w.Code, w.Body.String())
			}
			f.assertContent(t, "old")
			ds := f.history(t)
			if len(ds) != 2 || ds[0].Version != 2 || ds[0].Status != "failed" || ds[0].FailureReason == "" || ds[0].Production || ds[0].Available || !ds[1].Production || !ds[1].Available {
				t.Fatalf("history after rollback: %+v", ds)
			}
			if err := f.st.Close(); err != nil {
				t.Fatal(err)
			}
			f.st, err = store.NewSQLiteStore(f.dbPath)
			if err != nil {
				t.Fatal(err)
			}
			f.srv = server.New(f.st, f.sites, "droplydoc.com", []byte("recovery-test-signing-key"))
			if err = f.srv.PrepareDeployments(t.Context()); err != nil {
				t.Fatal(err)
			}
			f.assertContent(t, "old")
		})
	}
}

func TestDeploymentStartupImportsOnlyCurrentLegacyFiles(t *testing.T) {
	f := newRecoveryFixture(t)
	sub, err := f.st.GetSubdomainByName("recovery")
	if err != nil {
		t.Fatal(err)
	}
	p, err := f.st.CreateProject(sub.ID, "site")
	if err != nil {
		t.Fatal(err)
	}
	// Legacy history is metadata-only: only version 2 has files on disk.
	db, err := sql.Open("sqlite", f.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`INSERT INTO deployments(project_id,version,file_count,total_size,status) VALUES(?,1,1,3,'archived'),(?,2,1,3,'active')`, p.ID, p.ID); err != nil {
		t.Fatal(err)
	}
	legacy := filepath.Join(f.sites, "recovery", "site")
	if err = os.MkdirAll(legacy, 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(legacy, "index.html"), []byte("legacy current"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = f.srv.PrepareDeployments(t.Context()); err != nil {
		t.Fatal(err)
	}
	ds := f.history(t)
	if len(ds) != 2 || ds[0].Version != 2 || !ds[0].Available || !ds[0].Production || ds[0].Checksum == "" || ds[1].Available || ds[1].ArtifactID != "" || ds[1].Production {
		t.Fatalf("legacy history invented availability: %+v", ds)
	}
	// The retained migration backup must no longer be the serving source.
	if err = os.WriteFile(filepath.Join(legacy, "index.html"), []byte("changed backup"), 0600); err != nil {
		t.Fatal(err)
	}
	f.assertContent(t, "legacy current")
	f.srv = server.New(f.st, f.sites, "droplydoc.com", []byte("recovery-test-signing-key"))
	if err = f.srv.PrepareDeployments(t.Context()); err != nil {
		t.Fatal(err)
	}
	again := f.history(t)
	if len(again) != 2 || again[0].ArtifactID != ds[0].ArtifactID {
		t.Fatalf("migration was not idempotent: %+v", again)
	}
	f.assertContent(t, "legacy current")
}

func TestDeploymentStartupRecoversInterruptedPublication(t *testing.T) {
	for _, published := range []bool{false, true} {
		t.Run(fmt.Sprintf("published=%t", published), func(t *testing.T) {
			f := newRecoveryFixture(t)
			if w := f.upload(t, "old"); w.Code != 200 {
				t.Fatal(w.Body.String())
			}
			original := f.history(t)[0]
			id := rand.Text()
			pending, err := f.st.BeginDeployment(t.Context(), original.ProjectID, id)
			if err != nil {
				t.Fatal(err)
			}
			a, err := artifacts.New(filepath.Join(f.sites, ".artifacts"))
			if err != nil {
				t.Fatal(err)
			}
			if _, err = a.Stage(t.Context(), id, createTestTarGz(t, map[string]string{"index.html": "interrupted new"}), artifacts.Limits{}); err != nil {
				t.Fatal(err)
			}
			if published {
				if err = a.Publish(id); err != nil {
					t.Fatal(err)
				}
			}
			if err = f.st.Close(); err != nil {
				t.Fatal(err)
			}
			f.st, err = store.NewSQLiteStore(f.dbPath)
			if err != nil {
				t.Fatal(err)
			}
			f.srv = server.New(f.st, f.sites, "droplydoc.com", []byte("recovery-test-signing-key"))
			if err = f.srv.PrepareDeployments(t.Context()); err != nil {
				t.Fatal(err)
			}
			ds := f.history(t)
			if len(ds) != 2 || ds[0].ID != pending.ID || ds[0].Status != "failed" || ds[0].FailureReason == "" || ds[0].Available || ds[0].Production || ds[1].ID != original.ID || !ds[1].Production {
				t.Fatalf("interrupted history: %+v", ds)
			}
			f.assertContent(t, "old")
		})
	}
}

func TestConcurrentDeploymentsPublishUniqueCompleteVersions(t *testing.T) {
	f := newRecoveryFixture(t)
	const count = 6
	requests := make([]*http.Request, count)
	contents := make([]string, count)
	for i := range count {
		contents[i] = strings.Repeat(fmt.Sprintf("complete-%d-", i), 8192)
		requests[i] = buildDeployRequest(t, "/subdomains/recovery/projects/site/deploy", createTestTarGz(t, map[string]string{"index.html": contents[i]}), f.token)
	}
	var wg sync.WaitGroup
	start := make(chan struct{})
	results := make([]*httptest.ResponseRecorder, count)
	for i := range count {
		wg.Go(func() { <-start; results[i] = httptest.NewRecorder(); f.srv.ServeHTTP(results[i], requests[i]) })
	}
	close(start)
	wg.Wait()
	byVersion := map[int]string{}
	for i, w := range results {
		if w.Code != 200 {
			t.Fatalf("upload %d: %d %s", i, w.Code, w.Body.String())
		}
		var result struct {
			Version int `json:"version"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if _, exists := byVersion[result.Version]; exists {
			t.Fatalf("duplicate version %d", result.Version)
		}
		byVersion[result.Version] = contents[i]
	}
	ds := f.history(t)
	if len(ds) != count {
		t.Fatalf("history count=%d", len(ds))
	}
	active := 0
	ids := map[string]bool{}
	for _, d := range ds {
		if !d.Available || d.Checksum == "" || ids[d.ArtifactID] {
			t.Fatalf("invalid immutable artifact: %+v", d)
		}
		ids[d.ArtifactID] = true
		if d.Production {
			active++
			f.assertContent(t, byVersion[d.Version])
		}
	}
	if active != 1 {
		t.Fatalf("active deployments=%d", active)
	}
	// Every accepted version remains intact, not merely the final response body.
	a, err := artifacts.New(filepath.Join(f.sites, ".artifacts"))
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range ds {
		if _, err = a.Verify(t.Context(), d.ArtifactID); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(filepath.Join(a.Path(d.ArtifactID), "index.html"))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(data, []byte(byVersion[d.Version])) {
			t.Fatalf("version %d has mixed content", d.Version)
		}
	}
}
