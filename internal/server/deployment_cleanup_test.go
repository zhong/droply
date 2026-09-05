//go:build integration

package server_test

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zhong/droply/internal/artifacts"
	"github.com/zhong/droply/internal/server"
	"github.com/zhong/droply/internal/store"
)

type observedCleanup struct {
	DryRun           bool  `json:"dry_run"`
	UsedBytes        int64 `json:"used_bytes"`
	ReclaimableBytes int64 `json:"reclaimable_bytes"`
	Candidates       []struct {
		Version int `json:"version"`
	} `json:"candidates"`
	DeletedVersions []int    `json:"deleted_versions"`
	Errors          []string `json:"errors"`
}

func cleanupRequest(f *recoveryFixture, method, query string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "/subdomains/recovery/projects/site/cleanup"+query, nil)
	r.Header.Set("Authorization", "Bearer "+f.token)
	w := httptest.NewRecorder()
	f.srv.ServeHTTP(w, r)
	return w
}
func cleanupResult(t *testing.T, w *httptest.ResponseRecorder) observedCleanup {
	t.Helper()
	if w.Code != 200 {
		t.Fatalf("cleanup: %d %s", w.Code, w.Body.String())
	}
	var result observedCleanup
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	return result
}
func rollbackRequest(f *recoveryFixture, version int) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/subdomains/recovery/projects/site/rollback/%d", version), nil)
	r.Header.Set("Authorization", "Bearer "+f.token)
	w := httptest.NewRecorder()
	f.srv.ServeHTTP(w, r)
	return w
}
func uploadCleanupVersions(t *testing.T, f *recoveryFixture, count int) {
	t.Helper()
	for i := range count {
		if w := f.upload(t, fmt.Sprintf("version-%d", i+1)); w.Code != 200 {
			t.Fatal(w.Body.String())
		}
	}
}

