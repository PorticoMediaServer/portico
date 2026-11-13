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

func TestWindowsIPv6ClientPrefersStable(t *testing.T) {
	temporary, stable := netip.MustParseAddr("2001:db8::10"), netip.MustParseAddr("2001:db8::20")
	row := windows.MibUnicastIpAddressRow{InterfaceLuid: 7, PreferredLifetime: 3600, DadState: windows.IpDadStatePreferred, SuffixOrigin: windows.IpSuffixOriginRandom}
	row.Address = windows.RawSockaddrInet6{Family: windows.AF_INET6, Addr: temporary.As16()}
	rows := []windows.MibUnicastIpAddressRow{row, row}
	rows[1].Address.Addr, rows[1].SuffixOrigin = stable.As16(), windows.IpSuffixOriginManual
	if got := windowsIPv6Client(rows, 7, netip.Addr{}); got != stable {
		t.Fatal("temporary address won over stable", got)
	}
	rows[1].DadState = windows.IpDadStateDeprecated
	if got := windowsIPv6Client(rows, 7, netip.Addr{}); got != temporary {
		t.Fatal("no temporary fallback", got)
	}
	rows[0].DadState = windows.IpDadStateDeprecated
	if got := windowsIPv6Client(rows, 7, netip.Addr{}); got.IsValid() {
		t.Fatal("used a deprecated address", got)
	}
	rows[0].DadState, rows[1].DadState = windows.IpDadStatePreferred, windows.IpDadStatePreferred
	if got := windowsIPv6Client(rows, 7, temporary); got != temporary {
		t.Fatal("ignored the bound listener", got)
	}
}

func TestWindowsRandomSuffixPrefersTheEarliestCreatedAddress(t *testing.T) {
	temporary, stable := netip.MustParseAddr("2001:db8::10"), netip.MustParseAddr("2001:db8::20")
	row := windows.MibUnicastIpAddressRow{InterfaceLuid: 7, PreferredLifetime: 3600, DadState: windows.IpDadStatePreferred, SuffixOrigin: windows.IpSuffixOriginRandom}
	row.Address = windows.RawSockaddrInet6{Family: windows.AF_INET6, Addr: temporary.As16()}
	row.CreationTimeStamp = windows.Filetime{HighDateTime: 2, LowDateTime: 1}
	rows := []windows.MibUnicastIpAddressRow{row, row}
	rows[1].Address.Addr = stable.As16()
	rows[1].CreationTimeStamp = windows.Filetime{HighDateTime: 1, LowDateTime: 100}
	if got := windowsIPv6Client(rows, 7, netip.Addr{}); got != stable {
		t.Fatal("new temporary address won table order", got)
	}
	rows[0].SuffixOrigin = windows.IpSuffixOriginManual
	if got := windowsIPv6Client(rows, 7, netip.Addr{}); got != temporary {
		t.Fatal("lost the non-random preference", got)
	}
}
