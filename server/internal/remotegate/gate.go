// Package remotegate decides, before any HTTP response, whether a connection
// from outside the home network is answered (O-169).
//
// Remote access On / Private only / Off says whether outside connections are
// answered at all; Private only also counts the tailnet ranges as home.
// Remote security Enhanced answers outside connections only over HTTPS and
// only at this server's own Portico names (whatever its certificate covers) or
// an address the owner added; Standard answers outside connections at any
// address, with or without HTTPS. Home connections are never gated by name;
// the "Secure connections" setting still decides HTTP at home.
//
// A refusal is silent: a TLS handshake that fails, or a TCP connection closed
// with no bytes written, so a scanner can't tell Portico from nothing.
package remotegate

import (
	"net"
	"net/netip"
	"strings"
	"sync/atomic"
)

const (
	SecurityEnhanced = "enhanced"
	SecurityStandard = "standard"
	AccessOn         = "on"
	AccessPrivate    = "private"
	AccessOff        = "off"
)

// Tailnet ranges count as home when remote access is Private only.
var tailnet = []netip.Prefix{netip.MustParsePrefix("100.64.0.0/10"), netip.MustParsePrefix("fd7a:115c:a1e0::/48")}

// Policy is one immutable snapshot of the settings the gate reads. It is
// rebuilt on every settings save and swapped atomically.
type Policy struct {
	security string
	access   string
	home     []netip.Prefix
	proxies  []netip.Prefix
	hosts    map[string]struct{}
	// own reports the server's own public addresses (a client at one of them is
	// the server itself, through hairpin NAT, and counts as home).
	own func() []netip.Addr
}

// Settings are the inputs of a Policy.
type Settings struct {
	Security string
	Access   string
	Home     []netip.Prefix
	Proxies  []netip.Prefix
	// Hosts are the owner's custom addresses, the external address and the
	// custom-certificate domain: host names or origins, any case, ports ignored.
	Hosts []string
	Own   func() []netip.Addr
}

func NewPolicy(s Settings) *Policy {
	p := &Policy{security: s.Security, access: s.Access, proxies: append([]netip.Prefix(nil), s.Proxies...), hosts: map[string]struct{}{}, own: s.Own}
	if p.security != SecurityStandard {
		p.security = SecurityEnhanced
	}
	if p.access != AccessPrivate && p.access != AccessOff {
		p.access = AccessOn
	}
	p.home = append([]netip.Prefix(nil), s.Home...)
	if p.access == AccessPrivate {
		p.home = append(p.home, tailnet...)
	}
	for _, raw := range s.Hosts {
		if name := HostName(raw); name != "" {
			p.hosts[name] = struct{}{}
		}
	}
	return p
}

func (p *Policy) Security() string { return p.security }
func (p *Policy) Access() string   { return p.access }

// HostName reduces a Host header, SNI, host name or origin to a lower-case
// name without port or trailing dot. Empty when it is none of those.
func HostName(raw string) string {
	raw = strings.TrimSpace(strings.ToLower(raw))
	if i := strings.Index(raw, "://"); i >= 0 {
		raw = raw[i+3:]
	}
	if i := strings.IndexAny(raw, "/?#"); i >= 0 {
		raw = raw[:i]
	}
	if i := strings.LastIndex(raw, "@"); i >= 0 {
		raw = raw[i+1:]
	}
	if host, _, err := net.SplitHostPort(raw); err == nil {
		raw = host
	}
	raw = strings.TrimSuffix(strings.Trim(raw, "[]"), ".")
	if raw == "" || len(raw) > 253 {
		return ""
	}
	return raw
}

// Home reports whether a peer is on the home network (or the server itself).
func (p *Policy) Home(peer netip.Addr) bool {
	peer = peer.Unmap().WithZone("")
	if !peer.IsValid() {
		return false
	}
	if peer.IsLoopback() {
		return true
	}
	for _, prefix := range p.home {
		if prefix.Contains(peer) {
			return true
		}
	}
	if p.own != nil {
		for _, own := range p.own() {
			if own.Unmap() == peer {
				return true
			}
		}
	}
	return false
}

