//go:build windows

package storage

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"unsafe"

	"golang.org/x/sys/windows"
)

// FileInfo on Windows contains attributes, not the volume/file index. Acquire
// those from one handle while matching the snapshot attributes. Attribute-only
// opens do not exclude writers or replacements. Name-surrogate links are refused.
func observedObjectBinding(info os.FileInfo, path string) (string, string, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return "", "", err
	}
	handle, err := windows.CreateFile(name, windows.FILE_READ_ATTRIBUTES, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return "", "", err
	}
	file := os.NewFile(uintptr(handle), path)
	defer file.Close()
	current, err := file.Stat()
	if err != nil {
		return "", "", err
	}
	if !os.SameFile(info, current) || (!info.IsDir() && info.Size() != current.Size()) || !info.ModTime().Equal(current.ModTime()) || info.IsDir() != current.IsDir() {
		return "", "", ErrObservedSourceLost
	}
	return windowsFileBinding(file)
}

// windowsFileBinding observes an already acquired object without reopening its
// pathname. The 128-bit FileIdInfo identity is needed for ReFS.
func windowsFileBinding(file *os.File) (string, string, error) {
	handle := windows.Handle(file.Fd())
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &info); err != nil {
		return "", "", err
	}
	if err := rejectWindowsNameSurrogate(handle, info.FileAttributes); err != nil {
		return "", "", err
	}
	var extended struct {
		VolumeSerialNumber uint64
		FileID             [16]byte
	}
	err := windows.GetFileInformationByHandleEx(handle, windows.FileIdInfo, (*byte)(unsafe.Pointer(&extended)), uint32(unsafe.Sizeof(extended)))
	if err != nil {
		if !errors.Is(err, windows.ERROR_INVALID_PARAMETER) && !errors.Is(err, windows.ERROR_NOT_SUPPORTED) && !errors.Is(err, windows.ERROR_INVALID_FUNCTION) {
			return "", "", err
		}
		extended.VolumeSerialNumber = uint64(info.VolumeSerialNumber)
		binary.LittleEndian.PutUint64(extended.FileID[:8], uint64(info.FileIndexHigh)<<32|uint64(info.FileIndexLow))
	}
	object, err := observedWindowsID(extended.VolumeSerialNumber, extended.FileID)
	if err != nil {
		return "", "", err
	}
	// FILE_BASIC_INFO contains four LARGE_INTEGER timestamps and attributes.
	var basic struct {
		CreationTime, LastAccessTime, LastWriteTime, ChangeTime int64
		FileAttributes                                          uint32
	}
	if err := windows.GetFileInformationByHandleEx(handle, windows.FileBasicInfo, (*byte)(unsafe.Pointer(&basic)), uint32(unsafe.Sizeof(basic))); err != nil {
		return "", "", err
	}
	if basic.LastWriteTime != int64(uint64(info.LastWriteTime.HighDateTime)<<32|uint64(info.LastWriteTime.LowDateTime)) {
		return "", "", ErrObservedSourceLost
	}
	// The change token is content identity: file ID, size and last write time.
	// ChangeTime also moves on attribute, ACL and archive-bit writes (backup and
	// antivirus tools), which change no byte (DECISIONS 29 Oct, never ctime).
	return object, fmt.Sprintf("fp1:%s:size:%d:mtime:%d", object, int64(info.FileSizeHigh)<<32|int64(info.FileSizeLow), basic.LastWriteTime), nil
}

func checkLocalRoot(path string) error {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return err
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return ErrRootLease
	}
	return nil
}

// Non-name-surrogate tags, including Data Deduplication, retain the same object;
// they are not junctions or links that redirect the acquired namespace.
func rejectWindowsNameSurrogate(handle windows.Handle, attributes uint32) error {
	if attributes&windows.FILE_ATTRIBUTE_REPARSE_POINT == 0 {
		return nil
	}
	var tag struct{ FileAttributes, ReparseTag uint32 }
	if err := windows.GetFileInformationByHandleEx(handle, windows.FileAttributeTagInfo, (*byte)(unsafe.Pointer(&tag)), uint32(unsafe.Sizeof(tag))); err != nil {
		return err
	}
	if tag.ReparseTag&0x20000000 != 0 {
		return ErrObservedSourceUnsupported
	}
	return nil
}
