package networking

// DNS-SD is a source of short-lived, untrusted address hints, never identity or
// authorization. Clients must challenge the durable Ed25519 identity before
// sending credentials. RFC 6762/6763 wire limits apply even on a private LAN.
import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"net"
	"net/netip"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/networkpolicy"
	"github.com/PorticoMediaServer/portico/server/internal/supervise"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const discoveryService = "_portico._tcp.local."

type discoveryRecord struct {
	name   string
	kind   uint16
	data   []byte
	unique bool
}
type discoverySet struct {
	instance, host string
	records        []discoveryRecord
}
type Discovery struct {
	keys         *ProtectedKeys
	runner       *AuthorityRunner
	name         func() string
	homeNetworks func(context.Context) []netip.Prefix
}

func NewDiscovery(keys *ProtectedKeys, runner *AuthorityRunner, name func() string) *Discovery {
	return &Discovery{keys: keys, runner: runner, name: name}
}
func dnsName(name string) []byte {
	var out []byte
	for _, label := range strings.Split(strings.TrimSuffix(name, "."), ".") {
		if len(label) == 0 || len(label) > 63 {
			return nil
		}
		out = append(out, byte(len(label)))
		out = append(out, label...)
	}
	return append(out, 0)
}
func dnsReadName(packet []byte, offset int) (string, int, bool) {
	var labels []string
	next := -1
	seen := map[int]bool{}
	total := 0
	for hops := 0; hops < 128; hops++ {
		if offset < 0 || offset >= len(packet) || seen[offset] {
			return "", 0, false
		}
		seen[offset] = true
		n := int(packet[offset])
		offset++
		if n == 0 {
			if next < 0 {
				next = offset
			}
			return strings.ToLower(strings.Join(labels, ".") + "."), next, total <= 255
		}
		if n&0xc0 == 0xc0 {
			if offset >= len(packet) {
				return "", 0, false
			}
			if next < 0 {
				next = offset + 1
			}
			offset = (n&63)<<8 | int(packet[offset])
			continue
		}
		if n > 63 || offset+n > len(packet) {
			return "", 0, false
		}
		s := string(packet[offset : offset+n])
		if strings.ContainsAny(s, ".\x00\r\n") {
			return "", 0, false
		}
		labels = append(labels, s)
		offset += n
		total += n + 1
		if total > 255 {
			return "", 0, false
		}
	}
	return "", 0, false
}
func (s discoverySet) packet(ttl uint32, probe bool) []byte {
	b := make([]byte, 12)
	if probe {
		binary.BigEndian.PutUint16(b[4:6], 2)
		for _, name := range []string{s.host, s.instance} {
			b = append(b, dnsName(name)...)
			b = append(b, 0, 255, 0, 1)
		}
		binary.BigEndian.PutUint16(b[8:10], uint16(len(s.records)))
	} else {
		binary.BigEndian.PutUint16(b[2:4], 0x8400)
		binary.BigEndian.PutUint16(b[6:8], uint16(len(s.records)))
	}
	for _, r := range s.records {
		b = append(b, dnsName(r.name)...)
		h := make([]byte, 10)
		binary.BigEndian.PutUint16(h, r.kind)
		class := uint16(1)
		if r.unique && !probe {
			class |= 0x8000
		}
		binary.BigEndian.PutUint16(h[2:], class)
		binary.BigEndian.PutUint32(h[4:], ttl)
		binary.BigEndian.PutUint16(h[8:], uint16(len(r.data)))
		b = append(b, h...)
		b = append(b, r.data...)
	}
	if len(b) > 1472 {
		return nil
	}
	return b
}
func (s discoverySet) requested(packet []byte) bool {
	if len(packet) < 12 || len(packet) > 1472 || binary.BigEndian.Uint16(packet[2:4])&0xfa00 != 0 {
		return false
	}
	count := int(binary.BigEndian.Uint16(packet[4:6]))
	if count < 1 || count > 32 {
		return false
	}
	offset := 12
	wanted := false
	for i := 0; i < count; i++ {
		name, next, ok := dnsReadName(packet, offset)
		if !ok || next+4 > len(packet) {
			return false
		}
		kind := binary.BigEndian.Uint16(packet[next:])
		class := binary.BigEndian.Uint16(packet[next+2:]) & 0x7fff
		offset = next + 4
		if class == 1 && (kind == 255 || kind == 12 || kind == 16 || kind == 33 || kind == 1 || kind == 28) && (name == discoveryService || name == strings.ToLower(s.instance) || name == s.host) {
			wanted = true
		}
	}
	return wanted
}

