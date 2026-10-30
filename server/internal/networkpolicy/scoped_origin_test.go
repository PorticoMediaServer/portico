package networkpolicy

import (
	"net/http/httptest"
	"testing"
)

func TestScopedIPv6Origin(t *testing.T) {
	for _, zone := range []string{"eth0", "Ethernet_2", "5"} {
		want := "http://[fe80::1234%25" + zone + "]:32500"
		r := httptest.NewRequest("GET", want+"/", nil)
		r.RemoteAddr = "[fe80::9%eth0]:1"
		if got := Origin(r, nil); got != want {
			t.Fatalf("Host %q: origin=%q want %q", r.Host, got, want)
		}
	}
	for _, host := range []string{"evil%25.example", "[fe80::1%eth/0]:32500", "[fe80::1%eth@0]:32500", "[fe80::1%]:32500", "[127.0.0.1%eth0]:32500"} {
		r := httptest.NewRequest("GET", "http://backend/", nil)
		r.Host = host
		if got := Origin(r, nil); got != "" {
			t.Fatalf("invalid host %q accepted: %s", host, got)
		}
	}
}
