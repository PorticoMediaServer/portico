package portmap

import (
	"encoding/binary"
	"net"
	"net/netip"
	"syscall"
	"unsafe"
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
		client := darwinIPv6Client(addrs, bound, func(ip netip.Addr) (uint32, error) { return darwinAddressFlags(iface.Name, ip) })
		if client.IsValid() {
			return Router{Gateway: gateway, Client: client, PMPPort: 5351,
				SSDP: netip.AddrPortFrom(netip.MustParseAddr("ff02::c").WithZone(iface.Name), 1900)}
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

// Prefer a persistent address; temporary or unclassified addresses are only a fallback.
func darwinIPv6Client(addrs []net.Addr, bound netip.Addr, flags func(netip.Addr) (uint32, error)) netip.Addr {
	var fallback netip.Addr
	for _, addr := range addrs {
		prefix, err := netip.ParsePrefix(addr.String())
		if err != nil || !usableRouterClient(prefix.Addr(), bound) {
			continue
		}
		ip := prefix.Addr()
		bits, err := flags(ip)
		// IN6_IFF_TENTATIVE, DUPLICATED, DETACHED, DEPRECATED and OPTIMISTIC are not ready.
		if err == nil && bits&0x021e != 0 {
			continue
		}
		// IN6_IFF_TEMPORARY marks the rotating privacy address.
		if err == nil && bits&0x0080 == 0 {
			return ip
		}
		if !fallback.IsValid() {
			fallback = ip
		}
	}
	return fallback
}

// Darwin's SIOCGIFAFLAG_IN6 uses in6_ifreq (16-byte name + 272-byte union).
// The union takes sockaddr_in6 on input and returns ifru_flags6 at the same offset.
func darwinAddressFlags(name string, ip netip.Addr) (uint32, error) {
	fd, err := syscall.Socket(syscall.AF_INET6, syscall.SOCK_DGRAM, 0)
	if err != nil {
		return 0, err
	}
	defer syscall.Close(fd)
	var request [288]byte
	copy(request[:16], name)
	request[16], request[17] = 28, syscall.AF_INET6
	address := ip.As16()
	copy(request[24:40], address[:])
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), 0xc1206949, uintptr(unsafe.Pointer(&request[0])))
	if errno != 0 {
		return 0, errno
	}
	return binary.NativeEndian.Uint32(request[16:20]), nil
}
