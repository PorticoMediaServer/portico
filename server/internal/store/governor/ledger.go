package governor

import (
	"context"
	"errors"
	"math"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/hostlimits"
)

const (
	maxDevices     = 8
	maxScopes      = 16
	maxKeyBytes    = 1024
	maxDeviceBytes = 128
)

var (
	ErrBusy       = errors.New("governor busy")
	ErrGrant      = errors.New("invalid or revoked governor grant")
	ErrRetirement = errors.New("unknown or already retired physical holding")
	ErrIO         = errors.New("unknown or already completed device IO")
)

// Config assigns conservative, disjoint, nonloanable class partitions. These are
// resource capacities, not a service/fairness schedule or hardware qualification.
// Now must be a bounded monotonic clock, safe for concurrent calls.
type Config struct {
	Foreground  *ForegroundProfile  // optional trusted whole-server LIVE admission profile
	Preparation *PreparationProfile // opt-in phase2 only; nil preserves phase1a
	Partitions  [4]Amounts
	// MaxGrantsPerClass bounds retained metadata independently for each class,
	// so background descriptors cannot exhaust protected classes.
	MaxGrantsPerClass int
	Now               func() time.Time
	// WAL is required by the serving writer composition. Nil permits pure
	// resource-ledger simulations only; it makes no WAL-BOUND claim.
	WAL *WALGate
}

type holding struct {
	grant      *Grant
	request    Request
	foreground *foregroundHolding
	checkpoint bool       // private native checkpoint turn; never selected by workers
	snapshot   *ReadScope // trusted physical read path; memory can outlive native snapshot
	keeper     *CheckpointKeeper
	keeperRole keeperRole
}

type ledger struct {
	resetOwner     *Checkpointer
	changed        chan struct{}     // physical retirement notification; one bounded recovery waiter
	keeper         *CheckpointKeeper // protected by mu; reserves its shutdown floor
	preparation    preparationLedger
	foreground     foregroundLedger
	foregroundWait func(context.Context, time.Time, <-chan struct{})
	mu             ledgerMutex
	config         Config
	used           [4]Amounts
	counts         [4]int
	live           map[uint64]*holding
	sequence       uint64
	// Low bit is static mode; remaining bits are the epoch. Fencing does not
	// acquire the admission mutex and never clears physical debt.
	state atomic.Uint64
}

// Authority is a driver/supervisor capability. Keep it out of worker interfaces:
// a worker cannot attest that its own native operation or helper has exited.
type Authority struct{ ledger *ledger }

// New returns separate admission and driver capabilities. No defaults imply a
// qualified hardware envelope. In particular, zero capacity admits no resource.
func New(c Config) (Governor, *Authority, error) {
	var knownLimit int64
	if c.Foreground != nil {
		knownLimit = hostlimits.EffectiveMemoryBytes()
	}
	return newGovernor(c, knownLimit)
}

// Pure construction from an explicit source observation; production New owns
// the hostlimits read. Tests can supply known/unknown limits without changing
// process/cgroup/runtime settings or introducing a mutable global hook.
func newGovernor(c Config, knownLimit int64) (Governor, *Authority, error) {
	if c.Now == nil || c.MaxGrantsPerClass < 1 || c.MaxGrantsPerClass > 4096 {
		return nil, nil, errors.New("invalid governor configuration")
	}
	var total Amounts
	for _, partition := range c.Partitions {
		for k, n := range partition {
			if n < 0 || n > math.MaxInt64-total[k] {
				return nil, nil, errors.New("invalid governor partition")
			}
			total[k] += n
		}
	}
	foreground, err := configureForeground(c, knownLimit)
	if err != nil {
		return nil, nil, err
	}
	preparation, err := configurePreparation(c)
	if err != nil {
		return nil, nil, err
	}
	c.Preparation = nil // retain only the validated immutable profile
	c.Foreground = nil  // do not retain a mutable caller-owned profile pointer
	g := &ledger{preparation: preparation, foreground: foreground, config: c, live: make(map[uint64]*holding, 4*c.MaxGrantsPerClass)}
	g.foregroundWait = g.waitForeground
	g.state.Store(2)
	return g, &Authority{g}, nil
}

