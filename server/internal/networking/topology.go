package networking

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"github.com/PorticoMediaServer/portico/server/internal/networkpolicy"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
)

type Topology struct {
	Incomplete   bool     `json:"incomplete,omitempty"`
	Gateway      string   `json:"gateway,omitempty"`
	LocalAddress string   `json:"localAddress,omitempty"`
	LAN          []string `json:"lan"`
	Public       []string `json:"public"`
	Bind         string   `json:"bind"`
	ErrorCode    string   `json:"errorCode,omitempty"`
}

// VirtualInterface reports whether an interface name looks virtual, using the
// same prefix rule as topology observation and the connectivity report.
func VirtualInterface(name string) bool { return networkpolicy.VirtualInterface(name) }

// ifaceAddrs is one interface with its addresses as prefix strings, so tests
// can inject topologies without touching the host's interfaces.
type ifaceAddrs struct {
	Name  string
	Flags net.Flags
	Addrs []string
	// AddrsError marks an interface whose addresses could not be read, so the
	// observation is incomplete (the old iface.Addrs error path).
	AddrsError bool
}

// topologyInterfaces enumerates the host's up interfaces. topologyDialLocal
// returns the gateway-selected source address without sending a packet.
var topologyInterfaces = func() ([]ifaceAddrs, error) {
	raw, e := net.Interfaces()
	if e != nil {
		return nil, e
	}
	out := make([]ifaceAddrs, 0, len(raw))
	for _, iface := range raw {
		entry := ifaceAddrs{Name: iface.Name, Flags: iface.Flags}
		addresses, e := iface.Addrs()
		if e != nil {
			// A failing interface leaves the observation incomplete.
			entry.AddrsError = true
			out = append(out, entry)
			continue
		}
		for _, address := range addresses {
			entry.Addrs = append(entry.Addrs, address.String())
		}
		out = append(out, entry)
	}
	return out, nil
}

var topologyDialLocal = func(gateway string) (string, error) {
	c, e := net.DialUDP("udp4", nil, &net.UDPAddr{IP: net.ParseIP(gateway), Port: 5351})
	if e != nil {
		return "", e
	}
	defer c.Close()
	return c.LocalAddr().(*net.UDPAddr).IP.String(), nil
}

