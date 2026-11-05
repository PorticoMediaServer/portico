package qual

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

func captureNamespaces() map[string]NamespaceEvidence {
	out := map[string]NamespaceEvidence{}
	for key, name := range namespacePaths {
		var n NamespaceEvidence
		f, err := os.Open(name)
		if err != nil {
			n.Errors = append(n.Errors, "open: "+err.Error())
			out[key] = n
			continue
		}
		var stat unix.Stat_t
		var fs unix.Statfs_t
		if err := unix.Fstat(int(f.Fd()), &stat); err != nil {
			n.Errors = append(n.Errors, "fstat: "+err.Error())
		} else {
			n.Inode, n.Device = stat.Ino, uint64(stat.Dev)
		}
		if err := unix.Fstatfs(int(f.Fd()), &fs); err != nil {
			n.Errors = append(n.Errors, "fstatfs: "+err.Error())
		} else {
			n.Filesystem = uint64(fs.Type)
		}
		typ, err := unix.IoctlRetInt(int(f.Fd()), unix.NS_GET_NSTYPE)
		if err != nil {
			n.Errors = append(n.Errors, "NS_GET_NSTYPE: "+err.Error())
		} else {
			n.Type = uint64(typ)
		}
		if err := f.Close(); err != nil {
			n.Errors = append(n.Errors, fmt.Sprintf("close: %v", err))
		}
		out[key] = n
	}
	for key, name := range procfsPaths {
		out[key] = captureProcfsDirectory(name)
	}
	return out
}

func captureProcfsDirectory(name string) NamespaceEvidence {
	var n NamespaceEvidence
	f, err := os.Open(name)
	if err != nil {
		n.Errors = append(n.Errors, "open: "+err.Error())
		return n
	}
	var stat unix.Stat_t
	var fs unix.Statfs_t
	if err := unix.Fstat(int(f.Fd()), &stat); err != nil {
		n.Errors = append(n.Errors, "fstat: "+err.Error())
	} else {
		n.Inode, n.Device = stat.Ino, uint64(stat.Dev)
	}
	if err := unix.Fstatfs(int(f.Fd()), &fs); err != nil {
		n.Errors = append(n.Errors, "fstatfs: "+err.Error())
	} else {
		n.Filesystem = uint64(fs.Type)
	}
	if err := f.Close(); err != nil {
		n.Errors = append(n.Errors, "close: "+err.Error())
	}
	return n
}
