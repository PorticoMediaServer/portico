//go:build !windows

package datafolder

import (
	"errors"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
)

// others are the permission bits of the folder's group and of everyone else.
const others = 0o077

// Seams for the tests: opened is called between the look at the folder and its opening, where
// a test changes the folder; chmod stands for a drive that does not keep a change of mode.
var (
	opened = func() {}
	chmod  = func(f *os.File, mode os.FileMode) error { return f.Chmod(mode) }
)

// Look says whether accounts other than the folder's owner can open the data folder: whether
// its own mode, after following a link to it, has any bit for its group or for everyone else.
// Only the folder itself is looked at: while it is private no other account can reach anything
// inside it, so nothing beneath it is walked. While it is open, Look also says whether a fix
// can be made. That is known for this folder after one trial on a small file made and removed
// in it, kept for as long as the server runs, or after a fix was tried.
func Look(folder string) (State, error) {
	f, err := open(folder)
	if err != nil {
		return State{}, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return State{}, err
	}
	st := State{ReadableByOthers: info.Mode().Perm()&others != 0, Owner: ownerName(info)}
	if st.ReadableByOthers {
		st.Why = fixable(f, info)
		st.Fixable = st.Why == nil
	}
	return st, nil
}

// MakePrivate takes the group's and everyone else's bits off the data folder itself and keeps
// the owner's (0755 becomes 0700). Nothing beneath it is changed: no media, mount point, link
// or file linked from elsewhere. The folder is opened, and checked to be the one looked at,
// before the change, and the change is made through what was opened: a folder swapped in at
// that path, or a link put there, is refused and left as it is. The folder is looked at again
// after the change: a drive that accepts the change and does not keep it is refused as well.
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
	err = refusal(info, chmod(f, info.Mode().Perm()&^others))
	if err == nil {
		after, statErr := f.Stat()
		switch {
		case statErr != nil:
			return statErr
		case after.Mode().Perm()&others != 0:
			err = ErrNotKept
		}
	}
	if errors.Is(err, ErrNotKept) || errors.Is(err, ErrReadOnly) {
		remember(info, err)
	}
	return err
}

// refusal says why a change of mode was refused: a folder the server's account does not own,
// or one it owns on a drive that does not allow the change, or a read-only file system.
func refusal(info os.FileInfo, err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, syscall.EROFS):
		return ErrReadOnly
	case errors.Is(err, syscall.EPERM):
		if owns(info) {
			return ErrNotKept
		}
		return ErrNotOwner
	}
	return err
}

// owns says whether the server's account may change the folder's mode: it owns it, or it is
// the system's administrator.
func owns(info os.FileInfo) bool {
	st, ok := info.Sys().(*syscall.Stat_t)
	euid := os.Geteuid()
	return ok && (euid == 0 || uint32(euid) == st.Uid)
}

// ownerName names the account owning the folder, or gives its number when it has no name.
func ownerName(info os.FileInfo) string {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return ""
	}
	id := strconv.FormatUint(uint64(st.Uid), 10)
	if u, err := user.LookupId(id); err == nil {
		return u.Username
	}
	return id
}

// tried is what is known of each folder's drive (by device and inode): nil when it keeps a
// change of mode, else why not.
var tried sync.Map

type folderID struct{ dev, ino uint64 }

func idOf(info os.FileInfo) (folderID, bool) {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return folderID{}, false
	}
	return folderID{uint64(st.Dev), uint64(st.Ino)}, true
}

func remember(info os.FileInfo, err error) {
	if id, ok := idOf(info); ok {
		tried.Store(id, err)
	}
}

// fixable says why a fix of the open folder cannot be made, or nil. A folder the server's
// account does not own cannot; otherwise a small file made in it is given a private mode, and
// whether its drive kept it is kept for the folder.
func fixable(f *os.File, info os.FileInfo) error {
	if !owns(info) {
		return ErrNotOwner
	}
	id, ok := idOf(info)
	if !ok {
		return nil
	}
	if known, ok := tried.Load(id); ok {
		err, _ := known.(error)
		return err
	}
	err := trial(f.Name(), info)
	tried.Store(id, err)
	return err
}

// trial makes a small file in the folder, gives it a mode others can read and then takes their
// bits off, and looks whether the drive kept each. The file is removed.
func trial(folder string, info os.FileInfo) error {
	file, err := os.CreateTemp(folder, ".portico-mode-check-*")
	if err != nil {
		if errors.Is(err, syscall.EROFS) {
			return ErrReadOnly
		}
		return nil // the fix will say why itself
	}
	defer os.Remove(file.Name())
	defer file.Close()
	// Each mode is looked at once it is given: a drive with one fixed mode for every file keeps
	// one of the two and not the other.
	for _, want := range []os.FileMode{0o644, 0o600} {
		if err := chmod(file, want); err != nil {
			return refusal(info, err)
		}
		if after, err := file.Stat(); err == nil && after.Mode().Perm() != want {
			return ErrNotKept
		}
	}
	return nil
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
