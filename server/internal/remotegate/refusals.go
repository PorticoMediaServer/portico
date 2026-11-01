package remotegate

import (
	"net/netip"
	"sync"
	"time"
)

// Refused-connection reasons beyond the four a name can carry: ReasonIP is a
// client that asked for no name at all (an IP address in Host, or TLS without
// SNI), which is what scanners do; ReasonAccess is any connection from outside
// while remote access is Off or Private only (or before settings load).
const (
	ReasonIP     = "ip"
	ReasonAccess = "access"
)

// RefusalReasons lists every reason Refusals counts, in display order.
var RefusalReasons = []string{ReasonName, ReasonHTTP, ReasonCertificate, ReasonProxy, ReasonIP, ReasonAccess}

const refusalHours = 7 * 24

// Refusals counts connections from outside the home network that the gate
// refused, by reason, in hourly buckets for a week. Counts live in memory only:
// a refusal never writes to the database, so a flood costs a mutex and an add,
// and a restart starts the week again.
type Refusals struct {
	mu    sync.Mutex
	hours [refusalHours]refusalHour
	now   func() time.Time
}

type refusalHour struct {
	hour   int64
	counts [6]int64
}

func NewRefusals() *Refusals { return &Refusals{now: time.Now} }

func reasonIndex(reason string) int {
	for i, r := range RefusalReasons {
		if r == reason {
			return i
		}
	}
	return -1
}

// Count adds one refusal; an unknown reason counts nothing.
func (r *Refusals) Count(reason string) {
	i := reasonIndex(reason)
	if r == nil || i < 0 {
		return
	}
	hour := r.now().Unix() / 3600
	r.mu.Lock()
	b := &r.hours[hour%refusalHours]
	if b.hour != hour {
		*b = refusalHour{hour: hour}
	}
	b.counts[i]++
	r.mu.Unlock()
}

// Totals sums the last `hours` whole and current hours, by reason; every
// reason is present, zero when nothing was refused for it.
func (r *Refusals) Totals(hours int) (total int64, byReason map[string]int64) {
	byReason = make(map[string]int64, len(RefusalReasons))
	for _, reason := range RefusalReasons {
		byReason[reason] = 0
	}
	if r == nil {
		return 0, byReason
	}
	if hours > refusalHours {
		hours = refusalHours
	}
	now := r.now().Unix() / 3600
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, b := range r.hours {
		if b.hour > now-int64(hours) && b.hour <= now {
			for i, n := range b.counts {
				byReason[RefusalReasons[i]] += n
				total += n
			}
		}
	}
	return total, byReason
}

// NameReason is the reason to count for a refusal that carried this Host:
// ReasonIP when no name was asked for, otherwise the gate's own reason.
func NameReason(host, reason string) string {
	name := HostName(host)
	if name == "" {
		return ReasonIP
	}
	if _, err := netip.ParseAddr(name); err == nil {
		return ReasonIP
	}
	return reason
}
