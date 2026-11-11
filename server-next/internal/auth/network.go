package auth

import (
	"errors"
	"net"
	"net/http"
	"net/netip"
	"os"
	"strconv"
	"strings"
)

// Networks is the owner's answer to two questions: which addresses are home, and which
// reverse proxies are believed when they say who a request really came from. Nothing is
// guessed: a proxy or an overlay network the owner has not listed is not detected, it is
// simply an address like any other.
type Networks struct {
	// Home is the only definition of local and remote. An address in none of these is away.
	Home []netip.Prefix
	// Proxies are believed when they pass on a client's address in X-Forwarded-For. The
	// same header from any other address is ignored.
	Proxies []netip.Prefix
	// Gateway is the address this machine sends everything not on its own networks to: a
	// home router, or inside a container the host's end of the container network. While
	// the server is guessing what is home, a connection from this one address is not: some
	// set-ups (a container with a published port, a router that rewrites what it forwards)
	// make every visitor arrive from it, and all of them would count as at home. It is not
	// set when the owner has said what home is: then their list alone decides.
	Gateway netip.Addr
}

// maxForwardedHops is how many trusted proxies in a row a forwarded header is read through.
const maxForwardedHops = 32

func within(prefixes []netip.Prefix, addr netip.Addr) bool {
	for _, p := range prefixes {
		if p.Contains(addr) {
			return true
		}
	}
	return false
}

// Client returns the address a request came from: the connection's own address or, when
// that is one of the owner's trusted proxies, the nearest address in X-Forwarded-For that
// is not itself a trusted proxy. The header can be written by anyone, so it is read from
// the end the trusted proxy wrote and no further than the first address that is not a
// trusted proxy: whatever a client put before that is never looked at, so nothing a client
// sends can make it count as the proxy. If the proxy's own entry cannot be read, or it sent
// none, the answer is the proxy's own address: what wrong guesses are then counted against.
// It is never taken for the visitor's; see ProxyGaveNoAddress.
func (n Networks) Client(r *http.Request) (netip.Addr, bool) {
	ap, err := netip.ParseAddrPort(r.RemoteAddr)
	if err != nil {
		return netip.Addr{}, false
	}
	peer := ap.Addr().Unmap().WithZone("")
	if !within(n.Proxies, peer) {
		return peer, true
	}
	lines := r.Header.Values("X-Forwarded-For")
	for read, l := 0, len(lines)-1; l >= 0; l-- {
		rest := lines[l]
		for more := true; more; read++ {
			var raw string
			if at := strings.LastIndexByte(rest, ','); at >= 0 {
				rest, raw = rest[:at], rest[at+1:]
			} else {
				raw, more = rest, false
			}
			hop, err := netip.ParseAddr(strings.TrimSpace(raw))
			if err != nil || hop.Zone() != "" || hop.IsUnspecified() || hop.IsMulticast() || read == maxForwardedHops {
				return peer, true
			}
			if hop = hop.Unmap(); !within(n.Proxies, hop) {
				return hop, true
			}
		}
	}
	return peer, true
}

// ThroughUnlistedProxy reports whether a request carries X-Forwarded-For from an address
// that is not one of the owner's trusted proxies. The header is still not believed, but
// it shows the request was passed on by something, so the connection's own address is that
// thing's and not the person's.
func (n Networks) ThroughUnlistedProxy(r *http.Request) bool {
	if len(r.Header.Values("X-Forwarded-For")) == 0 {
		return false
	}
	ap, err := netip.ParseAddrPort(r.RemoteAddr)
	return err != nil || !within(n.Proxies, ap.Addr().Unmap().WithZone(""))
}

// UnlistedLocalProxy reports whether a request carries X-Forwarded-For from an address on
// this machine or a private network that is not one of the owner's trusted proxies: almost
// always the owner's own reverse proxy, not yet listed. The header is still not believed.
// Until the proxy is listed, everyone who comes through it is one address to this server.
func (n Networks) UnlistedLocalProxy(r *http.Request) bool {
	if !n.ThroughUnlistedProxy(r) {
		return false
	}
	ap, err := netip.ParseAddrPort(r.RemoteAddr)
	if err != nil {
		return false
	}
	peer := ap.Addr().Unmap().WithZone("")
	return peer.IsLoopback() || peer.IsPrivate() || peer.IsLinkLocalUnicast()
}

// ProxyGaveNoAddress reports whether a request came from one of the owner's trusted proxies
// without a visitor's address the server can use: no X-Forwarded-For, one that cannot be
// read, or one naming only trusted proxies. All that is known then is the proxy's own
// address, which says nothing about who is behind it.
func (n Networks) ProxyGaveNoAddress(r *http.Request) bool {
	ap, err := netip.ParseAddrPort(r.RemoteAddr)
	if err != nil {
		return false
	}
	peer := ap.Addr().Unmap().WithZone("")
	if !within(n.Proxies, peer) {
		return false
	}
	addr, _ := n.Client(r)
	return addr == peer
}

// FromHome reports whether a request came from one of the owner's home networks. Two kinds
// of request are never from home, whatever address they arrive from. One passed on by a
// proxy the owner has not listed: a proxy on the same machine or network would otherwise
// make everyone on the internet look like the household. And one from a listed proxy that
// passes on no visitor's address: the proxy itself is usually at home, and everyone behind
// it would be too. Both only ever take trust away, so no header can be used to gain anything.
func (n Networks) FromHome(r *http.Request) bool {
	addr, ok := n.Client(r)
	return ok && n.AtHome(addr) && !n.ThroughUnlistedProxy(r) && !n.ProxyGaveNoAddress(r)
}

