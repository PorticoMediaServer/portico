package remotemedia

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"
)

var ErrPolicy = errors.New("remote source is outside the approved network policy")
var ErrBusy = errors.New("remote playback capacity is busy; retry shortly")
var ErrUnavailable = errors.New("remote source is unavailable")
var ErrDenied = errors.New("remote source refused access; update its descriptor or credentials")
var ErrStalled = errors.New("remote source stopped responding")
var ErrUnsupported = errors.New("this STRM source is not a supported direct MP4 stream")

type Resolver interface {
	LookupNetIP(context.Context, string, string) ([]netip.Addr, error)
}
type Approval struct {
	Root      string   `json:"root"`
	Addresses []string `json:"addresses"`
}
type Policy struct {
	// ManualRedirects returns redirect responses untouched for callers that
	// validate each hop and create a fresh, independently admitted client.
	// The default remains automatic redirects within the admitted scope.
	ManualRedirects bool
	// SourceEndpoints applies the strict owner-saved source endpoint rules:
	// public or this origin's saved private addresses; never special ranges.
	// Descriptor/library approval behavior remains separate.
	SourceEndpoints   bool
	SourcePrivateOnly bool
	UserAgent         string
	ReadTimeout       time.Duration
	Approvals         []Approval
	Resolver          Resolver
}

