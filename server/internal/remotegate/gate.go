// Package remotegate decides, before any HTTP response, whether a connection
// from outside the home network is answered (O-169).
//
// Remote access On / Private only / Off says whether outside connections are
// answered at all. The tailnet ranges count as home under On and Private only
// (an internet scanner can't come from them), so Private only means "home and
// tailnet, nothing from the internet". Off means home only.
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

	"github.com/PorticoMediaServer/portico/server/internal/networkpolicy"
)

const (
	SecurityEnhanced = "enhanced"
	SecurityStandard = "standard"
	AccessOn         = "on"
	AccessPrivate    = "private"
	AccessOff        = "off"
)

// Tailnet ranges count as home unless remote access is Off.
var tailnet = []netip.Prefix{netip.MustParsePrefix("100.64.0.0/10"), netip.MustParsePrefix("fd7a:115c:a1e0::/48")}

// Local is every loopback, private and link-local range. These always count as home,
// on top of the detected and configured networks: none can arrive as a raw source
// address from the open internet, and a container in Docker bridge mode sees only its
// bridge (172.17.0.0/16) while the people at home are on 192.168.x (Justin, 1 Nov).
// First-run setup uses the same classification (LocalAddr, Policy.Home).
var Local = []netip.Prefix{
	netip.MustParsePrefix("127.0.0.0/8"), netip.MustParsePrefix("::1/128"),
	netip.MustParsePrefix("10.0.0.0/8"), netip.MustParsePrefix("172.16.0.0/12"), netip.MustParsePrefix("192.168.0.0/16"),
	netip.MustParsePrefix("169.254.0.0/16"), netip.MustParsePrefix("fe80::/10"), netip.MustParsePrefix("fc00::/7"),
}

// LocalAddr reports an address in Local. The tailnet's own IPv6 range sits inside
// fc00::/7 but is the tailnet, not the home network: it counts only through Policy.Home.
func LocalAddr(peer netip.Addr) bool {
	peer = peer.Unmap().WithZone("")
	if !peer.IsValid() || inPrefixes(tailnet, peer) {
		return false
	}
	return inPrefixes(Local, peer)
}

func inPrefixes(prefixes []netip.Prefix, peer netip.Addr) bool {
	for _, prefix := range prefixes {
		if prefix.Contains(peer) {
			return true
		}
	}
	return false
}

// Policy is one immutable snapshot of the settings the gate reads. It is
// rebuilt on every settings save and swapped atomically.
type Policy struct {
	security string
	access   string
	home     []netip.Prefix
	// configured is the detected and owner-configured home networks alone.
	configured []netip.Prefix
	// unconfigured: the server isn't set up yet, so no remote-access rule applies
	// (Justin, 1 Nov); setup itself admits outside callers only with the setup code.
	unconfigured bool
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
	// Unconfigured: the server has no owner yet. Every connection is answered;
	// first-run setup applies its own rule (the setup code from outside).
	Unconfigured bool
}

