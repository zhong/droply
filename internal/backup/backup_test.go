package backup

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/zhong/droply/internal/artifacts"
	"github.com/zhong/droply/internal/hosting"
	"github.com/zhong/droply/internal/store"
)

func fixture(t testing.TB) Config {
	t.Helper()
	parent := t.TempDir()
	root := filepath.Join(parent, "source")
	must(t, os.Mkdir(root, 0700))
	st, err := store.NewSQLiteStore(filepath.Join(root, "droply.db"))
	must(t, err)
	u, err := st.CreateUser("backup@example.test", "hash", "dp_backup")
	must(t, err)
	sub, err := st.CreateSubdomain(u.ID, "backup")
	must(t, err)
	p, err := st.CreateProject(sub.ID, "site")
	must(t, err)
	a, err := artifacts.New(filepath.Join(root, "sites", ".artifacts"))
	must(t, err)
	for i, body := range []string{"old version", "new version"} {
		id := []string{"first", "second"}[i]
		info, err := a.Stage(t.Context(), id, bytes.NewReader(siteArchive(t, body)), artifacts.Limits{})
		must(t, err)
		must(t, a.Publish(id))
		d, err := st.BeginDeployment(t.Context(), p.ID, id)
		must(t, err)
		must(t, st.CommitDeployment(t.Context(), d.ID, info.FileCount, info.TotalSize, info.Checksum))
	}
	must(t, st.Close())
	must(t, os.WriteFile(filepath.Join(root, "hmac.key"), bytes.Repeat([]byte{7}, 32), 0600))
	config := filepath.Join(parent, "server.env")
	must(t, os.WriteFile(config, []byte("DROPly flags and sensitive configuration"), 0600))
	return Config{DataDir: root, Output: filepath.Join(parent, "backup.tar.gz"), Configs: []string{config}}
}
func must(t testing.TB, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
func siteArchive(t testing.TB, body string) []byte {
	t.Helper()
	var b bytes.Buffer
	gz := gzip.NewWriter(&b)
	tw := tar.NewWriter(gz)
	must(t, tw.WriteHeader(&tar.Header{Name: "index.html", Mode: 0600, Size: int64(len(body))}))
	_, err := tw.Write([]byte(body))
	must(t, err)
	must(t, tw.Close())
	must(t, gz.Close())
	return b.Bytes()
}
func TestRoundTripAndSecondGeneration(t *testing.T) {
	cfg := fixture(t)
	must(t, Create(t.Context(), cfg))
	target := filepath.Join(filepath.Dir(cfg.DataDir), "restored")
	must(t, Restore(t.Context(), RestoreConfig{Input: cfg.Output, DataDir: target}))
	got, err := os.ReadFile(filepath.Join(target, "sites", ".artifacts", "second", "files", "index.html"))
	must(t, err)
	if string(got) != "new version" {
		t.Fatal(string(got))
	}
	db, err := store.NewSQLiteStore(filepath.Join(target, "droply.db"))
	must(t, err)
	_, err = db.CreateUser("after@example.test", "hash", "after")
	must(t, err)
	must(t, db.Close())
	cfg.DataDir = target
	cfg.Output = filepath.Join(filepath.Dir(target), "second.tar.gz")
	must(t, Create(t.Context(), cfg))
	must(t, Restore(t.Context(), RestoreConfig{Input: cfg.Output, DataDir: target + "2"}))
}
func TestBackupRejectsRunningServerAndMissingArtifacts(t *testing.T) {
	cfg := fixture(t)
	lock, err := hosting.LockDataDirectory(cfg.DataDir)
	must(t, err)
	if err = Create(t.Context(), cfg); err == nil {
		t.Fatal("accepted running server")
	}
	must(t, lock.Close())
	must(t, os.Remove(filepath.Join(cfg.DataDir, "sites", ".artifacts", "second", "files", "index.html")))
	if err = Create(t.Context(), cfg); err == nil {
		t.Fatal("accepted missing artifact")
	}
	if _, err = os.Stat(cfg.Output); !os.IsNotExist(err) {
		t.Fatal("published failed backup")
	}
}
func TestSnapshotIncludesCommittedWAL(t *testing.T) {
	cfg := fixture(t)
	db, err := sql.Open("sqlite", filepath.Join(cfg.DataDir, "droply.db"))
	must(t, err)
	defer db.Close()
	_, err = db.Exec("PRAGMA journal_mode=WAL; PRAGMA wal_autocheckpoint=0; CREATE TABLE wal_test(value TEXT); INSERT INTO wal_test VALUES('committed')")
	must(t, err)
	must(t, Create(t.Context(), cfg))
	target := cfg.DataDir + "-restored"
	must(t, Restore(t.Context(), RestoreConfig{Input: cfg.Output, DataDir: target}))
	restored, err := openDB(filepath.Join(target, "droply.db"))
	must(t, err)
	defer restored.Close()
	var value string
	must(t, restored.QueryRow("SELECT value FROM wal_test").Scan(&value))
	if value != "committed" {
		t.Fatal(value)
	}
}
func TestRestoreRejectsIntegrityVersionLinksAndExisting(t *testing.T) {
	for _, kind := range []string{"checksum", "version", "dbversion", "versionmismatch", "symlink", "hardlink", "traversal", "truncated", "gzip checksum", "tar footer", "existing"} {
		t.Run(kind, func(t *testing.T) {
			cfg := fixture(t)
			must(t, Create(t.Context(), cfg))
			data, err := os.ReadFile(cfg.Output)
			must(t, err)
			if kind == "truncated" {
				data = data[:len(data)-5]
			} else if kind == "gzip checksum" {
				data[len(data)-8] ^= 1
			} else if kind == "tar footer" {
				gz, err := gzip.NewReader(bytes.NewReader(data))
				must(t, err)
				raw, err := io.ReadAll(gz)
				must(t, err)
				must(t, gz.Close())
				var broken bytes.Buffer
				zw := gzip.NewWriter(&broken)
				_, err = zw.Write(raw[:len(raw)-1024])
				must(t, err)
				must(t, zw.Close())
				data = broken.Bytes()
			} else if kind != "existing" {
				data = rewrite(t, data, kind)
			}
			bad := cfg.Output + "bad"
			must(t, os.WriteFile(bad, data, 0600))
			target := cfg.DataDir + "-restored"
			if kind == "existing" {
				must(t, os.Mkdir(target, 0700))
				must(t, os.WriteFile(filepath.Join(target, "keep"), []byte("safe"), 0600))
			}
			if err = Restore(t.Context(), RestoreConfig{Input: bad, DataDir: target}); err == nil {
				t.Fatal("accepted invalid backup")
			}
			if kind == "existing" {
				got, err := os.ReadFile(filepath.Join(target, "keep"))
				must(t, err)
				if string(got) != "safe" {
					t.Fatal("overwritten")
				}
			} else if _, err = os.Stat(target); !os.IsNotExist(err) {
				t.Fatal("published invalid restore")
			}
		})
	}
}
func rewrite(t *testing.T, data []byte, kind string) []byte {
	t.Helper()
	gz, err := gzip.NewReader(bytes.NewReader(data))
	must(t, err)
	tr := tar.NewReader(gz)
	var out bytes.Buffer
	zw := gzip.NewWriter(&out)
	tw := tar.NewWriter(zw)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		must(t, err)
		body, err := io.ReadAll(tr)
		must(t, err)
		if h.Name == "backup.json" && (kind == "version" || kind == "dbversion" || kind == "versionmismatch") {
			var m Manifest
			must(t, json.Unmarshal(body, &m))
			if kind == "version" {
				m.Version = 99
			} else {
				m.SourceUserVersion = 99
				if kind == "versionmismatch" {
					m.SourceUserVersion = 1
				}
			}
			body, err = json.Marshal(m)
			must(t, err)
			h.Size = int64(len(body))
		}
		if h.Name == "data/hmac.key" && kind == "checksum" {
			body[0] ^= 1
		}
		must(t, tw.WriteHeader(h))
		_, err = tw.Write(body)
		must(t, err)
	}
	if kind == "symlink" {
		must(t, tw.WriteHeader(&tar.Header{Name: "data/link", Typeflag: tar.TypeSymlink, Linkname: "/etc/passwd"}))
	}
	if kind == "hardlink" {
		must(t, tw.WriteHeader(&tar.Header{Name: "data/hard", Typeflag: tar.TypeLink, Linkname: "data/hmac.key"}))
	}
	if kind == "traversal" {
		must(t, tw.WriteHeader(&tar.Header{Name: "../escape", Size: 0, Mode: 0600}))
	}
	must(t, tw.Close())
	must(t, zw.Close())
	return out.Bytes()
}
func TestCancellationAndSourceLinks(t *testing.T) {
	cfg := fixture(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if Create(ctx, cfg) == nil {
		t.Fatal("ignored cancellation")
	}
	must(t, os.Symlink(cfg.Configs[0], filepath.Join(cfg.DataDir, "link")))
	if Create(t.Context(), cfg) == nil {
		t.Fatal("copied symlink")
	}
}

func TestOverrideAndExternalSecrets(t *testing.T) {
	cfg := fixture(t)
	must(t, os.Remove(filepath.Join(cfg.DataDir, "hmac.key")))
	cfg.HMACFile = filepath.Join(filepath.Dir(cfg.DataDir), "override")
	must(t, os.WriteFile(cfg.HMACFile, []byte("exact-secret-no-newline"), 0600))
	must(t, Create(t.Context(), cfg))
	target := cfg.DataDir + "-restored"
	must(t, Restore(t.Context(), RestoreConfig{Input: cfg.Output, DataDir: target}))
	value, err := os.ReadFile(filepath.Join(target, "hmac.override"))
	must(t, err)
	if string(value) != "exact-secret-no-newline" {
		t.Fatal("override changed")
	}
	cfg.DataDir = target
	cfg.Output += "2"
	must(t, Create(t.Context(), cfg))
	info, err := os.Stat(cfg.Output)
	must(t, err)
	if info.Mode().Perm() != 0600 {
		t.Fatalf("backup mode %v", info.Mode())
	}
}
func TestSourceDatabaseVersionRejectedWithoutMigration(t *testing.T) {
	cfg := fixture(t)
	db, err := sql.Open("sqlite", filepath.Join(cfg.DataDir, "droply.db"))
	must(t, err)
	defer db.Close()
	_, err = db.Exec("PRAGMA user_version=99")
	must(t, err)
	if Create(t.Context(), cfg) == nil {
		t.Fatal("accepted future source")
	}
	var version int
	must(t, db.QueryRow("PRAGMA user_version").Scan(&version))
	if version != 99 {
		t.Fatal("modified source version")
	}
}

func TestRestoreCancellationAndLimits(t *testing.T) {
	cfg := fixture(t)
	must(t, Create(t.Context(), cfg))
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	for _, test := range []struct {
		name   string
		ctx    context.Context
		limits RestoreConfig
	}{
		{"canceled", canceled, RestoreConfig{}},
		{"bytes", t.Context(), RestoreConfig{MaxBytes: 32}},
		{"entries", t.Context(), RestoreConfig{MaxFiles: 1}},
	} {
		t.Run(test.name, func(t *testing.T) {
			config := test.limits
			config.Input = cfg.Output
			config.DataDir = cfg.DataDir + "-" + test.name
			if Restore(test.ctx, config) == nil {
				t.Fatal("accepted interrupted/limited restore")
			}
			if _, err := os.Stat(config.DataDir); !os.IsNotExist(err) {
				t.Fatal("published unsuccessful restore")
			}
		})
	}
}
func TestUnversionedSourcePreserved(t *testing.T) {
	cfg := fixture(t)
	db, err := sql.Open("sqlite", filepath.Join(cfg.DataDir, "droply.db"))
	must(t, err)
	_, err = db.Exec("PRAGMA user_version=0")
	must(t, err)
	must(t, db.Close())
	must(t, Create(t.Context(), cfg))
	source, err := openDB(filepath.Join(cfg.DataDir, "droply.db"))
	must(t, err)
	var version int
	must(t, source.QueryRow("PRAGMA user_version").Scan(&version))
	must(t, source.Close())
	if version != 0 {
		t.Fatal("backup migrated source")
	}
	target := cfg.DataDir + "-restored"
	must(t, Restore(t.Context(), RestoreConfig{Input: cfg.Output, DataDir: target}))
	restored, err := openDB(filepath.Join(target, "droply.db"))
	must(t, err)
	defer restored.Close()
	must(t, restored.QueryRow("PRAGMA user_version").Scan(&version))
	if version != 0 {
		t.Fatal("restore migrated database")
	}
}

func TestRestorePrivatePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix permissions")
	}
	cfg := fixture(t)
	must(t, Create(t.Context(), cfg))
	target := filepath.Join(filepath.Dir(cfg.DataDir), "restored")
	must(t, Restore(t.Context(), RestoreConfig{Input: cfg.Output, DataDir: target}))
	must(t, filepath.WalkDir(target, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		want := fs.FileMode(0600)
		if info.IsDir() {
			want = 0700
		}
		if info.Mode().Perm() != want {
			t.Errorf("%s mode = %o, want %o", path, info.Mode().Perm(), want)
		}
		return nil
	}))
}

