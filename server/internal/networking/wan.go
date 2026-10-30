package networking

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"time"
)

// A server behind NAT cannot see its own WAN address, and an owner who forwards a port by
// hand has no gateway mapping to report one. WANObserver finds it without Hosted: first by
// asking the router (local, free), then public address services (STUN, then HTTPS) that are
// not ours. Hosted hears about a WAN address only when it changes.
//
// The result is a route candidate, never a trusted fact: Hosted proves it by dialling back
// and every client verifies the server's pinned key, so a wrong answer costs a failed probe.
type WANObserver struct {
	mapper GatewayMapper
	stun   []string
	http   []string
	lookup func(ctx context.Context, server string) (netip.Addr, error)
	fetch  func(ctx context.Context, url string) (netip.Addr, error)
	now    func() time.Time

	mu       sync.Mutex
	remoteAt time.Time
	remote   netip.Addr
}

// wanRemoteInterval bounds how often anything outside the LAN is asked.
const wanRemoteInterval = 5 * time.Minute

var defaultSTUNServers = []string{"stun.cloudflare.com:3478", "stun.l.google.com:19302"}

// Plain-text address services, used only when UDP to the STUN services is blocked.
var defaultAddressServices = []string{"https://checkip.amazonaws.com", "https://icanhazip.com"}

func newWANObserver(mapper GatewayMapper) *WANObserver {
	return &WANObserver{mapper: mapper, stun: defaultSTUNServers, http: defaultAddressServices, lookup: stunAddress, fetch: httpAddress, now: time.Now}
}

