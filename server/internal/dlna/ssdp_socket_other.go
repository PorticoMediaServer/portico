//go:build !linux

package dlna

import "net"

// Other systems deliver a group's datagrams only to sockets that joined it on
// the receiving interface.
func ssdpSocketOptions(*net.UDPConn) error { return nil }