func TestPrepareRestoredTreeRejectsLinkAndCancellation(t *testing.T) {
	t.Run("link", func(t *testing.T) {
		root := t.TempDir()
		outside := filepath.Join(t.TempDir(), "outside")
		must(t, os.WriteFile(outside, []byte("untouched"), 0444))
		if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
			t.Skip(err)
		}
		if err := prepareRestoredTree(t.Context(), root); err == nil {
			t.Fatal("accepted link")
		}
		info, err := os.Stat(outside)
		must(t, err)
		if runtime.GOOS != "windows" && info.Mode().Perm() != 0444 {
			t.Fatal("changed link target permissions")
		}
	})
	t.Run("canceled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if err := prepareRestoredTree(ctx, t.TempDir()); !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v", err)
		}
	})
}

// Cancel at the first context check after the validated tree has moved into
// ready, exercising cleanup after the new staging transition.
type cancelWhenReady struct {
	context.Context
	parent string
}

func (c cancelWhenReady) Err() error {
	paths, _ := filepath.Glob(filepath.Join(c.parent, ".droply-restore-*", "ready"))
	if len(paths) > 0 {
		return context.Canceled
	}
	return c.Context.Err()
}
func TestRestoreCancellationAfterStagingMove(t *testing.T) {
	cfg := fixture(t)
	must(t, Create(t.Context(), cfg))
	parent := filepath.Dir(cfg.DataDir)
	target := filepath.Join(parent, "restored")
	err := Restore(cancelWhenReady{t.Context(), parent}, RestoreConfig{Input: cfg.Output, DataDir: target})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v", err)
	}
	if _, err := os.Lstat(target); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("target published: %v", err)
	}
	work, err := filepath.Glob(filepath.Join(parent, ".droply-restore-*"))
	must(t, err)
	if len(work) != 0 {
		t.Fatalf("scratch tree leaked: %v", work)
	}
	// A failed restore must leave the archive usable for a fresh attempt.
	must(t, Restore(t.Context(), RestoreConfig{Input: cfg.Output, DataDir: target}))
}
