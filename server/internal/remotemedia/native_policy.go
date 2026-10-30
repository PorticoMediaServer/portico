package remotemedia

import (
	"context"
	"net"
	"net/netip"
	"net/url"
	"sort"
	"strconv"
	"strings"
)

// NativeEndpoint is the exact saved SFTP/SMB destination. Its approval comes
// only from that source's owner save, never HTTP/library approvals or a picker.
type NativeEndpoint struct {
	Scheme, Host string
	Port         int
}

func (e NativeEndpoint) Root() (string, error) {
	if e.Scheme != "sftp" && e.Scheme != "smb" || e.Port < 1 || e.Port > 65535 || len(e.Host) > 255 || strings.ContainsAny(e.Host, "/@\\\x00\r\n\t %#?[]") {
		return "", ErrPolicy
	}
	host := strings.ToLower(strings.TrimSuffix(e.Host, "."))
	if host == "" || host == "metadata" || host == "metadata.google.internal" || host == "metadata.goog" || host == "metadata.tencentyun.com" || host == "instance-data" {
		return "", ErrPolicy
	}
	if ip, err := netip.ParseAddr(host); err == nil {
		if ip.Zone() != "" {
			return "", ErrPolicy
		}
		host = ip.Unmap().String()
	} else if strings.Contains(host, ":") {
		return "", ErrPolicy
	}
	u := url.URL{Scheme: e.Scheme, Host: net.JoinHostPort(host, strconv.Itoa(e.Port)), Path: "/"}
	return origin(&u) + "/", nil
}

func nativePrivate(ip netip.Addr) bool {
	ip = ip.Unmap()
	return ip.IsPrivate() || netip.MustParsePrefix("100.64.0.0/10").Contains(ip)
}

func nativeAddressAllowed(ip netip.Addr) bool {
	ip = ip.Unmap()
	if ip.Zone() != "" || hardBlocked(ip) || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
		return false
	}
	// IANA's IPv6 documentation/benchmark entries also stay closed. HTTP's
	// existing policy is intentionally unchanged by native endpoint admission.
	// https://www.iana.org/assignments/iana-ipv6-special-registry/
	for _, prefix := range []string{"2001:db8::/32", "3fff::/20", "2001:2::/48"} {
		if netip.MustParsePrefix(prefix).Contains(ip) {
			return false
		}
	}
	return public(ip) || nativePrivate(ip)
}

// PrepareNativeApproval observes candidate addresses without making a network
// connection or granting a source read. The real owner-save operation must test
// the connection, recheck its actor/CAS and only then persist these exact facts.
func (p Policy) PrepareNativeApproval(ctx context.Context, endpoint NativeEndpoint) (Approval, error) {
	if ctx == nil {
		return Approval{}, ErrPolicy
	}
	if err := ctx.Err(); err != nil {
		return Approval{}, err
	}
	root, err := endpoint.Root()
	if err != nil {
		return Approval{}, err
	}
	u, _ := url.Parse(root)
	ips, err := p.resolve(ctx, u.Hostname())
	if err != nil {
		return Approval{}, err
	}
	out := Approval{Root: root, Addresses: []string{}}
	seen := map[string]bool{}
	for _, address := range ips {
		ip := address.Unmap()
		if !nativeAddressAllowed(ip) {
			return Approval{}, ErrPolicy
		}
		if !public(ip) && !seen[ip.String()] {
			out.Addresses = append(out.Addresses, ip.String())
			seen[ip.String()] = true
		}
	}
	if err = ctx.Err(); err != nil {
		return Approval{}, err
	}
	sort.Strings(out.Addresses)
	return out, nil
}

// CheckNativeEndpoint re-resolves every address against THIS saved approval.
// It does not consult Policy.Approvals, refresh an approval, dial a provider,
// supply actor authority or claim to pin rclone's subsequent connection.
func (p Policy) CheckNativeEndpoint(ctx context.Context, endpoint NativeEndpoint, saved Approval) ([]netip.Addr, error) {
	if ctx == nil {
		return nil, ErrPolicy
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	root, err := endpoint.Root()
	if err != nil || saved.Root != root || len(saved.Addresses) > 32 {
		return nil, ErrPolicy
	}
	approved := map[netip.Addr]bool{}
	for _, address := range saved.Addresses {
		ip, err := netip.ParseAddr(address)
		if err != nil || ip != ip.Unmap() || ip.String() != address || !nativeAddressAllowed(ip) || !nativePrivate(ip) || approved[ip] {
			return nil, ErrPolicy
		}
		approved[ip] = true
	}
	u, _ := url.Parse(root)
	resolved, err := p.resolve(ctx, u.Hostname())
	if err != nil {
		return nil, err
	}
	checked := make([]netip.Addr, 0, len(resolved))
	for _, address := range resolved {
		ip := address.Unmap()
		if !nativeAddressAllowed(ip) || !public(ip) && !approved[ip] {
			return nil, ErrPolicy
		}
		checked = append(checked, ip)
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	return checked, nil
}
