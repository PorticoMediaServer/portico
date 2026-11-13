package portmap

import (
	"bufio"
	"encoding/hex"
	"io"
	"net/netip"
	"strconv"
	"strings"

	"log/slog"
)

// NewIPv6 keeps an independent IPv6 opening, using the same bounded lease and
// shutdown loop as IPv4. NAT-PMP does not provide IPv6 firewall control.
func NewIPv6(log *slog.Logger, router func() Router) *Mapper {
	if router == nil {
		router = func() Router { return DefaultIPv6Router(netip.Addr{}) }
	}
	m := New(log, router)
	m.ipv6 = true
	return m
}

// ClientAddress discovers the local global address without opening the router.
// It is also the address used when the owner handles the firewall by hand.
func (m *Mapper) ClientAddress() netip.Addr { return m.router().Client }

// DefaultIPv6Router reads the default IPv6 route and a global address
// on that interface. No guessed address or address from another interface is used.
func DefaultIPv6Router(bound netip.Addr) Router {
	if bound.IsValid() && (!bound.Is6() || bound.Is4In6()) {
		return Router{}
	}
	return defaultIPv6Router(bound)
}

func ipv6Hex(s string) netip.Addr {
	b, err := hex.DecodeString(s)
	if err != nil || len(b) != 16 {
		return netip.Addr{}
	}
	ip, _ := netip.AddrFromSlice(b)
	return ip
}

func readIPv6Router(routes, addresses io.Reader, bound netip.Addr) Router {
	var gateway netip.Addr
	var iface string
	var metric uint64 = ^uint64(0)
	sc := bufio.NewScanner(routes)
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) != 10 || f[0] != strings.Repeat("0", 32) || f[1] != "00" || f[3] != "00" {
			continue
		}
		flags, err := strconv.ParseUint(f[8], 16, 32)
		cost, costErr := strconv.ParseUint(f[5], 16, 32)
		gw := ipv6Hex(f[4])
		if err != nil || costErr != nil || flags&1 == 0 || flags&0x200 != 0 || !gw.IsValid() || gw.IsUnspecified() || cost >= metric {
			continue
		}
		gateway, iface, metric = gw, f[9], cost
	}
	if !gateway.IsValid() {
		return Router{}
	}
	if gateway.IsLinkLocalUnicast() {
		gateway = gateway.WithZone(iface)
	}
	r := Router{Gateway: gateway, PMPPort: 5351, SSDP: netip.AddrPortFrom(netip.MustParseAddr("ff02::c").WithZone(iface), 1900)}
	var temporary netip.Addr
	sc = bufio.NewScanner(addresses)
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) != 6 || f[5] != iface {
			continue
		}
		flags, err := strconv.ParseUint(f[4], 16, 32)
		ip := ipv6Hex(f[0])
		if bound.IsValid() && !bound.IsUnspecified() && ip != bound {
			continue
		}
		// Prefer a stable address, but a temporary address is usable when it
		// is all the interface has. Never use failed, deprecated or tentative ones.
		if err == nil && flags&0x68 == 0 && ip.IsGlobalUnicast() && !ip.IsPrivate() && !ip.IsLinkLocalUnicast() {
			if flags&1 != 0 {
				temporary = ip
				continue
			}
			r.Client = ip
			return r
		}
	}
	if temporary.IsValid() {
		r.Client = temporary
		return r
	}
	return Router{}
}

func usableRouterClient(ip, bound netip.Addr) bool {
	return ip.Is6() && !ip.Is4In6() && ip.IsGlobalUnicast() && !ip.IsPrivate() && !ip.IsLinkLocalUnicast() &&
		(!bound.IsValid() || bound.IsUnspecified() || ip == bound)
}
