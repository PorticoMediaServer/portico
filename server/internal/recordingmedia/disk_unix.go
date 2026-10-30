//go:build !windows

package recordingmedia

import (
	"github.com/PorticoMediaServer/portico/server/internal/livechannels/dvr"
	"syscall"
)

func freeBytes(path string) (int64, error) {
	var s syscall.Statfs_t
	if e := syscall.Statfs(path, &s); e != nil {
		return 0, e
	}
	n := uint64(s.Bavail)
	block := uint64(s.Bsize)
	if block == 0 || n > (uint64(1)<<63-1)/block {
		return 0, dvr.ErrStorageUnavailable
	}
	return int64(n * block), nil
}
