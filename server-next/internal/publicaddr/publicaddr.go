// Package publicaddr finds the server's public IPv4 address by asking well-known outside
// services that are not Portico's: STUN at Cloudflare and Google, and the plain-text address
// pages at Amazon and icanhazip. A server behind a home router cannot see that address itself,
// and the owner needs it to know where the server can be reached from outside.
//
// It asks rarely: when it starts, when this machine's own network addresses change, when the
// owner presses Check now, and otherwise every six hours. Each service gets a few seconds, and
// an address is believed only when at least two services give the same one. It is never run
// by a server that listens only on loopback, and never against the real services in a test.
package publicaddr

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// Service is one outside service: a STUN server ("host:port") or an address page ("https://...").
type Service struct {
	Name    string
	STUN    string
	Page    string
	Timeout time.Duration
}

// Default are the services asked, as the old server asked them.
var Default = []Service{
	{Name: "Cloudflare", STUN: "stun.cloudflare.com:3478", Timeout: 2 * time.Second},
	{Name: "Google", STUN: "stun.l.google.com:19302", Timeout: 2 * time.Second},
	{Name: "Amazon", Page: "https://checkip.amazonaws.com", Timeout: 4 * time.Second},
	{Name: "icanhazip", Page: "https://icanhazip.com", Timeout: 4 * time.Second},
}

// backstop is the longest the finder goes without asking, as the old server's quiet interval.
const backstop = 6 * time.Hour

// Answer is the public address as last found.
type Answer struct {
	// Address is empty until two services agreed on one.
	Address string `json:"address,omitempty"`
	// AgreedBy names the services that gave it.
	AgreedBy []string `json:"agreedBy,omitempty"`
	// Problem says why there is no answer this time; the last address is kept.
	Problem   string    `json:"problem,omitempty"`
	CheckedAt time.Time `json:"checkedAt,omitzero"`
}

// Finder keeps the public address up to date.
type Finder struct {
	log      *slog.Logger
	services []Service
	// ask asks one service. Tests give their own; with the real one a test is refused.
	ask  func(ctx context.Context, s Service) (netip.Addr, error)
	real bool
	// OnChange is told a new address, for Portico Services when it is connected.
	OnChange func(address string)
	// OnLocalChange is told when this machine's own addresses change, so the home addresses
	// the server publishes can be sent again.
	OnLocalChange func()

	mu      sync.Mutex
	want    bool
	answer  Answer
	kick    chan struct{}
	running bool
	rounds  chan struct{}
}

// New makes a finder that asks the default services.
func New(log *slog.Logger) *Finder {
	f := WithServices(log, Default, askService)
	f.real = true
	return f
}

// WithServices makes a finder that asks services with ask, which a test fakes.
func WithServices(log *slog.Logger, services []Service, ask func(ctx context.Context, s Service) (netip.Addr, error)) *Finder {
	return &Finder{log: log, services: services, ask: ask, kick: make(chan struct{}, 1), rounds: make(chan struct{})}
}

// Set says whether the public address is wanted (remote access on, an owner, a reachable port).
func (f *Finder) Set(want bool) {
	f.mu.Lock()
	changed := want != f.want
	f.want = want
	f.mu.Unlock()
	if changed && want {
		f.Check()
	}
}

// Check asks again soon (Check now).
func (f *Finder) Check() {
	select {
	case f.kick <- struct{}{}:
	default:
	}
}

// CheckNow asks again and waits for the answer, as long as ctx allows.
func (f *Finder) CheckNow(ctx context.Context) Answer {
	f.mu.Lock()
	running, ended := f.running, f.rounds
	f.mu.Unlock()
	if running {
		f.Check()
		select {
		case <-ended:
		case <-ctx.Done():
		}
	}
	return f.Answer()
}

// Answer is the public address as last found.
func (f *Finder) Answer() Answer {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.answer
}

// Run asks when kicked, when this machine's addresses change, and every six hours, until ctx ends.
func (f *Finder) Run(ctx context.Context) {
	f.mu.Lock()
	f.running = true
	f.mu.Unlock()
	backstopTimer := time.NewTicker(backstop)
	defer backstopTimer.Stop()
	local := time.NewTicker(time.Minute)
	defer local.Stop()
	seen := localAddresses()
	for {
		select {
		case <-ctx.Done():
			return
		case <-f.kick:
		case <-backstopTimer.C:
		case <-local.C:
			// Looking at this machine's own addresses asks nobody.
			now := localAddresses()
			if now == seen {
				continue
			}
			seen = now
			if f.OnLocalChange != nil {
				f.OnLocalChange()
			}
		}
		f.mu.Lock()
		want := f.want
		f.mu.Unlock()
		if want {
			f.round(ctx)
		}
		f.mu.Lock()
		close(f.rounds)
		f.rounds = make(chan struct{})
		f.mu.Unlock()
	}
}

func localAddresses() string {
	addrs, _ := net.InterfaceAddrs()
	out := make([]string, 0, len(addrs))
	for _, a := range addrs {
		out = append(out, a.String())
	}
	slices.Sort(out)
	return strings.Join(out, ",")
}

// errInTest: a test reached the real outside services.
var errInTest = errors.New("a test may not ask the real outside services")

