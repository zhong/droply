// Package backup creates offline, checksummed Droply backups and restores them
// only into new directories. Backup files contain credentials and private keys.
package backup

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/zhong/droply/internal/artifacts"
	"github.com/zhong/droply/internal/hosting"
	"github.com/zhong/droply/internal/safetree"
	"github.com/zhong/droply/internal/store"
	_ "modernc.org/sqlite"
)

const FormatVersion = 1

// SupportedDatabaseVersion tracks the server's versioned SQLite schema boundary.
const SupportedDatabaseVersion = store.SchemaVersion

type Config struct {
	DataDir, Output  string
	Configs, Include []string
	HMACFile         string
}
type RestoreConfig struct {
	Input, DataDir string
	MaxBytes       int64
	MaxFiles       int
}
type Mapping struct {
	Source string `json:"source"`
	Path   string `json:"path"`
	Kind   string `json:"kind"`
}
type File struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}
type Manifest struct {
	Version           int       `json:"version"`
	SourceUserVersion int       `json:"source_user_version"`
	CreatedAt         time.Time `json:"created_at"`
	HMACMode          string    `json:"hmac_mode"`
	Files             []File    `json:"files"`
	Directories       []string  `json:"directories"`
	External          []Mapping `json:"external"`
}

// Create holds the server's data lock throughout snapshotting and copying. No
// server publication, GC, analytics or certificate worker can run concurrently.
func Create(ctx context.Context, cfg Config) error {
	if cfg.DataDir == "" {
		return errors.New("data directory is required")
	}
	if len(cfg.Configs) == 0 {
		return errors.New("at least one --config file containing the actual service configuration is required")
	}
	root, err := filepath.Abs(cfg.DataDir)
	if err != nil {
		return err
	}
	if err = plainDirectory(root); err != nil {
		return err
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return err
	}
	output, err := filepath.Abs(cfg.Output)
	if err != nil || cfg.Output == "" {
		return errors.New("backup output is required")
	}
	outputParent, err := filepath.EvalSymlinks(filepath.Dir(output))
	if err != nil {
		return err
	}
	output = filepath.Join(outputParent, filepath.Base(output))
	if within(root, output) {
		return errors.New("backup output must be outside the data directory")
	}
	if err = absent(output); err != nil {
		return err
	}
	lock, err := hosting.LockDataDirectory(root)
	if err != nil {
		return err
	}
	defer lock.Close()
	work, err := os.MkdirTemp(filepath.Dir(output), ".droply-backup-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(work)
	payload := filepath.Join(work, "payload")
	if err = os.MkdirAll(filepath.Join(payload, "data"), 0700); err != nil {
		return err
	}
	version, err := snapshot(ctx, filepath.Join(root, "droply.db"), filepath.Join(payload, "data", "droply.db"))
	if err != nil {
		return err
	}
	if err = copyTree(ctx, root, filepath.Join(payload, "data"), func(path string) bool {
		return path == "droply.db" || path == "droply.db-wal" || path == "droply.db-shm" || path == "droply.db-journal" || path == "server.lock"
	}); err != nil {
		return err
	}
	m := Manifest{Version: FormatVersion, SourceUserVersion: version, CreatedAt: time.Now().UTC(), HMACMode: "persisted", External: []Mapping{}}
	if cfg.HMACFile != "" {
		info, err := os.Lstat(cfg.HMACFile)
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() || info.Size() == 0 || info.Size() > 1<<20 {
			return errors.New("HMAC override must be a nonempty regular file of at most 1 MiB")
		}
		if err = os.Remove(filepath.Join(payload, "data", "hmac.override")); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err = copyFile(ctx, cfg.HMACFile, filepath.Join(payload, "data", "hmac.override")); err != nil {
			return fmt.Errorf("copy explicit HMAC key: %w", err)
		}
		m.HMACMode = "override"
	} else {
		key, err := os.Stat(filepath.Join(payload, "data", "hmac.key"))
		if err != nil || !key.Mode().IsRegular() || key.Size() != 32 {
			return errors.New("missing valid persisted HMAC key: supply --hmac-key-file for an active explicit override")
		}
	}
	paths := append(append([]string(nil), cfg.Configs...), cfg.Include...)
	for i, input := range paths {
		source, err := filepath.Abs(input)
		if err != nil {
			return err
		}
		sourceParent, err := filepath.EvalSymlinks(filepath.Dir(source))
		if err != nil {
			return err
		}
		source = filepath.Join(sourceParent, filepath.Base(source))
		if within(source, work) || within(source, output) {
			return errors.New("included source contains the backup output or working directory")
		}
		info, err := os.Lstat(source)
		if err != nil {
			return err
		}
		kind := "include"
		if i < len(cfg.Configs) {
			kind = "config"
			if !info.Mode().IsRegular() {
				return errors.New("--config requires a regular file")
			}
		}
		relative := fmt.Sprintf("external/%04d/%s", i, filepath.Base(source))
		destination := filepath.Join(payload, filepath.FromSlash(relative))
		if info.IsDir() {
			err = copyTree(ctx, source, destination, nil)
		} else {
			err = copyFile(ctx, source, destination)
		}
		if err != nil {
			return fmt.Errorf("copy external %s: %w", kind, err)
		}
		m.External = append(m.External, Mapping{Source: source, Path: relative, Kind: kind})
	}
	if err = checkArtifacts(ctx, filepath.Join(payload, "data")); err != nil {
		return err
	}
	m.Files, m.Directories, err = inventory(ctx, payload)
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	if err = writeFile(filepath.Join(payload, "backup.json"), data); err != nil {
		return err
	}
	archive := filepath.Join(work, "backup.tar.gz")
	if err = pack(ctx, payload, archive); err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	// A hard-link publication is atomic and cannot replace an existing output.
	if err = os.Link(archive, output); err != nil {
		return err
	}
	return syncDir(filepath.Dir(output))
}

// Restore rejects existing destinations, unsupported versions and any integrity
// failure before publishing. External files remain under .restore/<restore-id>/external; the
// saved mapping tells the administrator which service paths need updating.
func Restore(ctx context.Context, cfg RestoreConfig) error {
	if cfg.DataDir == "" || cfg.Input == "" {
		return errors.New("restore input and new data directory are required")
	}
	target, err := filepath.Abs(cfg.DataDir)
	if err != nil {
		return err
	}
	if err = absent(target); err != nil {
		return err
	}
	parent := filepath.Dir(target)
	if err = plainDirectory(parent); err != nil {
		return err
	}
	work, err := os.MkdirTemp(parent, ".droply-restore-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(work)
	unpack := filepath.Join(work, "unpack")
	// Restore owns its scratch tree and preserves its original durable layout.
	for _, dir := range []string{unpack, filepath.Join(unpack, ".staging")} {
		if err = os.MkdirAll(dir, 0700); err != nil {
			return err
		}
		if err = plainDirectory(dir); err != nil {
			return err
		}
	}
	if err = errors.Join(syncDir(unpack), syncDir(filepath.Dir(unpack))); err != nil {
		return err
	}
	input, err := os.Open(cfg.Input)
	if err != nil {
		return err
	}
	defer input.Close()
	if cfg.MaxBytes <= 0 {
		cfg.MaxBytes = 64 << 30
	}
	if cfg.MaxFiles <= 0 {
		cfg.MaxFiles = 100000
	}
	payload, err := unpackBackup(ctx, unpack, input, safetree.Limits{MaxBytes: cfg.MaxBytes, MaxFiles: cfg.MaxFiles})
	if err != nil {
		return err
	}
	m, err := validate(ctx, payload)
	if err != nil {
		return err
	}
	ready := filepath.Join(work, "ready")
	if err = os.Rename(filepath.Join(payload, "data"), ready); err != nil {
		return err
	}
	restoreID := rand.Text()
	restored := filepath.Join(ready, ".restore", restoreID)
	if err = os.MkdirAll(restored, 0700); err != nil {
		return err
	}
	if _, err = os.Stat(filepath.Join(payload, "external")); err == nil {
		if err = os.Rename(filepath.Join(payload, "external"), filepath.Join(restored, "external")); err != nil {
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err = prepareRestoredTree(ctx, ready); err != nil {
		return err
	}
	// Paths are relative to .restore and never cause writes to the original machine.
	for i := range m.External {
		m.External[i].Path = filepath.ToSlash(filepath.Join(".restore", restoreID, filepath.FromSlash(m.External[i].Path)))
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	if err = writeFile(filepath.Join(restored, "manifest.json"), data); err != nil {
		return err
	}
	if err = syncTree(ready); err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	if err = absent(target); err != nil {
		return err
	}
	if err = os.Rename(ready, target); err != nil {
		return err
	}
	return syncDir(parent)
}

func plainDirectory(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("expected a plain directory")
	}
	return nil
}
func absent(path string) error {
	_, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	return errors.New("destination already exists; use a new path")
}
func within(parent, child string) bool {
	rel, err := filepath.Rel(parent, child)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
func openDB(path string) (*sql.DB, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("database must be a regular file")
	}
	uri := &url.URL{Scheme: "file", Path: path, RawQuery: "mode=ro"}
	db, err := sql.Open("sqlite", uri.String())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	return db, nil
}
func snapshot(ctx context.Context, source, destination string) (int, error) {
	db, err := openDB(source)
	if err != nil {
		return 0, err
	}
	defer db.Close()
	var version int
	if err = db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return 0, err
	}
	if version < 0 || version > SupportedDatabaseVersion {
		return 0, errors.New("unsupported source database version")
	}
	if _, err = db.ExecContext(ctx, "PRAGMA synchronous=FULL"); err != nil {
		return 0, err
	}
	if _, err = db.ExecContext(ctx, "VACUUM INTO ?", destination); err != nil {
		return 0, fmt.Errorf("snapshot database: %w", err)
	}
	f, err := os.OpenFile(destination, os.O_RDWR, 0600)
	if err != nil {
		return 0, err
	}
	err = errors.Join(f.Sync(), f.Close())
	if err != nil {
		return 0, err
	}
	return version, checkDB(ctx, destination, version)
}
func checkDB(ctx context.Context, path string, version int) error {
	db, err := openDB(path)
	if err != nil {
		return err
	}
	defer db.Close()
	var actual int
	if err = db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&actual); err != nil {
		return err
	}
	if actual != version || actual > SupportedDatabaseVersion {
		return errors.New("backup database version mismatch")
	}
	var result string
	if err = db.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&result); err != nil {
		return err
	}
	if result != "ok" {
		return errors.New("backup database integrity check failed")
	}
	rows, err := db.QueryContext(ctx, "PRAGMA foreign_key_check")
	if err != nil {
		return err
	}
	defer rows.Close()
	if rows.Next() {
		return errors.New("backup database foreign key check failed")
	}
	return rows.Err()
}
func copyTree(ctx context.Context, source, destination string, skip func(string) bool) error {
	if err := plainDirectory(source); err != nil {
		return err
	}
	if err := os.MkdirAll(destination, 0700); err != nil {
		return err
	}
	return filepath.WalkDir(source, func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		rel, err := filepath.Rel(source, name)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		if skip != nil && skip(filepath.ToSlash(rel)) {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		to := filepath.Join(destination, rel)
		if entry.IsDir() {
			return os.MkdirAll(to, 0700)
		}
		if !entry.Type().IsRegular() {
			return errors.New("backup source contains a link or special file")
		}
		return copyFile(ctx, name, to)
	})
}
func copyFile(ctx context.Context, source, destination string) error {
	info, err := os.Lstat(source)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errors.New("backup source must be a regular file")
	}
	if err = os.MkdirAll(filepath.Dir(destination), 0700); err != nil {
		return err
	}
	f, err := os.Open(source)
	if err != nil {
		return err
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil {
		return err
	}
	if !os.SameFile(info, opened) {
		return errors.New("backup source changed while opening")
	}
	out, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, contextReader{ctx, f})
	if copyErr == nil {
		after, err := f.Stat()
		if err != nil {
			copyErr = err
		} else if after.Size() != info.Size() || !after.ModTime().Equal(info.ModTime()) {
			copyErr = errors.New("backup source changed while copying")
		}
	}
	if copyErr == nil {
		copyErr = out.Sync()
	}
	return errors.Join(copyErr, out.Close())
}

