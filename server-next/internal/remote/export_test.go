package remote

import (
	"context"
	"net"
	"testing"
)

// LoopbackIsPublic makes this computer's own addresses count as on the internet for a test,
// so a stand-in server can play a provider out there.
func LoopbackIsPublic(t testing.TB) {
	before := kindOf
	kindOf = func(ip net.IP) addressKind {
		if ip.IsLoopback() {
			return publicAddress
		}
		return before(ip)
	}
	t.Cleanup(func() { kindOf = before })
}

// ResolveAs makes names resolve to the given addresses for a test.
func ResolveAs(t testing.TB, names map[string]string) {
	before := lookupIP
	lookupIP = func(ctx context.Context, host string) ([]net.IPAddr, error) {
		if address, ok := names[host]; ok {
			return []net.IPAddr{{IP: net.ParseIP(address)}}, nil
		}
		return before(ctx, host)
	}
	t.Cleanup(func() { lookupIP = before })
}

// LoopbackIsLinkLocal makes this computer's own addresses count as link-local for a test, to
// show what a name resolves to is checked as it is dialed.
func LoopbackIsLinkLocal(t testing.TB) {
	before := kindOf
	kindOf = func(ip net.IP) addressKind {
		if ip.IsLoopback() {
			return linkLocalAddress
		}
		return before(ip)
	}
	t.Cleanup(func() { kindOf = before })
}

// ResolveAllAs makes one name answer with several addresses for a test.
func ResolveAllAs(t testing.TB, name string, addresses []string) {
	before := lookupIP
	lookupIP = func(ctx context.Context, host string) ([]net.IPAddr, error) {
		if host == name {
			var out []net.IPAddr
			for _, a := range addresses {
				out = append(out, net.IPAddr{IP: net.ParseIP(a)})
			}
			return out, nil
		}
		return before(ctx, host)
	}
	t.Cleanup(func() { lookupIP = before })
}
