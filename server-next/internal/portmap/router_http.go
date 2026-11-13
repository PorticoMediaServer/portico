package portmap

import (
	"net"
	"net/http"
	"net/netip"
	"time"
)

// IGDv2 recommends that a pinhole request's source be its InternalClient.
// Bind directly to that global IPv6 address, without an environment proxy.
func routerHTTP(source netip.Addr) *http.Client {
	if !source.IsValid() {
		return upnpClient
	}
	dialer := &net.Dialer{Timeout: 3 * time.Second, LocalAddr: net.TCPAddrFromAddrPort(netip.AddrPortFrom(source, 0))}
	client := *upnpClient
	client.Transport = &http.Transport{DialContext: dialer.DialContext, DisableKeepAlives: true}
	return &client
}
