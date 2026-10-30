//go:build linux || darwin

package storage

import (
	"io"
	"os"
	"syscall"
)

// helperInput reads the request through the runtime poller, as a server
// helper's start can: the poller is then created first and takes descriptor 3,
// the one a scan command moves its media file to.
func helperInput() io.Reader {
	if syscall.SetNonblock(0, true) != nil {
		return os.Stdin
	}
	return os.NewFile(0, "stdin")
}
