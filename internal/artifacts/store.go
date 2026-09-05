// Package artifacts stores immutable, checksummed static-site deployments.
package artifacts

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
)

const defaultMaxBytes int64 = 256 << 20
const defaultMaxFiles = 10000

// ErrByteLimit distinguishes configured extraction limits from malformed input.
var ErrByteLimit = errors.New("artifact byte limit exceeded")

// Limits bounds file bytes and all filesystem entries, including directories.
type Limits struct {
	MaxBytes int64
	MaxFiles int
}
type Info struct {
	FileCount int
	TotalSize int64
	Checksum  string
}
type Entry struct {
	ID         string
	Staging    bool
	Size       int64
	ModifiedAt time.Time
}
type Store struct {
	root string
	mu   sync.Mutex
}
type record struct {
	Path   string `json:"path"`
	Type   string `json:"type"`
	Size   int64  `json:"size,omitzero"`
	SHA256 string `json:"sha256,omitempty"`
}
type manifest struct {
	Version int      `json:"version"`
	Entries []record `json:"entries"`
}

func New(root string) (*Store, error) {
	if root == "" {
		return nil, errors.New("artifact root is required")
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	for _, dir := range []string{absolute, filepath.Join(absolute, ".staging")} {
		if err = os.MkdirAll(dir, 0700); err != nil {
			return nil, fmt.Errorf("create artifact directory: %w", err)
		}
		if err = directory(dir); err != nil {
			return nil, err
		}
	}
	if err = errors.Join(syncDirectory(absolute), syncDirectory(filepath.Dir(absolute))); err != nil {
		return nil, err
	}
	return &Store{root: absolute}, nil
}
func validID(id string) bool {
	if len(id) == 0 || len(id) > 128 {
		return false
	}
	for _, c := range id {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return false
		}
	}
	return true
}
func (s *Store) location(id string, staging bool) (string, error) {
	if !validID(id) {
		return "", errors.New("invalid artifact ID")
	}
	if staging {
		return filepath.Join(s.root, ".staging", id), nil
	}
	return filepath.Join(s.root, id), nil
}

// Path returns the immutable files directory, or an empty string for an invalid ID.
func (s *Store) Path(id string) string {
	p, err := s.location(id, false)
	if err != nil {
		return ""
	}
	return filepath.Join(p, "files")
}
func effective(l Limits) Limits {
	if l.MaxBytes <= 0 {
		l.MaxBytes = defaultMaxBytes
	}
	if l.MaxFiles <= 0 {
		l.MaxFiles = defaultMaxFiles
	}
	return l
}
func archivePath(name string, dir bool) (string, error) {
	if dir {
		name = strings.TrimSuffix(name, "/")
	}
	if name == "" || len(name) > 4096 || name == "." || strings.ContainsAny(name, "\\:\x00") || strings.HasPrefix(name, "/") || path.Clean(name) != name {
		return "", errors.New("unsafe artifact path")
	}
	for component := range strings.SplitSeq(name, "/") {
		if component == "." || component == ".." || len(component) > 255 {
			return "", errors.New("unsafe artifact path")
		}
	}
	return name, nil
}
func directory(name string) error {
	info, err := os.Lstat(name)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("not a plain directory: %s", name)
	}
	return nil
}
func syncDirectory(name string) error {
	f, err := os.Open(name)
	if err != nil {
		return err
	}
	err = f.Sync()
	return errors.Join(err, f.Close())
}

type builder struct {
	ctx          context.Context
	stage, files string
	limits       Limits
	records      map[string]record
	info         Info
}

