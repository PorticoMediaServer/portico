package portmap

import (
	"net"
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

func TestDarwinIPv6ClientPrefersStable(t *testing.T) {
	temporary, stable := netip.MustParseAddr("2001:db8::10"), netip.MustParseAddr("2001:db8::20")
	addrs := []net.Addr{&net.IPNet{IP: net.IP(temporary.AsSlice()), Mask: net.CIDRMask(64, 128)},
		&net.IPNet{IP: net.IP(stable.AsSlice()), Mask: net.CIDRMask(64, 128)}}
	bits := map[netip.Addr]uint32{temporary: 0x0080, stable: 0}
	flags := func(ip netip.Addr) (uint32, error) { return bits[ip], nil }
	if got := darwinIPv6Client(addrs, netip.Addr{}, flags); got != stable {
		t.Fatal("temporary address won over stable", got)
	}
	bits[stable] = 0x0010 // deprecated: temporary is now the only usable address
	if got := darwinIPv6Client(addrs, netip.Addr{}, flags); got != temporary {
		t.Fatal("no temporary fallback", got)
	}
	bits[temporary] |= 0x0010
	if got := darwinIPv6Client(addrs, netip.Addr{}, flags); got.IsValid() {
		t.Fatal("used a deprecated address", got)
	}
	bits[temporary], bits[stable] = 0x0080, 0
	if got := darwinIPv6Client(addrs, temporary, flags); got != temporary {
		t.Fatal("ignored the bound listener", got)
	}
}
