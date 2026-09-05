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
