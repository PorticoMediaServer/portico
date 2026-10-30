//go:build !linux && !darwin && !freebsd && !openbsd && !netbsd

package networking

import (
	"errors"
	"net"
)

func discoverySocketOptions(*net.UDPConn, bool) error {
	return errors.New("on-link multicast unavailable on this platform")
}
func discoveryOnLink([]byte) bool { return false }

func discoveryTruncated(flags int) bool { return true }
