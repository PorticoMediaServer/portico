package remote

import (
	"context"
	"fmt"
	"net"
	"net/http/httptrace"
	"net/url"
	"strings"
	"sync"
	"syscall"
	"time"
)

// What a remote source may be pointed at (the first part of B-44 in the remote sources design
// notes).
//
// A source never reaches a link-local address, where a cloud machine keeps its own sign-in
// (169.254.169.254 and its names). Portico's own WebDAV client refuses one as it is dialed,
// whatever name led there, and tries the name's next address. An rclone remote is held to it in
// every option's value, by its text when it is pasted or made, and, each time rclone starts, by
// what each host it names resolves to: refused when any address is a metadata service's own or
// every address is link-local (rclone dials for itself, so a home NAS whose name also answers
// with its fe80:: address still works).
//
// A source out on the internet cannot send Portico's reads inward with a redirect: once a read
// was answered from a public address, a redirect to this computer or its network is refused,
// judged by the address really connected to and by every address the target resolves to, and
// again as the target is dialed.
//
// A NAS at a home address the owner typed is an ordinary source: nothing about home addresses
// is refused. What someone who controls a source's DNS can still do is in DEFERRED.md under B-44.

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
	// The metadata services' own addresses: the one most clouds use, AWS's over IPv6, Alibaba's.
	// The last two are outside link-local.
	metadataIPs = []net.IP{net.ParseIP("169.254.169.254"), net.ParseIP("fd00:ec2::254"), net.ParseIP("100.100.100.200")}
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

// textKind is the kind of a host by its text alone: an address, or a metadata service's name.
// name says it is any other name, whose addresses decide.
func textKind(host string) (kind addressKind, name bool) {
	host = strings.ToLower(strings.TrimSuffix(strings.Trim(host, "[]"), "."))
	if zone := strings.IndexByte(host, '%'); zone > 0 {
		host = host[:zone]
	}
	if metadataNames[host] {
		return linkLocalAddress, false
	}
	if ip := net.ParseIP(host); ip != nil {
		return kindOf(ip), false
	}
	return publicAddress, true
}

