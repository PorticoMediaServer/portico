package auth

import (
	"errors"
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// Networks is the owner's answer to two questions: which addresses are home, and which
// reverse proxies are believed when they say who a request really came from. Nothing is
// guessed: a proxy or an overlay network the owner has not listed is not detected, it is
// simply an address like any other.
type Networks struct {
	// Home is the only definition of local and remote. An address in none of these is away.
	Home []netip.Prefix
	// Proxies are believed when they pass on a client's address in X-Forwarded-For. The
	// same header from any other address is ignored.
	Proxies []netip.Prefix
}

// maxForwardedHops is how many trusted proxies in a row a forwarded header is read through.
const maxForwardedHops = 32

func within(prefixes []netip.Prefix, addr netip.Addr) bool {
	for _, p := range prefixes {
		if p.Contains(addr) {
			return true
		}
	}
	return false
}

// Client returns the address a request came from: the connection's own address or, when
// that is one of the owner's trusted proxies, the nearest address in X-Forwarded-For that
// is not itself a trusted proxy. The header can be written by anyone, so it is read from
// the end the trusted proxy wrote and no further than the first address that is not a
// trusted proxy: whatever a client put before that is never looked at, so nothing a client
// sends can make it count as the proxy. If the proxy's own entry cannot be read, the
// request counts as coming from the proxy.
func (n Networks) Client(r *http.Request) (netip.Addr, bool) {
	ap, err := netip.ParseAddrPort(r.RemoteAddr)
	if err != nil {
		return netip.Addr{}, false
	}
	peer := ap.Addr().Unmap().WithZone("")
	if !within(n.Proxies, peer) {
		return peer, true
	}
	lines := r.Header.Values("X-Forwarded-For")
	for read, l := 0, len(lines)-1; l >= 0; l-- {
		rest := lines[l]
		for more := true; more; read++ {
			var raw string
			if at := strings.LastIndexByte(rest, ','); at >= 0 {
				rest, raw = rest[:at], rest[at+1:]
			} else {
				raw, more = rest, false
			}
			hop, err := netip.ParseAddr(strings.TrimSpace(raw))
			if err != nil || hop.Zone() != "" || hop.IsUnspecified() || hop.IsMulticast() || read == maxForwardedHops {
				return peer, true
			}
			if hop = hop.Unmap(); !within(n.Proxies, hop) {
				return hop, true
			}
		}
	}
	return peer, true
}

// AtHome reports whether an address is on one of the owner's home networks.
func (n Networks) AtHome(addr netip.Addr) bool {
	return addr.IsValid() && within(n.Home, addr.Unmap().WithZone(""))
}

// SourceKey is what wrong guesses from an address are counted against: the IPv4 address,
// or the /64 of an IPv6 address, since one household is handed a whole /64.
func SourceKey(addr netip.Addr) string {
	if addr.Is6() {
		prefix, _ := addr.Prefix(64)
		return prefix.String()
	}
	return addr.String()
}

// DefaultHome is what counts as home until the owner says otherwise: this machine, and
// the private networks it is directly attached to. A public address is never home by default.
func DefaultHome() []netip.Prefix {
	out := []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8"), netip.MustParsePrefix("::1/128")}
	addrs, _ := net.InterfaceAddrs()
	for _, a := range addrs {
		p, err := netip.ParsePrefix(a.String())
		if err != nil {
			continue
		}
		p = p.Masked()
		if (p.Addr().IsPrivate() || p.Addr().IsLinkLocalUnicast()) && !within(out, p.Addr()) {
			out = append(out, p)
		}
	}
	return out
}

// ErrNetwork: an entry is not an address or a network such as 192.168.1.0/24.
var ErrNetwork = errors.New("not an address or a network")

// ParseNetworks reads a list of addresses and networks in CIDR form.
func ParseNetworks(list []string) ([]netip.Prefix, error) {
	out := make([]netip.Prefix, 0, len(list))
	for _, raw := range list {
		raw = strings.TrimSpace(raw)
		p, err := netip.ParsePrefix(raw)
		if err != nil {
			addr, err := netip.ParseAddr(raw)
			if err != nil || addr.Zone() != "" {
				return nil, ErrNetwork
			}
			p = netip.PrefixFrom(addr, addr.BitLen())
		}
		if p.Addr().Is4In6() {
			return nil, ErrNetwork
		}
		out = append(out, p.Masked())
	}
	return out, nil
}
