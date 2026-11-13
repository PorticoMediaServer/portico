// Package portmap asks the home router to open the server's port to the internet and keeps it
// open, with whichever of PCP, NAT-PMP and UPnP the router speaks, tried in that order. What
// the router agreed to lives in memory: after a restart the server simply asks again.
//
// Every question is bounded (a few seconds per protocol, one router address), and the only
// machine ever asked is the router the server's default route goes through.
package portmap

import (
	"context"
	"errors"
	"log/slog"
	"net/netip"
	"strconv"
	"sync"
	"time"
)

// Router is where the questions go. Production finds it from the default route; tests point
// it at fake routers.
type Router struct {
	// Gateway is the router's address. Invalid means it could not be found.
	Gateway netip.Addr
	// Client is the global IPv6 address on the default route's interface. IPv6
	// control requests must originate there, as does the port being opened.
	Client netip.Addr
	// PMPPort is the router's NAT-PMP and PCP port (5351). SSDP is where UPnP's search is
	// sent (239.255.255.250:1900).
	PMPPort int
	SSDP    netip.AddrPort
}

// Status is the router's answer as it stands.
type Status struct {
	// ClientAddress ties the answer to its source when the API publishes it.
	// It is an internal observation, not another address advertised to apps.
	ClientAddress netip.Addr `json:"-"`
	// State is "off", "trying", "open" or "failed".
	State string `json:"state"`
	// Protocol is the one the router answered: "PCP", "NAT-PMP" or "UPnP".
	Protocol     string `json:"protocol,omitempty"`
	ExternalIP   string `json:"externalAddress,omitempty"`
	ExternalPort int    `json:"externalPort,omitempty"`
	// Problem says in plain words why the port is not open.
	Problem string    `json:"problem,omitempty"`
	At      time.Time `json:"checkedAt,omitzero"`
}

const (
	// lifetime is how long the router is asked to keep the port open; it is asked again at half.
	lifetime = 2 * time.Hour
	// attempt bounds one round of asking, every protocol together.
	attempt = 15 * time.Second
	// The wait after a failure doubles from retryFirst to retryMax.
	retryFirst = time.Minute
	retryMax   = time.Hour
)

// Mapper keeps the server's port open on the router while the owner wants it.
type Mapper struct {
	ipv6 bool
	// OnChange publishes a completed answer. Set before Run; it is called
	// without the mapper lock so the callback may read Status.
	OnChange func()
	log      *slog.Logger
	router   func() Router

	mu       sync.Mutex
	want     bool
	internal int
	external int
	status   Status
	held     *mapping // what the router agreed to, to renew and to remove
	kick     chan struct{}
	running  bool
	rounds   chan struct{} // closed when the round under way ends
}

// New makes a mapper that finds the router with router (nil: the default route's gateway).
func New(log *slog.Logger, router func() Router) *Mapper {
	if router == nil {
		router = DefaultRouter
	}
	return &Mapper{log: log, router: router, kick: make(chan struct{}, 1), status: Status{State: "off"}, rounds: make(chan struct{})}
}

// Set says whether the port should be open, and which ports. It takes effect at once.
func (m *Mapper) Set(want bool, internal, external int) {
	m.mu.Lock()
	changed := want != m.want || internal != m.internal || external != m.external
	m.want, m.internal, m.external = want, internal, external
	m.mu.Unlock()
	if changed {
		m.Check()
	}
}

// Check asks the router again now ("Check now", "Try again").
func (m *Mapper) Check() {
	select {
	case m.kick <- struct{}{}:
	default:
	}
}

// CheckNow asks the router again and waits for its answer, as long as ctx allows.
func (m *Mapper) CheckNow(ctx context.Context) Status {
	m.mu.Lock()
	running, ended := m.running, m.rounds
	m.mu.Unlock()
	if running {
		m.Check()
		select {
		case <-ended:
		case <-ctx.Done():
		}
	}
	return m.Status()
}

// Wanted says whether the port should be open now: what the server last asked for.
func (m *Mapper) Wanted() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.want
}

// Status is the router's last answer.
func (m *Mapper) Status() Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.status
}

// Run keeps the port open until ctx ends, then asks the router to close it, within two seconds.
func (m *Mapper) Run(ctx context.Context) {
	m.mu.Lock()
	m.running = true
	m.mu.Unlock()
	timer := time.NewTimer(0)
	defer timer.Stop()
	retry := retryFirst
	for {
		select {
		case <-ctx.Done():
			done, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			m.remove(done)
			cancel()
			return
		case <-m.kick:
		case <-timer.C:
		}
		// A request to look again that came in before this round is answered by it.
		select {
		case <-m.kick:
		default:
		}
		next := m.round(ctx)
		m.mu.Lock()
		close(m.rounds)
		m.rounds = make(chan struct{})
		m.mu.Unlock()
		if m.Status().State == "failed" {
			next, retry = retry, min(retry*2, retryMax)
		} else {
			retry = retryFirst
		}
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		if next > 0 {
			timer.Reset(next)
		}
	}
}

