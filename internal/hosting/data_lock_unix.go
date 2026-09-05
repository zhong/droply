//go:build darwin || linux

package hosting

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// LockDataDirectory prevents a second process from recovering live uploads as
// interrupted. The OS releases the advisory lock even after an unclean exit.
func LockDataDirectory(directory string) (*os.File, error) {
	file, err := os.OpenFile(filepath.Join(directory, "server.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		file.Close()
		return nil, fmt.Errorf("data directory already in use or locking unavailable: %w", err)
	}
	return file, nil
}