// Conflicting unique A/AAAA/TXT records cause a withdrawal and a new instance
// suffix next cycle. Do not respond indefinitely to an attacker-supplied conflict.
func (s discoverySet) conflict(packet []byte) bool {
	if len(packet) < 12 || binary.BigEndian.Uint16(packet[2:4])&0x8000 == 0 {
		return false
	}
	off := 12
	q := int(binary.BigEndian.Uint16(packet[4:6]))
	if q > 32 {
		return false
	}
	for i := 0; i < q; i++ {
		_, n, ok := dnsReadName(packet, off)
		if !ok || n+4 > len(packet) {
			return false
		}
		off = n + 4
	}
	count := int(binary.BigEndian.Uint16(packet[6:8])) + int(binary.BigEndian.Uint16(packet[8:10])) + int(binary.BigEndian.Uint16(packet[10:12]))
	if count > 64 {
		return false
	}
	for i := 0; i < count; i++ {
		name, n, ok := dnsReadName(packet, off)
		if !ok || n+10 > len(packet) {
			return false
		}
		kind := binary.BigEndian.Uint16(packet[n:])
		ttl := binary.BigEndian.Uint32(packet[n+4:])
		size := int(binary.BigEndian.Uint16(packet[n+8:]))
		off = n + 10
		if off+size > len(packet) {
			return false
		}
		data := packet[off : off+size]
		off += size
		if ttl == 0 {
			continue
		}
		known, matched := false, false
		for _, r := range s.records {
			if r.unique && name == strings.ToLower(r.name) && kind == r.kind && (kind == 1 || kind == 28 || kind == 16) {
				known = true
				if string(data) == string(r.data) {
					matched = true
				}
			}
		}
		if known && !matched {
			return true
		}
	}
	return false
}
func (d *Discovery) snapshot(ctx context.Context, port int, addresses []netip.Addr, suffix string) (discoverySet, error) {
	var s discoverySet
	friendly := d.name()
	err := d.runner.Do(ctx, func(ctx context.Context) error {
		gated, e := dbwork.BeginSnapshot(ctx, d.keys.db)
		if e != nil {
			return e
		}
		tx := gated.Tx()
		defer gated.Rollback()
		id, e := CurrentIdentityTx(ctx, tx)
		if e != nil {
			return e
		}
		digest := sha256.Sum256(id.PublicKey)
		name := strings.ReplaceAll(friendly, ".", "-")
		name = strings.Map(func(r rune) rune {
			if r < 32 || r == 127 {
				return -1
			}
			return r
		}, name)
		for len(name) > 32 {
			_, n := utf8.DecodeLastRuneInString(name)
			name = name[:len(name)-n]
		}
		if name == "" {
			name = "Portico"
		}
		tag := hex.EncodeToString(digest[:8]) + suffix
		s.host = "portico-" + tag + ".local."
		s.instance = name + "-" + tag + "." + discoveryService
		s.records = append(s.records, discoveryRecord{discoveryService, 12, dnsName(s.instance), false})
		srv := make([]byte, 6)
		binary.BigEndian.PutUint16(srv[4:], uint16(port))
		srv = append(srv, dnsName(s.host)...)
		s.records = append(s.records, discoveryRecord{s.instance, 33, srv, true})
		var txt []byte
		// fp identifies this identity key, never a TLS certificate. Discovery
		// alone grants no trust: the client must verify a fresh v2 route proof
		// from this key binding the leaf observed on that same connection.
		for _, v := range []string{"v=1", "id=" + id.ServerID, "fp=" + base64.RawURLEncoding.EncodeToString(digest[:]), "port=" + strconv.Itoa(port), "path=/", "name=" + name} {
			txt = append(txt, byte(len(v)))
			txt = append(txt, v...)
		}
		s.records = append(s.records, discoveryRecord{s.instance, 16, txt, true})
		for _, ip := range addresses {
			kind := uint16(1)
			if ip.Is6() {
				kind = 28
			}
			s.records = append(s.records, discoveryRecord{s.host, kind, ip.AsSlice(), true})
		}
		return nil
	})
	return s, err
}

