//go:build integration

package server_test

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/zhong/droply/internal/server"
	"github.com/zhong/droply/internal/store"
)

func TestHistoricalSchemaServesLegacyFilesAfterUpgradeAndRestore(t *testing.T) {
	for _, milestone := range []string{"m0", "m1", "m2"} {
		t.Run(milestone, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "droply.db")
			schema, err := os.ReadFile(filepath.Join("..", "store", "testdata", "schema", milestone+".sql"))
			if err != nil {
				t.Fatal(err)
			}
			db, err := sql.Open("sqlite", path)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { db.Close() })
			if _, err := db.Exec(string(schema)); err != nil {
				t.Fatal(err)
			}
			// These releases can all contain pre-artifact history. Only the current
			// deployment has files; archived metadata must never become available.
			if _, err := db.Exec(`
INSERT INTO users(id,email,password,api_token) VALUES(1,'recovery@example.test','old-password-hash','old-api-token');
INSERT INTO subdomains(id,user_id,name) VALUES(1,1,'recovery');
INSERT INTO projects(id,subdomain_id,name) VALUES(1,1,'site');
INSERT INTO deployments(project_id,version,file_count,total_size,status) VALUES(1,1,1,14,'archived'),(1,2,1,14,'active');`); err != nil {
				t.Fatal(err)
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}

			check := func(dbPath, sites string) {
				t.Helper()
				legacy := filepath.Join(sites, "recovery", "site")
				if err := os.MkdirAll(legacy, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(legacy, "index.html"), []byte("legacy current"), 0600); err != nil {
					t.Fatal(err)
				}
				st, err := store.NewSQLiteStore(dbPath)
				if err != nil {
					t.Fatal(err)
				}
				f := &recoveryFixture{st: st, dbPath: dbPath, sites: sites, token: "old-api-token"}
				t.Cleanup(func() { f.st.Close() })
				f.srv = server.New(st, sites, "droplydoc.com", []byte("recovery-test-signing-key"))
				if err := f.srv.PrepareDeployments(t.Context()); err != nil {
					t.Fatal(err)
				}
				// HTTP history authenticates with the pre-upgrade API token.
				history := f.history(t)
				if len(history) != 2 || !history[0].Production || !history[0].Available || history[0].Checksum == "" || history[1].Available || history[1].ArtifactID != "" {
					t.Fatalf("legacy history: %+v", history)
				}
				f.assertContent(t, "legacy current")
				if err := os.WriteFile(filepath.Join(legacy, "index.html"), []byte("changed backup"), 0600); err != nil {
					t.Fatal(err)
				}
				if err := st.Close(); err != nil {
					t.Fatal(err)
				}
				f.st, err = store.NewSQLiteStore(dbPath)
				if err != nil {
					t.Fatal(err)
				}
				f.srv = server.New(f.st, sites, "droplydoc.com", []byte("recovery-test-signing-key"))
				if err := f.srv.PrepareDeployments(t.Context()); err != nil {
					t.Fatal(err)
				}
				again := f.history(t)
				if len(again) != 2 || again[0].ArtifactID != history[0].ArtifactID {
					t.Fatalf("restart changed artifact: %+v", again)
				}
				f.assertContent(t, "legacy current")
				if err := f.st.Close(); err != nil {
					t.Fatal(err)
				}
			}
			check(path, filepath.Join(root, "sites"))
			snapshots, err := filepath.Glob(filepath.Join(root, "upgrade-backups", "*.db"))
			if err != nil || len(snapshots) != 1 {
				t.Fatalf("snapshots %v: %v", snapshots, err)
			}
			restoredRoot := t.TempDir()
			restoredDB := filepath.Join(restoredRoot, "droply.db")
			contents, err := os.ReadFile(snapshots[0])
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(restoredDB, contents, 0600); err != nil {
				t.Fatal(err)
			}
			check(restoredDB, filepath.Join(restoredRoot, "sites"))
		})
	}
}
