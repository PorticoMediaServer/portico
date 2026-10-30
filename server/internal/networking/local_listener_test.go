package networking

import (
	"net"
	"strconv"
	"testing"
)

func TestLoopbackHTTPURLUsesActualBoundPort(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	port := listener.Addr().(*net.TCPAddr).Port
	if got := LoopbackHTTPURL(listener.Addr()); got != "http://localhost:"+strconv.Itoa(port) {
		t.Fatal("URL did not use the actual bound listener", got)
	}
}

func TestLoopbackHTTPURLOmitsBindingsThatDoNotServeLocalhost(t *testing.T) {
	for _, address := range []net.Addr{nil, (*net.TCPAddr)(nil), &net.UDPAddr{IP: net.IPv4zero, Port: 1234}, &net.TCPAddr{Port: 0}, &net.TCPAddr{Port: 65536}, &net.TCPAddr{IP: net.ParseIP("100.120.50.24"), Port: 1234}, &net.TCPAddr{IP: net.ParseIP("192.168.1.20"), Port: 1234}, &net.TCPAddr{IP: net.ParseIP("127.0.0.2"), Port: 1234}, &net.TCPAddr{IP: net.IPv6loopback, Port: 1234, Zone: "eth0"}} {
		if got := LoopbackHTTPURL(address); got != "" {
			t.Fatal("invented a localhost URL for a different binding", address, got)
		}
	}
	for _, ip := range []net.IP{nil, net.IPv4zero, net.IPv6unspecified, net.IPv4(127, 0, 0, 1), net.IPv6loopback} {
		if got := LoopbackHTTPURL(&net.TCPAddr{IP: ip, Port: 80}); got != "http://localhost" {
			t.Fatal("missing canonical localhost URL for actual binding", ip, got)
		}
	}
}