// round asks every service at once and believes an address two or more of them give.
func (f *Finder) round(ctx context.Context) {
	now := time.Now().UTC()
	if f.real && testing.Testing() {
		f.log.Error("refused to ask outside services for the public address from a test")
		f.setAnswer(Answer{Problem: errInTest.Error(), CheckedAt: now})
		return
	}
	type reply struct {
		name string
		addr netip.Addr
	}
	replies := make(chan reply, len(f.services))
	for _, s := range f.services {
		go func() {
			step, cancel := context.WithTimeout(ctx, s.Timeout)
			defer cancel()
			addr, err := f.ask(step, s)
			if err != nil || !public(addr) {
				addr = netip.Addr{}
			}
			replies <- reply{s.Name, addr}
		}()
	}
	byAddr := map[netip.Addr][]string{}
	for range f.services {
		r := <-replies
		if r.addr.IsValid() {
			byAddr[r.addr] = append(byAddr[r.addr], r.name)
		}
	}
	var best netip.Addr
	for addr, names := range byAddr {
		if len(names) >= 2 && (!best.IsValid() || len(names) > len(byAddr[best])) {
			best = addr
		}
	}
	if !best.IsValid() {
		problem := "The outside services did not agree on this server's public address."
		if len(byAddr) == 0 {
			problem = "The outside services that tell this server its public address could not be reached."
		}
		f.mu.Lock()
		f.answer.Problem, f.answer.CheckedAt = problem, now
		f.mu.Unlock()
		return
	}
	names := byAddr[best]
	slices.Sort(names)
	f.setAnswer(Answer{Address: best.String(), AgreedBy: names, CheckedAt: now})
}

func (f *Finder) setAnswer(a Answer) {
	f.mu.Lock()
	before := f.answer.Address
	if a.Address == "" {
		a.Address, a.AgreedBy = f.answer.Address, f.answer.AgreedBy
	}
	f.answer = a
	f.mu.Unlock()
	if a.Address != "" && a.Address != before && f.OnChange != nil {
		f.OnChange(a.Address)
	}
}

// public reports whether addr is a public IPv4 address, the only kind worth believing.
func public(addr netip.Addr) bool {
	return addr.Is4() && addr.IsGlobalUnicast() && !addr.IsPrivate() && !netip.MustParsePrefix("100.64.0.0/10").Contains(addr)
}

func askService(ctx context.Context, s Service) (netip.Addr, error) {
	if s.STUN != "" {
		return stunAddress(ctx, s.STUN)
	}
	return pageAddress(ctx, s.Page)
}

const stunCookie = 0x2112A442

// stunAddress sends one RFC 5389 binding request over IPv4 UDP.
func stunAddress(ctx context.Context, server string) (netip.Addr, error) {
	var dialer net.Dialer
	conn, err := dialer.DialContext(ctx, "udp4", server)
	if err != nil {
		return netip.Addr{}, err
	}
	defer conn.Close()
	if deadline, ok := ctx.Deadline(); ok {
		conn.SetDeadline(deadline)
	}
	req := make([]byte, 20)
	binary.BigEndian.PutUint16(req[0:], 0x0001)
	binary.BigEndian.PutUint32(req[4:], stunCookie)
	rand.Read(req[8:])
	if _, err := conn.Write(req); err != nil {
		return netip.Addr{}, err
	}
	reply := make([]byte, 576)
	n, err := conn.Read(reply)
	if err != nil {
		return netip.Addr{}, err
	}
	return parseSTUN(reply[:n], req[8:20])
}

var errSTUN = errors.New("not a STUN answer to this request")

// parseSTUN reads the XOR-MAPPED-ADDRESS (or MAPPED-ADDRESS) of a binding answer.
func parseSTUN(b, transaction []byte) (netip.Addr, error) {
	if len(b) < 20 || binary.BigEndian.Uint16(b[0:]) != 0x0101 || binary.BigEndian.Uint32(b[4:]) != stunCookie || string(b[8:20]) != string(transaction) {
		return netip.Addr{}, errSTUN
	}
	length := int(binary.BigEndian.Uint16(b[2:]))
	if length > len(b)-20 {
		return netip.Addr{}, errSTUN
	}
	var mapped netip.Addr
	for attrs := b[20 : 20+length]; len(attrs) >= 4; {
		kind, size := binary.BigEndian.Uint16(attrs[0:]), int(binary.BigEndian.Uint16(attrs[2:]))
		if size > len(attrs)-4 {
			return netip.Addr{}, errSTUN
		}
		v := attrs[4 : 4+size]
		if (kind == 0x0020 || kind == 0x0001) && size >= 8 && v[1] == 0x01 {
			raw := [4]byte{v[4], v[5], v[6], v[7]}
			if kind == 0x0020 {
				var cookie [4]byte
				binary.BigEndian.PutUint32(cookie[:], stunCookie)
				for i := range raw {
					raw[i] ^= cookie[i]
				}
				return netip.AddrFrom4(raw), nil
			}
			mapped = netip.AddrFrom4(raw)
		}
		attrs = attrs[min(4+(size+3)&^3, len(attrs)):]
	}
	if mapped.IsValid() {
		return mapped, nil
	}
	return netip.Addr{}, errSTUN
}

// pageAddress reads a plain-text address page over IPv4, following no redirect, 64 bytes at most.
func pageAddress(ctx context.Context, page string) (netip.Addr, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, page, nil)
	if err != nil {
		return netip.Addr{}, err
	}
	var dialer net.Dialer
	client := &http.Client{Transport: &http.Transport{DisableKeepAlives: true, DialContext: func(ctx context.Context, _, address string) (net.Conn, error) {
		return dialer.DialContext(ctx, "tcp4", address)
	}}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		return netip.Addr{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return netip.Addr{}, errors.New("the address page refused")
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 64))
	if err != nil {
		return netip.Addr{}, err
	}
	return netip.ParseAddr(strings.TrimSpace(string(raw)))
}
