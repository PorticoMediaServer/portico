//go:build windows

package storage

import "errors"

func isMount(path string) (bool, error) {
	return false, errors.New("managed mount observation is not supported on this platform")
}

func DirectoryIdentity(path string) (string, error) {
	return "", errors.New("directory identity unsupported")
}