func TestCleanupDryRunAndApplyProtectReferences(t *testing.T) {
	f := newRecoveryFixture(t)
	uploadCleanupVersions(t, f, 3)
	ds := f.history(t)
	if err := f.st.PutDeploymentReference(t.Context(), ds[1].ProjectID, "preview-pinned", ds[1].ID); err != nil {
		t.Fatal(err)
	}
	pending, err := f.st.BeginDeployment(t.Context(), ds[0].ProjectID, "live-upload")
	if err != nil {
		t.Fatal(err)
	}
	a, err := artifacts.New(filepath.Join(f.sites, ".artifacts"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.Stage(t.Context(), pending.ArtifactID, createTestTarGz(t, map[string]string{"index.html": "live"}), artifacts.Limits{}); err != nil {
		t.Fatal(err)
	}
	before, err := a.Usage()
	if err != nil {
		t.Fatal(err)
	}
	dry := cleanupResult(t, cleanupRequest(f, http.MethodGet, "?keep=0&days=0"))
	if !dry.DryRun || len(dry.Candidates) != 1 || dry.Candidates[0].Version != 1 || dry.ReclaimableBytes <= 0 || len(dry.DeletedVersions) != 0 {
		t.Fatalf("dry run: %+v", dry)
	}
	after, err := a.Usage()
	if err != nil || after != before {
		t.Fatalf("dry run changed disk usage: %d -> %d, %v", before, after, err)
	}
	for _, d := range ds {
		if _, err = a.Verify(t.Context(), d.ArtifactID); err != nil {
			t.Fatal(err)
		}
	}
	apply := cleanupResult(t, cleanupRequest(f, http.MethodPost, "?keep=0&days=0"))
	if apply.DryRun || !slices.Equal(apply.DeletedVersions, []int{1}) || len(apply.Errors) != 0 {
		t.Fatalf("apply: %+v", apply)
	}
	for _, d := range ds[:2] {
		if _, err = a.Verify(t.Context(), d.ArtifactID); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = os.Stat(filepath.Join(f.sites, ".artifacts", ".staging", pending.ArtifactID)); err != nil {
		t.Fatalf("live upload removed: %v", err)
	}
	if _, err = os.Stat(a.Path(ds[2].ArtifactID)); !os.IsNotExist(err) {
		t.Fatalf("cleaned artifact still exists: %v", err)
	}
	f.assertContent(t, "version-3")
	if w := rollbackRequest(f, 1); w.Code != 409 {
		t.Fatalf("rollback to deleted: %d %s", w.Code, w.Body.String())
	}
	for _, d := range f.history(t) {
		if d.Version == 1 && (d.Available || d.ArtifactState != "deleted") {
			t.Fatalf("deleted history: %+v", d)
		}
	}
	live, err := f.st.GetDeployment(t.Context(), pending.ProjectID, pending.Version)
	if err != nil || live.Status != "uploading" {
		t.Fatalf("pending changed: %+v %v", live, err)
	}
}

func TestCleanupCombinesCountAndAgeProtection(t *testing.T) {
	f := newRecoveryFixture(t)
	uploadCleanupVersions(t, f, 4)
	for _, d := range f.history(t) {
		if d.CreatedAt.IsZero() || time.Since(d.CreatedAt) > time.Minute {
			t.Fatalf("fresh deployment timestamp lost before retention: version %d created_at=%s", d.Version, d.CreatedAt)
		}
	}
	db, err := sql.Open("sqlite", f.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec(`UPDATE deployments SET created_at='2000-01-01 00:00:00' WHERE version IN (1,3,4)`); err != nil {
		t.Fatal(err)
	}
	result := cleanupResult(t, cleanupRequest(f, http.MethodPost, "?keep=2&days=7"))
	if !slices.Equal(result.DeletedVersions, []int{1}) {
		t.Fatalf("count OR age retention: %+v", result)
	}
	for _, d := range f.history(t) {
		if d.Version != 1 && !d.Available {
			t.Fatalf("protected version removed: %+v", d)
		}
	}
	f.assertContent(t, "version-4")
}

func TestCleanupRemovalFailureRetriesAfterRestart(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("permission failure requires an unprivileged process")
	}
	f := newRecoveryFixture(t)
	uploadCleanupVersions(t, f, 2)
	old := f.history(t)[1]
	dir := filepath.Join(f.sites, ".artifacts", old.ArtifactID, "files")
	if err := os.Chmod(dir, 0500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0700) })
	result := cleanupResult(t, cleanupRequest(f, http.MethodPost, "?keep=0&days=0"))
	if len(result.Errors) != 1 || len(result.DeletedVersions) != 0 {
		t.Fatalf("removal failure not reported: %+v", result)
	}
	history := f.history(t)
	if history[1].ArtifactState != "deleting" || history[1].Available {
		t.Fatalf("failed deletion not tombstoned: %+v", history[1])
	}
	if w := rollbackRequest(f, 1); w.Code != 409 {
		t.Fatalf("rollback to tombstone: %d %s", w.Code, w.Body.String())
	}
	f.assertContent(t, "version-2")
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := f.st.Close(); err != nil {
		t.Fatal(err)
	}
	var err error
	f.st, err = store.NewSQLiteStore(f.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	f.srv = server.New(f.st, f.sites, "droplydoc.com", []byte("recovery-test-signing-key"))
	if err = f.srv.PrepareDeployments(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err = f.srv.CleanupDeployments(t.Context()); err != nil {
		t.Fatal(err)
	}
	history = f.history(t)
	if history[1].ArtifactState != "deleted" || history[1].Available {
		t.Fatalf("cleanup did not converge: %+v", history[1])
	}
	if _, err = os.Stat(filepath.Dir(dir)); !os.IsNotExist(err) {
		t.Fatalf("retry retained artifact: %v", err)
	}
	f.assertContent(t, "version-2")
}

func TestCleanupAgedOrphanStagesKeepsLiveUploads(t *testing.T) {
	f := newRecoveryFixture(t)
	if err := f.srv.SetDeploymentOptions(server.DeploymentOptions{RetainCount: 2, OrphanGrace: time.Hour}); err != nil {
		t.Fatal(err)
	}
	uploadCleanupVersions(t, f, 1)
	d := f.history(t)[0]
	pending, err := f.st.BeginDeployment(t.Context(), d.ProjectID, "pending-live")
	if err != nil {
		t.Fatal(err)
	}
	a, err := artifacts.New(filepath.Join(f.sites, ".artifacts"))
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{pending.ArtifactID, "old-orphan", "new-orphan"} {
		if _, err = a.Stage(t.Context(), id, createTestTarGz(t, map[string]string{"index.html": id}), artifacts.Limits{}); err != nil {
			t.Fatal(err)
		}
	}
	old := time.Now().Add(-2 * time.Hour)
	for _, id := range []string{pending.ArtifactID, "old-orphan"} {
		if err = os.Chtimes(filepath.Join(f.sites, ".artifacts", ".staging", id), old, old); err != nil {
			t.Fatal(err)
		}
	}
	if err = f.srv.CleanupDeployments(t.Context()); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{pending.ArtifactID, "new-orphan"} {
		if _, err = os.Stat(filepath.Join(f.sites, ".artifacts", ".staging", id)); err != nil {
			t.Fatalf("protected stage %s lost: %v", id, err)
		}
	}
	if _, err = os.Stat(filepath.Join(f.sites, ".artifacts", ".staging", "old-orphan")); !os.IsNotExist(err) {
		t.Fatalf("aged orphan remains: %v", err)
	}
	f.assertContent(t, "version-1")
}

func TestDeploymentQuotaFailurePreservesProduction(t *testing.T) {
	f := newRecoveryFixture(t)
	if err := f.srv.SetDeploymentOptions(server.DeploymentOptions{MaxStorageBytes: 4096}); err != nil {
		t.Fatal(err)
	}
	if w := f.upload(t, "old"); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if w := f.upload(t, strings.Repeat("new", 4096)); w.Code < 400 {
		t.Fatalf("quota-exceeding upload succeeded: %d %s", w.Code, w.Body.String())
	}
	f.assertContent(t, "old")
	ds := f.history(t)
	if len(ds) != 2 || ds[0].Status != "failed" || ds[0].Available || !ds[1].Production {
		t.Fatalf("quota failure history: %+v", ds)
	}
}

func TestConcurrentDeploymentRollbackAndCleanupRemainConsistent(t *testing.T) {
	f := newRecoveryFixture(t)
	uploadCleanupVersions(t, f, 2)
	const rounds = 8
	for round := range rounds {
		content := fmt.Sprintf("new-%d", round)
		upload := buildDeployRequest(t, "/subdomains/recovery/projects/site/deploy", createTestTarGz(t, map[string]string{"index.html": content}), f.token)
		target := f.history(t)[0].Version
		var wg sync.WaitGroup
		start := make(chan struct{})
		var deploy, rollback, cleanup *httptest.ResponseRecorder
		var observed []*httptest.ResponseRecorder
		wg.Go(func() { <-start; deploy = httptest.NewRecorder(); f.srv.ServeHTTP(deploy, upload) })
		wg.Go(func() { <-start; rollback = rollbackRequest(f, target) })
		wg.Go(func() { <-start; cleanup = cleanupRequest(f, http.MethodPost, "?keep=0&days=0") })
		wg.Go(func() {
			<-start
			for range 30 {
				observed = append(observed, siteResponse(f.srv.NewSiteHandler(), "recovery.droplydoc.com", "/site/"))
			}
		})
		close(start)
		wg.Wait()
		for _, w := range observed {
			valid := w.Body.String() == "version-1" || w.Body.String() == "version-2"
			for i := 0; i <= round; i++ {
				valid = valid || w.Body.String() == fmt.Sprintf("new-%d", i)
			}
			if w.Code != 200 || !valid {
				t.Fatalf("concurrent reader saw incomplete response: %d %q", w.Code, w.Body.String())
			}
		}
		if deploy.Code != 200 || (rollback.Code != 200 && rollback.Code != 409) || cleanup.Code != 200 {
			t.Fatalf("concurrent statuses: deploy=%d rollback=%d cleanup=%d", deploy.Code, rollback.Code, cleanup.Code)
		}
		ds := f.history(t)
		active := 0
		a, err := artifacts.New(filepath.Join(f.sites, ".artifacts"))
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range ds {
			if d.Production {
				active++
				if !d.Available {
					t.Fatalf("active unavailable: %+v", d)
				}
				if _, err = a.Verify(t.Context(), d.ArtifactID); err != nil {
					t.Fatal(err)
				}
				data, err := os.ReadFile(filepath.Join(a.Path(d.ArtifactID), "index.html"))
				if err != nil {
					t.Fatal(err)
				}
				f.assertContent(t, string(data))
			}
		}
		if active != 1 {
			t.Fatalf("active count=%d", active)
		}
	}
}

func TestCleanupRejectsUnownedAndAnonymousRequests(t *testing.T) {
	f := newRecoveryFixture(t)
	uploadCleanupVersions(t, f, 2)
	other := registerAndGetToken(t, f.srv, "outsider@example.test", "password123")
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		for _, test := range []struct {
			token  string
			status int
		}{{"", 401}, {other, 403}} {
			r := httptest.NewRequest(method, "/subdomains/recovery/projects/site/cleanup?keep=0&days=0", nil)
			if test.token != "" {
				r.Header.Set("Authorization", "Bearer "+test.token)
			}
			w := httptest.NewRecorder()
			f.srv.ServeHTTP(w, r)
			if w.Code != test.status {
				t.Fatalf("%s cleanup returned %d, expected %d", method, w.Code, test.status)
			}
		}
	}
	for _, d := range f.history(t) {
		if !d.Available {
			t.Fatalf("unauthorized request removed artifact: %+v", d)
		}
	}
	f.assertContent(t, "version-2")
}
