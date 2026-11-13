package api

import (
	"crypto/tls"
	"net"
	"net/netip"
	"strconv"
	"strings"

	"github.com/PorticoMediaServer/portico/server-next/internal/auth"
	"github.com/PorticoMediaServer/portico/server-next/internal/portmap"
)

// The addresses this server publishes, for its signed document to Portico Services and so for
// the apps. A phone at home must reach the server over HTTPS without going through the home
// router (whose NAT loopback makes it count as away), so the server's own home-network
// addresses come first, then its public one. With a Portico Account each becomes a Portico
// name, <IP with dashes>.<name space>.portico.direct, which Portico's DNS answers with that
// IP and the server's one wildcard certificate covers, at home and away alike.
//
// A router with DNS-rebinding protection refuses a public DNS answer that points at a private
// address, so on such a router the home name does not resolve and the apps go on to the next
// address (the public one, through the router). The owner can allow portico.direct in the
// router's DNS settings. A phone that uses its own DNS (Private DNS, DNS over HTTPS) is not
// behind the router's filter, and the name resolves for it anyway.
//
// A server in a container on bridge networking sees only the container's own address, so it
// publishes no home route a device at home can use: the owner runs it with host networking, or
// devices at home use the public route.

// maxHomeAddresses bounds the home addresses published; the document holds eight in all.
const maxHomeAddresses = 4

// PublishedAddress is one address apps may use to reach the server.
type PublishedAddress struct {
	// Address is an IP address; Port the port to use at it.
	Address string `json:"address"`
	Port    int    `json:"port"`
	// Home is true for an address on the server's home networks, used by devices at home.
	Home bool `json:"home"`
	// Name is the Portico name for the address, once this server has a Portico name space.
	Name string `json:"name,omitempty"`
}

// PublishedAddresses are the addresses to publish now: home first, then public. A server
// that listens only on this machine publishes none, and the public address goes when remote
// access is off.
func (s *Server) PublishedAddresses() []PublishedAddress {
	port := s.listenPort()
	if port == 0 {
		return []PublishedAddress{}
	}
	var mine []interfaceAddress
	ifaces, _ := net.Interfaces()
	for _, iface := range ifaces {
		addrs, _ := iface.Addrs()
		for _, a := range addrs {
			if p, err := netip.ParsePrefix(a.String()); err == nil {
				mine = append(mine, interfaceAddress{iface.Name, p.Addr().Unmap()})
			}
		}
	}
	home := homeAddresses(mine, towardsRouter(portmap.DefaultRouter().Gateway), s.networks())
	v, _ := s.access.current()
	public, publicPort := "", v.PublicPort
	if v.RemoteAccess == "on" {
		public = s.public.Answer().Address
		if router := s.ports.Status(); router.State == "open" {
			publicPort = router.ExternalPort
			if public == "" {
				public = router.ExternalIP
			}
		}
	}
	out := publish(home, port, public, publicPort, s.porticoZone())
	if v.RemoteAccess == "on" && v.IPv6 && !s.ipv6ListenerMissing() {
		opening := s.ports6.Status()
		ip, _ := netip.ParseAddr(opening.ExternalIP)
		current := s.ports6.ClientAddress()
		port6, source := opening.ExternalPort, opening.ClientAddress
		if v.PortMapping == "off" {
			// The owner handles the router, as for manually forwarded IPv4.
			ip = current
			port6, source = port, current
		}
		if (opening.State == "open" || v.PortMapping == "off") && source == current && routableIPv6(ip) {
			out = append(out, PublishedAddress{Address: ip.String(), Port: port6, Name: porticoName(ip, s.porticoZone())})
		}
	}
	return out
}

// publish lists the home addresses, each at the port the server listens on, then the public
// one at the public port, each named under zone when there is one.
func publish(home []netip.Addr, homePort int, public string, publicPort int, zone string) []PublishedAddress {
	out := []PublishedAddress{}
	for _, ip := range home {
		out = append(out, PublishedAddress{Address: ip.String(), Port: homePort, Home: true, Name: porticoName(ip, zone)})
	}
	if ip, err := netip.ParseAddr(public); err == nil && routable(ip) {
		out = append(out, PublishedAddress{Address: ip.String(), Port: publicPort, Name: porticoName(ip, zone)})
	}
	return out
}

// sharedAddressSpace is 100.64.0.0/10, the carrier's NAT range: never reachable from outside.
var sharedAddressSpace = netip.MustParsePrefix("100.64.0.0/10")

// routable reports whether ip is an IPv4 address the internet can route to. A router behind a
// carrier's NAT reports 100.64.x.x, and many report 0.0.0.0 while their line is down: no
// public address is published rather than one nobody can reach.
func routable(ip netip.Addr) bool {
	return ip.Is4() && ip.IsGlobalUnicast() && !ip.IsPrivate() && !sharedAddressSpace.Contains(ip)
}

