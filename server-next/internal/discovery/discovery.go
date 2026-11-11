// Package discovery lets apps on the home network list this server without an address being
// typed ("Find this server at home"). It answers multicast DNS (RFC 6762) questions for the
// DNS-SD service _portico._tcp.local with the server's address and port and, in its TXT
// record, the server's id and identity fingerprint. It never carries the server's name.
//
// An announcement is a hint, never trust: an app checks the server's identity proof at the
// address before it sends anything else.
package discovery

import (
	"context"
	"encoding/binary"
	"log/slog"
	"net"
	"net/netip"
	"strings"
	"sync"
	"time"
)

const (
	service = "_portico._tcp.local."
	ttl     = 120 // seconds, as RFC 6762 suggests for records naming a host
)

var group = &net.UDPAddr{IP: net.IPv4(224, 0, 0, 251), Port: 5353}

// Record is what is announced.
type Record struct {
	// ServerID and Fingerprint are the server's identity, as its identity proof states them.
	ServerID, Fingerprint string
	Port                  int
}

// names are the instance and host names: "Portico-" and the first eight characters of the
// fingerprint, which tell two servers apart without saying anything about either.
func (r Record) names() (instance, host string) {
	tag := strings.ToLower(strings.Map(func(c rune) rune {
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' {
			return c
		}
		return -1
	}, r.Fingerprint))
	if len(tag) > 8 {
		tag = tag[:8]
	}
	return "Portico-" + tag + "." + service, "portico-" + tag + ".local."
}

func name(n string) []byte {
	var out []byte
	for _, label := range strings.Split(strings.TrimSuffix(n, "."), ".") {
		out = append(out, byte(len(label)))
		out = append(out, label...)
	}
	return append(out, 0)
}

// readName reads a possibly compressed name, lower case, with its end; ok is false for one
// that is malformed or loops.
func readName(p []byte, at int) (string, int, bool) {
	var labels []string
	end := -1
	for hops := 0; hops < 64; hops++ {
		if at >= len(p) {
			return "", 0, false
		}
		n := int(p[at])
		switch {
		case n == 0:
			if end < 0 {
				end = at + 1
			}
			return strings.ToLower(strings.Join(labels, ".")) + ".", end, true
		case n&0xc0 == 0xc0:
			if at+1 >= len(p) {
				return "", 0, false
			}
			if end < 0 {
				end = at + 2
			}
			at = (n&0x3f)<<8 | int(p[at+1])
		case n > 63 || at+1+n > len(p):
			return "", 0, false
		default:
			labels = append(labels, string(p[at+1:at+1+n]))
			at += 1 + n
		}
	}
	return "", 0, false
}

// asked reports whether a query packet asks about this server.
func asked(p []byte, r Record) bool {
	if len(p) < 12 || p[2]&0x80 != 0 { // too short, or an answer
		return false
	}
	instance, host := r.names()
	at := 12
	for range binary.BigEndian.Uint16(p[4:]) {
		n, end, ok := readName(p, at)
		if !ok || end+4 > len(p) {
			return false
		}
		at = end + 4
		if n == service || n == strings.ToLower(instance) || n == host {
			return true
		}
	}
	return false
}

// answer is the full answer for addr, with the given time to live (0 says goodbye).
func answer(r Record, addr netip.Addr, live uint32) []byte {
	instance, host := r.names()
	p := make([]byte, 12)
	binary.BigEndian.PutUint16(p[2:], 0x8400) // an authoritative answer
	add := func(owner string, kind uint16, unique bool, data []byte) {
		p = append(p, name(owner)...)
		class := uint16(1)
		if unique {
			class |= 0x8000 // cache-flush: this server alone owns these records
		}
		p = binary.BigEndian.AppendUint16(p, kind)
		p = binary.BigEndian.AppendUint16(p, class)
		p = binary.BigEndian.AppendUint32(p, live)
		p = binary.BigEndian.AppendUint16(p, uint16(len(data)))
		p = append(p, data...)
		binary.BigEndian.PutUint16(p[6:], binary.BigEndian.Uint16(p[6:])+1)
	}
	add(service, 12, false, name(instance))
	srv := binary.BigEndian.AppendUint16(make([]byte, 4), uint16(r.Port))
	add(instance, 33, true, append(srv, name(host)...))
	var txt []byte
	for _, kv := range []string{"v=1", "id=" + r.ServerID, "fp=" + r.Fingerprint} {
		txt = append(append(txt, byte(len(kv))), kv...)
	}
	add(instance, 16, true, txt)
	a := addr.As4()
	add(host, 1, true, a[:])
	return p
}

