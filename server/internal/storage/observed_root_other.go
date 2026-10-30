//go:build !linux && !darwin && !windows

package storage

import (
	"os"
	"os/exec"
)

const observedPlaybackSupported = false

func configureObservedRoot(*exec.Cmd, *os.File, *request) (func(), error) {
	return nil, ErrObservedSourceUnsupported
}
func inheritedObservedRoot(request) *os.File                             { return nil }
func sameObservedRoot(*os.File, *os.Root, os.FileInfo, os.FileInfo) bool { return false }
func observedFileBinding(*os.File) (string, string, error) {
	return "", "", ErrObservedSourceUnsupported
}
