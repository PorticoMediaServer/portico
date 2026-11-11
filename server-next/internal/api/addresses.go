package api

import (
	"crypto/tls"
	"net"
	"net/netip"
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
// router's DNS settings.

// maxHomeAddresses bounds the home addresses published; the document holds eight in all.
const maxHomeAddresses = 4

// PublishedAddress is one address apps may use to reach the server.
type PublishedAddress struct {
	// Address is an IPv4 address; Port the port to use at it.
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
	var mine []netip.Addr
	if addrs, err := net.InterfaceAddrs(); err == nil {
		for _, a := range addrs {
			if p, err := netip.ParsePrefix(a.String()); err == nil {
				mine = append(mine, p.Addr().Unmap())
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
	return publish(home, port, public, publicPort, s.porticoZone())
}

// publish lists the home addresses, each at the port the server listens on, then the public
// one at the public port, each named under zone when there is one.
func publish(home []netip.Addr, homePort int, public string, publicPort int, zone string) []PublishedAddress {
	out := []PublishedAddress{}
	for _, ip := range home {
		out = append(out, PublishedAddress{Address: ip.String(), Port: homePort, Home: true, Name: porticoName(ip, zone)})
	}
	if ip, err := netip.ParseAddr(public); err == nil && ip.Is4() && !ip.IsPrivate() {
		out = append(out, PublishedAddress{Address: ip.String(), Port: publicPort, Name: porticoName(ip, zone)})
	}
	return out
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

// homeAddresses are the private IPv4 addresses among mine that are on the home networks, the
// one used towards the router first, at most maxHomeAddresses.
func homeAddresses(mine []netip.Addr, first netip.Addr, networks auth.Networks) []netip.Addr {
	usable := func(ip netip.Addr) bool {
		return ip.Is4() && ip.IsPrivate() && networks.AtHome(ip)
	}
	var out []netip.Addr
	if usable(first) {
		out = append(out, first)
	}
	for _, ip := range mine {
		if usable(ip) && ip != first && len(out) < maxHomeAddresses {
			out = append(out, ip)
		}
	}
	return out
}

// porticoName is the Portico name for ip under zone ("<name space>.portico.direct"), or "".
func porticoName(ip netip.Addr, zone string) string {
	if zone == "" || !ip.Is4() {
		return ""
	}
	return strings.ReplaceAll(ip.String(), ".", "-") + "." + zone
}

func (s *Server) porticoZone() string {
	s.access.mu.RLock()
	defer s.access.mu.RUnlock()
	return s.access.zone
}

// SetPorticoZone is how the Portico certificate's side of the server says this server's
// Portico zone ("<name space>.portico.direct") and how to get the certificate for a name in
// it. The server serves that certificate for every name under the zone, its home names
// included, and the owner's own for any other. An empty zone stops it.
func (s *Server) SetPorticoZone(zone string, certificate func(*tls.ClientHelloInfo) (*tls.Certificate, error)) {
	zone = strings.ToLower(strings.TrimSuffix(zone, "."))
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
