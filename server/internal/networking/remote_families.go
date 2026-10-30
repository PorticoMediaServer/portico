package networking

import "net/netip"

func hasPublicIPv4(topo Topology) bool {
	for _, raw := range topo.Public {
		if ip, err := netip.ParseAddr(raw); err == nil && ip.Is4() {
			return true
		}
	}
	return false
}
func hasLocalIPv4(topo Topology) bool {
	for _, raw := range append(append([]string{}, topo.LAN...), topo.Public...) {
		if ip, err := netip.ParseAddr(raw); err == nil && ip.Is4() {
			return true
		}
	}
	return false
}
