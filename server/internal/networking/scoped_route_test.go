package networking

import (
	"net/netip"
	"testing"
)

func TestScopedRouteURLs(t *testing.T) {
	for _, tc := range []struct {
		scheme string
		port   int
		want   string
	}{
		{"http", 80, "http://[fe80::9%25Ethernet_2]"},
		{"https", 443, "https://[fe80::9%25Ethernet_2]"},
		{"http", 32500, "http://[fe80::9%25Ethernet_2]:32500"},
	} {
		if got := routeURL(netip.MustParseAddr("fe80::9%Ethernet_2"), tc.port, tc.scheme); got != tc.want {
			t.Fatalf("%s:%d: got %s want %s", tc.scheme, tc.port, got, tc.want)
		}
	}
}
