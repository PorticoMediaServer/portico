package portmap

import (
	"net"
	"net/netip"
	"unsafe"

	"golang.org/x/sys/windows"
)

func defaultRouter() Router                     { return windowsRouter(windows.AF_INET, netip.Addr{}) }
func defaultIPv6Router(bound netip.Addr) Router { return windowsRouter(windows.AF_INET6, bound) }

func windowsRouter(family uint16, bound netip.Addr) Router {
	var table *windows.MibIpForwardTable2
	if windows.GetIpForwardTable2(family, &table) != nil {
		return Router{}
	}
	defer windows.FreeMibTable(unsafe.Pointer(table))
	var best *windows.MibIpForwardRow2
	var metric uint64 = ^uint64(0)
	for i := range table.Rows() {
		row := &table.Rows()[i]
		if !windowsDefaultRoute(*row, family) {
			continue
		}
		iface := windows.MibIpInterfaceRow{Family: family, InterfaceLuid: row.InterfaceLuid}
		if windows.GetIpInterfaceEntry(&iface) != nil || iface.Connected == 0 || iface.DisableDefaultRoutes != 0 {
			continue
		}
		cost := uint64(row.Metric) + uint64(iface.Metric)
		if cost < metric {
			best, metric = row, cost
		}
	}
	if best == nil {
		return Router{}
	}
	gateway := windowsRouteAddress(best.NextHop)
	if family == windows.AF_INET {
		if !gateway.IsPrivate() && !gateway.IsLinkLocalUnicast() {
			return Router{}
		}
		return Router{Gateway: gateway, PMPPort: 5351, SSDP: netip.MustParseAddrPort("239.255.255.250:1900")}
	}
	iface, err := net.InterfaceByIndex(int(best.InterfaceIndex))
	if err != nil {
		return Router{}
	}
	zone := iface.Name
	if gateway.IsLinkLocalUnicast() {
		gateway = gateway.WithZone(zone)
	}
	var addresses *windows.MibUnicastIpAddressTable
	if windows.GetUnicastIpAddressTable(family, &addresses) != nil {
		return Router{}
	}
	defer windows.FreeMibTable(unsafe.Pointer(addresses))
	client := windowsIPv6Client(unsafe.Slice(&addresses.Table[0], addresses.NumEntries), best.InterfaceLuid, bound)
	if !client.IsValid() {
		return Router{}
	}
	return Router{Gateway: gateway, Client: client, PMPPort: 5351,
		SSDP: netip.AddrPortFrom(netip.MustParseAddr("ff02::c").WithZone(zone), 1900)}
}

func windowsIPv6Client(rows []windows.MibUnicastIpAddressRow, luid uint64, bound netip.Addr) netip.Addr {
	// Windows marks both stable and temporary SLAAC identifiers as random. Among
	// those, prefer the earliest-created address; real Windows behaviour remains unproved.
	var temporary netip.Addr
	var oldest uint64
	for _, row := range rows {
		if row.InterfaceLuid != luid || row.SkipAsSource != 0 || row.DadState != windows.IpDadStatePreferred || row.PreferredLifetime == 0 {
			continue
		}
		ip := netip.AddrFrom16(row.Address.Addr)
		if !usableRouterClient(ip, bound) {
			continue
		}
		if row.SuffixOrigin != windows.IpSuffixOriginRandom {
			return ip
		}
		created := uint64(row.CreationTimeStamp.HighDateTime)<<32 | uint64(row.CreationTimeStamp.LowDateTime)
		if !temporary.IsValid() || created < oldest {
			temporary, oldest = ip, created
		}
	}
	return temporary
}

func windowsDefaultRoute(row windows.MibIpForwardRow2, family uint16) bool {
	return row.DestinationPrefix.PrefixLength == 0 && row.DestinationPrefix.Prefix.Family == family &&
		windowsRouteAddress(row.DestinationPrefix.Prefix).IsUnspecified() && row.NextHop.Family == family &&
		!windowsRouteAddress(row.NextHop).IsUnspecified() && row.Loopback == 0 && row.ValidLifetime != 0
}

func windowsRouteAddress(raw windows.RawSockaddrInet) netip.Addr {
	switch raw.Family {
	case windows.AF_INET:
		return netip.AddrFrom4((*windows.RawSockaddrInet4)(unsafe.Pointer(&raw)).Addr)
	case windows.AF_INET6:
		return netip.AddrFrom16((*windows.RawSockaddrInet6)(unsafe.Pointer(&raw)).Addr)
	}
	return netip.Addr{}
}