// addressKinds are the kinds of a host's addresses: its own, or each one its name resolves to. A name
// that does not resolve has none; reaching it fails on its own.
func addressKinds(ctx context.Context, host string) []addressKind {
	kind, name := textKind(host)
	if !name {
		return []addressKind{kind}
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	addresses, _ := lookupIP(ctx, strings.TrimSuffix(host, "."))
	out := make([]addressKind, 0, len(addresses))
	for _, a := range addresses {
		out = append(out, kindOf(a.IP))
	}
	return out
}

// chain follows one request of Portico's WebDAV client through its redirects: whether any
// connection it used was to a public address, as really connected to.
type chain struct {
	mu     sync.Mutex
	public bool
}

type chainKey struct{}

// withChain starts following a request.
func withChain(ctx context.Context) context.Context {
	c := &chain{}
	ctx = context.WithValue(ctx, chainKey{}, c)
	return httptrace.WithClientTrace(ctx, &httptrace.ClientTrace{GotConn: func(info httptrace.GotConnInfo) {
		if addr, ok := info.Conn.RemoteAddr().(*net.TCPAddr); ok && kindOf(addr.IP) == publicAddress {
			c.mu.Lock()
			c.public = true
			c.mu.Unlock()
		}
	}})
}

// wentPublic says whether the request was answered from a public address. Without a followed
// request (as in a test of the rule alone), it says whether any earlier hop has a public
// address: no second record makes a public source a home one.
func wentPublic(ctx context.Context, via []*url.URL) bool {
	if c, ok := ctx.Value(chainKey{}).(*chain); ok {
		c.mu.Lock()
		defer c.mu.Unlock()
		return c.public
	}
	for _, from := range via {
		for _, k := range addressKinds(ctx, from.Hostname()) {
			if k == publicAddress {
				return true
			}
		}
	}
	return false
}

// checkRedirect refuses a redirect inward: once a read was answered from an address on the
// internet, it is not followed to a target any of whose addresses is on this computer or its
// network. The same kind of address is followed (public to public, home to home), and home to
// public.
func checkRedirect(req *url.URL, via []*url.URL, ctx context.Context) error {
	if !wentPublic(ctx, via) {
		return nil
	}
	for _, k := range addressKinds(ctx, req.Hostname()) {
		if k != publicAddress {
			return ErrRedirectInward
		}
	}
	return nil
}

// dialControl is the WebDAV dialer's Control for one request: it refuses the address being
// dialed, after its name was resolved, when it is link-local, and when the request was already
// answered from a public address and this one is not public.
func dialControl(ctx context.Context) func(network, address string, c syscall.RawConn) error {
	return func(_, address string, _ syscall.RawConn) error {
		host, _, err := net.SplitHostPort(address)
		ip := net.ParseIP(host)
		if err != nil || ip == nil {
			return nil
		}
		switch kind := kindOf(ip); {
		case kind == linkLocalAddress:
			return ErrAddressRefused
		case kind != publicAddress && wentPublic(ctx, nil):
			return ErrRedirectInward
		}
		return nil
	}
}

// rcloneAddressOption says an option's value names a place to connect to: its name says so
// (host, hostname, namenode, endpoint, url, auth, or ending in _url, _endpoint or _address),
// or its value is an address with a scheme.
func rcloneAddressOption(key, value string) bool {
	switch key {
	case "host", "hostname", "namenode", "endpoint", "url", "auth":
		return true
	}
	return strings.HasSuffix(key, "_url") || strings.HasSuffix(key, "_endpoint") || strings.HasSuffix(key, "_address") || strings.Contains(value, "://")
}

// rcloneHosts are the hosts a remote's options name.
func rcloneHosts(options map[string]string) []string {
	var hosts []string
	for key, value := range options {
		value = strings.TrimSpace(value)
		if key == "type" || value == "" || value == "true" || value == "false" || !rcloneAddressOption(key, value) {
			continue
		}
		host := value
		if strings.Contains(value, "://") {
			if u, err := url.Parse(value); err == nil {
				host = u.Hostname()
			}
		} else {
			// host, host:port, [v6]:port, or node@host:port.
			host = host[strings.LastIndex(host, "@")+1:]
			if h, _, err := net.SplitHostPort(host); err == nil {
				host = h
			}
		}
		if host != "" {
			hosts = append(hosts, host)
		}
	}
	return hosts
}

// valuePieces are the parts of an option's value that could each be a host, however it is
// written: split on commas, slashes, "@" and spaces (so an address with a scheme, a user in it,
// a list of hosts, a node@host), each with any port taken off.
func valuePieces(value string) []string {
	var pieces []string
	for _, piece := range strings.FieldsFunc(value, func(c rune) bool { return strings.ContainsRune(",/@ \t;", c) }) {
		if host, _, err := net.SplitHostPort(piece); err == nil {
			piece = host
		}
		pieces = append(pieces, piece)
	}
	return pieces
}

// rcloneAddressRefusal refuses, as a remote is pasted or made, any option whose value holds a
// link-local or metadata address or a metadata service's name, wherever in the value it is and
// whatever the option is called. What a name resolves to is checked each time rclone starts
// (checkRcloneHost).
func rcloneAddressRefusal(options map[string]string) error {
	for key, value := range options {
		if key == "type" {
			continue
		}
		for _, piece := range valuePieces(value) {
			if kind, _ := textKind(piece); kind == linkLocalAddress {
				return ErrAddressRefused
			}
		}
	}
	return nil
}

func isMetadataIP(ip net.IP) bool {
	for _, m := range metadataIPs {
		if m.Equal(ip) {
			return true
		}
	}
	return false
}

// checkRcloneHost refuses a remote with a host that answers with a metadata service's own
// address, or whose every address is link-local. rclone dials for itself and tries a name's
// addresses in turn, so a name that also answers with an ordinary address (a home NAS's fe80::
// or self-assigned 169.254 address beside its 192.168 one) is not refused; a metadata
// service's address is never a home device's.
func checkRcloneHost(ctx context.Context, options map[string]string) error {
	for _, host := range rcloneHosts(options) {
		kind, name := textKind(host)
		if !name {
			if kind == linkLocalAddress {
				return ErrAddressRefused
			}
			continue
		}
		lookup, cancel := context.WithTimeout(ctx, 10*time.Second)
		addresses, _ := lookupIP(lookup, strings.TrimSuffix(host, "."))
		cancel()
		every := len(addresses) > 0
		for _, a := range addresses {
			if isMetadataIP(a.IP) {
				return ErrAddressRefused
			}
			every = every && kindOf(a.IP) == linkLocalAddress
		}
		if every {
			return ErrAddressRefused
		}
	}
	return nil
}