// Announcer answers on every network interface with a private IPv4 address while on.
type Announcer struct {
	log    *slog.Logger
	record Record

	mu      sync.Mutex
	on      bool
	changed chan struct{}
}

// New makes an announcer for record. It is off until Set turns it on.
func New(log *slog.Logger, record Record) *Announcer {
	return &Announcer{log: log, record: record, changed: make(chan struct{}, 1)}
}

// Set turns the announcement on or off.
func (a *Announcer) Set(on bool) {
	a.mu.Lock()
	differs := a.on != on
	a.on = on
	a.mu.Unlock()
	if differs {
		select {
		case a.changed <- struct{}{}:
		default:
		}
	}
}

func (a *Announcer) isOn() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.on
}

// Run answers until ctx ends. When it is turned off, or stops, it says goodbye so apps drop
// the server from their lists at once.
func (a *Announcer) Run(ctx context.Context) {
	for {
		if a.isOn() {
			a.serve(ctx)
		}
		select {
		case <-ctx.Done():
			return
		case <-a.changed:
		}
	}
}

type socket struct {
	conn *net.UDPConn
	addr netip.Addr
}

// serve joins the group on each private IPv4 interface, announces, answers, and says goodbye.
func (a *Announcer) serve(ctx context.Context) {
	var socks []socket
	ifaces, _ := net.Interfaces()
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagMulticast == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		var addr netip.Addr
		addrs, _ := iface.Addrs()
		for _, raw := range addrs {
			if p, err := netip.ParsePrefix(raw.String()); err == nil && p.Addr().Is4() && p.Addr().IsPrivate() {
				addr = p.Addr()
				break
			}
		}
		if !addr.IsValid() {
			continue
		}
		conn, err := net.ListenMulticastUDP("udp4", &iface, group)
		if err != nil {
			a.log.Warn("this server cannot be announced on a network", "interface", iface.Name, "error", err)
			continue
		}
		socks = append(socks, socket{conn, addr})
	}
	if len(socks) == 0 {
		return
	}
	stop := make(chan struct{})
	var wg sync.WaitGroup
	for _, s := range socks {
		wg.Add(1)
		go func() {
			defer wg.Done()
			buf := make([]byte, 9000)
			for {
				n, _, err := s.conn.ReadFromUDP(buf)
				if err != nil {
					return
				}
				if asked(buf[:n], a.record) {
					s.conn.WriteToUDP(answer(a.record, s.addr, ttl), group)
				}
			}
		}()
	}
	// Announce twice, a second apart, as RFC 6762 asks, and then only answer.
	for i := range 2 {
		for _, s := range socks {
			s.conn.WriteToUDP(answer(a.record, s.addr, ttl), group)
		}
		if i == 0 {
			select {
			case <-time.After(time.Second):
			case <-ctx.Done():
			case <-stop:
			}
		}
	}
	select {
	case <-ctx.Done():
	case <-a.changed:
		// Run looks at the switch again; put the signal back for it.
		select {
		case a.changed <- struct{}{}:
		default:
		}
	}
	close(stop)
	for _, s := range socks {
		s.conn.WriteToUDP(answer(a.record, s.addr, 0), group)
		s.conn.Close()
	}
	wg.Wait()
}