type discoverySocket struct {
	conn        *net.UDPConn
	destination *net.UDPAddr
	set         discoverySet
	signature   string
	home        []netip.Prefix
}

func (d *Discovery) sockets(ctx context.Context, bind string, serial uint64) []discoverySocket {
	host, p, e := net.SplitHostPort(bind)
	if e != nil {
		return nil
	}
	port, e := strconv.Atoi(p)
	if host == "" {
		host = "::"
	}
	bound, e2 := netip.ParseAddr(host)
	if e != nil || e2 != nil || port < 1 || port > 65535 || bound.IsLoopback() {
		return nil
	}
	interfaces, e := net.Interfaces()
	if e != nil {
		return nil
	}
	home := d.networks(ctx)
	var out []discoverySocket
	for _, iface := range interfaces {
		if len(out) >= 16 {
			break
		}
		if iface.Flags&(net.FlagUp|net.FlagMulticast) != net.FlagUp|net.FlagMulticast || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		raw, _ := iface.Addrs()
		var addresses []netip.Addr
		for _, a := range raw {
			p, e := netip.ParsePrefix(a.String())
			if e != nil {
				continue
			}
			ip := p.Addr().Unmap()
			if (!bound.IsUnspecified() && ip != bound) || bound.Is4() && ip.Is6() || !networkpolicy.Contains(home, ip) {
				continue
			}
			addresses = append(addresses, ip)
			if len(addresses) == 4 {
				break
			}
		}
		if len(addresses) == 0 {
			continue
		}
		sort.Slice(addresses, func(i, j int) bool { return addresses[i].Less(addresses[j]) })
		suffix := discoveryInterfaceSuffix(iface.Name)
		if serial > 0 {
			suffix += "-" + strconv.FormatUint(serial, 36)
		}
		set, e := d.snapshot(ctx, port, addresses, suffix)
		if e != nil || set.packet(120, false) == nil {
			continue
		}
		for _, family := range []string{"udp4", "udp6"} {
			has := false
			for _, a := range addresses {
				if a.Is4() == (family == "udp4") {
					has = true
				}
			}
			if !has {
				continue
			}
			dest := &net.UDPAddr{IP: net.ParseIP("224.0.0.251"), Port: 5353}
			if family == "udp6" {
				dest.IP = net.ParseIP("ff02::fb")
				dest.Zone = iface.Name
			}
			c, e := net.ListenMulticastUDP(family, &iface, dest)
			if e != nil {
				continue
			}
			if e = discoverySocketOptions(c, family == "udp6"); e != nil {
				c.Close()
				continue
			}
			_ = c.SetReadBuffer(16384)
			signature := iface.Name + "/" + family + "/" + string(set.packet(120, false))
			out = append(out, discoverySocket{c, dest, set, signature, home})
		}
	}
	return out
}

