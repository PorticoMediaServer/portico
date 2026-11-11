package dlna

import (
	"golang.org/x/sys/unix"
	"net"
)

// Linux hands a multicast datagram to every socket bound to the port, whatever
// interface it joined the group on. Turning that off keeps each interface's
// socket to its own interface's searches, so its answers carry that
// interface's address.
func ssdpSocketOptions(c *net.UDPConn) error {
	raw, err := c.SyscallConn()
	if err != nil {
		return err
	}
	var inner error
	if err = raw.Control(func(fd uintptr) {
		inner = unix.SetsockoptInt(int(fd), unix.IPPROTO_IP, unix.IP_MULTICAST_ALL, 0)
	}); err != nil {
		return err
	}
	return inner
}
