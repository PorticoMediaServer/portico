package stateownership

import "os"

// The scan opening requires an exact native VFS/file-key mapping. No Unix
// device/inode comparison is inferred for a Windows file handle.
func SingleLinkedFileIdentity(os.FileInfo) (device, inode uint64, err error) {
	return 0, 0, ErrUnsupported
}
