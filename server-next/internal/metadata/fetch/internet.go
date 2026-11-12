package fetch

import (
	"context"
	"fmt"
	"net"
	"net/http"
)

// PublicAddress reports whether ip is on the Internet: not this machine, the local network, a
// link-local address or an unspecified one (definition area 4, rule 7: a provider's answer
// never sends the server to an address of its own network).
func PublicAddress(ip net.IP) bool {
	return !(ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() ||
		ip.IsInterfaceLocalMulticast() || ip.IsMulticast() || sharedAddresses.Contains(ip))
}

// sharedAddresses is 100.64.0.0/10, the carrier-grade NAT range that Tailscale and similar
// private networks also use.
var sharedAddresses = &net.IPNet{IP: net.IPv4(100, 64, 0, 0), Mask: net.CIDRMask(10, 32)}

// InternetTransport connects to a host that checked says to check only at an address it has
// just looked up and found on the Internet, so a name that answered a public address when an
// answer named it cannot answer a local one when the connection is made. A host checked says
// not to check is dialled as it is: a test's stand-in on this machine.
func InternetTransport(checked func(host string) bool) *http.Transport {
	t := http.DefaultTransport.(*http.Transport).Clone()
	var dialer net.Dialer
	t.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil || !checked(host) {
			return dialer.DialContext(ctx, network, address)
		}
		addresses, err := net.DefaultResolver.LookupIPAddr(ctx, host)
		if err != nil {
			return nil, err
		}
		for _, a := range addresses {
			if !PublicAddress(a.IP) {
				return nil, fmt.Errorf("%s is not on the Internet", host)
			}
		}
		for _, a := range addresses {
			conn, dialErr := dialer.DialContext(ctx, network, net.JoinHostPort(a.IP.String(), port))
			if dialErr == nil {
				return conn, nil
			}
			err = dialErr
		}
		if err == nil {
			err = fmt.Errorf("%s has no address", host)
		}
		return nil, err
	}
	return t
}