func (s *Store) begin(ctx context.Context, id string, limits Limits) (*builder, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	stage, err := s.location(id, true)
	if err != nil {
		return nil, err
	}
	target, _ := s.location(id, false)
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err = os.Lstat(target); !errors.Is(err, os.ErrNotExist) {
		if err != nil {
			return nil, err
		}
		return nil, errors.New("artifact already exists")
	}
	if err = os.Mkdir(stage, 0700); err != nil {
		return nil, fmt.Errorf("create artifact staging: %w", err)
	}
	files := filepath.Join(stage, "files")
	if err = os.Mkdir(files, 0755); err != nil {
		return nil, err
	}
	return &builder{ctx: ctx, stage: stage, files: files, limits: effective(limits), records: map[string]record{}}, nil
}
func (b *builder) ensureDir(name string) error {
	if name == "." || name == "" {
		return nil
	}
	if strings.Count(name, "/")+1 > b.limits.MaxFiles {
		return errors.New("artifact directory limit exceeded")
	}
	if r, ok := b.records[name]; ok {
		if r.Type != "directory" {
			return errors.New("file conflicts with parent directory")
		}
		return nil
	}
	if err := b.ensureDir(path.Dir(name)); err != nil {
		return err
	}
	if len(b.records) >= b.limits.MaxFiles {
		return errors.New("artifact entry limit exceeded")
	}
	if err := b.ctx.Err(); err != nil {
		return err
	}
	if err := os.Mkdir(filepath.Join(b.files, filepath.FromSlash(name)), 0755); err != nil {
		return err
	}
	b.records[name] = record{Path: name, Type: "directory"}
	return nil
}
func (b *builder) addFile(name string, size int64, r io.Reader) error {
	if size < 0 || size > b.limits.MaxBytes-b.info.TotalSize {
		return ErrByteLimit
	}
	if err := b.ensureDir(path.Dir(name)); err != nil {
		return err
	}
	if _, ok := b.records[name]; ok {
		return errors.New("duplicate artifact path")
	}
	if len(b.records) >= b.limits.MaxFiles {
		return errors.New("artifact entry limit exceeded")
	}
	f, err := os.OpenFile(filepath.Join(b.files, filepath.FromSlash(name)), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	hash := sha256.New()
	n, copyErr := io.CopyN(io.MultiWriter(f, hash), contextReader{b.ctx, r}, size)
	if copyErr == nil {
		copyErr = b.ctx.Err()
	}
	if copyErr == nil {
		copyErr = f.Chmod(0444)
	}
	if copyErr == nil {
		copyErr = f.Sync()
	}
	closeErr := f.Close()
	if err = errors.Join(copyErr, closeErr); err != nil {
		return err
	}
	b.records[name] = record{Path: name, Type: "file", Size: n, SHA256: hex.EncodeToString(hash.Sum(nil))}
	b.info.FileCount++
	b.info.TotalSize += n
	return nil
}
func (b *builder) finish() (result Info, resultErr error) {
	defer func() {
		if resultErr != nil {
			err := os.Remove(filepath.Join(b.stage, "manifest.json"))
			if err != nil && !errors.Is(err, os.ErrNotExist) {
				resultErr = errors.Join(resultErr, err)
			}
		}
	}()
	if err := b.ctx.Err(); err != nil {
		return Info{}, err
	}
	entries := make([]record, 0, len(b.records))
	for _, r := range b.records {
		entries = append(entries, r)
	}
	slices.SortFunc(entries, func(a, b record) int { return strings.Compare(a.Path, b.Path) })
	data, err := json.Marshal(manifest{Version: 1, Entries: entries})
	if err != nil {
		return Info{}, err
	}
	if len(data) > 64<<20 {
		return Info{}, errors.New("artifact manifest exceeds size limit")
	}
	f, err := os.OpenFile(filepath.Join(b.stage, "manifest.json"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return Info{}, err
	}
	_, writeErr := f.Write(data)
	if writeErr == nil {
		writeErr = f.Chmod(0444)
	}
	if writeErr == nil {
		writeErr = f.Sync()
	}
	if err = errors.Join(writeErr, f.Close()); err != nil {
		return Info{}, err
	}
	// Children must be durable before their parents and the completed staging entry.
	for i := len(entries) - 1; i >= 0; i-- {
		if entries[i].Type == "directory" {
			if err = syncDirectory(filepath.Join(b.files, filepath.FromSlash(entries[i].Path))); err != nil {
				return Info{}, err
			}
		}
	}
	for _, dir := range []string{b.files, b.stage, filepath.Dir(b.stage)} {
		if err = syncDirectory(dir); err != nil {
			return Info{}, err
		}
	}
	if err = b.ctx.Err(); err != nil {
		return Info{}, err
	}
	sum := sha256.Sum256(data)
	b.info.Checksum = hex.EncodeToString(sum[:])
	return b.info, nil
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

// tar.Reader accepts EOF at a header boundary without the two terminal blocks.
// Track the bytes consumed by its final Next to enforce a complete tar stream.
type tarStream struct {
	r    io.Reader
	n    int64
	tail [1024]byte
}

func (r *tarStream) Read(p []byte) (int, error) {
	n, err := r.r.Read(p)
	r.n += int64(n)
	if n >= len(r.tail) {
		copy(r.tail[:], p[n-len(r.tail):n])
	} else {
		copy(r.tail[:], r.tail[n:])
		copy(r.tail[len(r.tail)-n:], p[:n])
	}
	return n, err
}

// Stage extracts into a unique private staging directory. Failed attempts remain
// identifiable for recovery; they are never visible through Path or Publish.
func (s *Store) Stage(ctx context.Context, id string, r io.Reader, limits Limits) (Info, error) {
	if r == nil {
		return Info{}, errors.New("archive reader is required")
	}
	b, err := s.begin(ctx, id, limits)
	if err != nil {
		return Info{}, err
	}
	gz, err := gzip.NewReader(contextReader{ctx, r})
	if err != nil {
		return Info{}, fmt.Errorf("open gzip: %w", err)
	}
	defer gz.Close()
	// Bound padding and extended metadata as well as extracted bytes. Without this,
	// a small archive could hide an arbitrarily large gzip bomb after tar's EOF.
	ceiling := int64(math.MaxInt64 - 1)
	if int64(b.limits.MaxFiles) < (math.MaxInt64-4096)/2048 {
		overhead := int64(b.limits.MaxFiles)*2048 + 4096
		if b.limits.MaxBytes <= math.MaxInt64-1-overhead {
			ceiling = b.limits.MaxBytes + overhead
		}
	}
	limited := &io.LimitedReader{R: contextReader{ctx, gz}, N: ceiling + 1}
	stream := &tarStream{r: limited}
	tr := tar.NewReader(stream)
	seen := map[string]bool{}
	for {
		before := stream.n
		header, err := tr.Next()
		if errors.Is(err, io.EOF) {
			if stream.n-before < 1024 {
				return Info{}, errors.New("truncated tar footer")
			}
			for _, value := range stream.tail {
				if value != 0 {
					return Info{}, errors.New("invalid tar footer")
				}
			}
			break
		}
		if err != nil {
			return Info{}, fmt.Errorf("read tar: %w", err)
		}
		if header.Typeflag != tar.TypeDir && header.Typeflag != tar.TypeReg && header.Typeflag != tar.TypeRegA {
			return Info{}, errors.New("unsupported archive entry type")
		}
		name, err := archivePath(header.Name, header.Typeflag == tar.TypeDir)
		if err != nil {
			return Info{}, err
		}
		if seen[name] {
			return Info{}, errors.New("duplicate archive entry")
		}
		seen[name] = true
		if len(seen) > b.limits.MaxFiles {
			return Info{}, errors.New("archive entry limit exceeded")
		}
		if header.Typeflag == tar.TypeDir {
			if header.Size != 0 {
				return Info{}, errors.New("directory entry has data")
			}
			err = b.ensureDir(name)
		} else {
			err = b.addFile(name, header.Size, tr)
		}
		if err != nil {
			return Info{}, fmt.Errorf("extract %q: %w", name, err)
		}
	}
	// Read through gzip's footer, reject truncation/checksum errors and non-padding
	// data after the first tar stream. tar.Reader alone does not validate the footer.
	var tail [32768]byte
	for {
		n, err := limited.Read(tail[:])
		for _, value := range tail[:n] {
			if value != 0 {
				return Info{}, errors.New("unexpected data after tar archive")
			}
		}
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return Info{}, fmt.Errorf("validate gzip trailer: %w", err)
		}
	}
	if limited.N == 0 {
		return Info{}, errors.New("archive stream limit exceeded")
	}
	return b.finish()
}

// Import copies a legacy tree without sharing mutable files or accepting links.
func (s *Store) Import(ctx context.Context, id, legacyDir string, limits Limits) (Info, error) {
	if err := directory(legacyDir); err != nil {
		return Info{}, err
	}
	source, err := os.OpenRoot(legacyDir)
	if err != nil {
		return Info{}, err
	}
	defer source.Close()
	b, err := s.begin(ctx, id, limits)
	if err != nil {
		return Info{}, err
	}
	err = fs.WalkDir(source.FS(), ".", func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if name == "." {
			return nil
		}
		canonical, err := archivePath(name, entry.IsDir())
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return b.ensureDir(canonical)
		}
		if !entry.Type().IsRegular() {
			return errors.New("legacy tree contains a link or unsupported file")
		}
		before, err := entry.Info()
		if err != nil {
			return err
		}
		f, err := source.Open(name)
		if err != nil {
			return err
		}
		after, statErr := f.Stat()
		if statErr != nil {
			f.Close()
			return statErr
		}
		if !after.Mode().IsRegular() || !os.SameFile(before, after) {
			f.Close()
			return errors.New("legacy file changed during import")
		}
		linked, linkErr := multipleLinks(f)
		if linkErr != nil || linked {
			f.Close()
			if linkErr != nil {
				return linkErr
			}
			return errors.New("legacy tree contains a hard link")
		}
		copyErr := b.addFile(canonical, after.Size(), f)
		// Growing sources are not a coherent snapshot and must not silently truncate.
		if copyErr == nil {
			var extra [1]byte
			n, e := f.Read(extra[:])
			if n != 0 || !errors.Is(e, io.EOF) {
				copyErr = errors.New("legacy file changed during import")
			}
		}
		return errors.Join(copyErr, f.Close())
	})
	if err != nil {
		return Info{}, err
	}
	return b.finish()
}

