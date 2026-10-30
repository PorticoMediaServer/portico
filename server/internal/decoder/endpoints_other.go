//go:build !darwin && !linux

package decoder

import (
	"net"
	"os"
)

// Windows cannot hand a child a listening socket through ExtraFiles, and does
// not need to: the server's job object ends every decoder with the server, so a
// crashed server's ports can't be held by an orphan. The reservation still
// fences the endpoint for the process's lifetime in this server.
func duplicateEndpoint(*net.TCPListener) (*os.File, error) { return nil, nil }
