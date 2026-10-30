package networking

import "net/netip"

// directCertificateIPv4 selects only a current, complete interface observation.
// Keep a still-observed address; otherwise require one unambiguous public IPv4.
// Naming it grants no inbound reachability, certificate or return registration.
func directCertificateIPv4(topo Topology, retained string) netip.Addr {
	if topo.Incomplete {
		return netip.Addr{}
	}
	var selected netip.Addr
	ambiguous := false
	for _, raw := range topo.Public {
		ip, err := netip.ParseAddr(raw)
		if err != nil || !ip.Is4() || !publicAddress(ip) {
			continue
		}
		if ip.String() == retained {
			return ip
		}
		if selected.IsValid() && selected != ip {
			ambiguous = true
		}
		selected = ip
	}
	if ambiguous {
		return netip.Addr{}
	}
	return selected
}
