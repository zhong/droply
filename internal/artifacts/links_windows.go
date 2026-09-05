package artifacts

import (
	"golang.org/x/sys/windows"
	"os"
)

func multipleLinks(f *os.File) (bool, error) {
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(windows.Handle(f.Fd()), &info); err != nil {
		return false, err
	}
	return info.NumberOfLinks > 1, nil
}
