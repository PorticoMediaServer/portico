//go:build !devtrust || release

package main

import "net/netip"

// A normal or release build trusts only the proxies an owner configures.
func developmentTrustedProxies(string) ([]netip.Prefix, error) { return nil, nil }
