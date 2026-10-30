//go:build linux || darwin || freebsd || openbsd || netbsd

package networking

import (
	"encoding/binary"
	"net"
	"syscall"
)

func discoverySocketOptions(c *net.UDPConn, v6 bool) error {
	raw, e := c.SyscallConn()
	if e != nil {
		return e
	}
	var inner error
	e = raw.Control(func(fd uintptr) {
		if v6 {
			inner = syscall.SetsockoptInt(int(fd), syscall.IPPROTO_IPV6, syscall.IPV6_MULTICAST_HOPS, 255)
			if inner == nil {
				inner = syscall.SetsockoptInt(int(fd), syscall.IPPROTO_IPV6, discoveryReceiveHopLimit, 1)
			}
		} else {
			inner = syscall.SetsockoptInt(int(fd), syscall.IPPROTO_IP, syscall.IP_MULTICAST_TTL, 255)
			if inner == nil {
				inner = syscall.SetsockoptInt(int(fd), syscall.IPPROTO_IP, syscall.IP_RECVTTL, 1)
			}
		}
	})
	if e != nil {
		return e
	}
	return inner
}
func discoveryTruncated(flags int) bool { return flags&(syscall.MSG_TRUNC|syscall.MSG_CTRUNC) != 0 }
func discoveryOnLink(oob []byte) bool {
	messages, e := syscall.ParseSocketControlMessage(oob)
	if e != nil {
		return false
	}
	for _, m := range messages {
		if m.Header.Level == syscall.IPPROTO_IP && (m.Header.Type == syscall.IP_TTL || m.Header.Type == syscall.IP_RECVTTL) || m.Header.Level == syscall.IPPROTO_IPV6 && m.Header.Type == discoveryHopLimit {
			if len(m.Data) == 1 {
				return m.Data[0] == 255
			}
			if len(m.Data) < 4 {
				return false
			}
			return binary.NativeEndian.Uint32(m.Data) == 255
		}
	}
	return false
}