// Observe returns the current public IPv4 address and where it came from. force skips the
// remote rate limit (a topology change is a reason to ask now).
//
// The source is the whole of what a caller can know about how sure this answer is:
// "gateway", "stun" and "http" are fresh answers; "cached" means the rate limit declined to
// ask again, so the address is a memory, not an observation; "unavailable" means a fresh
// attempt was made and every service failed, so the remembered address is returned but nothing
// was learned. Neither of the last two is evidence that an address family has gone away —
// which is why withdrawing one never rests on them.
func (o *WANObserver) Observe(ctx context.Context, topo Topology, port int, force bool) (netip.Addr, string) {
	if o == nil {
		return netip.Addr{}, ""
	}
	if ip, ok := o.fromGateway(ctx, topo, port); ok {
		return ip, "gateway"
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	now := o.now()
	if !force && !o.remoteAt.IsZero() && now.Sub(o.remoteAt) < wanRemoteInterval {
		return o.remote, "cached"
	}
	o.remoteAt = now
	for _, server := range o.stun {
		step, cancel := context.WithTimeout(ctx, 2*time.Second)
		ip, e := o.lookup(step, server)
		cancel()
		if e == nil && publicAddress(ip) {
			o.remote = ip
			return ip, "stun"
		}
	}
	for _, service := range o.http {
		step, cancel := context.WithTimeout(ctx, 4*time.Second)
		ip, e := o.fetch(step, service)
		cancel()
		if e == nil && publicAddress(ip) {
			o.remote = ip
			return ip, "http"
		}
	}
	// Keep the last answer through a transient failure: forgetting the address would
	// withdraw a working route and cost Hosted a publication for nothing. The source says
	// the attempt happened and learned nothing, which is not the same as "cached".
	return o.remote, "unavailable"
}

// WANSourceFresh reports whether a source string is an answer this pass actually obtained,
// rather than a memory ("cached") or a failed attempt ("unavailable").
func WANSourceFresh(source string) bool {
	return source == "gateway" || source == "stun" || source == "http"
}

// WANSourceAttempted reports whether outside services were consulted this pass at all. A
// family can only be called absent on a pass that looked.
func WANSourceAttempted(source string) bool {
	return WANSourceFresh(source) || source == "unavailable"
}

// fromGateway asks the router for its external address without creating a mapping.
func (o *WANObserver) fromGateway(ctx context.Context, topo Topology, port int) (netip.Addr, bool) {
	if o.mapper == nil || !gatewayAddress(topo.Gateway) || net.ParseIP(topo.LocalAddress) == nil || port < 1 || port > 65535 {
		return netip.Addr{}, false
	}
	for _, protocol := range []string{"natpmp", "upnp"} {
		step, cancel := context.WithTimeout(ctx, 2*time.Second)
		m, e := o.mapper.Prepare(step, Mapping{Protocol: protocol, Gateway: topo.Gateway, Client: topo.LocalAddress, InternalPort: port, ExternalPort: port})
		cancel()
		if e != nil {
			continue
		}
		// A private answer means CGNAT or double NAT: the router's "external" side is not the
		// WAN, so fall through to an outside observer.
		if ip, e := netip.ParseAddr(m.ExternalAddress); e == nil && publicAddress(ip) {
			return ip, true
		}
	}
	return netip.Addr{}, false
}

const stunMagicCookie = 0x2112A442

// stunAddress performs one RFC 5389 binding request over IPv4 UDP.
func stunAddress(ctx context.Context, server string) (netip.Addr, error) {
	var dialer net.Dialer
	conn, e := dialer.DialContext(ctx, "udp4", server)
	if e != nil {
		return netip.Addr{}, e
	}
	defer conn.Close()
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	request := make([]byte, 20)
	binary.BigEndian.PutUint16(request[0:], 0x0001)
	binary.BigEndian.PutUint32(request[4:], stunMagicCookie)
	if _, e = rand.Read(request[8:]); e != nil {
		return netip.Addr{}, e
	}
	if _, e = conn.Write(request); e != nil {
		return netip.Addr{}, e
	}
	reply := make([]byte, 576)
	n, e := conn.Read(reply)
	if e != nil {
		return netip.Addr{}, e
	}
	return parseSTUNAddress(reply[:n], request[8:20])
}

var errSTUN = errors.New("invalid STUN response")

func parseSTUNAddress(b, transaction []byte) (netip.Addr, error) {
	if len(b) < 20 || binary.BigEndian.Uint16(b[0:]) != 0x0101 || binary.BigEndian.Uint32(b[4:]) != stunMagicCookie || string(b[8:20]) != string(transaction) {
		return netip.Addr{}, errSTUN
	}
	length := int(binary.BigEndian.Uint16(b[2:]))
	if length > len(b)-20 {
		return netip.Addr{}, errSTUN
	}
	var mapped netip.Addr
	for attributes := b[20 : 20+length]; len(attributes) >= 4; {
		kind, size := binary.BigEndian.Uint16(attributes[0:]), int(binary.BigEndian.Uint16(attributes[2:]))
		if size > len(attributes)-4 {
			return netip.Addr{}, errSTUN
		}
		value := attributes[4 : 4+size]
		// XOR-MAPPED-ADDRESS survives NATs that rewrite embedded addresses; prefer it.
		if (kind == 0x0020 || kind == 0x0001) && size >= 8 && value[1] == 0x01 {
			raw := [4]byte{value[4], value[5], value[6], value[7]}
			if kind == 0x0020 {
				var cookie [4]byte
				binary.BigEndian.PutUint32(cookie[:], stunMagicCookie)
				for i := range raw {
					raw[i] ^= cookie[i]
				}
				return netip.AddrFrom4(raw), nil
			}
			mapped = netip.AddrFrom4(raw)
		}
		// Attributes are padded to four bytes; a final one may omit its padding.
		attributes = attributes[min(4+(size+3)&^3, len(attributes)):]
	}
	if mapped.IsValid() {
		return mapped, nil
	}
	return netip.Addr{}, errSTUN
}

// httpAddress reads a plain-text address over IPv4 so the answer is the IPv4 WAN address.
func httpAddress(ctx context.Context, service string) (netip.Addr, error) {
	request, e := http.NewRequestWithContext(ctx, http.MethodGet, service, nil)
	if e != nil {
		return netip.Addr{}, e
	}
	var dialer net.Dialer
	client := &http.Client{Transport: &http.Transport{DisableKeepAlives: true, DialContext: func(ctx context.Context, _, address string) (net.Conn, error) {
		return dialer.DialContext(ctx, "tcp4", address)
	}}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, e := client.Do(request)
	if e != nil {
		return netip.Addr{}, e
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return netip.Addr{}, errors.New("address service refused")
	}
	raw, e := io.ReadAll(io.LimitReader(response.Body, 64))
	if e != nil {
		return netip.Addr{}, e
	}
	return netip.ParseAddr(strings.TrimSpace(string(raw)))
}
