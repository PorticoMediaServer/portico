package networking

import (
	"context"
	"errors"
	"net/netip"
	"strconv"
)

// IPv6 inbound pinholes (UPnP IGDv2 WANIPv6FirewallControl:1).
//
// IPv6 has no address translation, but home routers still drop unsolicited
// inbound connections. A global IPv6 address on the server is therefore only a
// route when the router lets connections in: an explicit pinhole for the
// listener, or a router that reports its IPv6 firewall is off. The gateway is
// found and spoken to over IPv4 exactly like the NAT mapping (Client and
// Gateway are the IPv4 LAN tuple); PinholeClient is the global IPv6 address the
// pinhole admits, and it is also the published external address.
//
// PCP over IPv6 is not attempted: it needs the router's IPv6 address as the
// PCP server and an IPv6 source that matches the internal address, which this
// build cannot discover portably. Routers without IGDv2 firewall control rely
// on the owner saying the address is reachable (RemoteConfig.IPv6Open).

const pinholeProtocol = "upnp6"

func pinholePrepare(ctx context.Context, m Mapping) (Mapping, error) {
	if ip, e := netip.ParseAddr(m.PinholeClient); e != nil || !ip.Is6() || ip.Is4In6() || !publicAddress(ip) {
		return m, ErrInvalid
	}
	return discoverUPnPService(ctx, m, firewallService)
}

func pinholeMap(ctx context.Context, m Mapping, lifetime uint32) (Mapping, error) {
	if lifetime == 0 {
		if m.PinholeID == "" || m.FirewallOpen {
			return grantedMapping(m, 0, 0)
		}
		_, e := upnpCallFields(ctx, m, "DeletePinhole", [][2]string{{"UniqueID", m.PinholeID}}, "")
		if e != nil && !errors.Is(e, errUPnPMissing) {
			return m, e
		}
		m.PinholeID = ""
		return grantedMapping(m, 0, 0)
	}
	status, e := upnpCallFields(ctx, m, "GetFirewallStatus", nil, "")
	if e != nil {
		return m, e
	}
	m.ExternalAddress = m.PinholeClient
	m.ExternalPort = m.InternalPort
	if status["FirewallEnabled"] == "0" || status["FirewallEnabled"] == "false" {
		// Nothing to open: inbound IPv6 already reaches the server. Re-checked at
		// each renewal, because the owner can turn the firewall back on.
		m.FirewallOpen = true
		return grantedMapping(m, lifetime, 0)
	}
	m.FirewallOpen = false
	if status["InboundPinholeAllowed"] == "0" || status["InboundPinholeAllowed"] == "false" {
		return m, errMappingDenied
	}
	lease := strconv.FormatUint(uint64(min(lifetime, 86400)), 10)
	if m.PinholeID != "" {
		_, e = upnpCallFields(ctx, m, "UpdatePinhole", [][2]string{{"UniqueID", m.PinholeID}, {"NewLeaseTime", lease}}, "")
		if e == nil {
			return grantedMapping(m, min(lifetime, 86400), 0)
		}
		if !errors.Is(e, errUPnPMissing) {
			return m, e
		}
		// The router forgot it (reboot): open a new one.
		m.PinholeID = ""
	}
	out, e := upnpCallFields(ctx, m, "AddPinhole", [][2]string{
		{"RemoteHost", ""}, {"RemotePort", "0"}, {"InternalClient", m.PinholeClient},
		{"InternalPort", strconv.Itoa(m.InternalPort)}, {"Protocol", "6"}, {"LeaseTime", lease},
	}, "")
	if errors.Is(e, errFirewallDisabled) {
		m.FirewallOpen = true
		return grantedMapping(m, lifetime, 0)
	}
	if e != nil {
		return m, e
	}
	id := out["UniqueID"]
	if _, err := strconv.ParseUint(id, 10, 16); err != nil {
		// A pinhole we can't name can't be renewed or removed; treat the reply
		// as invalid and let its lease run out.
		return m, errMappingResponse
	}
	m.PinholeID = id
	return grantedMapping(m, min(lifetime, 86400), 0)
}
