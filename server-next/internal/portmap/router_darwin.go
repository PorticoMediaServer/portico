package portmap

import (
	"net"
	"net/netip"
	"syscall"
)

func defaultRouter() Router                     { return darwinRouter(syscall.AF_INET, netip.Addr{}) }
func defaultIPv6Router(bound netip.Addr) Router { return darwinRouter(syscall.AF_INET6, bound) }

func darwinRouter(family int, bound netip.Addr) Router {
	rib, err := syscall.RouteRIB(syscall.NET_RT_DUMP, 0)
	if err != nil {
		return Router{}
	}
	messages, err := syscall.ParseRoutingMessage(rib)
	if err != nil {
		return Router{}
	}
	for _, message := range messages {
		route, ok := message.(*syscall.RouteMessage)
		// Interface-scoped routes do not choose the machine's default interface.
		if !ok || route.Header.Flags&(syscall.RTF_UP|syscall.RTF_GATEWAY) != syscall.RTF_UP|syscall.RTF_GATEWAY ||
			route.Header.Flags&(syscall.RTF_REJECT|syscall.RTF_BLACKHOLE|syscall.RTF_IFSCOPE) != 0 {
			continue
		}
		addresses, err := syscall.ParseRoutingSockaddr(route)
		if err != nil {
			continue
		}
		gateway := darwinDefaultGateway(addresses, family)
		if !gateway.IsValid() {
			continue
		}
		iface, err := net.InterfaceByIndex(int(route.Header.Index))
		if err != nil || iface.Flags&net.FlagUp == 0 {
			continue
		}
		if family == syscall.AF_INET {
			return Router{Gateway: gateway, PMPPort: 5351, SSDP: netip.MustParseAddrPort("239.255.255.250:1900")}
		}
		if gateway.IsLinkLocalUnicast() {
			gateway = gateway.WithZone(iface.Name)
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			prefix, err := netip.ParsePrefix(addr.String())
			if err != nil {
				continue
			}
			ip := prefix.Addr()
			if usableRouterClient(ip, bound) {
				return Router{Gateway: gateway, Client: ip, PMPPort: 5351,
					SSDP: netip.AddrPortFrom(netip.MustParseAddr("ff02::c").WithZone(iface.Name), 1900)}
			}
		}
	}
	return Router{}
}

func darwinDefaultGateway(addresses []syscall.Sockaddr, family int) netip.Addr {
	if len(addresses) <= syscall.RTAX_NETMASK {
		return netip.Addr{}
	}
	addr := func(sa syscall.Sockaddr) netip.Addr {
		switch sa := sa.(type) {
		case *syscall.SockaddrInet4:
			return netip.AddrFrom4(sa.Addr)
		case *syscall.SockaddrInet6:
			ip := sa.Addr
			// Darwin embeds the interface index in bytes 2 and 3 of link-local addresses.
			if ip[0] == 0xfe && ip[1]&0xc0 == 0x80 {
				ip[2], ip[3] = 0, 0
			}
			return netip.AddrFrom16(ip)
		}
		return netip.Addr{}
	}
	destination, gateway, mask := addr(addresses[syscall.RTAX_DST]), addr(addresses[syscall.RTAX_GATEWAY]), addr(addresses[syscall.RTAX_NETMASK])
	if !destination.IsUnspecified() || (mask.IsValid() && !mask.IsUnspecified()) || !gateway.IsValid() || gateway.IsUnspecified() {
		return netip.Addr{}
	}
	if family == syscall.AF_INET && gateway.Is4() && (gateway.IsPrivate() || gateway.IsLinkLocalUnicast()) {
		return gateway
	}
	if family == syscall.AF_INET6 && gateway.Is6() {
		return gateway
	}
	return netip.Addr{}
}
