package remote

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"strings"
	"syscall"
)

// What a remote source may be pointed at (the first part of B-44 in the remote sources design
// notes). A source never reaches a link-local address, where a cloud machine keeps its own
// sign-in (169.254.169.254 and its names): checked on the address a name resolves to when the
// source is tested before saving and every time it connects, not only on the text. And a
// source out on the internet cannot send Portico's reads inward with a redirect. A NAS at a
// home address the owner typed is an ordinary source: nothing about home addresses is refused.

// ErrAddressRefused says a source's address, or the address its name resolves to, is
// link-local.
var ErrAddressRefused = fmt.Errorf("%w: Portico never reads from a link-local address, where a cloud machine keeps its own sign-in", ErrNeedsAttention)

// ErrRedirectInward says a source on the internet redirected a read to an address on this
// computer or its network.
var ErrRedirectInward = fmt.Errorf("%w: the source sent Portico from an address on the internet to one on this computer or its network, which Portico does not follow", ErrNeedsAttention)

type addressKind int

const (
	publicAddress addressKind = iota
	homeAddress               // this computer, or its own network: private, carrier-grade NAT (Tailscale)
	linkLocalAddress
)

var (
	carrierNAT = mustCIDR("100.64.0.0/10")
	// Metadata addresses outside link-local: AWS's over IPv6, Alibaba's.
	metadataIPs = []net.IP{net.ParseIP("fd00:ec2::254"), net.ParseIP("100.100.100.200")}
	// The well-known names of a cloud machine's metadata service.
	metadataNames = map[string]bool{"metadata": true, "metadata.google.internal": true, "metadata.goog": true, "instance-data": true,
		"instance-data.ec2.internal": true}
)

func mustCIDR(s string) *net.IPNet { _, n, _ := net.ParseCIDR(s); return n }

func kindOfIP(ip net.IP) addressKind {
	for _, m := range metadataIPs {
		if m.Equal(ip) {
			return linkLocalAddress
		}
	}
	switch {
	case ip.IsLinkLocalUnicast(), ip.IsLinkLocalMulticast():
		return linkLocalAddress
	case ip.IsLoopback(), ip.IsPrivate(), ip.IsUnspecified(), carrierNAT.Contains(ip):
		return homeAddress
	}
	return publicAddress
}

// kindOf says what kind of address an IP is. Tests replace it.
var kindOf = kindOfIP

// lookupIP resolves a name. Tests replace it.
var lookupIP = net.DefaultResolver.LookupIPAddr

// hostKind is the kind of a host, by its text and by every address its name resolves to: the
// most inward of them. A name that does not resolve is taken as public; reaching it fails on
// its own.
func hostKind(ctx context.Context, host string) addressKind {
	kind, name := textKind(host)
	if !name {
		return kind
	}
	addresses, err := lookupIP(ctx, host)
	if err != nil {
		return publicAddress
	}
	for _, a := range addresses {
		kind = max(kind, kindOf(a.IP))
	}
	return kind
}

// textKind is the kind of a host by its text alone: an address, or a metadata service's name.
// name says it is any other name, whose addresses decide.
func textKind(host string) (kind addressKind, name bool) {
	host = strings.ToLower(strings.TrimSuffix(strings.Trim(host, "[]"), "."))
	if metadataNames[host] {
		return linkLocalAddress, false
	}
	if ip := net.ParseIP(host); ip != nil {
		return kindOf(ip), false
	}
	return publicAddress, true
}

// refuseLinkLocal is a dialer's Control: it refuses the connection when the address being
// dialed, after the name was resolved, is link-local.
func refuseLinkLocal(_, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if ip := net.ParseIP(host); err == nil && ip != nil && kindOf(ip) == linkLocalAddress {
		return ErrAddressRefused
	}
	return nil
}

// checkRedirect refuses a redirect inward: once a read was sent to an address on the
// internet, it is not followed to this computer or its network. The same kind of address is
// followed (public to public, home to home), and home to public.
func checkRedirect(req *url.URL, via []*url.URL, ctx context.Context) error {
	to := hostKind(ctx, req.Hostname())
	for _, from := range via {
		if to != publicAddress && hostKind(ctx, from.Hostname()) == publicAddress {
			return ErrRedirectInward
		}
	}
	return nil
}

// rcloneHostOptions are the options that hold a remote's host, for the kinds of storage whose
// host is a plain option: a host name, or an address the host is taken from.
var rcloneHostOptions = map[string]struct {
	option string
	url    bool
}{"sftp": {"host", false}, "smb": {"host", false}, "ftp": {"host", false}, "http": {"url", true}, "webdav": {"url", true}}

// rcloneHost is a remote's host, where it is a plain option.
func rcloneHost(options map[string]string) string {
	where, ok := rcloneHostOptions[options["type"]]
	if !ok {
		return ""
	}
	value := strings.TrimSpace(options[where.option])
	if !where.url {
		return value
	}
	u, err := url.Parse(value)
	if err != nil {
		return value
	}
	return u.Hostname()
}

// rcloneAddressRefusal refuses, as a remote is pasted or made, a host that is link-local by
// its text; what its name resolves to is checked each time it connects (checkRcloneHost).
func rcloneAddressRefusal(options map[string]string) error {
	if host := rcloneHost(options); host != "" {
		if kind, _ := textKind(host); kind == linkLocalAddress {
			return ErrAddressRefused
		}
	}
	return nil
}

// checkRcloneHost refuses a remote whose host, or what its name resolves to, is link-local.
func checkRcloneHost(ctx context.Context, options map[string]string) error {
	if host := rcloneHost(options); host != "" && hostKind(ctx, host) == linkLocalAddress {
		return ErrAddressRefused
	}
	return nil
}