func (s *Store) Publish(id string) error {
	stage, err := s.location(id, true)
	if err != nil {
		return err
	}
	target, _ := s.location(id, false)
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err = s.verify(context.Background(), stage); err != nil {
		return fmt.Errorf("verify staged artifact: %w", err)
	}
	if _, err = os.Lstat(target); !errors.Is(err, os.ErrNotExist) {
		if err != nil {
			return err
		}
		return errors.New("artifact already exists")
	}
	if err = os.Rename(stage, target); err != nil {
		return err
	}
	return errors.Join(syncDirectory(s.root), syncDirectory(filepath.Join(s.root, ".staging")))
}

func (s *Store) Verify(ctx context.Context, id string) (Info, error) {
	dir, err := s.location(id, false)
	if err != nil {
		return Info{}, err
	}
	return s.verify(ctx, dir)
}
func (s *Store) verify(ctx context.Context, dir string) (Info, error) {
	if err := ctx.Err(); err != nil {
		return Info{}, err
	}
	if err := directory(dir); err != nil {
		return Info{}, err
	}
	children, err := os.ReadDir(dir)
	if err != nil {
		return Info{}, err
	}
	if len(children) != 2 {
		return Info{}, errors.New("artifact has missing or unexpected metadata")
	}
	files := filepath.Join(dir, "files")
	if err = directory(files); err != nil {
		return Info{}, err
	}
	manifestPath := filepath.Join(dir, "manifest.json")
	stat, err := os.Lstat(manifestPath)
	if err != nil {
		return Info{}, err
	}
	if !stat.Mode().IsRegular() || stat.Size() > 64<<20 {
		return Info{}, errors.New("invalid artifact manifest file")
	}
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return Info{}, err
	}
	var m manifest
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&m); err != nil {
		return Info{}, err
	}
	if err = decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return Info{}, errors.New("trailing manifest data")
	}
	if m.Version != 1 {
		return Info{}, errors.New("unsupported artifact manifest")
	}
	expected := make(map[string]record, len(m.Entries))
	var info Info
	for _, r := range m.Entries {
		name, e := archivePath(r.Path, r.Type == "directory")
		if e != nil || name != r.Path {
			return Info{}, errors.New("invalid manifest path")
		}
		if _, ok := expected[name]; ok {
			return Info{}, errors.New("duplicate manifest path")
		}
		if r.Type == "directory" {
			if r.Size != 0 || r.SHA256 != "" {
				return Info{}, errors.New("invalid manifest directory")
			}
		} else if r.Type == "file" {
			hash, e := hex.DecodeString(r.SHA256)
			if e != nil || len(hash) != sha256.Size || r.Size < 0 || r.Size > math.MaxInt64-info.TotalSize {
				return Info{}, errors.New("invalid manifest file")
			}
			info.FileCount++
			info.TotalSize += r.Size
		} else {
			return Info{}, errors.New("invalid manifest entry type")
		}
		expected[name] = r
	}
	err = filepath.WalkDir(files, func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if name == files {
			return nil
		}
		rel, e := filepath.Rel(files, name)
		if e != nil {
			return e
		}
		rel = filepath.ToSlash(rel)
		r, ok := expected[rel]
		if !ok {
			return errors.New("unexpected artifact entry")
		}
		delete(expected, rel)
		if entry.IsDir() {
			if r.Type != "directory" {
				return errors.New("artifact type mismatch")
			}
			return nil
		}
		if !entry.Type().IsRegular() || r.Type != "file" {
			return errors.New("artifact contains a link or unexpected file type")
		}
		stat, e := entry.Info()
		if e != nil {
			return e
		}
		if stat.Size() != r.Size {
			return errors.New("artifact size mismatch")
		}
		f, e := os.Open(name)
		if e != nil {
			return e
		}
		linked, linkErr := multipleLinks(f)
		if linkErr != nil || linked {
			f.Close()
			if linkErr != nil {
				return linkErr
			}
			return errors.New("artifact contains a hard link")
		}
		hash := sha256.New()
		_, copyErr := io.Copy(hash, contextReader{ctx, f})
		e = errors.Join(copyErr, f.Close())
		if e != nil {
			return e
		}
		if hex.EncodeToString(hash.Sum(nil)) != r.SHA256 {
			return errors.New("artifact checksum mismatch")
		}
		return nil
	})
	if err != nil {
		return Info{}, err
	}
	if len(expected) != 0 {
		return Info{}, errors.New("missing artifact entry")
	}
	sum := sha256.Sum256(data)
	info.Checksum = hex.EncodeToString(sum[:])
	return info, nil
}

