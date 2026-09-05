//go:build !windows

package artifacts

import (
	"os"
	"syscall"
)

func multipleLinks(f *os.File) (bool, error) {
	info, err := f.Stat()
	if err != nil {
		return false, err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && stat.Nlink > 1, nil
}
