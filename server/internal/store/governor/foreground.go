package governor

import (
	"context"
	"errors"
	"time"
)

const ForegroundRequestBytes int64 = 20 << 20

// ForegroundProfile is supplied by trusted whole-server composition. Assigned
// and Effective are configured bounds, not attestations of the host. New checks
// their minimum against hostlimits independently. NonBrowse must include normal
// TMDB for THAT profile even when its smaller fallback is active.
// No defaults select a qualified profile; payload reservations remain separate.
// Budget/profile changes require a new validated composition after draining;
// changing a caller-owned pointer cannot resize existing live custody.
type ForegroundProfile struct {
	AssignedBytes, EffectiveBytes, NonBrowseBytes int64 // NonBrowse is B_cap, INCLUDING protected profile contingency
	MaxWaiters                                    int
	MinimumLive                                   int // owner-adopted funded floor for THIS profile (not a global cap)
	BookkeepingBytes                              int64
	MaxWait                                       time.Duration
}

// ForegroundBudgetReason is a typed configuration refusal, not a runtime
// override or authority to erase an owner reservation to fit a smaller host.
type ForegroundBudgetReason uint8

const (
	ForegroundInvalidBudget ForegroundBudgetReason = iota + 1
	ForegroundEffectiveLimitExceeded
	ForegroundUnfundedBudget
)

type ForegroundBudgetError struct {
	Reason                          ForegroundBudgetReason
	ConfiguredBytes, EffectiveBytes int64
}

func (e *ForegroundBudgetError) Error() string {
	switch e.Reason {
	case ForegroundEffectiveLimitExceeded:
		return "foreground profile exceeds known effective server memory"
	case ForegroundUnfundedBudget:
		return "foreground server memory cannot fund the selected profile"
	default:
		return "invalid foreground server memory profile"
	}
}

// Capacity is arithmetic with an UNKNOWN external limit. It proves no budget
// provenance. New always supplies hostlimits.EffectiveMemoryBytes separately.
func (p ForegroundProfile) Capacity() (int, error) { return p.CapacityAtLimit(0) }

// CapacityAtLimit is PURE: knownLimit is the observed hostlimits source; <=0
// means unknown and uses configured M. Effective M=min(configured,knownLimit),
// but a known smaller limit REFUSES the oversized profile, rather than silently
// changing an owner-approved profile. The caller must explicitly pass the min
// and a funded profile. No runtime settings, adapter or default are installed.
func (p ForegroundProfile) CapacityAtLimit(knownLimit int64) (int, error) {
	configured := min(p.AssignedBytes, p.EffectiveBytes)
	m := configured
	if knownLimit > 0 {
		m = min(m, knownLimit)
	}
	refuse := func(reason ForegroundBudgetReason) (int, error) {
		return 0, &ForegroundBudgetError{Reason: reason, ConfiguredBytes: configured, EffectiveBytes: m}
	}
	if configured <= 0 || p.NonBrowseBytes <= 0 || p.MinimumLive < 1 {
		return refuse(ForegroundInvalidBudget)
	}
	if configured > m {
		return refuse(ForegroundEffectiveLimitExceeded)
	}
	if p.NonBrowseBytes >= m {
		return refuse(ForegroundUnfundedBudget)
	}
	reserve := m / 5
	if m%5 != 0 {
		reserve++
	}
	cap := (m - p.NonBrowseBytes - reserve) / ForegroundRequestBytes
	if cap < int64(p.MinimumLive) || cap > int64(^uint(0)>>1) {
		return refuse(ForegroundUnfundedBudget)
	}
	return int(cap), nil
}

type ForegroundRequest struct {
	Key                              JobKey
	Owner                            OwnerID
	ReadyDeadline, ProtectedDeadline time.Time // ORIGINAL absolutes, never renewed after waiting
}

type foregroundHolding struct {
	readyDeadline time.Time
	waitUntil     time.Time
	wake          chan struct{}
	waiting       bool
	signalled     bool
}
type foregroundLedger struct {
	profile       ForegroundProfile // immutable copy; no caller-owned pointer
	capacity      int
	live, waiters int
}

func configureForeground(c Config, knownLimit int64) (foregroundLedger, error) {
	if c.Foreground == nil {
		return foregroundLedger{}, nil
	}
	p := *c.Foreground
	n, err := p.CapacityAtLimit(knownLimit)
	if err != nil {
		return foregroundLedger{}, err
	}
	// Three holdings per active request (live descriptor, read, output) plus
	// finite queued descriptors must fit existing grant metadata. Other routes
	// share these resources; this does not create more grants or worker slots.
	if p.MaxWaiters < 0 || p.MaxWaiters > 4096 || n > (c.MaxGrantsPerClass-p.MaxWaiters)/3 ||
		p.BookkeepingBytes < 4096 || p.BookkeepingBytes > (64<<20)/int64(n+p.MaxWaiters) ||
		p.MaxWait <= 0 || p.MaxWait > 100*time.Millisecond {
		return foregroundLedger{}, errors.New("invalid bounded foreground bookkeeping profile")
	}
	metadata := int64(n+p.MaxWaiters) * p.BookkeepingBytes
	if c.Partitions[Foreground][MemoryBytes] < int64(n)*ForegroundRequestBytes+metadata || c.Partitions[Foreground][QueueBytes] < metadata {
		return foregroundLedger{}, errors.New("foreground resource partition underfunded")
	}
	return foregroundLedger{profile: p, capacity: n}, nil
}

