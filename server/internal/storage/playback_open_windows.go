//go:build windows

package storage

import (
	"os"

	"golang.org/x/sys/windows"
)

func openPlaybackNoFollow(path string) (*os.File, error) {
	wide, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	handle, err := windows.CreateFile(wide, windows.GENERIC_READ, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(handle), path)
	var info windows.ByHandleFileInformation
	if err = windows.GetFileInformationByHandle(handle, &info); err == nil {
		err = rejectWindowsNameSurrogate(handle, info.FileAttributes)
	}
	if err != nil {
		file.Close()
		return nil, err
	}
	return file, nil
}
