package portmap

import (
	"net/netip"
	"syscall"
	"testing"
)

func TestDarwinDefaultGateway(t *testing.T) {
	addresses := make([]syscall.Sockaddr, syscall.RTAX_MAX)
	addresses[syscall.RTAX_DST] = &syscall.SockaddrInet6{}
	addresses[syscall.RTAX_GATEWAY] = &syscall.SockaddrInet6{Addr: [16]byte{0xfe, 0x80, 0, 7, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1}}
	addresses[syscall.RTAX_NETMASK] = &syscall.SockaddrInet6{}
	if got := darwinDefaultGateway(addresses, syscall.AF_INET6); got != netip.MustParseAddr("fe80::1") {
		t.Fatal("embedded interface index reached the gateway", got)
	}
	addresses[syscall.RTAX_NETMASK] = &syscall.SockaddrInet6{Addr: [16]byte{0xff}}
	if got := darwinDefaultGateway(addresses, syscall.AF_INET6); got.IsValid() {
		t.Fatal("used a non-default route", got)
	}
	addresses[syscall.RTAX_DST] = &syscall.SockaddrInet4{}
	addresses[syscall.RTAX_GATEWAY] = &syscall.SockaddrInet4{Addr: [4]byte{192, 168, 1, 1}}
	addresses[syscall.RTAX_NETMASK] = nil
	if got := darwinDefaultGateway(addresses, syscall.AF_INET); got != netip.MustParseAddr("192.168.1.1") {
		t.Fatal(got)
	}
}
