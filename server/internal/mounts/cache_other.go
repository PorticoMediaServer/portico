//go:build !linux && !darwin

package mounts

import "errors"

// CacheVolumeKey refuses platforms without the filesystem identity primitive.
func CacheVolumeKey(string) (string, error) {
	return "", errors.New("cache volume identity is unavailable on this platform")
}

func cacheSpaceAvailable(string, int64) bool { return false }
func MountSupported() bool                   { return false }

func cacheSpaceFor(string, int64, int64) bool { return false }
