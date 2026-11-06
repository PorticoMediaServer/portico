package governor

import (
	"context"
	"errors"
	"time"
)

const ForegroundRequestBytes int64 = 20 << 20

// ForegroundProfile is supplied by trusted whole-server composition. Effective
// is the actual process/cgroup/ancestor ceiling, NOT host RAM. NonBrowse must
// include normal TMDB for THAT profile even when its smaller fallback is active.
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

// Capacity preserves ceil(20% M) without floating point or overflow. A profile
// that cannot fund the adopted minimum returns a conflict, never max(MinimumLive, cap).
func (p ForegroundProfile) Capacity() (int, error) {
	m := min(p.AssignedBytes, p.EffectiveBytes)
	if m <= 0 || p.NonBrowseBytes <= 0 || p.NonBrowseBytes >= m || p.MinimumLive < 1 {
		return 0, errors.New("foreground server-memory profile conflict")
	}
	reserve := m / 5
	if m%5 != 0 {
		reserve++
	}
	cap := (m - p.NonBrowseBytes - reserve) / ForegroundRequestBytes
	if cap < int64(p.MinimumLive) || cap > int64(^uint(0)>>1) {
		return 0, errors.New("foreground live capacity/profile conflict")
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

func configureForeground(c Config) (foregroundLedger, error) {
	if c.Foreground == nil {
		return foregroundLedger{}, nil
	}
	p := *c.Foreground
	n, err := p.Capacity()
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
	// wake is immutable after admission. Even immediate admission is completed
	// under the same boundary checks, so cancellation cannot leak a live slot.
	if f.wake != nil {
		g.foregroundWait(ctx, until, f.wake)
	}
	g.mu.Lock()
	h := g.find(d.Grant)
	admitted := h != nil && !h.foreground.waiting && !d.Grant.cancelled.Load() && d.Grant.epoch == g.state.Load() &&
		ctx.Err() == nil && g.config.Now().Before(until)
	g.mu.Unlock()
	if !admitted {
		// No page/consumer has ever escaped: this call can prove metadata-only
		// physical retirement. This never refunds an already returned LIVE grant.
		g.Cancel(d.Grant)
		_ = a.Retired(d.Grant)
		return refuse(Deadline)
	}
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
	if a.ledger.foreground.capacity == 0 || q.ReadyDeadline.IsZero() {
		return ErrGrant
	}
	return a.check(p, a.ledger.foregroundRequest(q), q.ReadyDeadline)
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
