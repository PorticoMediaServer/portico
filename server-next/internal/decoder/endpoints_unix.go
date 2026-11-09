//go:build darwin || linux

package decoder

import (
	"net"
	"os"

	"golang.org/x/sys/unix"
)

// TCPListener.File returns a socket wrapper whose Fd method changes the shared
// open-file description to blocking mode. exec.Cmd calls Fd for ExtraFiles,
// which would also make the original listener's Accept uncancellable. Duplicate
// under RawConn's lifetime guard and use NewFile, which preserves O_NONBLOCK.
func duplicateEndpoint(listener *net.TCPListener) (*os.File, error) {
	raw, err := listener.SyscallConn()
	if err != nil {
		return nil, err
	}
	fd := -1
	var duplicateErr error
	if err = raw.Control(func(source uintptr) {
		fd, duplicateErr = unix.FcntlInt(source, unix.F_DUPFD_CLOEXEC, 0)
	}); err != nil {
		return nil, err
	}
	if duplicateErr != nil {
		return nil, duplicateErr
	}
	return os.NewFile(uintptr(fd), "decoder-endpoint-reservation"), nil
}
