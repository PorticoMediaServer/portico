//go:build linux || darwin

package mounts

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"os"
	"os/exec"
	"runtime"

	"golang.org/x/sys/unix"
)

// CacheVolumeKey identifies the filesystem that actually backs a validated
// private storage directory. It is process-local accounting identity, never
// source identity, a path or permission to write there.
func CacheVolumeKey(path string) (string, error) {
	var stat unix.Statfs_t
	if err := unix.Statfs(path, &stat); err != nil {
		return "", err
	}
	var fsid [8]byte
	binary.BigEndian.PutUint32(fsid[:4], uint32(stat.Fsid.Val[0]))
	binary.BigEndian.PutUint32(fsid[4:], uint32(stat.Fsid.Val[1]))
	hash := sha256.New()
	hash.Write([]byte("portico-cache-volume-v1\x00" + runtime.GOOS + "\x00"))
	hash.Write(fsid[:])
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func cacheSpaceAvailable(path string, floor int64) bool {
	var stat unix.Statfs_t
	return unix.Statfs(path, &stat) == nil && uint64(stat.Bavail)*uint64(stat.Bsize) >= uint64(floor)
}
func MountSupported() bool {
	if runtime.GOOS == "linux" {
		if _, e := os.Stat("/dev/fuse"); e != nil {
			return false
		}
		if _, e := exec.LookPath("fusermount3"); e == nil {
			return true
		}
		_, e := exec.LookPath("fusermount")
		return e == nil
	}
	if runtime.GOOS == "darwin" {
		_, e := os.Stat("/Library/Filesystems/macfuse.fs")
		return e == nil
	}
	return false
}

func cacheSpaceFor(path string, floor, growth int64) bool {
	var stat unix.Statfs_t
	if unix.Statfs(path, &stat) != nil {
		return false
	}
	free := uint64(stat.Bavail) * uint64(stat.Bsize)
	if uint64(growth) >= free {
		return false
	}
	return free-uint64(growth) >= uint64(floor)
}
