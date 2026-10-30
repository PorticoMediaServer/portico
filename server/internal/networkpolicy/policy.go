// Package networkpolicy owns inbound client address and home-network decisions.
// It does not govern outbound fetches, which have separate SSRF restrictions.
package networkpolicy

import (
	"context"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
)

// VirtualInterface excludes container, bridge and overlay interfaces from
// automatic LAN routes/defaults. Owners may add explicit LAN networks.
func VirtualInterface(name string) bool {
	for _, prefix := range []string{"docker", "br", "veth", "virbr", "vmnet", "vboxnet", "cni", "flannel", "cali", "lxcbr", "lxdbr", "podman", "zt", "wg", "tun", "tap", "utun", "tailscale"} {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}
func DefaultHomeNetworks() []string {
	out := []string{"127.0.0.0/8", "::1/128", "169.254.0.0/16", "fe80::/10"}
	seen := map[string]bool{}
	interfaces, _ := net.Interfaces()
	for _, iface := range interfaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 || VirtualInterface(iface.Name) {
			continue
		}
		addresses, _ := iface.Addrs()
		for _, address := range addresses {
			p, err := netip.ParsePrefix(address.String())
			if err != nil || !p.Addr().IsPrivate() || netip.MustParsePrefix("fd7a:115c:a1e0::/48").Contains(p.Addr()) {
				continue
			}
			value := p.Masked().String()
			if !seen[value] && len(out) < 64 {
				out = append(out, value)
				seen[value] = true
			}
		}
	}
	return out
}

func Contains(prefixes []netip.Prefix, addr netip.Addr) bool {
	addr = addr.Unmap().WithZone("")
	if !addr.IsValid() || addr.IsUnspecified() || addr.IsMulticast() {
		return false
	}
	for _, p := range prefixes {
		if p.Contains(addr) {
			return true
		}
	}
	return false
}

func ParseAddress(raw string) (netip.Addr, bool) {
	if host, _, err := net.SplitHostPort(raw); err == nil {
		raw = host
	}
	addr, err := netip.ParseAddr(strings.Trim(raw, "[]"))
	if err != nil || addr.IsUnspecified() || addr.IsMulticast() {
		return netip.Addr{}, false
	}
	return addr.Unmap().WithZone(""), true
}

func Peer(r *http.Request) (netip.Addr, bool) { return ParseAddress(r.RemoteAddr) }

// headerValues also handles deliberately noncanonical header maps.
func headerValues(r *http.Request, key string) []string {
	var out []string
	for k, values := range r.Header {
		if strings.EqualFold(k, key) {
			out = append(out, values...)
		}
	}
	return out
}

// Forwarded is bounded and rejects ambiguous or malformed chains in full,
// including attacker-supplied entries to the left of the real client.
func Forwarded(r *http.Request, proxies []netip.Prefix) (netip.Addr, bool) {
	spellings := 0
	for key := range r.Header {
		if strings.EqualFold(key, "X-Forwarded-For") {
			spellings++
		}
	}
	if spellings > 1 {
		return netip.Addr{}, false
	}
	xff := headerValues(r, "X-Forwarded-For")
	var hops []netip.Addr
	if len(xff) > 0 {
		for _, row := range xff {
			if len(row) > 8192 {
				return netip.Addr{}, false
			}
			for _, raw := range strings.Split(row, ",") {
				a, err := netip.ParseAddr(strings.TrimSpace(raw))
				if err != nil || a.Zone() != "" || a.IsUnspecified() || a.IsMulticast() {
					return netip.Addr{}, false
				}
				hops = append(hops, a.Unmap())
				if len(hops) > 32 {
					return netip.Addr{}, false
				}
			}
		}
	} else {
		elements, ok := forwardedElements(r)
		if !ok {
			return netip.Addr{}, false
		}
		for _, fields := range elements {
			raw := fields["for"]
			a, ok := ParseAddress(raw)
			if !ok || strings.Contains(raw, "%") {
				return netip.Addr{}, false
			}
			hops = append(hops, a)
		}
	}
	for i := len(hops) - 1; i >= 0; i-- {
		if !Contains(proxies, hops[i]) || i == 0 {
			return hops[i], true
		}
	}
	return netip.Addr{}, false
}

func Client(r *http.Request, proxies []netip.Prefix) (netip.Addr, bool) {
	peer, ok := Peer(r)
	if !ok || !Contains(proxies, peer) {
		return peer, ok
	}
	return Forwarded(r, proxies)
}

