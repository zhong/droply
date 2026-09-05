package store

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSchemaUpgradeSnapshotAndReentry(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "droply.db")
	legacy, project := openLegacyDeploymentFixture(t, path)
	if _, err := legacy.db.Exec(`INSERT INTO deployments(project_id,version,status) VALUES(?,1,'active')`, project); err != nil {
		t.Fatal(err)
	}
	// Leave WAL enabled and committed pages uncheckpointed during the snapshot.
	if _, err := legacy.db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		t.Fatal(err)
	}
	if _, err := legacy.db.Exec("UPDATE users SET email='wal@example.com'"); err != nil {
		t.Fatal(err)
	}
	if err := prepareSchemaUpgrade(legacy.db); err != nil {
		t.Fatal(err)
	}
	legacy.Close()
	snapshots, err := filepath.Glob(filepath.Join(dir, "upgrade-backups", "*.db"))
	if err != nil || len(snapshots) != 1 {
		t.Fatalf("snapshots %v: %v", snapshots, err)
	}
	snapshot, err := sql.Open("sqlite", snapshots[0])
	if err != nil {
		t.Fatal(err)
	}
	defer snapshot.Close()
	var email string
	if err := snapshot.QueryRow("SELECT email FROM users").Scan(&email); err != nil || email != "wal@example.com" {
		t.Fatalf("snapshot missing WAL: %s %v", email, err)
	}
	var version int
	if err := snapshot.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != 0 {
		t.Fatalf("backup version=%d: %v", version, err)
	}
	info, err := os.Stat(snapshots[0])
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("snapshot permissions: %v %v", info, err)
	}
	s, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != SchemaVersion {
		t.Fatalf("version=%d: %v", version, err)
	}
	user, err := s.GetUserByToken("token")
	if err != nil || user.Email != email {
		t.Fatalf("legacy user: %+v %v", user, err)
	}
	role, err := s.ProjectRole(t.Context(), project, user.ID)
	if err != nil || role != "owner" {
		t.Fatalf("role %s %v", role, err)
	}
	s.Close()
	before, _ := filepath.Glob(filepath.Join(dir, "upgrade-backups", "*.db"))
	s, err = NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	after, _ := filepath.Glob(filepath.Join(dir, "upgrade-backups", "*.db"))
	if len(before) != len(after) {
		t.Fatal("current schema created another upgrade snapshot")
	}
}

func TestSchemaRejectFutureBeforeMutation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "future.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("PRAGMA user_version=99; CREATE TABLE future_only(value TEXT); INSERT INTO future_only VALUES('keep')"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	s, err := NewSQLiteStore(path)
	if err == nil {
		s.Close()
		t.Fatal("future schema accepted")
	}
	if !strings.Contains(err.Error(), "unsupported database schema") {
		t.Fatal(err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("future database modified")
	}
}

func TestSchemaSnapshotFailurePreventsMigration(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "legacy.db")
	legacy, _ := openLegacyDeploymentFixture(t, path)
	legacy.Close()
	if err := os.WriteFile(filepath.Join(dir, "upgrade-backups"), []byte("blocked"), 0600); err != nil {
		t.Fatal(err)
	}
	s, err := NewSQLiteStore(path)
	if err == nil {
		s.Close()
		t.Fatal("upgrade without backup")
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	cols, err := (&SQLiteStore{db: db}).tableColumns("deployments")
	if err != nil {
		t.Fatal(err)
	}
	if cols["artifact_state"] {
		t.Fatal("schema mutated after backup failure")
	}
}

