package administration

import (
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/storagepaths"
)

// This is a positive observation, not a guarantee that unrecognized filesystems
// are local. Resolve aliases before choosing the deepest containing mount.
func observedNetworkDirectory(directory string) *bool {
	resolved, err := filepath.EvalSymlinks(directory)
	if err != nil {
		return nil
	}
	mounts, err := storagepaths.CurrentMounts()
	if err != nil {
		return nil
	}
	switch storagepaths.Filesystem(resolved, mounts) {
	case "fuse.shfs", "nfs", "nfs4", "cifs", "smb3", "smbfs", "fuse.sshfs":
		network := true
		return &network
	default:
		return nil
	}
}