func (g *ledger) Try(r Request) Decision { return g.try(r, false, nil) }

// tryCheckpoint is reserved for the governor's private checkpoint driver. The
// ordinary worker/admission interface cannot select a checkpoint turn.
func (a *Authority) tryCheckpoint(r Request) Decision { return a.ledger.try(r, true, nil) }

func (g *ledger) try(r Request, checkpoint bool, ticket *checkpointTicket) Decision {
	return g.tryMode(r, checkpoint, ticket, nil)
}
func (g *ledger) tryMode(r Request, checkpoint bool, ticket *checkpointTicket, foreground *foregroundHolding) Decision {
	return g.tryReserved(r, checkpoint, ticket, foreground, nil, nil)
}

func (g *ledger) tryReserved(r Request, checkpoint bool, ticket *checkpointTicket, foreground *foregroundHolding, shutdown *CheckpointKeeper, read *ReadScope) Decision {
	now := g.config.Now()
	refuse := func(code RefusalCode, cause RefusalCause) Decision {
		retryAt := now.Add(100 * time.Millisecond)
		if foreground != nil {
			retryAt = minTime(retryAt, foreground.waitUntil)
		}
		return Decision{Refusal: &Refusal{Code: code, Cause: cause, RetryAt: retryAt}}
	}
	if !validRequest(r) || (r.RestartRisk != (WALRestartRisk{}) && (!r.Writer || g.config.WAL == nil)) {
		return refuse(Invalid, CauseInvalidRequest)
	}
	if !r.Deadline.After(now) {
		return refuse(Deadline, CauseDeadline)
	}
	if (r.Writer || checkpoint) && (r.MaxHold <= 0 || r.MaxHold > g.maxHold(checkpoint) ||
		r.Budget.RawRows == 0 || r.Budget.VMOps == 0 || r.Budget.Pages == 0 || (r.Writer && r.Need[WALBytes] == 0)) {
		return refuse(Unqualified, CauseUnqualified)
	}
	if checkpoint && (r.Writer || r.Class != Recovery || g.config.WAL == nil ||
		r.Need[Reader] != 1 || r.Need[MemoryBytes] <= 0 || r.Need[CPUMilli] <= 0 || r.Need[IOBytes] <= 0 || r.Need[WALBytes] != 0) {
		return refuse(Unqualified, CauseUnqualified)
	}
	if ticket != nil && ticket.owner != g {
		return refuse(Invalid, CauseInvalidRequest)
	}
	// Contention consumes the original deadline; it is never false capacity.
	contended := !g.mu.TryLock()
	if contended {
		if err := g.mu.lockUntil(context.Background(), r.Deadline, now); err != nil {
			return refuse(Deadline, CauseDeadline)
		}
	}
	defer g.mu.Unlock()
	if contended {
		now = g.config.Now()
	}
	if !r.Deadline.After(now) {
		return refuse(Deadline, CauseDeadline)
	}
	if shutdown != nil && (g.keeper != shutdown || !shutdown.shutdownShape(r)) {
		return refuse(Invalid, CauseInvalidRequest)
	}
	if g.resetOwner != nil && (r.Writer || r.Need[Reader] != 0) && !(checkpoint && ticket != nil && ticket.recovery && ticket.owner == g) {
		return refuse(Capacity, CauseRecoveryDrain)
	}
	state := g.state.Load()
	if state&1 != 0 && r.Class == Background {
		return refuse(FailSafe, CauseStatic)
	}
	limit := g.config.MaxGrantsPerClass
	if shutdown == nil && r.Class == Recovery && g.keeper != nil && g.keeper.flags.Load()&keeperClosed == 0 {
		limit--
	}
	if g.counts[r.Class] >= limit || g.sequence == math.MaxUint64 {
		return refuse(Capacity, CauseGrantCapacity)
	}
	for k, n := range r.Need {
		available := g.config.Partitions[r.Class][k] - g.used[r.Class][k]
		if shutdown == nil && r.Class == Recovery && g.keeper != nil && g.keeper.flags.Load()&keeperClosed == 0 {
			available -= g.keeper.resources.StartupRequest.Need[k]
		}
		if n > available {
			return refuse(Capacity, CausePartition)
		}
	}
	for _, h := range g.live {
		if r.Writer && h.request.Writer {
			return refuse(Capacity, CauseWriterActive)
		}
		for _, want := range r.IO {
			for _, held := range h.request.IO {
				if want.DeviceID == held.DeviceID {
					// Finish the SAME quarantined native CP, under its still-held
					// device custody plus new reserved shutdown bytes. No second stream.
					if shutdown != nil && h.checkpoint && shutdown.cp.pending != nil && shutdown.cp.pending.grant == h.grant {
						continue
					}
					if g.config.WAL != nil && g.config.WAL.policy.ConcurrentPassive && pairedCheckpointIO(r, checkpoint, h) {
						continue
					}
					return refuse(Capacity, CauseDeviceActive)
				}
			}
		}
	}
	if code := g.preparationPublicationCode(r, now); code != 0 {
		return refuse(code, CausePreparation)
	}
	if r.Writer && g.config.WAL != nil {
		if code, cause := g.config.WAL.reserveForLedger(r, now, g); code != 0 {
			return refuse(code, cause)
		}
	}
	if checkpoint {
		if code, cause := g.config.WAL.reserveCheckpoint(ticket, now, r.Deadline); code != 0 {
			return refuse(code, cause)
		}
	}
	if foreground != nil {
		if !now.Before(foreground.waitUntil) {
			return refuse(Deadline, CauseDeadline)
		}
		g.promoteForeground(now)
		foreground.waiting = g.foreground.live == g.foreground.capacity
		if foreground.waiting && g.foreground.waiters == g.foreground.profile.MaxWaiters {
			return refuse(Capacity, CauseGrantCapacity)
		}
		if foreground.waiting {
			foreground.wake = make(chan struct{})
		}
	}
	g.preparationPublicationAdmitted(r)
	// Only admitted, bounded metadata is copied. Work buffers are allocated by
	// the driver after Check, under the granted byte custody.
	r.Key.ID = strings.Clone(r.Key.ID)
	r.IO = slices.Clone(r.IO)
	for i := range r.IO {
		r.IO[i].DeviceID = strings.Clone(r.IO[i].DeviceID)
	}
	r.Footprint = slices.Clone(r.Footprint)
	g.sequence++
	p := &Grant{id: g.sequence, epoch: state, issuer: g, deadline: r.Deadline, cancelledDone: make(chan struct{})}
	g.live[p.id] = &holding{grant: p, request: r, checkpoint: checkpoint, foreground: foreground, snapshot: read}
	if shutdown != nil {
		g.live[p.id].keeper, g.live[p.id].keeperRole = shutdown, keeperShutdown
	}
	if foreground != nil {
		if foreground.waiting {
			g.foreground.waiters++
		} else {
			g.foreground.live++
		}
	}
	g.counts[r.Class]++
	for k, n := range r.Need {
		g.used[r.Class][k] += n
	}
	return Decision{Grant: p}
}

