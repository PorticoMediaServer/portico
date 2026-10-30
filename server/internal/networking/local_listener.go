package networking

import (
	"net"
	"strconv"
)

// LoopbackHTTPURL reports only a bound TCP listener that actually serves the
// literal localhost route. It is not derived from a default, proxy header,
// configured public address or the port the browser happened to use.
func LoopbackHTTPURL(addr net.Addr) string {
	tcp, ok := addr.(*net.TCPAddr)
	if !ok || tcp == nil || tcp.Port < 1 || tcp.Port > 65535 || tcp.Zone != "" {
		return ""
	}
	ip := tcp.IP
	if len(ip) != 0 && !ip.IsUnspecified() && !ip.Equal(net.IPv4(127, 0, 0, 1)) && !ip.Equal(net.IPv6loopback) {
		return ""
	}
	if tcp.Port == 80 {
		return "http://localhost"
	}
	return "http://localhost:" + strconv.Itoa(tcp.Port)
}
