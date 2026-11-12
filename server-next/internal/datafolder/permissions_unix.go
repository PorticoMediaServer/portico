//go:build !windows

package datafolder

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
)

// others are the permission bits of the folder's group and of everyone else.
const others = 0o077

// opened is called between the look at the folder and its opening; tests change the folder
// there.
var opened = func() {}

// ReadableByOthers says whether accounts other than the server's can open the data folder:
// whether its own mode, after following a link to it, has any bit for its group or for
// everyone else. Only the folder itself is looked at: while it is private no other account
// can reach anything inside it, so nothing beneath it is walked. It is one look at one folder.
func ReadableByOthers(folder string) (bool, error) {
	f, err := open(folder)
	if err != nil {
		return false, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return false, err
	}
	return info.Mode().Perm()&others != 0, nil
}

// MakePrivate takes the group's and everyone else's bits off the data folder itself and keeps
// the owner's (0755 becomes 0700). Nothing beneath it is changed: no media, mount point, link
// or file linked from elsewhere. The folder is opened, and checked to be the one looked at,
// before the change, and the change is made through what was opened: a folder swapped in at
// that path, or a link put there, is refused and left as it is.
func MakePrivate(folder string) error {
	f, err := open(folder)
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	if info.Mode().Perm()&others == 0 {
		return nil
	}
	switch err := f.Chmod(info.Mode().Perm() &^ others); {
	case errors.Is(err, syscall.EPERM):
		return ErrNotOwner
	case errors.Is(err, syscall.EROFS):
		return ErrReadOnly
	default:
		return err
	}
}

// open opens the data folder as a folder, never through a link at its last step, and checks
// it is the folder looked at first. The data folder may itself be a link to the real one: that
// is followed once, before the look.
func open(folder string) (*os.File, error) {
	real, err := filepath.EvalSymlinks(folder)
	if err != nil {
		return nil, err
	}
	before, err := os.Lstat(real)
	if err != nil {
		return nil, err
	}
	if !before.IsDir() {
		return nil, ErrChanged
	}
	opened()
	f, err := os.OpenFile(real, os.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		if errors.Is(err, syscall.ELOOP) || errors.Is(err, syscall.ENOTDIR) || errors.Is(err, os.ErrNotExist) {
			return nil, ErrChanged
		}
		return nil, err
	}
	now, err := f.Stat()
	if err != nil || !os.SameFile(before, now) {
		f.Close()
		if err != nil {
			return nil, err
		}
		return nil, ErrChanged
	}
	return f, nil
}
