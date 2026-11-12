// Package datafolder looks after the server's data folder itself: whether other accounts on
// the computer can open it, and making it private again when the owner asks (12-console rules
// 10 and 75). The data folder holds the database, the token keys and the setup code.
package datafolder

import "errors"

// Why a fix was refused, or cannot be made, in words the owner can act on.
var (
	// ErrNotOwner: the server's account does not own the folder, so it may not change who
	// can open it. Whoever owns it can, or the server can be run as that account.
	ErrNotOwner = errors.New("the server's account does not own the data folder, so it cannot change who may open it")
	// ErrNotKept: the folder's drive does not keep who may open a folder (a share mounted
	// without Unix permissions, a FAT, exFAT or NTFS drive): a change is not kept, or not
	// permitted, there.
	ErrNotKept = errors.New("this drive cannot limit who opens the folder. Move Portico's data folder to a drive that can")
	// ErrReadOnly: the folder is on a read-only file system.
	ErrReadOnly = errors.New("the data folder is on a read-only file system, so who may open it cannot be changed")
	// ErrChanged: the folder at that path was replaced between the look and the change, so
	// nothing was changed.
	ErrChanged = errors.New("the data folder was replaced while it was being changed, so nothing was changed; look again")
	// ErrNotChecked: on this system the folder's mode bits do not say who may open it.
	ErrNotChecked = errors.New("who may open the data folder is not checked on this system")
)

// State is who may open the data folder now.
type State struct {
	// ReadableByOthers is true while accounts other than the folder's owner can open it.
	ReadableByOthers bool
	// Owner is the account that owns the folder: after a fix, only it can open the folder.
	Owner string
	// Fixable is whether Fix permissions can make it private; Why is the reason it cannot
	// (ErrNotOwner, ErrNotKept or ErrReadOnly). Both are said only while ReadableByOthers.
	Fixable bool
	Why     error
}