func TestSchemaUpgradeRetriesAfterInterruptedMigration(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "legacy.db")
	legacy, project := openLegacyDeploymentFixture(t, path)
	if _, err := legacy.db.Exec(`INSERT INTO deployments(project_id,version,status) VALUES(?,1,'active'),(?,1,'archived')`, project, project); err != nil {
		t.Fatal(err)
	}
	legacy.Close()
	if s, err := NewSQLiteStore(path); err == nil {
		s.Close()
		t.Fatal("ambiguous migration accepted")
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	var version int
	if err := db.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != 0 {
		t.Fatalf("failed migration marked complete: %d %v", version, err)
	}
	// Operator resolves the ambiguity, then the idempotent migrations can resume.
	if _, err := db.Exec("DELETE FROM deployments WHERE status='archived'"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	s, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != SchemaVersion {
		t.Fatalf("retry version %d %v", version, err)
	}
	snapshots, err := filepath.Glob(filepath.Join(dir, "upgrade-backups", "*.db"))
	if err != nil || len(snapshots) != 2 {
		t.Fatalf("original recovery points not retained: %v %v", snapshots, err)
	}
}

// Load immutable historical DDL rather than invoking current migration builders.
func openHistoricalSchema(t *testing.T, path, milestone string) *SQLiteStore {
	t.Helper()
	schema, err := os.ReadFile(filepath.Join("testdata", "schema", milestone+".sql"))
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(string(schema)); err != nil {
		t.Fatal(err)
	}
	return &SQLiteStore{db: db}
}

func TestHistoricalSchemaUpgradeAndBackupRestore(t *testing.T) {
	for _, milestone := range []string{"m0", "m1", "m2"} {
		t.Run(milestone, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "old.db")
			old := openHistoricalSchema(t, path, milestone)
			if _, err := old.db.Exec(`
INSERT INTO users(id,email,password,api_token) VALUES(1,'old@example.com','old-password-hash','old-token');
INSERT INTO subdomains(id,user_id,name) VALUES(1,1,'old');
INSERT INTO projects(id,subdomain_id,name) VALUES(1,1,'site');
INSERT INTO deployments(id,project_id,version,file_count,total_size,status) VALUES(1,1,1,1,7,'active');
INSERT INTO custom_domains(project_id,domain,verified,verification_token) VALUES(1,'old.example.com',1,'old-challenge');
INSERT INTO access_rules(subdomain_id,project_id,password_hash,wework_enabled,allowed_wework_users) VALUES(1,1,'access-password-hash',1,'["alice"]');`); err != nil {
				t.Fatal(err)
			}
			if milestone != "m0" {
				if _, err := old.db.Exec(`UPDATE deployments SET artifact_id='old-artifact',artifact_state='available',checksum='old-checksum'`); err != nil {
					t.Fatal(err)
				}
			}
			const rawToken = "dply_p_abcdefghijklmnopqrstuvwxyzabcdefghijklmnopqrstuvwxyz"
			if milestone == "m2" {
				if _, err := old.db.Exec(`UPDATE projects SET host_label='p-historical'; INSERT INTO project_tokens(project_id,digest,name,scopes,expires_at,created_at) VALUES(1,?,'legacy-ci','["production"]','9999-01-01T00:00:00Z','2026-01-01T00:00:00Z')`, tokenDigest(rawToken)); err != nil {
					t.Fatal(err)
				}
			}
			if err := old.Close(); err != nil {
				t.Fatal(err)
			}
			check := func(path string) string {
				t.Helper()
				s, err := NewSQLiteStore(path)
				if err != nil {
					t.Fatal(err)
				}
				defer s.Close()
				var version int
				if err := s.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != SchemaVersion {
					t.Fatalf("version %d: %v", version, err)
				}
				user, err := s.GetUserByToken("old-token")
				if err != nil || user.Email != "old@example.com" || user.Password != "old-password-hash" {
					t.Fatalf("old credential: %+v %v", user, err)
				}
				role, err := s.ProjectRole(t.Context(), 1, user.ID)
				if err != nil || role != "owner" {
					t.Fatalf("owner: %q %v", role, err)
				}
				deployment, err := s.GetActiveDeployment(t.Context(), 1)
				if err != nil {
					t.Fatal(err)
				}
				if deployment.Version != 1 || deployment.FileCount != 1 || deployment.TotalSize != 7 || deployment.Environment != "production" {
					t.Fatalf("metadata: %+v", deployment)
				}
				if milestone == "m0" {
					if deployment.Available || deployment.ArtifactState != "legacy" {
						t.Fatalf("invented artifact: %+v", deployment)
					}
				} else if !deployment.Available || deployment.ArtifactID != "old-artifact" || deployment.Checksum != "old-checksum" {
					t.Fatalf("lost artifact: %+v", deployment)
				}
				domain, err := s.GetCustomDomain("old.example.com")
				if err != nil || !domain.Verified || domain.VerificationToken != "old-challenge" {
					t.Fatalf("domain: %+v %v", domain, err)
				}
				rule, err := s.GetAccessRule(t.Context(), 1, new(int64(1)))
				if err != nil || rule.PasswordHash != "access-password-hash" || !rule.WeWorkEnabled || len(rule.AllowedWeWorkUsers) != 1 || rule.AllowedWeWorkUsers[0] != "alice" {
					t.Fatalf("rule: %+v %v", rule, err)
				}
				project, err := s.GetProject(1, "site")
				if err != nil || project.HostLabel == "" {
					t.Fatalf("host: %+v %v", project, err)
				}
				if milestone == "m2" {
					if project.HostLabel != "p-historical" {
						t.Fatalf("host changed: %s", project.HostLabel)
					}
					token, err := s.AuthenticateProjectToken(t.Context(), rawToken)
					if err != nil || token.IssuerID != user.ID || token.ProjectID != 1 || len(token.Scopes) != 1 || token.Scopes[0] != "production" {
						t.Fatalf("issuer backfill: %+v %v", token, err)
					}
				}
				return project.HostLabel
			}
			label := check(path)
			if again := check(path); again != label {
				t.Fatalf("restart changed host: %s -> %s", label, again)
			}
			snapshots, err := filepath.Glob(filepath.Join(filepath.Dir(path), "upgrade-backups", "*.db"))
			if err != nil || len(snapshots) != 1 {
				t.Fatalf("snapshots %v: %v", snapshots, err)
			}
			// Reopening the untouched pre-upgrade snapshot exercises restoration of
			// the old schema and credentials, not a second copy of the upgraded DB.
			check(snapshots[0])
		})
	}
}

