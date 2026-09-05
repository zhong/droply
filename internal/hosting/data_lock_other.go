//go:build !darwin && !linux

package hosting

import (
	"errors"
	"os"
)

func LockDataDirectory(directory string) (*os.File, error) {
	return nil, errors.New("standalone server requires Linux or macOS for data directory locking")
}
