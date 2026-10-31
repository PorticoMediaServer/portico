//go:build devtrust && !release

package main

import (
	"fmt"
	"net/netip"
	"strings"
)

// The end-to-end setup rig serves the server through an HTTPS front on loopback (its
// Security-return flow needs an https page), and the front must tell the server the
// browser's scheme from first start, as a trusted proxy an owner adds would. It becomes
// the ordinary trusted proxies (an owner's saved setting still replaces it), loopback
// only, in development trust builds only.
func developmentTrustedProxies(raw string) ([]netip.Prefix, error) {
	if raw == "" {
		return nil, nil
	}
	var out []netip.Prefix
	for _, value := range strings.Split(raw, ",") {
		prefix, err := netip.ParsePrefix(strings.TrimSpace(value))
		if err != nil || !loopbackPrefix(prefix.Masked()) {
			return nil, fmt.Errorf("PORTICO_DEV_TRUSTED_PROXIES: %q is not a loopback CIDR", strings.TrimSpace(value))
		}
		out = append(out, prefix.Masked())
	}
	return out, nil
}

// loopbackPrefix: every address in it is loopback (127.0.0.0/8 or narrower, or ::1/128).
func loopbackPrefix(p netip.Prefix) bool {
	a := p.Addr()
	if a.Is4() {
		return p.Bits() >= 8 && a.As4()[0] == 127
	}
	return a.Is6() && !a.Is4In6() && p.Bits() == 128 && a == netip.IPv6Loopback()
}
