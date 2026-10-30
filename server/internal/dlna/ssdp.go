package dlna

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"math/rand/v2"
	"net"
	"net/http"
	"net/netip"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/networkpolicy"
	"github.com/PorticoMediaServer/portico/server/internal/supervise"
)

// SSDP (UPnP 1.0 discovery) on IPv4. Advertisements go out only on interfaces
// with an address inside the home networks, multicast with the default TTL of
// one so they never leave the link, and a search is answered only when it
// comes from a home address.
const (
	ssdpPort  = 1900
	maxAge    = 1800
	ssdpRenew = 10 * time.Minute
	// topologyEvery is how often the interfaces and home networks are compared.
	topologyEvery = 5 * time.Second
)

var ssdpGroup = &net.UDPAddr{IP: net.IPv4(239, 255, 255, 250), Port: ssdpPort}

// Advertisement is what the advertiser announces.
type Advertisement struct {
	// Bind is the HTTP listener's address; its port goes in LOCATION.
	Bind   string
	UUID   string
	Server string
	Home   func(context.Context) []netip.Prefix
}

// notificationTypes are the device's root, id, type and each service.
func notificationTypes(uuid string) []string {
	out := []string{"upnp:rootdevice", "uuid:" + uuid, deviceType}
	for _, v := range services {
		out = append(out, v.kind)
	}
	return out
}

func uniqueName(uuid, nt string) string {
	if nt == "uuid:"+uuid {
		return nt
	}
	return "uuid:" + uuid + "::" + nt
}

func notifyPacket(a Advertisement, location, nt, nts string) []byte {
	var b bytes.Buffer
	b.WriteString("NOTIFY * HTTP/1.1\r\nHOST: 239.255.255.250:1900\r\n")
	if nts == "ssdp:alive" {
		b.WriteString("CACHE-CONTROL: max-age=" + strconv.Itoa(maxAge) + "\r\nLOCATION: " + location + "\r\nSERVER: " + a.Server + "\r\n")
	}
	b.WriteString("NT: " + nt + "\r\nNTS: " + nts + "\r\nUSN: " + uniqueName(a.UUID, nt) + "\r\nCONTENT-LENGTH: 0\r\n\r\n")
	return b.Bytes()
}

func searchResponse(a Advertisement, location, st string, now time.Time) []byte {
	return []byte("HTTP/1.1 200 OK\r\nCACHE-CONTROL: max-age=" + strconv.Itoa(maxAge) + "\r\nDATE: " + now.UTC().Format(http.TimeFormat) +
		"\r\nEXT:\r\nLOCATION: " + location + "\r\nSERVER: " + a.Server + "\r\nST: " + st + "\r\nUSN: " + uniqueName(a.UUID, st) + "\r\nCONTENT-LENGTH: 0\r\n\r\n")
}

// parseSearch reads an M-SEARCH: its search target and the seconds the
// searcher waits for answers.
func parseSearch(packet []byte) (string, int, bool) {
	request, err := http.ReadRequest(bufio.NewReader(bytes.NewReader(packet)))
	if err != nil || request.Method != "M-SEARCH" || request.RequestURI != "*" {
		return "", 0, false
	}
	if strings.Trim(request.Header.Get("MAN"), `"`) != "ssdp:discover" {
		return "", 0, false
	}
	st := strings.TrimSpace(request.Header.Get("ST"))
	mx, err := strconv.Atoi(strings.TrimSpace(request.Header.Get("MX")))
	if st == "" || len(st) > 256 {
		return "", 0, false
	}
	if err != nil || mx < 1 {
		mx = 1
	}
	return st, min(mx, 5), true
}

// answers are the search targets a search is answered with: every type for
// ssdp:all, else the one asked for when this device has it.
func answers(st, uuid string) []string {
	types := notificationTypes(uuid)
	if st == "ssdp:all" {
		return types
	}
	for _, v := range types {
		if strings.EqualFold(v, st) {
			return []string{v}
		}
	}
	return nil
}

type ssdpSocket struct {
	conn      *net.UDPConn
	addresses []netip.Prefix
	home      []netip.Prefix
}

// location is the description URL on the interface address nearest peer.
func (s ssdpSocket) location(peer netip.Addr, port int) string {
	chosen := s.addresses[0].Addr()
	for _, p := range s.addresses {
		if p.Contains(peer) {
			chosen = p.Addr()
			break
		}
	}
	return "http://" + netip.AddrPortFrom(chosen, uint16(port)).String() + descriptionPath
}

func listenerPort(bind string) (netip.Addr, int, bool) {
	host, raw, err := net.SplitHostPort(bind)
	if err != nil {
		return netip.Addr{}, 0, false
	}
	port, err := strconv.Atoi(raw)
	if host == "" {
		host = "0.0.0.0"
	}
	bound, err2 := netip.ParseAddr(host)
	if err != nil || err2 != nil || port < 1 || port > 65535 || bound.IsLoopback() {
		return netip.Addr{}, 0, false
	}
	return bound.Unmap(), port, true
}

func (a Advertisement) networks(ctx context.Context) []netip.Prefix {
	if a.Home == nil {
		return nil
	}
	return a.Home(ctx)
}

