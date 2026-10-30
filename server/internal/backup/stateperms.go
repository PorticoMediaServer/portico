package backup

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
)

// ErrStatePermissionsUnsupported prevents a POSIX chmod repair from claiming
// to tighten Windows ACLs.
var ErrStatePermissionsUnsupported = errors.New("state-folder access was not checked on this platform")

// CheckPermissions checks the POSIX traversal boundary of the state folder.
// A symlinked state path selects its resolved directory as the boundary.
// A private boundary protects even loose descendants, without walking caches,
// mounted libraries or recordings. Windows synthetic modes cannot assess ACLs.
func CheckPermissions(state string) (bool, error) {
	if runtime.GOOS == "windows" {
		return false, ErrStatePermissionsUnsupported
	}
	file, err := openStatePermissionBoundary(state)
	if err != nil {
		return false, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return false, err
	}
	return info.Mode().Perm()&0077 != 0, nil
}

// FixPermissions tightens only the acquired state directory, preserving owner
// bits. This blocks other accounts from traversing any of its contents without
// chmodding media, mount points, symlinks or hard-linked files beneath it.
func FixPermissions(state string) error {
	if runtime.GOOS == "windows" {
		return ErrStatePermissionsUnsupported
	}
	file, err := openStatePermissionBoundary(state)
	if err != nil {
		return err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	return file.Chmod(info.Mode().Perm() &^ 0077)
}

func openStatePermissionBoundary(state string) (*os.File, error) {
	resolved, err := filepath.EvalSymlinks(state)
	if err != nil {
		return nil, err
	}
	before, err := os.Lstat(resolved)
	if err != nil {
		return nil, err
	}
	if !before.IsDir() {
		return nil, errors.New("state permission boundary is not a directory")
	}
	file, err := os.Open(resolved)
	if err != nil {
		return nil, err
	}
	current, err := file.Stat()
	if err != nil || !os.SameFile(before, current) {
		file.Close()
		if err != nil {
			return nil, err
		}
		return nil, errors.New("state permission boundary changed")
	}
	return file, nil
}
