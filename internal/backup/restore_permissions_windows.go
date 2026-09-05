package backup

import (
	"errors"
	"io/fs"
	"os"

	"golang.org/x/sys/windows"
)

func prepareRestoredEntry(name string, info fs.FileInfo) error {
	path, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return err
	}
	// Read-only extracted files cannot be opened with GENERIC_WRITE yet.
	// Attribute access permits clearing READONLY through this verified handle.
	h, err := windows.CreateFile(path, windows.FILE_READ_ATTRIBUTES|windows.FILE_WRITE_ATTRIBUTES,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING,
		windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return err
	}
	f := os.NewFile(uintptr(h), name)
	opened, err := f.Stat()
	if err == nil && !os.SameFile(info, opened) {
		err = errors.New("restored tree changed while opening")
	}
	if err == nil {
		err = f.Chmod(0600)
	}
	if err = errors.Join(err, f.Close()); err != nil {
		return err
	}
	if info.IsDir() {
		return nil
	}
	// FlushFileBuffers requires GENERIC_WRITE. Reopen after clearing READONLY,
	// check identity again, and propagate flush and close errors.
	f, err = os.OpenFile(name, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	opened, err = f.Stat()
	if err == nil && !os.SameFile(info, opened) {
		err = errors.New("restored tree changed while opening")
	}
	if err == nil {
		err = f.Sync()
	}
	return errors.Join(err, f.Close())
}
