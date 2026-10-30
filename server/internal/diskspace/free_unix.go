//go:build !windows

package diskspace

import (
	"errors"
	"syscall"
)

func freeBytes(path string) (int64, error) {
	var s syscall.Statfs_t
	if e := syscall.Statfs(path, &s); e != nil {
		return 0, e
	}
	available := uint64(s.Bavail)
	block := uint64(s.Bsize)
	if block == 0 || available > (uint64(1)<<63-1)/block {
		return 0, errors.New("diskspace: implausible filesystem geometry")
	}
	return int64(available * block), nil
}
