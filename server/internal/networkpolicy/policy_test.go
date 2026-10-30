package networkpolicy

import (
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
)

func TestClientAddressAndOrigin(t *testing.T) {
	proxies := []netip.Prefix{netip.MustParsePrefix("127.0.0.1/32"), netip.MustParsePrefix("10.0.0.2/32")}
	for _, tc := range []struct {
		name, peer, xff, forwarded, proto, host, wantAddress, wantOrigin string
		valid                                                            bool
	}{
		{"untrusted", "203.0.113.1:42", "127.0.0.1", "for=127.0.0.1;proto=https;host=evil.example", "https", "evil.example", "203.0.113.1", "http://backend:32500", true},
		{"chain", "127.0.0.1:42", "192.168.1.1, 203.0.113.1, 10.0.0.2", "", "https", "media.example", "203.0.113.1", "https://media.example", true},
		{"forwarded IPv6", "127.0.0.1:42", "", "for=\"[2001:db8::9]:443\";proto=https;host=media.example, for=10.0.0.2;proto=https;host=media.example", "", "", "2001:db8::9", "https://media.example", true},
		{"scoped peer", "[fe80::9%eth0]:42", "", "", "", "", "fe80::9", "http://backend:32500", true},
		{"missing forwarded", "127.0.0.1:42", "", "", "", "", "", "http://backend:32500", false},
		{"malformed left entry", "127.0.0.1:42", "bad, 203.0.113.9", "", "", "", "", "http://backend:32500", false},
		{"obfuscated forwarded", "127.0.0.1:42", "", "for=_hidden", "", "", "", "http://backend:32500", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("POST", "http://backend:32500/v1/setup/browser", nil)
			r.RemoteAddr = tc.peer
			for k, v := range map[string]string{"X-Forwarded-For": tc.xff, "Forwarded": tc.forwarded, "X-Forwarded-Proto": tc.proto, "X-Forwarded-Host": tc.host} {
				if v != "" {
					r.Header.Set(k, v)
				}
			}
			addr, ok := Client(r, proxies)
			if ok != tc.valid || ok && addr.String() != tc.wantAddress {
				t.Fatalf("client=%s,%v", addr, ok)
			}
			if got := Origin(r, proxies); got != tc.wantOrigin {
				t.Fatalf("origin=%s", got)
			}
		})
	}
	r := httptest.NewRequest("GET", "http://backend:32500", nil)
	r.RemoteAddr = "127.0.0.1:42"
	r.Header.Set("X-Forwarded-For", strings.Repeat("10.0.0.2,", 32)+"203.0.113.9")
	if _, ok := Client(r, proxies); ok {
		t.Fatal("unbounded chain accepted")
	}
}
func TestHomeNetworksHaveNoFallback(t *testing.T) {
	ranges := []netip.Prefix{netip.MustParsePrefix("100.64.0.0/10"), netip.MustParsePrefix("fe80::/10")}
	if !Contains(ranges, netip.MustParseAddr("100.64.1.9")) || !Contains(ranges, netip.MustParseAddr("fe80::9%eth0")) {
		t.Fatal("owner networks ignored")
	}
	for _, ip := range []string{"127.0.0.1", "192.168.1.1", "100.64.1.9", "::1"} {
		if Contains(nil, netip.MustParseAddr(ip)) {
			t.Fatal("empty list has implicit home networks")
		}
	}
}
