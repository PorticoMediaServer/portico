//go:build linux

package storage

import (
	"fmt"
	"os"
	"github.com/PorticoMediaServer/portico/server/internal/mediasource"
	"syscall"
)

// localVersionEvidence is content identity: device, inode, size and mtime,
// never ctime (DECISIONS 29 Oct, "File change detection ignores ctime"). A
// permission, ownership or ACL write must not end playback or move a version.
// The revision keeps its three-field mtime:ctime-sec:ctime-nsec shape with
// the retired ctime fields fixed at zero, so every parser stays unchanged.
func localVersionEvidence(info os.FileInfo, scope string) (mediasource.Evidence, error) {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.Mode().IsRegular() {
		return mediasource.Evidence{}, mediasource.ErrIdentityRequired
	}
	return mediasource.Evidence{Kind: mediasource.LocalRevision, Scope: scope, Object: fmt.Sprintf("dev:%d:ino:%d", st.Dev, st.Ino), Revision: fmt.Sprintf("%d:0:0", info.ModTime().UnixNano()), Size: info.Size(), NoInPlaceMutation: true}, nil
}
