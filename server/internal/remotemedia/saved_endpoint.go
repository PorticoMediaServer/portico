package remotemedia

import (
	"context"
	"net/netip"
	"net/url"
	"sort"
	"strings"
)

// EndpointOrigin normalizes an endpoint without granting access or retaining
// path/query credentials in approval facts. The private raw config stays with
// its existing owner; Save separately resolves and tests this real origin.
func EndpointOrigin(raw string) (string, error) {
	u, err := parseURL(raw)
	if err != nil {
		return "", err
	}
	return origin(u) + "/", nil
}

// PrepareEndpointApproval captures only the exact HTTP origin's permitted
// non-public addresses for an owner Save. It is not a reusable owner grant;
// connection testing, actor/CAS rechecks and atomic source publication follow.
// Existing descriptor/library approval behavior is deliberately separate.
func (p Policy) PrepareEndpointApproval(ctx context.Context, raw string) (Approval, error) {
	u, err := parseURL(raw)
	if err != nil || u.RawQuery != "" || ctx == nil {
		return Approval{}, ErrPolicy
	}
	root := origin(u) + "/"
	addresses, err := p.resolve(ctx, u.Hostname())
	if err != nil {
		return Approval{}, err
	}
	out := Approval{Root: root, Addresses: []string{}}
	seen := map[string]bool{}
	for _, address := range addresses {
		ip := address.Unmap()
		if !nativeAddressAllowed(ip) {
			return Approval{}, ErrPolicy
		}
		if !public(ip) && !seen[ip.String()] {
			out.Addresses = append(out.Addresses, ip.String())
			seen[ip.String()] = true
		}
	}
	sort.Strings(out.Addresses)
	if err := ctx.Err(); err != nil {
		return Approval{}, err
	}
	return out, nil
}

// CheckEndpointApproval re-resolves this endpoint against its own saved facts.
// Native schemes, a different origin, path-scoped approvals and forbidden
// addresses never cover it. No Policy.Approvals or library rules are inferred.
func (p Policy) CheckEndpointApproval(ctx context.Context, raw string, saved Approval) error {
	u, approved, err := savedEndpointAddresses(raw, saved)
	if err != nil || ctx == nil {
		return ErrPolicy
	}
	addresses, err := p.resolve(ctx, u.Hostname())
	if err != nil {
		return err
	}
	for _, address := range addresses {
		ip := address.Unmap()
		if !nativeAddressAllowed(ip) || !public(ip) && !approved[ip] || p.SourcePrivateOnly && !nativePrivate(ip) {
			return ErrPolicy
		}
	}
	return ctx.Err()
}

// savedEndpointAddresses is purely local validation, safe in a caller-owned
// transaction. DNS belongs at actual use/start/reconnect, outside that reader.
func savedEndpointAddresses(raw string, saved Approval) (*url.URL, map[netip.Addr]bool, error) {
	u, err := parseURL(raw)
	if err != nil || u.RawQuery != "" || saved.Root != origin(u)+"/" || len(saved.Addresses) > 32 {
		return nil, nil, ErrPolicy
	}
	root, err := url.Parse(saved.Root)
	if err != nil || root.Path != "/" || root.RawQuery != "" || !strings.HasPrefix(saved.Root, u.Scheme+"://") {
		return nil, nil, ErrPolicy
	}
	approved := map[netip.Addr]bool{}
	for _, value := range saved.Addresses {
		ip, err := netip.ParseAddr(value)
		if err != nil || ip != ip.Unmap() || ip.String() != value || !nativeAddressAllowed(ip) || !nativePrivate(ip) || approved[ip] {
			return nil, nil, ErrPolicy
		}
		approved[ip] = true
	}
	return u, approved, nil
}