// AtHome reports whether an address is on one of the owner's home networks.
func (n Networks) AtHome(addr netip.Addr) bool {
	addr = addr.Unmap().WithZone("")
	return addr.IsValid() && addr != n.Gateway && within(n.Home, addr)
}

// DefaultGateway returns the IPv4 address this machine's default route goes through, or the
// zero address where there is none or it cannot be read (it is read from Linux's route
// table; a container is always Linux inside).
func DefaultGateway() netip.Addr {
	table, err := os.ReadFile("/proc/net/route")
	if err != nil {
		return netip.Addr{}
	}
	return gatewayIn(string(table))
}

// gatewayIn reads the default route's gateway from the text of /proc/net/route: the row
// whose destination and mask are zero and whose flags say it has a gateway. The addresses
// are hexadecimal with the bytes in the machine's order, which is reversed on every
// machine this server runs on.
func gatewayIn(table string) netip.Addr {
	for _, line := range strings.Split(table, "\n")[1:] {
		f := strings.Fields(line)
		if len(f) < 8 || f[1] != "00000000" || f[7] != "00000000" {
			continue
		}
		flags, err := strconv.ParseUint(f[3], 16, 32)
		raw, err2 := strconv.ParseUint(f[2], 16, 32)
		if err != nil || err2 != nil || flags&2 == 0 || raw == 0 {
			continue
		}
		return netip.AddrFrom4([4]byte{byte(raw), byte(raw >> 8), byte(raw >> 16), byte(raw >> 24)})
	}
	return netip.Addr{}
}

// SourceKey is what wrong guesses from an address are counted against: the IPv4 address,
// or the /64 of an IPv6 address, since one household is handed a whole /64.
func SourceKey(addr netip.Addr) string {
	if addr.Is6() {
		prefix, _ := addr.Prefix(64)
		return prefix.String()
	}
	return addr.String()
}

// DefaultHome is what counts as home until the owner says otherwise: this machine, and
// the private networks it is directly attached to. A public address is never home by default.
//
// In a container on a network of its own (a bridge), nothing is home by default: Docker
// Desktop, rootless Docker and Podman, and a host with its firewall rules off relay every
// visitor, from outside too, so that they arrive from an address on the container's network,
// and the server cannot tell who is at home. Everyone counts as away until the owner lists
// their home networks or runs the container with host networking.
func DefaultHome() []netip.Prefix {
	if ContainerNetwork() {
		return []netip.Prefix{}
	}
	out := []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8"), netip.MustParsePrefix("::1/128")}
	addrs, _ := net.InterfaceAddrs()
	for _, a := range addrs {
		p, err := netip.ParsePrefix(a.String())
		if err != nil {
			continue
		}
		p = p.Masked()
		if (p.Addr().IsPrivate() || p.Addr().IsLinkLocalUnicast()) && !within(out, p.Addr()) {
			out = append(out, p)
		}
	}
	return out
}

// ContainerNetwork reports whether this server runs in a container on a network of its own. It
// is a variable so tests on any machine can say either.
var ContainerNetwork = containerNetwork

// containerNetwork: a container says so by /.dockerenv (Docker), /run/.containerenv (Podman),
// the container variable (Podman, systemd-nspawn, LXC) or a runtime named in the first
// process's control group (Kubernetes and others). With host networking the container sees
// the host's own interfaces, the container runtime's bridge among them, and then it is not on
// a network of its own. A wrong "yes" costs only that the household is away until the owner
// lists their home networks; a wrong "no" leaves the guess the server makes on a plain machine.
func containerNetwork() bool {
	_, docker := os.Stat("/.dockerenv")
	_, podman := os.Stat("/run/.containerenv")
	cgroup, _ := os.ReadFile("/proc/1/cgroup")
	if docker != nil && podman != nil && os.Getenv("container") == "" && !runtimeIn(string(cgroup)) {
		return false
	}
	ifaces, _ := net.Interfaces()
	for _, i := range ifaces {
		if hostBridge(i.Name) {
			return false
		}
	}
	return true
}

// runtimeIn reports whether a /proc/1/cgroup text names a container runtime.
func runtimeIn(cgroup string) bool {
	for _, name := range []string{"docker", "kubepods", "containerd", "libpod", "lxc"} {
		if strings.Contains(cgroup, name) {
			return true
		}
	}
	return false
}

// hostBridge reports whether an interface name is a container runtime's bridge or a container's
// end of one, which only the host's own network namespace has.
func hostBridge(name string) bool {
	return name == "docker0" || name == "podman0" || name == "cni-podman0" || name == "cni0" ||
		strings.HasPrefix(name, "br-") || strings.HasPrefix(name, "veth")
}

// ErrNetwork: an entry is not an address or a network such as 192.168.1.0/24.
var ErrNetwork = errors.New("not an address or a network")

// ParseNetworks reads a list of addresses and networks in CIDR form.
func ParseNetworks(list []string) ([]netip.Prefix, error) {
	out := make([]netip.Prefix, 0, len(list))
	for _, raw := range list {
		raw = strings.TrimSpace(raw)
		p, err := netip.ParsePrefix(raw)
		if err != nil {
			addr, err := netip.ParseAddr(raw)
			if err != nil || addr.Zone() != "" {
				return nil, ErrNetwork
			}
			p = netip.PrefixFrom(addr, addr.BitLen())
		}
		if p.Addr().Is4In6() {
			return nil, ErrNetwork
		}
		out = append(out, p.Masked())
	}
	return out, nil
}
