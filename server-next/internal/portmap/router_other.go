//go:build !linux && !darwin && !windows

package portmap

import "net/netip"

func defaultRouter() Router               { return Router{} }
func defaultIPv6Router(netip.Addr) Router { return Router{} }
