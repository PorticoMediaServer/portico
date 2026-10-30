package networkpolicy

import (
	"net/http/httptest"
	"net/netip"
	"testing"
)

func TestForwardedMalformedQuotes(t *testing.T) {
	trusted := []netip.Prefix{netip.MustParsePrefix("127.0.0.1/32")}
	for _, value := range []string{`for="`, `for=""`, `for="127.0.0.1`, `for=127.0.0.1;host="`, `for=127.0.0.1;proto="`} {
		t.Run(value, func(t *testing.T) {
			defer func() {
				if p := recover(); p != nil {
					t.Fatalf("malformed forwarded value panicked: %v", p)
				}
			}()
			r := httptest.NewRequest("GET", "http://backend:32500/", nil)
			r.RemoteAddr = "127.0.0.1:1"
			r.Header.Set("Forwarded", value)
			if _, ok := Client(r, trusted); ok {
				t.Fatal("malformed chain supplied client authority")
			}
			if got := Origin(r, trusted); got != "http://backend:32500" {
				t.Fatalf("malformed origin metadata believed: %s", got)
			}
		})
	}
}