func forwardedElements(r *http.Request) ([]map[string]string, bool) {
	var out []map[string]string
	for _, row := range headerValues(r, "Forwarded") {
		if len(row) > 8192 {
			return nil, false
		}
		// IPs and origins do not contain escaped quotes, semicolons or commas.
		for _, element := range strings.Split(row, ",") {
			fields := map[string]string{}
			for _, part := range strings.Split(element, ";") {
				key, value, found := strings.Cut(strings.TrimSpace(part), "=")
				key = strings.ToLower(key)
				if !found || fields[key] != "" {
					return nil, false
				}
				if strings.HasPrefix(value, "\"") || strings.HasSuffix(value, "\"") {
					if len(value) < 2 || !strings.HasPrefix(value, "\"") || !strings.HasSuffix(value, "\"") {
						return nil, false
					}
					value = value[1 : len(value)-1]
				}
				if value == "" || strings.ContainsAny(value, "\"\\\r\n\t ") {
					return nil, false
				}
				fields[key] = value
			}
			out = append(out, fields)
			if len(out) > 32 {
				return nil, false
			}
		}
	}
	return out, len(out) > 0
}

// Origin honors the nearest proxy's scheme and host only when that socket peer
// is trusted. Untrusted forwarding headers never change the request origin.
func Origin(r *http.Request, proxies []netip.Prefix) string {
	scheme, host := "http", r.Host
	if r.TLS != nil {
		scheme = "https"
	}
	peer, ok := Peer(r)
	if ok && Contains(proxies, peer) {
		if elements, ok := forwardedElements(r); ok {
			last := elements[len(elements)-1]
			if p := last["proto"]; p == "http" || p == "https" {
				scheme = p
			}
			if h := last["host"]; validHost(h) {
				host = h
			}
		} else {
			if p := singleHeader(r, "X-Forwarded-Proto"); p == "http" || p == "https" {
				scheme = p
			}
			if h := singleHeader(r, "X-Forwarded-Host"); validHost(h) {
				host = h
			}
		}
	}
	host, ok = CanonicalHost(host)
	if !ok {
		return ""
	}
	return (&url.URL{Scheme: scheme, Host: host}).String()
}

func singleHeader(r *http.Request, key string) string {
	values := headerValues(r, key)
	if len(values) != 1 || len(values[0]) > 300 {
		return ""
	}
	return strings.TrimSpace(values[0])
}
func validHost(host string) bool {
	_, ok := CanonicalHost(host)
	return ok
}

// CanonicalHost validates HTTP authority syntax. A zone is permitted only on
// a bracketed IPv6 literal; its case is preserved because interface names can
// be case-sensitive. URL.String encodes its percent delimiter in an origin.
func CanonicalHost(host string) (string, bool) {
	if host == "" || len(host) > 255 || strings.ContainsAny(host, "/\\@?# \r\n\t,") {
		return "", false
	}
	if strings.HasPrefix(host, "[") {
		end := strings.IndexByte(host, ']')
		if end < 0 {
			return "", false
		}
		addr, err := netip.ParseAddr(host[1:end])
		if err != nil || !addr.Is6() {
			return "", false
		}
		zone := addr.Zone()
		if len(zone) > 64 {
			return "", false
		}
		for _, c := range zone {
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("._~-", c)) {
				return "", false
			}
		}
		suffix := host[end+1:]
		if suffix != "" && (!strings.HasPrefix(suffix, ":") || len(suffix) == 1) {
			return "", false
		}
		ip, _, scoped := strings.Cut(host[1:end], "%")
		if scoped && zone == "" {
			return "", false
		}
		host = "[" + strings.ToLower(ip)
		if scoped {
			host += "%" + zone
		}
		host += "]" + suffix
	} else {
		if strings.Contains(host, "%") {
			return "", false
		}
		host = strings.ToLower(host)
	}
	u, err := url.Parse((&url.URL{Scheme: "http", Host: host}).String())
	if err != nil || u.Hostname() == "" || u.User != nil {
		return "", false
	}
	return host, true
}

// HostsMatch compares an HTTP Host with an origin's decoded authority. Zones
// select a client's local interface and are removed by native HTTP clients;
// they do not change the endpoint address or port being proved.
func HostsMatch(a, b string) bool {
	a, okA := CanonicalHost(a)
	b, okB := CanonicalHost(b)
	if !okA || !okB {
		return false
	}
	withoutZone := func(host string) string {
		if strings.HasPrefix(host, "[") {
			end := strings.IndexByte(host, ']')
			if zone := strings.IndexByte(host[:end], '%'); zone >= 0 {
				return host[:zone] + host[end:]
			}
		}
		return host
	}
	return withoutZone(a) == withoutZone(b)
}

type homeKey struct{}

func WithHome(ctx context.Context, home bool) context.Context {
	return context.WithValue(ctx, homeKey{}, home)
}
func Home(ctx context.Context) bool { value, _ := ctx.Value(homeKey{}).(bool); return value }
