package portmap

import (
	"golang.org/x/sys/windows"
	"net/netip"
	"testing"
	"unsafe"
)

func TestWindowsDefaultGateway(t *testing.T) {
	row := windows.MibIpForwardRow2{ValidLifetime: 3600}
	row.DestinationPrefix.Prefix.Family = windows.AF_INET
	*(*windows.RawSockaddrInet4)(unsafe.Pointer(&row.NextHop)) = windows.RawSockaddrInet4{Family: windows.AF_INET, Addr: [4]byte{192, 168, 1, 1}}
	if !windowsDefaultRoute(row, windows.AF_INET) || windowsRouteAddress(row.NextHop) != netip.MustParseAddr("192.168.1.1") {
		t.Fatal(row)
	}
	row.DestinationPrefix.PrefixLength = 24
	if windowsDefaultRoute(row, windows.AF_INET) {
		t.Fatal("used a non-default route")
	}
	row.DestinationPrefix.PrefixLength = 0
	row.DestinationPrefix.Prefix.Family = windows.AF_INET6
	*(*windows.RawSockaddrInet6)(unsafe.Pointer(&row.NextHop)) = windows.RawSockaddrInet6{Family: windows.AF_INET6, Addr: [16]byte{0xfe, 0x80, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1}}
	if !windowsDefaultRoute(row, windows.AF_INET6) || windowsRouteAddress(row.NextHop) != netip.MustParseAddr("fe80::1") {
		t.Fatal(row)
	}
	row.ValidLifetime = 0
	if windowsDefaultRoute(row, windows.AF_INET6) {
		t.Fatal("used an expired route")
	}
}