func TestSchemaUpgradeRetriesAfterIssuerBackfillFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	old := openHistoricalSchema(t, path, "m2")
	const rawToken = "dply_p_abcdefghijklmnopqrstuvwxyzabcdefghijklmnopqrstuvwxyz"
	if _, err := old.db.Exec(`
INSERT INTO users(id,email,password,api_token) VALUES(1,'old@example.com','hash','token');
INSERT INTO subdomains(id,user_id,name) VALUES(1,1,'old');
INSERT INTO projects(id,subdomain_id,name) VALUES(1,1,'site');
INSERT INTO project_tokens(project_id,digest,name,scopes,expires_at,created_at) VALUES(1,?,'ci','["production"]','9999-01-01T00:00:00Z','2026-01-01T00:00:00Z');
CREATE TRIGGER stop_issuer_backfill BEFORE UPDATE ON project_tokens BEGIN SELECT RAISE(ABORT,'backfill interrupted'); END;`, tokenDigest(rawToken)); err != nil {
		t.Fatal(err)
	}
	if err := old.Close(); err != nil {
		t.Fatal(err)
	}
	s, err := NewSQLiteStore(path)
	if err == nil {
		s.Close()
		t.Fatal("backfill failure ignored")
	}
	if !strings.HasPrefix(err.Error(), "migrate members: ") || !strings.Contains(err.Error(), "backfill interrupted") {
		t.Fatalf("migration error context: %v", err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	var version int
	if err := db.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != 0 {
		t.Fatalf("failed upgrade marked complete: %d %v", version, err)
	}
	var issuer sql.NullInt64
	if err := db.QueryRow("SELECT issuer_id FROM project_tokens").Scan(&issuer); err != nil || issuer.Valid {
		t.Fatalf("interrupted issuer: %+v %v", issuer, err)
	}
	var admin bool
	if err := db.QueryRow("SELECT is_admin FROM users").Scan(&admin); err != nil {
		t.Fatalf("earlier identity migration did not run: %v", err)
	}
	if _, err := db.Exec("DROP TRIGGER stop_issuer_backfill"); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	token, err := s.AuthenticateProjectToken(t.Context(), rawToken)
	if err != nil || token.IssuerID != 1 {
		t.Fatalf("resumed issuer backfill: %+v %v", token, err)
	}
	if err := s.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != SchemaVersion {
		t.Fatalf("retry version: %d %v", version, err)
	}
}
