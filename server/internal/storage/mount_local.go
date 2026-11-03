package storage

import "strings"

type mountEntry struct {
	point  string
	remote bool
}

// localUnder reports whether everything at and beneath path is on local
// filesystems: the longest mount point containing path is local, and no
// network or FUSE mount is mounted anywhere beneath it. A scan walks the whole
// root, so one NFS share mounted inside a local library would otherwise be
// walked in process, where a hung syscall can't be killed.
func localUnder(path string, mounts []mountEntry) bool {
	best, remote := -1, true
	for _, m := range mounts {
		switch {
		case path == m.point || m.point == "/" || strings.HasPrefix(path, m.point+"/"):
			if len(m.point) > best {
				best, remote = len(m.point), m.remote
			}
		case strings.HasPrefix(m.point, path+"/") || path == "/":
			if m.remote {
				return false
			}
		}
	}
	return best >= 0 && !remote
}