func (a Advertisement) sockets(ctx context.Context) ([]ssdpSocket, int) {
	bound, port, ok := listenerPort(a.Bind)
	if !ok || bound.Is6() && !bound.IsUnspecified() {
		return nil, 0
	}
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil, 0
	}
	home := a.networks(ctx)
	var out []ssdpSocket
	for _, iface := range interfaces {
		if len(out) >= 16 {
			break
		}
		if iface.Flags&(net.FlagUp|net.FlagMulticast) != net.FlagUp|net.FlagMulticast || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		raw, _ := iface.Addrs()
		var addresses []netip.Prefix
		for _, address := range raw {
			p, err := netip.ParsePrefix(address.String())
			if err != nil || !p.Addr().Unmap().Is4() {
				continue
			}
			p = netip.PrefixFrom(p.Addr().Unmap(), p.Bits())
			if !bound.IsUnspecified() && p.Addr() != bound || !networkpolicy.Contains(home, p.Addr()) {
				continue
			}
			addresses = append(addresses, p)
		}
		if len(addresses) == 0 {
			continue
		}
		sort.Slice(addresses, func(i, j int) bool { return addresses[i].Addr().Less(addresses[j].Addr()) })
		conn, err := net.ListenMulticastUDP("udp4", &iface, ssdpGroup)
		if err != nil {
			continue
		}
		if err = ssdpSocketOptions(conn); err != nil {
			conn.Close()
			continue
		}
		_ = conn.SetReadBuffer(64 << 10)
		out = append(out, ssdpSocket{conn, addresses, home})
	}
	return out, port
}

// topology changes when the home networks, the listener or any interface
// address does; the advertiser rebuilds its sockets when it does.
func (a Advertisement) topology(ctx context.Context) string {
	parts := []string{a.Bind}
	for _, p := range a.networks(ctx) {
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
	sort.Strings(parts)
	sum := sha256.Sum256([]byte(strings.Join(parts, "\n")))
	return hex.EncodeToString(sum[:])
}

// limiter bounds answers across every socket, so a burst of searches (or a
// spoofed one) cannot turn this server into a packet source.
type limiter struct {
	mu     sync.Mutex
	window time.Time
	sent   int
}

func (l *limiter) allow(n int) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	if now.Sub(l.window) >= time.Second {
		l.window, l.sent = now, 0
	}
	if l.sent+n > 120 {
		return false
	}
	l.sent += n
	return true
}

// Advertise announces the device until ctx ends, then says goodbye. It joins
// every socket reader before returning.
func Advertise(ctx context.Context, a Advertisement) {
	budget := &limiter{}
	for ctx.Err() == nil {
		signature := a.topology(ctx)
		sockets, port := a.sockets(ctx)
		cycle, cancel := context.WithCancel(ctx)
		var wg sync.WaitGroup
		for _, socket := range sockets {
			wg.Add(1)
			supervise.Go("dlna.ssdp.socket", func() {
				defer wg.Done()
				a.serve(cycle, socket, port, budget)
			})
		}
		// A changed home-network list or interface rebuilds the sockets within
		// seconds, so narrowing the home networks stops answering there too.
		ticker := time.NewTicker(topologyEvery)
		for waiting := true; waiting; {
			select {
			case <-ctx.Done():
				waiting = false
			case <-ticker.C:
				waiting = len(sockets) > 0 && a.topology(ctx) == signature
			}
		}
		ticker.Stop()
		cancel()
		wg.Wait()
	}
}

func (a Advertisement) notifyAll(s ssdpSocket, port int, nts string) {
	location := s.location(s.addresses[0].Addr(), port)
	for _, nt := range notificationTypes(a.UUID) {
		_, _ = s.conn.WriteToUDP(notifyPacket(a, location, nt, nts), ssdpGroup)
	}
}

func (a Advertisement) serve(ctx context.Context, s ssdpSocket, port int, budget *limiter) {
	defer s.conn.Close()
	defer func() {
		// A byebye for each type, so players drop this server at once.
		_ = s.conn.SetWriteDeadline(time.Now().Add(200 * time.Millisecond))
		a.notifyAll(s, port, "ssdp:byebye")
	}()
	supervise.Go("dlna.ssdp.deadline", func() { <-ctx.Done(); _ = s.conn.SetReadDeadline(time.Now()) })
	// Announce three times at start (UDP is lossy), then well before the
	// advertised lifetime runs out.
	again := []time.Duration{time.Second, 2 * time.Second}
	next := time.Now()
	packet := make([]byte, 2048)
	for ctx.Err() == nil {
		if !time.Now().Before(next) {
			a.notifyAll(s, port, "ssdp:alive")
			next = time.Now().Add(ssdpRenew)
			if len(again) > 0 {
				next, again = time.Now().Add(again[0]), again[1:]
			}
		}
		_ = s.conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
		n, peer, err := s.conn.ReadFromUDP(packet)
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				continue
			}
			return
		}
		address, ok := netip.AddrFromSlice(peer.IP)
		if !ok || !networkpolicy.Contains(s.home, address.Unmap()) || n >= len(packet) {
			continue
		}
		st, _, ok := parseSearch(packet[:n])
		if !ok {
			continue
		}
		targets := answers(st, a.UUID)
		if len(targets) == 0 || !budget.allow(len(targets)) {
			continue
		}
		location := s.location(address.Unmap(), port)
		target := *peer
		// Every searcher waits at least a second (MX >= 1); answers spread
		// over it rather than arriving as one burst.
		time.AfterFunc(rand.N(time.Second), func() {
			if ctx.Err() != nil {
				return
			}
			for _, st := range targets {
				_, _ = s.conn.WriteToUDP(searchResponse(a, location, st, time.Now()), &target)
			}
		})
	}
}
