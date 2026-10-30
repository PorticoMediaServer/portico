package httpapi

import (
	"errors"
	"net/http"
	"net/netip"
	"github.com/PorticoMediaServer/portico/server/internal/networkpolicy"
	"sort"
	"strings"
	"sync"
	"time"
)

// The sign-in limiter is the one piece of admission that a single client could
// turn into a refusal for everybody else. Its table was bounded at 1,024 entries
// and, once full of entries that were not yet stale, it answered `false` — which
// is to say: it stopped every sign-in on the server, for everyone, until the
// minute rolled over. A client that can make the server refuse other clients is
// the thing this whole workstream exists to prevent, and a limiter is a
// particularly bad place for it, because the flood that fills the table is
// exactly the situation in which honest people are trying to sign in.
//
// So the table is never a reason to refuse. It is bounded by eviction — stale
// windows first, then the least recently seen entry — and the limit itself is
// counted at two breadths:
//
//   - the exact client address, at the same twenty attempts a minute it has
//     always been, so nothing a real client does changes;
//   - the client's network, far more generously, so that an attacker who has a
//     /64 of IPv6 addresses (which is every IPv6 customer) cannot evade the
//     first count simply by using a new address for every attempt.
//
// The network ceiling is deliberately sixty-four times the per-address one. Two
// hundred people behind one home router signing in after a restart is about two
// hundred attempts a minute between them; a rotation flood is thousands. The gap
// is wide enough that the first is never touched and narrow enough that the
// second is stopped.
const (
	rateAttemptsPerAddress = 20
	rateAttemptsPerNetwork = rateAttemptsPerAddress * 64
	rateAddressEntries     = 8192
	rateNetworkEntries     = 1024
)

type rateEntry struct {
	window time.Time
	seen   time.Time
	count  int
}

type limiter struct {
	mu             sync.Mutex
	entries        map[string]rateEntry
	networks       map[string]rateEntry
	trustedProxies []netip.Prefix
	// proxies, when set, is the live trusted-proxy list (environment plus the
	// trustedProxies setting); trustedProxies is the fixed list tests use.
	proxies func(*http.Request) []netip.Prefix
}

func (l *limiter) allow(r *http.Request) bool {
	trusted := l.trustedProxies
	if l.proxies != nil {
		trusted = l.proxies(r)
	}
	address := clientAddress(r, trusted)
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.entries == nil {
		l.entries = map[string]rateEntry{}
		l.networks = map[string]rateEntry{}
	}
	// Both counts are tested before either is charged, so a request refused by
	// one does not inflate the other.
	if !countable(l.entries, address+":"+r.URL.Path, now, rateAttemptsPerAddress) {
		return false
	}
	if !countable(l.networks, clientNetwork(address)+":"+r.URL.Path, now, rateAttemptsPerNetwork) {
		return false
	}
	charge(l.entries, address+":"+r.URL.Path, now, rateAddressEntries)
	charge(l.networks, clientNetwork(address)+":"+r.URL.Path, now, rateNetworkEntries)
	return true
}

// countable reports whether one more attempt fits in the key's current minute.
func countable(table map[string]rateEntry, key string, now time.Time, ceiling int) bool {
	v, ok := table[key]
	if !ok || now.Sub(v.window) > time.Minute {
		return true
	}
	return v.count < ceiling
}

// charge records the attempt and keeps the table inside its bound. Eviction
// never refuses: a full table drops the entries whose window has already
// expired, and if that frees nothing, the one least recently seen. The worst an
// eviction can do is forgive an attacker one window; refusing would have denied
// every other client instead, which is not a trade worth making.
func charge(table map[string]rateEntry, key string, now time.Time, bound int) {
	v := table[key]
	if now.Sub(v.window) > time.Minute {
		v = rateEntry{window: now}
	}
	v.count++
	v.seen = now
	if _, present := table[key]; !present && len(table) >= bound {
		evict(table, now, bound)
	}
	table[key] = v
}

// evict makes room. Stale windows go first — they are free to drop, because an
// entry whose minute has passed would have been reset anyway. If that frees
// nothing, an eighth of the table goes by least-recently-seen, in one pass
// rather than one scan per insertion: a flood is exactly when this runs, and a
// per-insertion scan of a full table would turn the limiter itself into the
// denial of service it exists to stop.
func evict(table map[string]rateEntry, now time.Time, bound int) {
	for k, v := range table {
		if now.Sub(v.window) > time.Minute {
			delete(table, k)
		}
	}
	if len(table) < bound {
		return
	}
	drop := max(1, bound/8)
	ages := make([]time.Time, 0, len(table))
	for _, v := range table {
		ages = append(ages, v.seen)
	}
	sort.Slice(ages, func(i, j int) bool { return ages[i].Before(ages[j]) })
	cutoff := ages[min(drop, len(ages)-1)]
	for k, v := range table {
		if len(table) < bound-drop/2 {
			break
		}
		if !v.seen.After(cutoff) {
			delete(table, k)
		}
	}
	// A table where every entry carries the same instant would survive the pass
	// above; one unconditional removal guarantees progress.
	for k := range table {
		if len(table) < bound {
			break
		}
		delete(table, k)
	}
}

// clientNetwork is the allocation an address belongs to rather than the address
// itself: a /64 for IPv6, which is what a customer is given, and a /24 for IPv4,
// which is the smallest routable block. Anything unparseable is its own network,
// which is the conservative answer.
func clientNetwork(address string) string {
	addr, err := netip.ParseAddr(address)
	if err != nil {
		return address
	}
	bits := 24
	if addr.Is6() && !addr.Is4In6() {
		bits = 64
	}
	prefix, err := addr.Unmap().Prefix(bits)
	if err != nil {
		return address
	}
	return prefix.String()
}

// ParseTrustedProxyCIDRs accepts explicit peer networks only; no forwarded
// address is trusted unless the immediate transport peer belongs to one.
func ParseTrustedProxyCIDRs(value string) ([]netip.Prefix, error) {
	if strings.TrimSpace(value) == "" {
		return nil, nil
	}
	parts := strings.Split(value, ",")
	if len(parts) > 32 {
		return nil, errors.New("too many trusted proxy networks")
	}
	result := make([]netip.Prefix, 0, len(parts))
	for _, part := range parts {
		prefix, err := netip.ParsePrefix(strings.TrimSpace(part))
		if err != nil || prefix.Bits() == 0 || prefix.Addr().Is4In6() {
			return nil, errors.New("trusted proxy CIDRs must be explicit non-global networks")
		}
		result = append(result, prefix.Masked())
	}
	return result, nil
}
func clientAddress(r *http.Request, trusted []netip.Prefix) string {
	if addr, ok := networkpolicy.Client(r, trusted); ok {
		return addr.String()
	}
	// A malformed trusted chain has no local authority. Its transport peer still
	// provides a bounded rate-limit bucket rather than an attacker-selected key.
	if addr, ok := networkpolicy.Peer(r); ok {
		return addr.String()
	}
	return "unknown"
}
