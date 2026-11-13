package discovery

import (
	"net"
	"net/netip"
)

// DiscoveryInterface is one interface the owner can choose for local announcements.
type DiscoveryInterface struct {
	Name      string   `json:"name"`
	Addresses []string `json:"addresses"`
}

// Interfaces lists up, non-loopback interfaces with an address usable for local discovery.
func Interfaces() []DiscoveryInterface {
	out := []DiscoveryInterface{}
	ifaces, _ := net.Interfaces()
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 || iface.Flags&net.FlagMulticast == 0 {
			continue
		}
		addresses, _ := iface.Addrs()
		row := DiscoveryInterface{Name: iface.Name, Addresses: []string{}}
		for _, raw := range addresses {
			if p, err := netip.ParsePrefix(raw.String()); err == nil && p.Addr().Is4() && p.Addr().IsPrivate() {
				row.Addresses = append(row.Addresses, p.Addr().String())
			}
		}
		if len(row.Addresses) > 0 {
			out = append(out, row)
		}
	}
	return out
}

// PreferredInterface falls back to automatic if the saved interface is no longer usable.
func PreferredInterface(preferred string, interfaces []DiscoveryInterface) string {
	for _, iface := range interfaces {
		if iface.Name == preferred {
			return preferred
		}
	}
	return ""
}
