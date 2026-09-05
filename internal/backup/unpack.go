package backup

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/zhong/droply/internal/safetree"
)

// unpackBackup retains a sealed, reverified scratch tree before backup-format
// validation and the independent payload-to-ready copy. Its local tree manifest
// protects extraction integrity; backup.json owns the actual backup contract.
func unpackBackup(ctx context.Context, root string, input io.Reader, limits safetree.Limits) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	stage := filepath.Join(root, ".staging", "bundle")
	target := filepath.Join(root, "bundle")
	if err := absent(target); err != nil {
		return "", err
	}
	if err := os.Mkdir(stage, 0700); err != nil {
		return "", err
	}
	tree, err := safetree.NewBuilder(ctx, stage, limits)
	if err != nil {
		return "", err
	}
	if _, err := tree.ExtractTarGzip(input); err != nil {
		return "", fmt.Errorf("extract backup: %w", err)
	}
	// Preserve the prior complete verification pass before the local rename.
	// Cancellation is checked by subsequent backup validation and final publish.
	if _, err := safetree.Verify(context.Background(), stage); err != nil {
		return "", fmt.Errorf("verify unpacked backup: %w", err)
	}
	if err := absent(target); err != nil {
		return "", err
	}
	if err := os.Rename(stage, target); err != nil {
		return "", err
	}
	if err := errors.Join(syncDir(root), syncDir(filepath.Join(root, ".staging"))); err != nil {
		return "", err
	}
	return filepath.Join(target, "files"), nil
}