func validRequest(r Request) bool {
	if r.Class > Background || r.Owner == 0 || len(r.Key.ID) > maxKeyBytes ||
		len(r.IO) > maxDevices || len(r.Footprint) > maxScopes {
		return false
	}
	for _, n := range r.Need {
		if n < 0 {
			return false
		}
	}
	var ioBytes int64
	for i, io := range r.IO {
		if io.DeviceID == "" || len(io.DeviceID) > maxDeviceBytes || io.Class > NetworkIO ||
			io.Outstanding != 1 || io.Bytes <= 0 || io.Bytes > r.Need[IOBytes]-ioBytes {
			return false
		}
		ioBytes += io.Bytes
		for _, previous := range r.IO[:i] {
			if previous.DeviceID == io.DeviceID {
				return false
			}
		}
	}
	return true
}

func (g *ledger) Cancel(p *Grant) {
	if p != nil && p.issuer == g && p.cancelled.CompareAndSwap(false, true) {
		if p.cancelledDone != nil {
			close(p.cancelledDone)
		}
	}
}

// Check verifies exact remaining authority at a private backend entry point.
// Admitted validation waits within the issuer-captured original deadline;
// admission also waits within its original deadline. Contention is not refusal.
// The expected request must come from a closed purpose registry, not a worker's
// priority string. Cancellation/fencing during a running operation revokes new
// operations; it cannot preempt an fsync or refund that operation's custody.
func (a *Authority) Check(p *Grant, expected Request) error {
	return a.check(p, expected, time.Time{})
}

