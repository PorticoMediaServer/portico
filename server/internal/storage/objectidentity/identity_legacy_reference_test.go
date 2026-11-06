// Frozen pre-extraction reference for differential regression only.
// Source: 4e92e38b48f56caa2c7ad096f32587772b7150a0 server/internal/storage/stable_identity.go
// Source SHA256: 4645a5959f8f7073be353a817094b5d9e167adbb9debb82d9b29c9c14c70da14
// Keep this independent of the production identity functions.
package objectidentity

import (
	"strconv"
	"strings"
)

// A Unix object identity is "dev:<st_dev>:ino:<st_ino>". The inode number
// belongs to the file; the device number belongs to this boot: network, FUSE
// and pooled mounts, btrfs subvolumes, ZFS datasets and removable disks get
// whatever number the kernel hands out in mount or detection order, so the
// same folder can come back under another one after a restart. Anything kept
// across restarts therefore compares the inode and treats a device number as
// evidence only within one observation.

// legacyUnixIdentity splits "dev:D:ino:N"; ok is false for every other form
// (a Windows volume and file id, a remote object id, an empty identity).
func legacyUnixIdentity(identity string) (device, inode uint64, ok bool) {
	rest, found := strings.CutPrefix(identity, "dev:")
	if !found {
		return 0, 0, false
	}
	d, i, found := strings.Cut(rest, ":ino:")
	if !found {
		return 0, 0, false
	}
	device, err := strconv.ParseUint(d, 10, 64)
	if err != nil {
		return 0, 0, false
	}
	i, _, _ = strings.Cut(i, ":fsid:")
	inode, err = strconv.ParseUint(i, 10, 64)
	if err != nil {
		return 0, 0, false
	}
	return device, inode, true
}

// legacySameRoot reports whether a recorded root identity and a freshly observed one
// name the same folder. A changed device number requires a matching nonzero
// filesystem identity as well as the root inode. Without that evidence only
// an exact identity matches; accepting a replacement remains an owner action.
func legacySameRoot(recorded, observed string) bool {
	if recorded == observed {
		return recorded != ""
	}
	_, a, okA := legacyUnixIdentity(recorded)
	_, b, okB := legacyUnixIdentity(observed)
	_, fsA, hasA := strings.Cut(recorded, ":fsid:")
	_, fsB, hasB := strings.Cut(observed, ":fsid:")
	return okA && okB && a == b && hasA && hasB && fsA != "" && fsA == fsB
}

// legacyLocalChangeToken is the change token of a local observation: the object's
// inode, size and modification time. The file's ctime is deliberately absent.
// A permission, ownership or ACL write moves ctime without touching a byte, so
// it is not content evidence and must neither refuse a current source nor
// republish it. The device number is absent for the same reason as in
// legacyStableObjectIdentity: a reboot may rename the filesystem. A real edit moves
// the modification time (and usually the size or inode), which is what
// republishes a new revision.
func legacyLocalChangeToken(inode uint64, size, modifiedNS int64) string {
	return "fp1:ino:" + strconv.FormatUint(inode, 10) + ":size:" + strconv.FormatInt(size, 10) + ":mtime:" + strconv.FormatInt(modifiedNS, 10)
}

// legacyStableObjectIdentity is the identity of an object under a root in the form
// that is kept across restarts. An object on the root's own filesystem is named
// by its inode alone ("ino:N"); one on another filesystem mounted beneath the
// root keeps its device number, because inode numbers repeat across
// filesystems. Identities of any other form are returned unchanged.
func legacyStableObjectIdentity(object, root string) string {
	device, inode, ok := legacyUnixIdentity(object)
	if !ok {
		return object
	}
	rootDevice, _, ok := legacyUnixIdentity(root)
	if !ok || rootDevice != device {
		return object
	}
	return "ino:" + strconv.FormatUint(inode, 10)
}
