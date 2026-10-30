//go:build windows

package storage

import (
	"os"
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

const observedPlaybackSupported = true

func configureObservedRoot(cmd *exec.Cmd, file *os.File, req *request) (func(), error) {
	var handle windows.Handle
	if err := windows.DuplicateHandle(windows.CurrentProcess(), windows.Handle(file.Fd()), windows.CurrentProcess(), &handle, 0, true, windows.DUPLICATE_SAME_ACCESS); err != nil {
		return nil, err
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{AdditionalInheritedHandles: []syscall.Handle{syscall.Handle(handle)}}
	req.RootHandle = uint64(handle)
	return func() { windows.CloseHandle(handle) }, nil
}

func inheritedObservedRoot(req request) *os.File {
	if req.RootHandle == 0 {
		return nil
	}
	return os.NewFile(uintptr(req.RootHandle), "registered-source-root")
}

func sameObservedRoot(registered *os.File, root *os.Root, _, _ os.FileInfo) bool {
	current, err := root.Open(".")
	if err != nil {
		return false
	}
	defer current.Close()
	a, _, err := windowsFileBinding(registered)
	if err != nil {
		return false
	}
	b, _, err := windowsFileBinding(current)
	return err == nil && a == b
}

func observedFileBinding(file *os.File) (string, string, error) { return windowsFileBinding(file) }
