package remotegate

import (
	"context"
	"net"
	"net/netip"
	"regexp"
	"sort"
	"sync"
	"time"
)

// Refused records the host names outside clients asked for and were refused
// in Enhanced, so the owner can see "why doesn't my domain work" and allow it
// with one tap. Only names are kept: no addresses, paths or user agents.
//
// Scanners ask for IP literals or random names; a person's own domain points at
// their server. So a name is listed only once a DNS lookup shows it resolves to
// one of this server's public addresses. Lookups never run on a connection's
// path: they are queued, at most one per name per day (a failure is cached
// too), within a small budget per minute, and never for IP literals or names
// with no dot. A flood of names therefore costs at most the bounded table.
type Refused struct {
	mu      sync.Mutex
	entries map[string]*RefusedName
	lookups map[string]lookup
	queue   chan string
	queued  map[string]bool
	dirty   bool
	resolve func(context.Context, string) ([]netip.Addr, error)
	now     func() time.Time
	perMin  int
	window  time.Time
	spent   int
}

// RefusedName is one name, as the owner sees it.
type RefusedName struct {
	Name      string    `json:"name"`
	Via       string    `json:"via"`
	FirstSeen time.Time `json:"firstSeen"`
	LastSeen  time.Time `json:"lastSeen"`
	Count     int       `json:"count"`
}

type lookup struct {
	at    time.Time
	addrs []netip.Addr
}

const (
	refusedMax       = 256
	lookupQueue      = 64
	lookupEvery      = 24 * time.Hour
	lookupsPerMinute = 10
	lookupTimeout    = 3 * time.Second
)

var dnsName = regexp.MustCompile(`^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]([a-z0-9-]{0,61}[a-z0-9])?$`)

func NewRefused() *Refused {
	resolver := net.DefaultResolver
	return &Refused{
		entries: map[string]*RefusedName{}, lookups: map[string]lookup{}, queue: make(chan string, lookupQueue), queued: map[string]bool{},
		resolve: func(ctx context.Context, name string) ([]netip.Addr, error) {
			return resolver.LookupNetIP(ctx, "ip", name)
		},
		now: time.Now, perMin: lookupsPerMinute,
	}
}

// Plausible is the syntax half of the noise filter: a real DNS name with at
// least one dot, not an IP literal and not localhost.
func Plausible(name string) bool {
	if name == "" || name == "localhost" || len(name) > 253 {
		return false
	}
	if _, err := netip.ParseAddr(name); err == nil {
		return false
	}
	return dnsName.MatchString(name)
}

// Record notes one refusal. It never blocks and never does network work.
func (r *Refused) Record(raw, via string) {
	name := HostName(raw)
	if !Plausible(name) {
		return
	}
	now := r.now()
	r.mu.Lock()
	defer r.mu.Unlock()
	e := r.entries[name]
	if e == nil {
		if len(r.entries) >= refusedMax {
			r.evictLocked()
		}
		e = &RefusedName{Name: name, Via: via, FirstSeen: now}
		r.entries[name] = e
	}
	e.LastSeen, e.Count, e.Via = now, e.Count+1, via
	r.dirty = true
	if l, ok := r.lookups[name]; (!ok || now.Sub(l.at) >= lookupEvery) && !r.queued[name] {
		select {
		case r.queue <- name:
			r.queued[name] = true
		default:
			// The queue is full: this name waits for its next refusal.
		}
	}
}

func (r *Refused) evictLocked() {
	var oldest string
	var at time.Time
	for name, e := range r.entries {
		if oldest == "" || e.LastSeen.Before(at) {
			oldest, at = name, e.LastSeen
		}
	}
	delete(r.entries, oldest)
	delete(r.lookups, oldest)
}

// Forget drops a name, after the owner allowed it.
func (r *Refused) Forget(raw string) {
	name := HostName(raw)
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.entries[name]; ok {
		delete(r.entries, name)
		r.dirty = true
	}
}

// List returns the refused names that resolve to one of this server's public
// addresses and that the owner hasn't allowed since, most recent first.
func (r *Refused) List(own []netip.Addr, allowed func(string) bool) []RefusedName {
	ours := map[netip.Addr]bool{}
	for _, a := range own {
		ours[a.Unmap()] = true
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	out := []RefusedName{}
	for name, e := range r.entries {
		if allowed != nil && allowed(name) {
			continue
		}
		matches := false
		for _, a := range r.lookups[name].addrs {
			if ours[a.Unmap()] {
				matches = true
				break
			}
		}
		if matches {
			out = append(out, *e)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].LastSeen.After(out[j].LastSeen) })
	return out
}

// Run resolves queued names until ctx ends, within the per-minute budget.
func (r *Refused) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case name := <-r.queue:
			if !r.spend() {
				// Over budget: try again next minute, unless the queue is full.
				r.mu.Lock()
				delete(r.queued, name)
				r.mu.Unlock()
				select {
				case <-ctx.Done():
					return
				case <-time.After(time.Until(r.windowEnd())):
				}
				continue
			}
			lookupCtx, cancel := context.WithTimeout(ctx, lookupTimeout)
			addrs, err := r.resolve(lookupCtx, name)
			cancel()
			if err != nil {
				addrs = nil
			}
			r.mu.Lock()
			delete(r.queued, name)
			if _, ok := r.entries[name]; ok {
				r.lookups[name] = lookup{at: r.now(), addrs: addrs}
			}
			r.mu.Unlock()
		}
	}
}

func (r *Refused) spend() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.now()
	if now.Sub(r.window) >= time.Minute {
		r.window, r.spent = now, 0
	}
	if r.spent >= r.perMin {
		return false
	}
	r.spent++
	return true
}

func (r *Refused) windowEnd() time.Time {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.window.Add(time.Minute)
}

// Snapshot and Restore persist the record across restarts; the caller saves a
// snapshot at most once a minute, only when it changed.
func (r *Refused) Snapshot() (names []RefusedName, changed bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	changed, r.dirty = r.dirty, false
	for _, e := range r.entries {
		names = append(names, *e)
	}
	return names, changed
}

func (r *Refused) Restore(names []RefusedName) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, n := range names {
		if len(r.entries) >= refusedMax {
			break
		}
		if name := HostName(n.Name); Plausible(name) {
			copy := n
			copy.Name = name
			r.entries[name] = &copy
			// Looked up again in the background, within the same budget; a
			// name that doesn't fit the queue waits for its next refusal.
			select {
			case r.queue <- name:
				r.queued[name] = true
			default:
			}
		}
	}
}