func ParseDescriptor(raw []byte) (string, error) {
	if len(raw) > 64<<10 {
		return "", ErrPolicy
	}
	locator := ""
	for _, line := range strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if locator != "" {
			return "", errors.New("STRM must contain exactly one URL")
		}
		locator = line
	}
	if locator == "" {
		return "", errors.New("STRM is empty")
	}
	u, e := parseURL(locator)
	if e != nil {
		return "", e
	}
	return u.String(), nil
}
func parseURL(raw string) (*url.URL, error) {
	u, e := url.Parse(raw)
	if e != nil || u.Host == "" || u.User != nil || u.Fragment != "" || (u.Scheme != "http" && u.Scheme != "https") || strings.ContainsAny(raw, "\r\n\x00\\") {
		return nil, ErrPolicy
	}
	host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	if host == "" || host == "metadata" || host == "metadata.google.internal" || host == "metadata.goog" || host == "metadata.tencentyun.com" || host == "instance-data" {
		return nil, ErrPolicy
	}
	port := u.Port()
	if port != "" {
		n, e := strconv.Atoi(port)
		if e != nil || n < 1 || n > 65535 {
			return nil, ErrPolicy
		}
	}
	if u.Path == "" {
		u.Path = "/"
	}
	if strings.Contains(u.Path, "%") || strings.Contains(u.Path, "\\") || path.Clean(u.Path) != strings.TrimSuffix(u.Path, "/") && u.Path != "/" {
		return nil, ErrPolicy
	}
	return u, nil
}
func origin(u *url.URL) string {
	port := u.Port()
	if port == "" {
		if u.Scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}
	return u.Scheme + "://" + net.JoinHostPort(strings.ToLower(strings.TrimSuffix(u.Hostname(), ".")), port)
}
func hardBlocked(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsValid() || ip.IsUnspecified() || ip.IsMulticast() {
		return true
	}
	for _, raw := range []string{"169.254.0.23", "100.100.100.200", "192.0.0.192", "168.63.129.16", "fd00:ec2::254", "fd00:ec2::23", "fd20:ce::254"} {
		if ip.WithZone("") == netip.MustParseAddr(raw) {
			return true
		}
	}
	// Transition addresses can encode a blocked IPv4 destination. They must
	// remain closed even under a saved approval; pinning the IPv6 address alone
	// does not constrain what a translator or tunnel reaches.
	for _, raw := range []string{"169.254.169.0/24", "169.254.170.0/24", "64:ff9b::/96", "64:ff9b:1::/48", "2002::/16", "2001::/32"} {
		if netip.MustParsePrefix(raw).Contains(ip) {
			return true
		}
	}
	return false
}
func public(ip netip.Addr) bool {
	ip = ip.Unmap()
	if hardBlocked(ip) || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
		return false
	}
	for _, raw := range []string{"100.64.0.0/10", "198.18.0.0/15", "192.0.0.0/24", "192.0.2.0/24", "198.51.100.0/24", "203.0.113.0/24"} {
		if netip.MustParsePrefix(raw).Contains(ip) {
			return false
		}
	}
	return true
}
func (p Policy) resolve(ctx context.Context, host string) ([]netip.Addr, error) {
	if ip, e := netip.ParseAddr(host); e == nil {
		return []netip.Addr{ip.Unmap()}, nil
	}
	r := p.Resolver
	if r == nil {
		r = net.DefaultResolver
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	ips, e := r.LookupNetIP(ctx, "ip", host)
	if e != nil || len(ips) == 0 || len(ips) > 32 {
		return nil, ErrUnavailable
	}
	return ips, nil
}
func (p Policy) Approve(ctx context.Context, raw string) (Approval, error) {
	u, e := parseURL(raw)
	if e != nil || u.RawQuery != "" {
		return Approval{}, ErrPolicy
	}
	ips, e := p.resolve(ctx, u.Hostname())
	if e != nil {
		return Approval{}, e
	}
	a := Approval{Root: origin(u) + strings.TrimSuffix(u.Path, "/") + "/", Addresses: []string{}}
	seen := map[string]bool{}
	for _, ip := range ips {
		ip = ip.Unmap()
		if hardBlocked(ip) {
			return Approval{}, ErrPolicy
		}
		if !seen[ip.String()] {
			a.Addresses = append(a.Addresses, ip.String())
			seen[ip.String()] = true
		}
	}
	return a, nil
}

type scope struct {
	policy        Policy
	root          *url.URL
	addresses     map[netip.Addr]bool
	initialScheme string
}

func (p Policy) admit(ctx context.Context, raw string) (*url.URL, *scope, error) {
	u, e := parseURL(raw)
	if e != nil {
		return nil, nil, e
	}
	s := &scope{policy: p, initialScheme: u.Scheme}
	for _, a := range p.Approvals {
		root, e := url.Parse(a.Root)
		if e != nil {
			continue
		}
		if origin(u) == origin(root) && (strings.HasPrefix(u.Path, root.Path) || u.Path == strings.TrimSuffix(root.Path, "/")) {
			s.root = root
			s.addresses = map[netip.Addr]bool{}
			for _, v := range a.Addresses {
				if ip, e := netip.ParseAddr(v); e == nil {
					s.addresses[ip.Unmap()] = true
				}
			}
			break
		}
	}
	if ips, e := s.resolve(ctx, u.Hostname()); e != nil {
		if errors.Is(e, ErrPolicy) && confirmable(ips) {
			return nil, nil, &ConfirmationRequired{Root: origin(u) + "/"}
		}
		return nil, nil, e
	}
	return u, s, nil
}

// ConfirmationRequired is ErrPolicy for a destination the owner may approve: an
// address on their own network (private, loopback, link-local, CGNAT) that no
// confirmed root covers. Root is exactly what to confirm: scheme, host, port.
type ConfirmationRequired struct{ Root string }

func (e *ConfirmationRequired) Error() string   { return ErrPolicy.Error() }
func (e *ConfirmationRequired) Is(t error) bool { return t == ErrPolicy }

// confirmable reports whether a refused destination is one the owner could
// confirm, as opposed to one that is never reachable (metadata services,
// unspecified or multicast addresses).
func confirmable(ips []netip.Addr) bool {
	if len(ips) == 0 {
		return false
	}
	for _, ip := range ips {
		if hardBlocked(ip) {
			return false
		}
	}
	return true
}

// Destination classifies one URL against the policy: Covered when a confirmed
// root admits it, Public when it needs no confirmation, otherwise the owner
// must confirm Root (whose current Addresses are given).
type Destination struct {
	Root      string
	Addresses []string
	Covered   bool
	Public    bool
}

// Classify never connects. A hard-blocked destination is ErrPolicy; a name that
// does not resolve is ErrUnavailable.
func (p Policy) Classify(ctx context.Context, raw string) (Destination, error) {
	u, e := parseURL(raw)
	if e != nil {
		return Destination{}, e
	}
	d := Destination{Root: origin(u) + "/", Addresses: []string{}}
	for _, a := range p.Approvals {
		root, e := url.Parse(a.Root)
		if e == nil && origin(u) == origin(root) && (strings.HasPrefix(u.Path, root.Path) || u.Path == strings.TrimSuffix(root.Path, "/")) {
			d.Covered = true
			return d, nil
		}
	}
	ips, e := p.resolve(ctx, u.Hostname())
	if e != nil {
		return d, e
	}
	d.Public = true
	for _, ip := range ips {
		ip = ip.Unmap()
		if hardBlocked(ip) {
			return Destination{}, ErrPolicy
		}
		if !public(ip) {
			d.Public = false
		}
		d.Addresses = append(d.Addresses, ip.String())
	}
	return d, nil
}
func (s *scope) checkURL(u *url.URL) error {
	parsed, e := parseURL(u.String())
	if e != nil {
		return e
	}
	if s.initialScheme == "https" && parsed.Scheme != "https" {
		return ErrPolicy
	}
	if s.root != nil && (origin(parsed) != origin(s.root) || !(strings.HasPrefix(parsed.Path, s.root.Path) || parsed.Path == strings.TrimSuffix(s.root.Path, "/"))) {
		return ErrPolicy
	}
	return nil
}
func (s *scope) resolve(ctx context.Context, host string) ([]netip.Addr, error) {
	ips, e := s.policy.resolve(ctx, host)
	if e != nil {
		return nil, e
	}
	for i, ip := range ips {
		ips[i] = ip.Unmap()
	}
	for _, ip := range ips {
		allowed := !hardBlocked(ip) && (s.root != nil && s.addresses[ip] || s.root == nil && public(ip))
		if s.policy.SourceEndpoints {
			allowed = nativeAddressAllowed(ip) && (public(ip) || s.root != nil && s.addresses[ip])
			if s.policy.SourcePrivateOnly {
				allowed = allowed && nativePrivate(ip)
			}
		}
		if !allowed {
			// The addresses come back with the refusal so admit can tell a
			// confirmable LAN destination from a blocked one; no caller dials them.
			return ips, ErrPolicy
		}
	}
	return ips, nil
}
