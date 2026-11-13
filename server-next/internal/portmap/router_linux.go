package portmap

import (
	"bufio"
	"net/netip"
	"os"
	"strconv"
	"strings"
)

func defaultRouter() Router {
	r := Router{PMPPort: 5351, SSDP: netip.MustParseAddrPort("239.255.255.250:1900")}
	f, err := os.Open("/proc/net/route")
	if err != nil {
		return r
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 3 || fields[1] != "00000000" {
			continue
		}
		raw, err := strconv.ParseUint(fields[2], 16, 32)
		if err != nil || raw == 0 {
			continue
		}
		// The file holds the address in the machine's byte order, which is little-endian
		// everywhere Portico runs.
		gw := netip.AddrFrom4([4]byte{byte(raw), byte(raw >> 8), byte(raw >> 16), byte(raw >> 24)})
		if gw.IsPrivate() || gw.IsLinkLocalUnicast() {
			r.Gateway = gw
		}
		return r
	}
	return r
}

func defaultIPv6Router(bound netip.Addr) Router {
	routes, err := os.Open("/proc/net/ipv6_route")
	if err != nil {
		return Router{}
	}
	defer routes.Close()
	addresses, err := os.Open("/proc/net/if_inet6")
	if err != nil {
		return Router{}
	}
	defer addresses.Close()
	return readIPv6Router(routes, addresses, bound)
}
