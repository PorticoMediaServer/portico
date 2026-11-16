package strm

import (
	"context"
	"net"
	"net/netip"
	"net/url"
	"testing"
)

// TestClient maps one checked public address to a test's local HTTP fixture.
// It cannot be enabled in a server binary; private addresses remain refused.
func TestClient(t testing.TB, fixture string) (*Client, string) {
	t.Helper()
	if !testing.Testing() {
		panic("STRM fixture client is only for tests")
	}
	u, err := url.Parse(fixture)
	if err != nil {
		t.Fatal(err)
	}
	c := New(nil)
	c.lookup = func(context.Context, string, string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("93.184.216.34")}, nil
	}
	c.dial = func(ctx context.Context, network, address string) (net.Conn, error) {
		if address != "93.184.216.34:80" {
			return nil, ErrRefused
		}
		return (&net.Dialer{}).DialContext(ctx, network, u.Host)
	}
	return c, "http://media.test"
}