// CheckContext additionally observes an earlier operation cancellation/deadline.
// Neither context nor a caller request can extend the issuer's grant deadline.
func (a *Authority) CheckContext(ctx context.Context, p *Grant, expected Request) error {
	return a.checkContext(ctx, p, expected, time.Time{})
}
func (a *Authority) check(p *Grant, expected Request, readyDeadline time.Time) error {
	return a.checkContext(context.Background(), p, expected, readyDeadline)
}
func (a *Authority) checkContext(ctx context.Context, p *Grant, expected Request, readyDeadline time.Time) error {
	return a.checkContextUntil(ctx, p, expected, readyDeadline, time.Time{})
}
func (a *Authority) checkContextUntil(ctx context.Context, p *Grant, expected Request, readyDeadline, until time.Time) error {
	g := a.ledger
	if ctx == nil || p == nil || p.issuer != g || p.cancelled.Load() || p.epoch != g.state.Load() {
		return ErrGrant
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if !g.mu.TryLock() {
		// This is already admitted authority, not a new capacity request.
		// Immutable issuer-captured deadline prevents a caller from extending it.
		deadline := p.deadline
		if !until.IsZero() {
			deadline = minTime(deadline, until)
		}
		if err := g.mu.lockWithin(deadline.Sub(g.config.Now()), p.cancelledDone, ctx); err != nil {
			if err == ErrGrant && !until.IsZero() && !p.cancelled.Load() {
				return context.DeadlineExceeded
			}
			return err
		}
	}
	defer g.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if !until.IsZero() && !until.After(g.config.Now()) {
		return context.DeadlineExceeded
	}
	h := g.find(p)
	if h == nil || p.cancelled.Load() || p.epoch != g.state.Load() ||
		!h.request.Deadline.After(g.config.Now()) || !sameRequest(h.request, expected) ||
		(!readyDeadline.IsZero() && (h.foreground == nil || h.foreground.waiting || !h.foreground.readyDeadline.Equal(readyDeadline))) {
		return ErrGrant
	}
	return nil
}

func sameRequest(x, y Request) bool {
	return x.Key == y.Key && x.Owner == y.Owner && x.Class == y.Class && x.Need == y.Need &&
		x.RestartRisk == y.RestartRisk && x.Writer == y.Writer && x.Budget == y.Budget && x.MaxHold == y.MaxHold &&
		x.Deadline.Equal(y.Deadline) && slices.Equal(x.IO, y.IO) && slices.Equal(x.Footprint, y.Footprint)
}

func (g *ledger) find(p *Grant) *holding {
	if p == nil || p.issuer != g {
		return nil
	}
	h := g.live[p.id]
	if h == nil || h.grant != p {
		return nil
	}
	return h
}

// Retired requires physical close/Wait/return of every remaining resource. Old
// epochs may retire their exact holding, but cannot authorize new work.
func (a *Authority) Retired(p *Grant) error {
	g := a.ledger
	g.mu.Lock()
	defer g.mu.Unlock()
	h := g.find(p)
	if h == nil {
		return ErrRetirement
	}
	if h.keeper != nil && !h.keeper.canRetire(h.keeperRole) {
		return ErrRetirement
	}
	if h.request.Writer && g.config.WAL != nil {
		g.config.WAL.returned()
	}
	if h.checkpoint {
		g.config.WAL.checkpointReturned()
	}
	for k, n := range h.request.Need {
		g.used[h.request.Class][k] -= n
	}
	g.counts[h.request.Class]--
	delete(g.live, p.id)
	if g.changed != nil {
		close(g.changed)
		g.changed = nil
	}
	if h.foreground != nil {
		if h.foreground.waiting {
			g.foreground.waiters--
		} else {
			g.foreground.live--
		}
		g.promoteForeground(g.config.Now())
	}
	return nil
}

// IOCompleted is for the trusted IO broker only, after every read capability for
// this device has closed/joined. It releases no helper, memory or CPU custody.
// Until a broker can prove that seam, retain IO through whole-helper Wait.
func (a *Authority) IOCompleted(p *Grant, device string) error {
	g := a.ledger
	g.mu.Lock()
	defer g.mu.Unlock()
	h := g.find(p)
	if h == nil {
		return ErrIO
	}
	for i, io := range h.request.IO {
		if io.DeviceID != device {
			continue
		}
		h.request.IO = slices.Delete(h.request.IO, i, i+1)
		h.request.Need[IOBytes] -= io.Bytes
		g.used[h.request.Class][IOBytes] -= io.Bytes
		return nil
	}
	return ErrIO
}

// EnterStatic independently fences backend entry without waiting for the
// admission mutex. Physical holdings remain charged. This is an epoch primitive,
// not a claim that the physical inventory/watchdog integration is complete.
func (a *Authority) EnterStatic() {
	for {
		old := a.ledger.state.Load()
		if a.ledger.state.CompareAndSwap(old, (old+2)|1) {
			return
		}
	}
}

// Recover uses the conservative all-holdings-retired condition. Real supervisor
// recovery additionally requires independently verified driver/WAL health.
func (a *Authority) Recover() bool {
	g := a.ledger
	g.mu.Lock()
	defer g.mu.Unlock()
	old := g.state.Load()
	return old&1 != 0 && len(g.live) == 0 && g.state.CompareAndSwap(old, (old+2)&^1)
}

// Usage is a diagnostic snapshot, including cancelled, expired and old epochs.
func (a *Authority) Usage() [4]Amounts {
	a.ledger.mu.Lock()
	defer a.ledger.mu.Unlock()
	return a.ledger.used
}

// Concurrent maintenance has a distinct contract; ordinary W remains <=25ms.
func (g *ledger) maxHold(checkpoint bool) time.Duration {
	if checkpoint && g.config.WAL != nil && g.config.WAL.policy.ConcurrentPassive {
		return g.config.WAL.policy.CheckpointMaxHold
	}
	return 25 * time.Millisecond
}
func pairedCheckpointIO(r Request, checkpoint bool, h *holding) bool {
	// Only the private CP with W or bounded foreground database reads. Never
	// arbitrary scan/helper IO, CP/CP, or a second W stream.
	if checkpoint == h.checkpoint {
		return false
	}
	other := r
	if checkpoint {
		other = h.request
	}
	return other.Need[Helper] == 0 && (other.Writer || (other.Class == Foreground && other.Need[Reader] > 0))
}

// The read path installs its native custody witness atomically with admission.
// It conveys no additional grant/SQL authority and never releases payload bytes.
func (a *Authority) trySnapshot(r Request, scope *ReadScope) Decision {
	return a.ledger.tryReserved(r, false, nil, nil, nil, scope)
}
func (a *Authority) snapshotPhysicallySafe(scope *ReadScope) {
	scope.nativeSafe.Store(true)
	g := a.ledger
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.changed != nil {
		close(g.changed)
		g.changed = nil
	}
}
