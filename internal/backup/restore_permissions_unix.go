//go:build !windows

package backup

import (
	"errors"
	"io/fs"
	"os"
)

func prepareRestoredEntry(name string, info fs.FileInfo) error {
	f, err := os.Open(name)
	if err != nil {
		return err
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil {
		return err
	}
	if !os.SameFile(info, opened) {
		return errors.New("restored tree changed while opening")
	}
	mode := fs.FileMode(0600)
	if info.IsDir() {
		mode = 0700
	}
	if err := f.Chmod(mode); err != nil {
		return err
	}
	if !info.IsDir() {
		return f.Sync()
	}
	return nil
}