func (s *Store) Remove(id string) error      { return s.remove(id, false) }
func (s *Store) RemoveStage(id string) error { return s.remove(id, true) }
func (s *Store) remove(id string, staging bool) error {
	dir, err := s.location(id, staging)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err = os.RemoveAll(dir); err != nil {
		return err
	}
	return syncDirectory(filepath.Dir(dir))
}

// Entries includes incomplete staging directories and unreferenced published
// artifacts, so recovery can discover abandoned writes after a process restart.
func (s *Store) Entries() ([]Entry, error) {
	var result []Entry
	for _, staging := range []bool{false, true} {
		parent := s.root
		if staging {
			parent = filepath.Join(parent, ".staging")
		}
		entries, err := os.ReadDir(parent)
		if err != nil {
			return nil, err
		}
		for _, entry := range entries {
			if !staging && entry.Name() == ".staging" {
				continue
			}
			if !validID(entry.Name()) || !entry.IsDir() {
				return nil, errors.New("unexpected entry in artifact storage")
			}
			stat, err := entry.Info()
			if err != nil {
				return nil, err
			}
			size, err := treeSize(filepath.Join(parent, entry.Name()))
			if err != nil {
				return nil, err
			}
			result = append(result, Entry{ID: entry.Name(), Staging: staging, Size: size, ModifiedAt: stat.ModTime()})
		}
	}
	slices.SortFunc(result, func(a, b Entry) int {
		if a.Staging != b.Staging {
			if a.Staging {
				return 1
			}
			return -1
		}
		return strings.Compare(a.ID, b.ID)
	})
	return result, nil
}

// Usage counts actual regular-file bytes, including manifests and partial writes.
func (s *Store) Usage() (int64, error) { return treeSize(s.root) }
func treeSize(root string) (int64, error) {
	var total int64
	err := filepath.WalkDir(root, func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		if !entry.Type().IsRegular() {
			return errors.New("artifact storage contains a link or special file")
		}
		stat, err := entry.Info()
		if err != nil {
			return err
		}
		if stat.Size() > math.MaxInt64-total {
			return errors.New("artifact usage overflow")
		}
		total += stat.Size()
		return nil
	})
	return total, err
}
