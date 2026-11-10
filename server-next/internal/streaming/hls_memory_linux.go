//go:build linux

package streaming

import (
	"errors"
	"syscall"

	"github.com/PorticoMediaServer/portico/server-next/internal/diskspace"
	"github.com/PorticoMediaServer/portico/server-next/internal/hostlimits"
)

func checkMemoryOutput(root string) error {
	var fs syscall.Statfs_t
	if err := syscall.Statfs(root, &fs); err != nil {
		return err
	}
	if fs.Type != 0x01021994 {
		return errors.New("memory conversion output requires Linux tmpfs")
	}
	return nil
}

func readMemoryFacts(root string) (memoryFacts, error) {
	available, total, err := hostlimits.AvailableMemory()
	if err != nil {
		return memoryFacts{}, err
	}
	free, err := diskspace.Free(root)
	if err != nil {
		return memoryFacts{}, err
	}
	var fs syscall.Statfs_t
	if err = syscall.Statfs(root, &fs); err != nil {
		return memoryFacts{}, err
	}
	block := int64(fs.Bsize)
	if fs.Type != 0x01021994 || block <= 0 || fs.Blocks > uint64((1<<63-1)/block) {
		return memoryFacts{}, errors.New("unbounded memory filesystem")
	}
	return memoryFacts{available: available, total: total, free: free, filesystemLimit: int64(fs.Blocks) * block}, nil
}