// round does what the owner wants now and says when to look again (0: not until asked).
func (m *Mapper) round(ctx context.Context) time.Duration {
	m.mu.Lock()
	want, internal, external, held := m.want, m.internal, m.external, m.held
	m.mu.Unlock()
	if m.ipv6 && held != nil {
		r := m.router()
		if r.Gateway != held.router.Gateway || r.Client != held.router.Client {
			m.remove(ctx)
			held = nil
		}
	}
	if held != nil && (!want || held.internal != internal || held.asked != external) {
		m.remove(ctx)
	}
	if !want || internal == 0 {
		m.setStatus(Status{State: "off"}, nil)
		return 0
	}
	m.setStatus(Status{State: "trying"}, nil)
	ctx, cancel := context.WithTimeout(ctx, attempt)
	defer cancel()
	got, err := m.ask(ctx, internal, external, lifetime)
	now := time.Now().UTC()
	if err != nil {
		problem := "The router did not open the port: " + err.Error() + ". Turn port mapping off and forward the port by hand if it never works."
		if errors.Is(err, errUnsupported) {
			problem = "The router did not answer PCP, NAT-PMP or UPnP. Turn on UPnP in the router, or turn port mapping off and forward the port by hand."
			if m.ipv6 {
				problem = "The router did not answer IPv6 PCP or UPnP firewall control. Turn port mapping off to handle the IPv6 firewall by hand."
			}
		} else if errors.Is(err, errNoRouter) {
			problem = "The router could not be found from this server's network settings."
			if m.ipv6 {
				problem = "This server has no usable public IPv6 address and router on its default network route."
			}
		}
		m.setStatus(Status{State: "failed", Problem: problem, At: now}, nil)
		m.log.Warn("the router did not open the server's port", "problem", problem)
		return 0
	}
	got.internal, got.asked = internal, external
	st := Status{State: "open", Protocol: got.protocol, ExternalPort: got.externalPort, ClientAddress: got.router.Client, At: now}
	if got.externalIP.IsValid() {
		st.ExternalIP = got.externalIP.String()
	}
	if got.externalPort != external {
		st.Problem = "The router opened port " + strconv.Itoa(got.externalPort) + " instead of " + strconv.Itoa(external) + "."
	}
	m.setStatus(st, &got)
	if got.lifetime == 0 {
		return 0 // a permanent mapping, removed when the server stops
	}
	return max(got.lifetime/2, time.Minute)
}

// setStatus records the router's answer, and a mapping it newly agreed to. What the router
// holds is forgotten only by remove: a failed re-ask leaves the mapping it may still hold to be
// renewed or removed.
func (m *Mapper) setStatus(st Status, held *mapping) {
	m.mu.Lock()
	m.status = st
	if held != nil {
		m.held = held
	}
	m.mu.Unlock()
	if m.OnChange != nil && st.State != "trying" {
		m.OnChange()
	}
}

var errNoRouter = errors.New("no router")

// ask tries PCP, then NAT-PMP, then UPnP. A router that refuses outright is not asked again
// in another protocol: it understood and said no.
func (m *Mapper) ask(ctx context.Context, internal, external int, life time.Duration) (mapping, error) {
	r := m.router()
	if !r.Gateway.IsValid() || m.ipv6 && !r.Client.IsValid() {
		return mapping{}, errNoRouter
	}
	pmp := netip.AddrPortFrom(r.Gateway, uint16(r.PMPPort))
	// A renewal of the mapping held carries its nonce.
	var nonce []byte
	m.mu.Lock()
	held := m.held
	if m.held != nil && m.held.protocol == "PCP" && m.held.internal == internal && m.held.asked == external {
		nonce = m.held.nonce
	}
	m.mu.Unlock()
	got, err := pcp(ctx, pmp, internal, external, life, nonce, r.Client)
	if m.ipv6 {
		if errors.Is(err, errUnsupported) {
			got, err = ipv6Pinhole(ctx, r, internal, life, held)
		}
		if err == nil && (!got.externalIP.Is6() || got.externalIP.Is4In6()) {
			err = errors.New("the router did not return an IPv6 address")
		}
		got.router = r
		return got, err
	}
	if errors.Is(err, errUnsupported) {
		got, err = natPMP(ctx, pmp, internal, external, life)
	}
	if errors.Is(err, errUnsupported) {
		local, lerr := localAddrTowards(r.Gateway)
		if lerr != nil {
			return mapping{}, errNoRouter
		}
		got, err = upnp(ctx, r.SSDP, r.Gateway, local, internal, external, life)
	}
	got.router = r
	return got, err
}

// remove asks the router to close what it opened, with the protocol it opened it with.
func (m *Mapper) remove(ctx context.Context) {
	m.mu.Lock()
	held := m.held
	m.held = nil
	m.mu.Unlock()
	if held == nil {
		return
	}
	r := held.router
	if !r.Gateway.IsValid() {
		r = m.router()
	}
	if !r.Gateway.IsValid() {
		return
	}
	pmp := netip.AddrPortFrom(r.Gateway, uint16(r.PMPPort))
	var err error
	switch held.protocol {
	case "PCP":
		_, err = pcp(ctx, pmp, held.internal, held.externalPort, 0, held.nonce, r.Client)
	case "NAT-PMP":
		_, err = natPMP(ctx, pmp, held.internal, 0, 0)
	case "UPnP":
		local, _ := localAddrTowards(r.Gateway)
		_, err = upnp(ctx, r.SSDP, r.Gateway, local, held.internal, held.externalPort, 0)
	case "UPnP IPv6":
		_, err = ipv6Pinhole(ctx, r, held.internal, 0, held)
	}
	if err != nil {
		m.log.Warn("the router did not close the server's port; it closes by itself when its time runs out", "error", err)
	}
}

// DefaultRouter is the gateway behind this machine's default IPv4 route.
func DefaultRouter() Router { return defaultRouter() }