// Proxy reports whether a peer is a trusted reverse proxy, whose requests are
// judged one by one from what it forwards.
func (p *Policy) Proxy(peer netip.Addr) bool {
	peer = peer.Unmap().WithZone("")
	for _, prefix := range p.proxies {
		if prefix.Contains(peer) {
			return true
		}
	}
	return false
}

// Listed reports whether a name is one the owner added (custom address,
// external address or custom-certificate domain).
func (p *Policy) Listed(name string) bool {
	_, ok := p.hosts[HostName(name)]
	return ok
}

// Connection is the first decision, on a new socket. Defer means the peer is a
// trusted proxy (or home) and each request is judged instead.
type Connection int

const (
	Admit Connection = iota
	Refuse
	// RefuseAfterHost refuses plain HTTP from outside in Enhanced; the listener
	// reads only the request head, to record the name asked for, and closes.
	RefuseAfterHost
)

// Socket judges a new connection by its peer and transport alone.
func (p *Policy) Socket(peer netip.Addr, tls bool) Connection {
	if p.Home(peer) || p.Proxy(peer) {
		return Admit
	}
	if p.access != AccessOn {
		return Refuse
	}
	if !tls && p.security == SecurityEnhanced {
		return RefuseAfterHost
	}
	return Admit
}

// Request judges one HTTP request from a client outside the home network.
// client is the real client (the forwarded one behind a trusted proxy);
// serverName is the TLS server name this server validated, empty without TLS;
// forwardedHTTPS reports a trusted proxy's X-Forwarded-Proto of https.
// It returns whether to answer, and whether the Host should be recorded as a
// refused name.
func (p *Policy) Request(client netip.Addr, proxied bool, serverName string, forwardedHTTPS bool, host string) (admit, record bool) {
	if p.Home(client) {
		return true, false
	}
	if p.access != AccessOn {
		return false, false
	}
	if p.security == SecurityStandard {
		return true, false
	}
	name := HostName(host)
	// Over this server's own TLS, the certificate already proved the server
	// name is one it serves; the Host must be that name or one the owner added.
	if serverName != "" {
		if name != "" && (name == HostName(serverName) || p.Listed(name)) {
			return true, false
		}
		return false, true
	}
	// Without this server's TLS, only a trusted proxy that terminated HTTPS
	// may forward, and only for an address the owner added.
	if proxied && forwardedHTTPS && p.Listed(name) {
		return true, false
	}
	return false, true
}

// Gate holds the live Policy and the refused-name record.
type Gate struct {
	policy  atomic.Pointer[Policy]
	Refused *Refused
}

// New starts with no policy. Until the settings document is installed the
// gate doesn't know the home networks, so it judges nothing rather than
// guessing (a guess would refuse people at home); the server installs the
// policy while it starts, before it serves anything but the startup page.
func New() *Gate { return &Gate{Refused: NewRefused()} }

// Policy is nil until the first Set.
func (g *Gate) Policy() *Policy {
	if g == nil {
		return nil
	}
	return g.policy.Load()
}
func (g *Gate) Set(p *Policy) {
	if p != nil {
		g.policy.Store(p)
	}
}

// Record notes a refused name, in Enhanced only.
func (g *Gate) Record(name, via string) {
	if p := g.Policy(); p == nil || g.Refused == nil || p.security != SecurityEnhanced {
		return
	}
	g.Refused.Record(name, via)
}

// PeerAddr extracts the peer address of a socket.
func PeerAddr(addr net.Addr) netip.Addr {
	if addr == nil {
		return netip.Addr{}
	}
	if tcp, ok := addr.(*net.TCPAddr); ok {
		ip, _ := netip.AddrFromSlice(tcp.IP)
		return ip.Unmap()
	}
	ap, err := netip.ParseAddrPort(addr.String())
	if err != nil {
		return netip.Addr{}
	}
	return ap.Addr().Unmap()
}
