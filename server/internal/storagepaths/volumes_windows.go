package storagepaths

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

func distinctNativeVolumes(a, b string) (bool, error) {
	left, err := nativeVolumeIdentity(a)
	if err != nil {
		return false, err
	}
	right, err := nativeVolumeIdentity(b)
	if err != nil {
		return false, err
	}
	return !strings.EqualFold(left, right), nil
}

func nativeVolumeIdentity(path string) (string, error) {
	// GetVolumePathName needs an existing path. Resolve the nearest existing
	// ancestor of an owner-chosen folder that has not been created yet.
	for {
		_, err := os.Stat(path)
		if err == nil {
			break
		}
		if !errors.Is(err, os.ErrNotExist) || path == filepath.Dir(path) {
			return "", err
		}
		path = filepath.Dir(path)
	}
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return "", err
	}
	var mount [32768]uint16
	if err := windows.GetVolumePathName(name, &mount[0], uint32(len(mount))); err != nil {
		return "", err
	}
	var identity [128]uint16
	// Volume GUIDs compare the underlying local volume, including drive aliases.
	// Remote UNC identities cannot be verified with this API and remain refused.
	if err := windows.GetVolumeNameForVolumeMountPoint(&mount[0], &identity[0], uint32(len(identity))); err != nil {
		return "", err
	}
	return windows.UTF16ToString(identity[:]), nil
}
