//go:build linux || darwin

package storage

import (
	"os"
	"os/exec"
)

const observedPlaybackSupported = true

func configureObservedRoot(cmd *exec.Cmd, file *os.File, _ *request) (func(), error) {
	cmd.ExtraFiles = []*os.File{file}
	return func() {}, nil
}
func inheritedObservedRoot(request) *os.File { return os.NewFile(3, "registered-source-root") }
func sameObservedRoot(_ *os.File, _ *os.Root, registered, current os.FileInfo) bool {
	return os.SameFile(registered, current)
}
func observedFileBinding(file *os.File) (string, string, error) {
	info, err := file.Stat()
	if err != nil {
		return "", "", err
	}
	object, token, err := nativeObjectBinding(info)
	if err == nil && info.IsDir() {
		object, err = directoryFilesystemIdentityFromFile(file, object)
	}
	return object, token, err
}
