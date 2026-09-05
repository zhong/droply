// Package artifacts stores immutable, checksummed static-site deployments.
package artifacts

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/zhong/droply/internal/safetree"
)

// Limits bounds extracted bytes and filesystem entries.
type Limits = safetree.Limits
type Info = safetree.Info

// ErrByteLimit distinguishes configured extraction limits from malformed input.
var ErrByteLimit = safetree.ErrByteLimit

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

// StagingPath returns the staged files directory for validation before publication.
func (s *Store) StagingPath(id string) string {
	p, err := s.location(id, true)
	if err != nil {
		return ""
	}
	return filepath.Join(p, "files")
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

func (s *Store) begin(ctx context.Context, id string, limits Limits) (*safetree.Builder, error) {
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
	return safetree.NewBuilder(ctx, stage, limits)
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
	return b.ExtractTarGzip(r)
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
	return b.ImportRoot(source)
}

func (s *Store) Publish(id string) error {
	stage, err := s.location(id, true)
	if err != nil {
		return err
	}
	target, _ := s.location(id, false)
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err = safetree.Verify(context.Background(), stage); err != nil {
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
	return safetree.Verify(ctx, dir)
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