func (g *ledger) foregroundRequest(q ForegroundRequest) Request {
	return Request{Key: q.Key, Owner: q.Owner, Class: Foreground,
		Need:     Amounts{MemoryBytes: g.foreground.profile.BookkeepingBytes, QueueBytes: g.foreground.profile.BookkeepingBytes},
		Deadline: q.ProtectedDeadline}
}

// AdmitForeground reserves ONE LIVE request through actual output AND consumer
// release. The returned ordinary Grant covers bookkeeping ONLY, never page,
// native, output or encoding bytes. RD retains its separate read/output grants.
// Only trusted composition may call Retired after ALL physical work/consumers
// release. Cancel/expiry/native-window closure cannot return this live slot.
// Other foreground routes share this owner until separately budgeted.
func (a *Authority) AdmitForeground(ctx context.Context, q ForegroundRequest) Decision {
	g := a.ledger
	now := g.config.Now()
	refuse := func(code RefusalCode) Decision {
		return Decision{Refusal: &Refusal{Code: code, RetryAt: now}}
	}
	if g.foreground.capacity == 0 || q.ReadyDeadline.IsZero() || q.ProtectedDeadline.IsZero() {
		return refuse(Unqualified)
	}
	until := minTime(minTime(q.ReadyDeadline, q.ProtectedDeadline), now.Add(g.foreground.profile.MaxWait))
	if deadline, ok := ctx.Deadline(); ok {
		until = minTime(until, deadline)
	}
	if ctx.Err() != nil || !until.After(now) {
		return refuse(Deadline)
	}
	f := &foregroundHolding{readyDeadline: q.ReadyDeadline, waitUntil: until}
	d := g.tryMode(g.foregroundRequest(q), false, nil, f)
	if d.Refusal != nil {
		return d
	}
	handedOff := false
	defer func() {
		if handedOff {
			return
		}
		primary := recover()
		rollback := func() { g.Cancel(d.Grant); _ = a.Retired(d.Grant) }
		if primary == nil {
			rollback()
			return
		}
		// Metadata only: no payload/consumer has escaped. A clock panic during
		// promotion after retirement must not replace the original callback fault.
		func() { defer func() { _ = recover() }(); rollback() }()
		panic(primary)
	}()
	// wake is immutable after admission. Even immediate admission is completed
	// under the same boundary checks, so cancellation cannot leak a live slot.
	if f.wake != nil {
		g.foregroundWait(ctx, until, f.wake)
	}
	admitted := func() bool {
		g.mu.Lock()
		defer g.mu.Unlock()
		h := g.find(d.Grant)
		return h != nil && !h.foreground.waiting && !d.Grant.cancelled.Load() && d.Grant.epoch == g.state.Load() &&
			ctx.Err() == nil && g.config.Now().Before(until)
	}()
	if !admitted {
		// No page/consumer has ever escaped: this call can prove metadata-only
		// physical retirement. This never refunds an already returned LIVE grant.
		return refuse(Deadline)
	}
	handedOff = true
	return d
}

func (g *ledger) waitForeground(ctx context.Context, until time.Time, wake <-chan struct{}) {
	timer := time.NewTimer(max(0, until.Sub(g.config.Now())))
	defer timer.Stop()
	select {
	case <-wake:
	case <-ctx.Done():
	case <-timer.C:
	}
}

// CheckForeground is exact descriptor/lifetime validation, not payload
// authority. The caller must carry ReadyDeadline into actual route execution;
// admission does not restart the ready budget or qualify the <=1s ceiling.
func (a *Authority) CheckForeground(p *Grant, q ForegroundRequest) error {
	return a.CheckForegroundContext(context.Background(), p, q)
}

// CheckForegroundContext preserves exact LIVE binding and observes earlier
// protected-operation cancellation. It never renews either original deadline.
func (a *Authority) CheckForegroundContext(ctx context.Context, p *Grant, q ForegroundRequest) error {
	if a.ledger.foreground.capacity == 0 || q.ReadyDeadline.IsZero() {
		return ErrGrant
	}
	return a.checkContext(ctx, p, a.ledger.foregroundRequest(q), q.ReadyDeadline)
}

type ForegroundUsage struct {
	Capacity, Live, Waiters int
	BookkeepingBytes        int64
}

func (a *Authority) ForegroundUsage() ForegroundUsage {
	g := a.ledger
	g.mu.Lock()
	defer g.mu.Unlock()
	f := g.foreground
	return ForegroundUsage{f.capacity, f.live, f.waiters, int64(f.live+f.waiters) * f.profile.BookkeepingBytes}
}

// Same holding map is the only wait/live ownership record. FIFO is grant ID;
// no external queue, second semaphore or new grant issuer. Caller holds g.mu.
func (g *ledger) promoteForeground(now time.Time) {
	for g.foreground.live < g.foreground.capacity {
		var first *holding
		for _, h := range g.live {
			f := h.foreground
			if f == nil || !f.waiting {
				continue
			}
			if h.grant.cancelled.Load() || h.grant.epoch != g.state.Load() || !now.Before(f.waitUntil) {
				if !f.signalled {
					close(f.wake)
					f.signalled = true
				}
				continue
			}
			if first == nil || h.grant.id < first.grant.id {
				first = h
			}
		}
		if first == nil {
			return
		}
		first.foreground.waiting = false
		g.foreground.waiters--
		g.foreground.live++
		close(first.foreground.wake)
		first.foreground.signalled = true
	}
}