func publicAddress(ip netip.Addr) bool {
	if !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.Is4In6() || ip.IsLinkLocalUnicast() || ip.Zone() != "" {
		return false
	}
	for _, p := range []string{"100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24", "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "2001:db8::/32"} {
		if netip.MustParsePrefix(p).Contains(ip) {
			return false
		}
	}
	return true
}
func gatewayAddress(raw string) bool {
	ip, e := netip.ParseAddr(raw)
	return e == nil && ip.Is4() && ip.IsPrivate() && !ip.IsLoopback()
}
func defaultGateway(ctx context.Context) (string, error) {
	if runtime.GOOS == "linux" {
		f, e := os.Open("/proc/net/route")
		if e != nil {
			return "", e
		}
		defer f.Close()
		scanner := bufio.NewScanner(f)
		selected := ""
		metric := int64(1 << 62)
		for scanner.Scan() {
			fields := strings.Fields(scanner.Text())
			if len(fields) < 8 || fields[1] != "00000000" || fields[7] != "00000000" {
				continue
			}
			flags, e := strconv.ParseUint(fields[3], 16, 32)
			if e != nil || flags&3 != 3 {
				continue
			}
			cost, e := strconv.ParseInt(fields[6], 10, 64)
			if e != nil || cost < 0 || cost >= metric {
				continue
			}
			b, e := hex.DecodeString(fields[2])
			if e != nil || len(b) != 4 {
				continue
			}
			var out [4]byte
			binary.BigEndian.PutUint32(out[:], binary.LittleEndian.Uint32(b))
			ip := netip.AddrFrom4(out)
			if gatewayAddress(ip.String()) {
				selected = ip.String()
				metric = cost
			}
		}
		if selected != "" {
			return selected, nil
		}
		return "", errors.New("default gateway unavailable")
	}
	if runtime.GOOS == "darwin" {
		q, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		b, e := exec.CommandContext(q, "/sbin/route", "-n", "get", "default").Output()
		if e != nil || len(b) > 16384 {
			return "", errors.New("default gateway unavailable")
		}
		for _, line := range strings.Split(string(b), "\n") {
			f := strings.Fields(line)
			if len(f) == 2 && f[0] == "gateway:" && gatewayAddress(f[1]) {
				return f[1], nil
			}
		}
	}
	return "", errors.New("automatic gateway observation unsupported")
}
func observeTopology(ctx context.Context, bind, gateway, advertisedInterface string) (Topology, error) {
	out := Topology{Bind: bind, LAN: []string{}, Public: []string{}}
	host, _, e := net.SplitHostPort(bind)
	if e != nil {
		return out, e
	}
	if host == "" {
		host = "::"
	}
	bound, e := netip.ParseAddr(host)
	if e != nil {
		return out, e
	}
	if bound.IsLoopback() {
		out.ErrorCode = "listener_loopback_only"
		return out, nil
	}
	interfaces, e := topologyInterfaces()
	if e != nil {
		return out, e
	}
	if gateway == "" {
		gateway, _ = defaultGateway(ctx)
	}
	if gateway != "" && !gatewayAddress(gateway) {
		return out, ErrInvalid
	}
	out.Gateway = gateway
	// Observe the gateway source while keeping virtual interfaces excluded.
	gatewayLocal := ""
	gatewayIface := ""
	if gateway != "" {
		// UDP connect observes the selected source address without sending a packet.
		if local, e := topologyDialLocal(gateway); e == nil {
			gatewayLocal = local
			for _, iface := range interfaces {
				if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 || VirtualInterface(iface.Name) {
					continue
				}
				for _, raw := range iface.Addrs {
					prefix, e := netip.ParsePrefix(raw)
					if e != nil {
						continue
					}
					if prefix.Addr().Unmap().String() == gatewayLocal {
						gatewayIface = iface.Name
						break
					}
				}
				if gatewayIface != "" {
					break
				}
			}
		}
	}
	// An explicitly advertised interface restricts the LAN to its addresses. A
	// missing or down interface falls back to automatic observation.
	effectiveAdvertised := advertisedInterface
	advertisedFound := false
	if effectiveAdvertised != "" {
		for _, iface := range interfaces {
			if iface.Name == effectiveAdvertised && iface.Flags&net.FlagUp != 0 && iface.Flags&net.FlagLoopback == 0 {
				advertisedFound = true
				break
			}
		}
		if !advertisedFound {
			effectiveAdvertised = ""
		}
	}
	for _, iface := range interfaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 || VirtualInterface(iface.Name) {
			continue
		}
		if len(iface.Addrs) == 0 && iface.AddrsError {
			out.Incomplete = true
		}
		for _, raw := range iface.Addrs {
			prefix, e := netip.ParsePrefix(raw)
			if e != nil {
				continue
			}
			ip := prefix.Addr().Unmap()
			if !bound.IsUnspecified() && ip != bound {
				continue
			}
			// An IPv4 wildcard does not accept IPv6. IPv6 wildcard is the Go dual-stack
			// listener where the OS supports it; proof remains the reachability authority.
			if bound.Is4() && ip.Is6() {
				continue
			}
			if ip.IsPrivate() {
				if effectiveAdvertised != "" {
					if iface.Name != effectiveAdvertised {
						continue
					}
				}
				out.LAN = append(out.LAN, ip.String())
			} else if publicAddress(ip) {
				out.Public = append(out.Public, ip.String())
			}
		}
	}
	sort.Strings(out.LAN)
	sort.Strings(out.Public)
	if len(out.LAN) > 64 {
		out.Incomplete = true
		out.LAN = out.LAN[:64]
	}
	if len(out.Public) > 2 {
		out.Incomplete = true
		first, _ := netip.ParseAddr(out.Public[0])
		selected := append([]string{}, out.Public[:2]...)
		for _, raw := range out.Public[1:] {
			ip, _ := netip.ParseAddr(raw)
			if ip.Is4() != first.Is4() {
				selected[1] = raw
				break
			}
		}
		out.Public = selected
		sort.Strings(out.Public)
	}
	if gatewayLocal != "" {
		for _, ip := range out.LAN {
			if ip == gatewayLocal {
				out.LocalAddress = gatewayLocal
				break
			}
		}
	}
	if out.Gateway == "" {
		out.ErrorCode = "gateway_not_found"
	} else if out.LocalAddress == "" {
		out.ErrorCode = "gateway_outside_listener"
	}
	if advertisedInterface != "" && !advertisedFound && out.ErrorCode == "" {
		out.ErrorCode = "advertised_interface_missing"
	}
	return out, nil
}