type contextReader struct {
	ctx context.Context
	r   io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.r.Read(p)
}
func writeFile(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	return errors.Join(err, f.Close())
}
func inventory(ctx context.Context, root string) ([]File, []string, error) {
	files := []File{}
	dirs := []string{}
	err := filepath.WalkDir(root, func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		rel, err := filepath.Rel(root, name)
		if err != nil {
			return err
		}
		if rel == "." || rel == "backup.json" {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if entry.IsDir() {
			dirs = append(dirs, rel)
			return nil
		}
		if !entry.Type().IsRegular() {
			return errors.New("backup contains a link or special file")
		}
		f, err := os.Open(name)
		if err != nil {
			return err
		}
		hash := sha256.New()
		n, copyErr := io.Copy(hash, contextReader{ctx, f})
		if err = errors.Join(copyErr, f.Close()); err != nil {
			return err
		}
		files = append(files, File{Path: rel, Size: n, SHA256: hex.EncodeToString(hash.Sum(nil))})
		return nil
	})
	return files, dirs, err
}
func validate(ctx context.Context, root string) (*Manifest, error) {
	info, err := os.Stat(filepath.Join(root, "backup.json"))
	if err != nil {
		return nil, err
	}
	if info.Size() > 32<<20 {
		return nil, errors.New("backup manifest too large")
	}
	data, err := os.ReadFile(filepath.Join(root, "backup.json"))
	if err != nil {
		return nil, err
	}
	if len(data) > 32<<20 {
		return nil, errors.New("backup manifest too large")
	}
	var m Manifest
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.DisallowUnknownFields()
	if err = dec.Decode(&m); err != nil {
		return nil, err
	}
	if err = dec.Decode(new(any)); !errors.Is(err, io.EOF) {
		return nil, errors.New("trailing manifest data")
	}
	if m.Version != FormatVersion || m.SourceUserVersion < 0 || m.SourceUserVersion > SupportedDatabaseVersion {
		return nil, errors.New("unsupported backup or database version")
	}
	files, dirs, err := inventory(ctx, root)
	if err != nil {
		return nil, err
	}
	if !slices.Equal(files, m.Files) || !slices.Equal(dirs, m.Directories) {
		return nil, errors.New("backup manifest checksum or entry mismatch")
	}
	for _, dir := range dirs {
		if dir != "data" && dir != "external" && !strings.HasPrefix(dir, "data/") && !strings.HasPrefix(dir, "external/") {
			return nil, errors.New("unexpected backup directory")
		}
	}
	have := map[string]bool{}
	for _, file := range files {
		have[file.Path] = true
		if !strings.HasPrefix(file.Path, "data/") && !strings.HasPrefix(file.Path, "external/") {
			return nil, errors.New("unexpected backup payload")
		}
	}
	if !have["data/droply.db"] {
		return nil, errors.New("backup has no database")
	}
	if m.HMACMode == "persisted" {
		key, err := os.Stat(filepath.Join(root, "data", "hmac.key"))
		if err != nil || !key.Mode().IsRegular() || key.Size() != 32 {
			return nil, errors.New("backup has no valid HMAC key")
		}
	} else if m.HMACMode != "override" || !have["data/hmac.override"] {
		return nil, errors.New("backup HMAC mode invalid")
	} else {
		key, err := os.Stat(filepath.Join(root, "data", "hmac.override"))
		if err != nil || !key.Mode().IsRegular() || key.Size() == 0 || key.Size() > 1<<20 {
			return nil, errors.New("backup HMAC override invalid")
		}
	}
	config := false
	for _, mapping := range m.External {
		if mapping.Kind != "config" && mapping.Kind != "include" {
			return nil, errors.New("invalid external mapping kind")
		}
		if !strings.HasPrefix(mapping.Path, "external/") || filepath.ToSlash(filepath.Clean(mapping.Path)) != mapping.Path {
			return nil, errors.New("invalid external mapping path")
		}
		mapped, err := os.Lstat(filepath.Join(root, filepath.FromSlash(mapping.Path)))
		if err != nil {
			return nil, errors.New("missing external mapping payload")
		}
		if mapping.Kind == "config" {
			if !mapped.Mode().IsRegular() {
				return nil, errors.New("service configuration must be a regular file")
			}
			config = true
		}
	}
	if !config {
		return nil, errors.New("backup contains no service configuration")
	}
	if err = checkDB(ctx, filepath.Join(root, "data", "droply.db"), m.SourceUserVersion); err != nil {
		return nil, err
	}
	if err = checkArtifacts(ctx, filepath.Join(root, "data")); err != nil {
		return nil, err
	}
	return &m, nil
}
func pack(ctx context.Context, root, output string) error {
	f, err := os.OpenFile(output, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	walkErr := filepath.WalkDir(root, func(name string, entry fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		rel, e := filepath.Rel(root, name)
		if e != nil {
			return e
		}
		if rel == "." {
			return nil
		}
		info, e := entry.Info()
		if e != nil {
			return e
		}
		header, e := tar.FileInfoHeader(info, "")
		if e != nil {
			return e
		}
		header.Name = filepath.ToSlash(rel)
		if e = tw.WriteHeader(header); e != nil {
			return e
		}
		if entry.IsDir() {
			return nil
		}
		input, e := os.Open(name)
		if e != nil {
			return e
		}
		_, e = io.Copy(tw, contextReader{ctx, input})
		return errors.Join(e, input.Close())
	})
	err = errors.Join(walkErr, tw.Close(), gz.Close())
	if err == nil {
		err = f.Sync()
	}
	return errors.Join(err, f.Close())
}
func syncDir(name string) error {
	f, err := os.Open(name)
	if err != nil {
		return err
	}
	return errors.Join(f.Sync(), f.Close())
}
func syncTree(root string) error {
	var dirs []string
	err := filepath.WalkDir(root, func(name string, entry fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if entry.IsDir() {
			dirs = append(dirs, name)
		}
		return nil
	})
	if err != nil {
		return err
	}
	for i := len(dirs) - 1; i >= 0; i-- {
		if err = syncDir(dirs[i]); err != nil {
			return err
		}
	}
	return nil
}

// Available deployment records must point to complete, immutable artifacts.
func checkArtifacts(ctx context.Context, data string) error {
	db, err := openDB(filepath.Join(data, "droply.db"))
	if err != nil {
		return err
	}
	defer db.Close()
	// Pre-M1 databases use legacy site directories rather than artifact IDs.
	var artifactColumns int
	if err = db.QueryRowContext(ctx, "SELECT count(*) FROM pragma_table_info('deployments') WHERE name IN ('artifact_id','artifact_state','checksum')").Scan(&artifactColumns); err != nil {
		return err
	}
	if artifactColumns == 0 {
		var deployments int
		if err = db.QueryRowContext(ctx, "SELECT count(*) FROM deployments").Scan(&deployments); err != nil {
			return err
		}
		return nil
	}
	if artifactColumns != 3 {
		return errors.New("incomplete deployment schema in backup")
	}
	rows, err := db.QueryContext(ctx, "SELECT artifact_id, checksum FROM deployments WHERE artifact_state='available'")
	if err != nil {
		return fmt.Errorf("read artifact references: %w", err)
	}
	defer rows.Close()
	root := filepath.Join(data, "sites", ".artifacts")
	for rows.Next() {
		var id, checksum string
		if err = rows.Scan(&id, &checksum); err != nil {
			return err
		}
		if err = plainDirectory(root); err != nil {
			return errors.New("backup is missing referenced artifacts")
		}
		a, err := artifacts.New(root)
		if err != nil {
			return err
		}
		info, err := a.Verify(ctx, id)
		if err != nil {
			return fmt.Errorf("verify referenced artifact: %w", err)
		}
		if info.Checksum != checksum {
			return errors.New("referenced artifact checksum mismatch")
		}
	}
	return rows.Err()
}

// prepareRestoredTree makes the verified, private unpack tree writable without
// copying its contents. Each file was synced during extraction; sync again after
// changing its mode. Restore syncs the directories before the final rename.
func prepareRestoredTree(ctx context.Context, root string) error {
	return filepath.WalkDir(root, func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.IsDir() && !info.Mode().IsRegular() {
			return errors.New("restored tree contains a link or special file")
		}
		return prepareRestoredEntry(name, info)
	})
}
