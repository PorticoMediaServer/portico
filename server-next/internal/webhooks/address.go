package webhooks

import (
	"errors"
	"net"
	"net/netip"
	"net/url"
	"strings"
	"syscall"
	"unicode/utf8"
)

// Where the sender may connect. The owner types the address, and owners point webhooks at
// Home Assistant and the like on their own network, so private addresses (10.x, 192.168.x,
// 172.16-31.x, 100.64/10, fc00::/7) are allowed. What is never reached, whatever the
// address says and whatever its name resolves to at the moment of connecting:
//
//   - loopback (127.0.0.0/8, ::1), "this host" (0.0.0.0/8, ::), link-local (169.254.0.0/16,
//     where cloud metadata services live, and fe80::/10), multicast and broadcast;
//   - the same, hidden inside an IPv6 address (IPv4-mapped, IPv4-compatible, 6to4, NAT64,
//     Teredo);
//   - any of this machine's own addresses on the port Portico itself listens on, so a
//     webhook can never call Portico's own API. Another port on this machine (Home
//     Assistant beside Portico) is allowed.
//
// The check runs on the address actually dialled, after the name is resolved, so a name
// that resolves elsewhere later cannot get round it. No redirect is ever followed, so the
// only addresses reached are what the owner's own address resolves to. No proxy from the
// environment is used.

// ValidURL says what is wrong with an address the owner typed, or "" when it can be used.
func ValidURL(raw string) string {
	if raw == "" || len(raw) > 2048 || !utf8.ValidString(raw) || strings.ContainsAny(raw, " \t\r\n") {
		return "Enter the full address, starting with http:// or https://."
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.Opaque != "" {
		return "Enter the full address, starting with http:// or https://."
	}
	if u.User != nil {
		return "Leave the username and password out of the address; check the signature instead."
	}
	if u.Fragment != "" {
		return "Leave out the part after #."
	}
	host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return "A webhook cannot call this machine's loopback address. Use its network address instead."
	}
	if ip, err := netip.ParseAddr(host); err == nil && forbidden(ip) != "" {
		return "A webhook cannot call " + forbidden(ip) + "."
	}
	return ""
}

// forbidden names the kind of address ip is when the sender must never connect to it.
func forbidden(ip netip.Addr) string {
	ip = ip.Unmap()
	if inner, ok := embeddedIPv4(ip); ok {
		if why := forbidden(inner); why != "" {
			return why
		}
	}
	switch {
	case ip.IsLoopback():
		return "a loopback address"
	case ip.IsUnspecified(), ip.Is4() && ip.As4()[0] == 0:
		return "an unspecified address"
	case ip.IsLinkLocalUnicast():
		return "a link-local address"
	case ip.IsMulticast(), ip == netip.AddrFrom4([4]byte{255, 255, 255, 255}):
		return "a multicast or broadcast address"
	}
	return ""
}

var (
	sixToFour  = netip.MustParsePrefix("2002::/16")
	nat64      = netip.MustParsePrefix("64:ff9b::/96")
	nat64Local = netip.MustParsePrefix("64:ff9b:1::/48")
	teredo     = netip.MustParsePrefix("2001::/32")
	compat     = netip.MustParsePrefix("::/96")
)

// embeddedIPv4 is the IPv4 address an IPv6 address carries inside it, for the forms that
// route to one.
func embeddedIPv4(ip netip.Addr) (netip.Addr, bool) {
	if !ip.Is6() {
		return netip.Addr{}, false
	}
	b := ip.As16()
	switch {
	case sixToFour.Contains(ip):
		return netip.AddrFrom4([4]byte{b[2], b[3], b[4], b[5]}), true
	case nat64.Contains(ip), nat64Local.Contains(ip), compat.Contains(ip) && ip != netip.IPv6Unspecified() && ip != netip.IPv6Loopback():
		return netip.AddrFrom4([4]byte{b[12], b[13], b[14], b[15]}), true
	case teredo.Contains(ip):
		return netip.AddrFrom4([4]byte{^b[12], ^b[13], ^b[14], ^b[15]}), true
	}
	return netip.Addr{}, false
}

var errForbidden = errors.New("refused")

// checkDial is the rule above, applied to the address being dialled.
func (s *Service) checkDial(address string) error {
	ap, err := netip.ParseAddrPort(address)
	if err != nil {
		return err
	}
	if why := forbidden(ap.Addr()); why != "" {
		return &dialRefused{why}
	}
	if port := s.ownPort.Load(); port != 0 && int(ap.Port()) == int(port) && ownAddress(ap.Addr().Unmap()) {
		return &dialRefused{"Portico's own address"}
	}
	return nil
}

type dialRefused struct{ what string }

func (e *dialRefused) Error() string { return "webhooks may not call " + e.what }
func (e *dialRefused) Unwrap() error { return errForbidden }

// ownAddress says whether ip is one of this machine's addresses.
func ownAddress(ip netip.Addr) bool {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return true // cannot tell, so do not risk it
	}
	for _, a := range addrs {
		if n, ok := a.(*net.IPNet); ok {
			if mine, ok := netip.AddrFromSlice(n.IP); ok && mine.Unmap() == ip {
				return true
			}
		}
	}
	return false
}

// control runs on every connection the sender makes, after the name is resolved.
func (s *Service) control(_, address string, _ syscall.RawConn) error {
	return s.allow(address)
}
