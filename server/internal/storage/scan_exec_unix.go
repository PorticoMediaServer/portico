//go:build linux || darwin

package storage

import (
	"errors"
	"runtime"
	"syscall"

	"github.com/PorticoMediaServer/portico/server/internal/mediaexec"
)

// descriptorInput is how a media tool names inherited descriptor 3.
var descriptorInput = func() string {
	if runtime.GOOS == "darwin" {
		return "/dev/fd/3"
	}
	return "/proc/self/fd/3"
}()

// scanCPUSeconds bounds one scan-time probe or artwork extraction: generous for
// a slow disk and a large file, fatal only to a pathological input.
const scanCPUSeconds = 300

// descriptorDirectoryArgv rejects a directory command that is not the closed
// bubblewrap invocation mounting descriptor 3. The helper never accepts a
// directory read that does not go through that mount.
func descriptorDirectoryArgv(argv []string) bool {
	for i := 0; i+1 < len(argv); i++ {
		if argv[i] == "--ro-bind-fd" {
			return argv[i+1] == "3"
		}
	}
	return false
}

// inventoryDirectoryCommandHelper is the descriptor-bound disc folder read:
// it opens the admitted directory and hands the sandbox one directory
// descriptor, which bubblewrap mounts read-only at descriptorDirectoryRoot.
func inventoryDirectoryCommandHelper(r request) error {
	if err := validateInventoryCommand(r); err != nil {
		return err
	}
	if !descriptorDirectoryArgv(r.Argv) {
		return errors.New("directory scan requires the descriptor mount")
	}
	dir, err := openInventoryDirectory(*r.Inventory)
	if err != nil {
		return err
	}
	defer dir.Close()
	if int(dir.Fd()) != 3 {
		if err = dupTo(int(dir.Fd()), 3); err != nil {
			return err
		}
		defer syscall.Close(3)
	}
	if _, _, errno := syscall.Syscall(syscall.SYS_FCNTL, 3, syscall.F_SETFD, 0); errno != 0 {
		return errno
	}
	return mediaexec.ExecArgv(r.Argv)
}

func inventoryCommandHelper(r request) error {
	if err := validateInventoryCommand(r); err != nil {
		return err
	}
	file, err := openInventoryContent(*r.Inventory)
	if err != nil {
		return err
	}
	defer file.Close()
	if int(file.Fd()) != 3 {
		if err = dupTo(int(file.Fd()), 3); err != nil {
			return err
		}
		defer syscall.Close(3)
	}
	// Dup2 clears CLOEXEC except when source and destination are identical.
	if _, _, errno := syscall.Syscall(syscall.SYS_FCNTL, 3, syscall.F_SETFD, 0); errno != 0 {
		return errno
	}
	// This helper is already its own supervised process group; the tool replaces
	// it, so no unmanaged grandchild can outlive the supervisor's Wait.
	return mediaexec.ExecArgv(r.Argv)
}