func routableIPv6(ip netip.Addr) bool {
	return ip.Is6() && !ip.Is4In6() && ip.IsGlobalUnicast() && !ip.IsPrivate() && !ip.IsLinkLocalUnicast()
}

// towardsRouter is this machine's address on the way to the router, or none.
func towardsRouter(gateway netip.Addr) netip.Addr {
	if !gateway.IsValid() {
		return netip.Addr{}
	}
	conn, err := net.DialUDP("udp4", nil, net.UDPAddrFromAddrPort(netip.AddrPortFrom(gateway, 9)))
	if err != nil {
		return netip.Addr{}
	}
	defer conn.Close()
	return conn.LocalAddr().(*net.UDPAddr).AddrPort().Addr().Unmap()
}

// interfaceAddress is one of this machine's addresses with the interface it is on.
type interfaceAddress struct {
	name string
	addr netip.Addr
}

// virtualInterfaces are the name prefixes of container bridges, virtual machine bridges and VPN
// tunnels. An address on one is never a home address: no device at home reaches the server
// there, and it would be a dead route tried before the real one, or push the real one out.
var virtualInterfaces = []string{"docker", "br-", "virbr", "wg", "tun", "tap", "veth"}

func virtualInterface(name string) bool {
	for _, prefix := range virtualInterfaces {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

// homeAddresses are the private IPv4 addresses among mine that are on the home networks and not
// on a virtual interface, the one used towards the router first, at most maxHomeAddresses.
func homeAddresses(mine []interfaceAddress, first netip.Addr, networks auth.Networks) []netip.Addr {
	usable := func(ip netip.Addr) bool {
		return ip.Is4() && ip.IsPrivate() && networks.AtHome(ip)
	}
	var out []netip.Addr
	for _, m := range mine {
		if m.addr == first && virtualInterface(m.name) {
			first = netip.Addr{}
		}
	}
	if usable(first) {
		out = append(out, first)
	}
	for _, m := range mine {
		if usable(m.addr) && m.addr != first && !virtualInterface(m.name) && len(out) < maxHomeAddresses {
			out = append(out, m.addr)
		}
	}
	return out
}

// porticoName is the Portico name for ip under zone ("<name space>.portico.direct"), or "".
func porticoName(ip netip.Addr, zone string) string {
	if zone == "" || !ip.IsValid() {
		return ""
	}
	label := strings.ReplaceAll(ip.String(), ".", "-")
	return strings.ReplaceAll(label, ":", "-") + "." + zone
}

// underOwnZone reports whether address ("host:port") is a name in this server's own Portico
// zone at a port the server publishes: the listen port, the public port, or the port the
// router mapped. Under Enhanced such a name is approved, whatever IP it carries, so the name
// published for a changing public address keeps working. Another server's zone is not.
func (s *Server) underOwnZone(address string, v RemoteAccessSettings) bool {
	zone := s.porticoZone()
	host, portText, err := net.SplitHostPort(address)
	if zone == "" || err != nil || !strings.HasSuffix(strings.ToLower(host), "."+zone) {
		return false
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		return false
	}
	if port == s.listenPort() || port == v.PublicPort {
		return true
	}
	router := s.ports.Status()
	return router.State == "open" && port == router.ExternalPort
}

func (s *Server) porticoZone() string {
	s.access.mu.RLock()
	defer s.access.mu.RUnlock()
	return s.access.zone
}

// SetPorticoZone is how the Portico certificate's side of the server says this server's
// Portico zone ("<name space>.portico.direct") and how to get the certificate for a name in
// it. The server serves that certificate for every name under the zone, its home names
// included, and the owner's own for any other. An empty zone stops it. A set zone is in force
// for Enhanced security at once, with no wait for a certificate, so the caller sets it only
// once the certificate can be served (porticoCertificate does).
func (s *Server) SetPorticoZone(zone string, certificate func(*tls.ClientHelloInfo) (*tls.Certificate, error)) {
	zone = strings.ToLower(strings.TrimSuffix(zone, "."))
	if certificate == nil {
		// No name is published that the server could not serve a certificate for.
		zone = ""
	}
	s.access.mu.Lock()
	s.access.zone = zone
	s.access.mu.Unlock()
	s.certs.ServeZone(zone, certificate)
	s.addressesChanged()
}

// OnAddressesChange registers the one function told when the published addresses may have
// changed, for the signed document to be sent again. It must return at once.
func (s *Server) OnAddressesChange(fn func()) {
	s.access.mu.Lock()
	s.access.onAddressesChange = fn
	s.access.mu.Unlock()
}

func (s *Server) addressesChanged() {
	s.access.mu.RLock()
	fn := s.access.onAddressesChange
	s.access.mu.RUnlock()
	if fn != nil {
		fn()
	}
}