func NewPolicy(s Settings) *Policy {
	p := &Policy{security: s.Security, access: s.Access, proxies: append([]netip.Prefix(nil), s.Proxies...), hosts: map[string]struct{}{}, own: s.Own, unconfigured: s.Unconfigured}
	if p.security != SecurityStandard {
		p.security = SecurityEnhanced
	}
	if p.access != AccessPrivate && p.access != AccessOff {
		p.access = AccessOn
	}
	p.configured = append([]netip.Prefix(nil), s.Home...)
	p.home = append(append([]netip.Prefix(nil), s.Home...), Local...)
	if p.access != AccessOff {
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

// Unconfigured reports whether this policy answers everything because the server isn't set up.
func (p *Policy) Unconfigured() bool { return p.unconfigured }

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
	// With remote access Off the tailnet is outside, though its IPv6 range is inside
	// fc00::/7, unless the owner made it a home network.
	if p.access == AccessOff && inPrefixes(tailnet, peer) && !inPrefixes(p.configured, peer) {
		return false
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
	if p.unconfigured || p.Home(peer) || p.Proxy(peer) {
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

// Why a name was refused, which says what fixes it (the owner sees it with
// the name):
//   - ReasonName: the name isn't one this server answers, over this server's
//     TLS or through a trusted proxy. Allow this address fixes it.
//   - ReasonHTTP: plain HTTP from outside (or a proxy saying it forwarded
//     plain HTTP). Enhanced needs HTTPS; Standard fixes it.
//   - ReasonCertificate: HTTPS for a name this server has no certificate for,
//     refused by the handshake in either mode. Your own certificate (or a
//     reverse proxy) for the name fixes it.
//   - ReasonProxy: a trusted proxy forwarded a request without the address
//     people used (its Host is the proxy's upstream address, not a name), so
//     no name can be allowed. Recorded under the proxy's own address. Setting
//     the proxy to pass Host (or X-Forwarded-Host), or Standard, fixes it.
const (
	ReasonName        = "name"
	ReasonHTTP        = "http"
	ReasonCertificate = "certificate"
	ReasonProxy       = "proxy"
)

// Request judges one HTTP request from a client outside the home network.
// client is the real client (the forwarded one behind a trusted proxy);
// serverName is the TLS server name this server validated, empty without TLS;
// forwardedProto is a trusted proxy's X-Forwarded-Proto, empty when it sent
// none. It returns whether to answer and, for a refusal worth showing the
// owner, its reason ("" records nothing).
func (p *Policy) Request(client netip.Addr, proxied bool, serverName, forwardedProto, host string) (admit bool, reason string) {
	if p.unconfigured || p.Home(client) {
		return true, ""
	}
	if p.access != AccessOn {
		return false, ""
	}
	if p.security == SecurityStandard {
		return true, ""
	}
	name := HostName(host)
	// Over this server's own TLS, the certificate already proved the server
	// name is one it serves; the Host must be that name or one the owner added.
	if serverName != "" {
		if name != "" && (name == HostName(serverName) || p.Listed(name)) {
			return true, ""
		}
		return false, ReasonName
	}
	// Without this server's TLS, only a trusted proxy may forward, and only
	// for an address the owner added. A proxy that sends no X-Forwarded-Proto
	// (nginx's default) is taken at its word as terminating HTTPS: the owner
	// declared it trusted and added the name. Only an explicit "http" is
	// refused.
	if proxied {
		if strings.EqualFold(strings.TrimSpace(forwardedProto), "http") {
			return false, ReasonHTTP
		}
		if p.Listed(name) {
			return true, ""
		}
		return false, ReasonName
	}
	return false, ReasonHTTP
}

// Gate holds the live Policy, the refused-name record and the refusal counts.
type Gate struct {
	policy   atomic.Pointer[Policy]
	Refused  *Refused
	Refusals *Refusals
}

// New starts with the startup policy: until the settings document is
// installed, every connection from outside is refused, and home is loopback,
// link-local and this machine's own private subnets (the default home
// networks), so the people at home aren't locked out if settings can't load.
func New() *Gate {
	g := &Gate{Refused: NewRefused(), Refusals: NewRefusals()}
	g.policy.Store(StartupPolicy())
	return g
}

// StartupPolicy refuses every outside connection and records nothing.
func StartupPolicy() *Policy {
	var home []netip.Prefix
	for _, raw := range networkpolicy.DefaultHomeNetworks() {
		if p, err := netip.ParsePrefix(raw); err == nil {
			home = append(home, p)
		}
	}
	return NewPolicy(Settings{Security: SecurityEnhanced, Access: AccessOff, Home: home})
}

// Policy is the live policy (the startup policy until the first Set); nil
// only on a nil Gate.
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

// Record notes a refused name, only while remote access is On in Enhanced
// (with access Off or Private only, or before the settings are installed,
// a name is never the reason for a refusal).
func (g *Gate) Record(name, reason string) {
	if p := g.Policy(); p == nil || g.Refused == nil || p.unconfigured || p.security != SecurityEnhanced || p.access != AccessOn {
		return
	}
	g.Refused.Record(name, reason)
}

// Count notes one refused connection from outside, in every mode, for the
// owner's security summary.
func (g *Gate) Count(reason string) {
	if g != nil {
		g.Refusals.Count(reason)
	}
}

// RecordProxy notes that a trusted proxy forwarded a request without a name,
// under the proxy's address, in Enhanced with remote access On only.
func (g *Gate) RecordProxy(proxy netip.Addr) {
	if p := g.Policy(); p == nil || g.Refused == nil || p.unconfigured || p.security != SecurityEnhanced || p.access != AccessOn {
		return
	}
	g.Refused.RecordProxy(proxy)
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
