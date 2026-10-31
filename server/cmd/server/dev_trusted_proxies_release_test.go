//go:build !devtrust || release

package main

import "testing"

// A normal or release build ignores PORTICO_DEV_TRUSTED_PROXIES: only an owner's
// configured trusted proxies count.
func TestReleaseBuildIgnoresDevelopmentTrustedProxies(t *testing.T) {
	for _, raw := range []string{"127.0.0.1/32", "0.0.0.0/0", "garbage"} {
		if got, err := developmentTrustedProxies(raw); got != nil || err != nil {
			t.Fatalf("%q: %v, %v", raw, got, err)
		}
	}
}