// Run joins all socket readers before returning. No permission prompt, gateway
// mutation, or Hosted request is triggered by advertising a public identity hint.
func (d *Discovery) topologySignature(ctx context.Context, bind string) string {
	var parts []string
	parts = append(parts, bind)
	for _, p := range d.networks(ctx) {
		parts = append(parts, p.String())
	}
	interfaces, _ := net.Interfaces()
	for _, iface := range interfaces {
		parts = append(parts, iface.Name+"/"+iface.Flags.String())
		addresses, _ := iface.Addrs()
		for _, address := range addresses {
			parts = append(parts, iface.Name+"/"+address.String())
		}
	}
	_, rawPort, _ := net.SplitHostPort(bind)
	port, _ := strconv.Atoi(rawPort)
	if state, e := d.snapshot(ctx, port, nil, ""); e == nil {
		parts = append(parts, string(state.packet(120, false)))
	}
	sort.Strings(parts)
	sum := sha256.Sum256([]byte(strings.Join(parts, "\n")))
	return hex.EncodeToString(sum[:])
}
func (d *Discovery) Run(ctx context.Context, bind string) {
	var serial uint64
	for ctx.Err() == nil {
		signature := d.topologySignature(ctx, bind)
		sockets := d.sockets(ctx, bind, serial)
		cycle, cancel := context.WithCancel(ctx)
		var wg sync.WaitGroup
		conflict := make(chan struct{}, 1)
		closed := make(chan struct{}, 1)
		for _, socket := range sockets {
			wg.Add(1)
			supervise.Go("networking.discovery.socket", func() {
				s := socket
				defer wg.Done()
				defer s.conn.Close()
				defer func() {
					if cycle.Err() == nil {
						select {
						case closed <- struct{}{}:
						default:
						}
					}
				}()
				supervise.Go("networking.discovery.deadline", func() { <-cycle.Done(); _ = s.conn.SetReadDeadline(time.Now()) })
				packet := make([]byte, 1473)
				ancillary := make([]byte, 128)
				announced := false
				last := time.Time{}
				probe := 0
				next := time.Now().Add(time.Duration(time.Now().UnixNano()%250) * time.Millisecond)
				defer func() {
					if announced {
						_ = s.conn.SetWriteDeadline(time.Now().Add(100 * time.Millisecond))
						_, _ = s.conn.WriteToUDP(s.set.packet(0, false), s.destination)
					}
				}()
				for cycle.Err() == nil {
					now := time.Now()
					if !now.Before(next) {
						if probe < 3 {
							_, _ = s.conn.WriteToUDP(s.set.packet(120, true), s.destination)
							probe++
							next = now.Add(250 * time.Millisecond)
						} else {
							_, _ = s.conn.WriteToUDP(s.set.packet(120, false), s.destination)
							announced = true
							next = now.Add(60 * time.Second)
						}
					}
					_ = s.conn.SetReadDeadline(time.Now().Add(250 * time.Millisecond))
					n, o, flags, peer, e := s.conn.ReadMsgUDP(packet, ancillary)
					if e != nil {
						if ne, ok := e.(net.Error); ok && ne.Timeout() {
							continue
						}
						return
					}
					if discoveryTruncated(flags) || n > 1472 || !discoveryOnLink(ancillary[:o]) || peer.Port != 5353 {
						continue
					}
					ip, ok := netip.AddrFromSlice(peer.IP)
					if !ok || !networkpolicy.Contains(s.home, ip.Unmap()) {
						continue
					}
					if s.set.conflict(packet[:n]) {
						select {
						case conflict <- struct{}{}:
						default:
						}
						return
					}
					if announced && s.set.requested(packet[:n]) && time.Since(last) >= time.Second {
						_, _ = s.conn.WriteToUDP(s.set.packet(120, false), s.destination)
						last = time.Now()
					}
				}
			})
		}
		timer := time.NewTicker(30 * time.Second)
		waiting := true
		for waiting {
			select {
			case <-ctx.Done():
				waiting = false
			case <-timer.C:
				if len(sockets) == 0 || d.topologySignature(ctx, bind) != signature {
					waiting = false
				}
			case <-conflict:
				serial++
				waiting = false
			case <-closed:
				waiting = false
			}
		}
		timer.Stop()
		cancel()
		wg.Wait()
	}
}

// Distinct names prevent two interfaces on one link from conflicting over
// different address RRsets. The advertised server ID remains the deduplication key.
func discoveryInterfaceSuffix(name string) string {
	sum := sha256.Sum256([]byte(name))
	return "-" + hex.EncodeToString(sum[:5])
}
func (d *Discovery) SetHomeNetworks(source func(context.Context) []netip.Prefix) {
	d.homeNetworks = source
}
func (d *Discovery) networks(ctx context.Context) []netip.Prefix {
	if d.homeNetworks != nil {
		return d.homeNetworks(ctx)
	}
	var out []netip.Prefix
	for _, raw := range networkpolicy.DefaultHomeNetworks() {
		if p, err := netip.ParsePrefix(raw); err == nil {
			out = append(out, p)
		}
	}
	return out
}
