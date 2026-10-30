package dbwork

import (
	"context"
	"errors"
	"fmt"
	"log"
	"runtime"
	"slices"
	"sync"
	"sync/atomic"
	"time"
)

// StuckWait is how long one hold may last before a waiter treats it as a leak
// rather than contention. A gated transaction should never run for anything
// near this long: the batch sizes and the rule against doing filesystem,
// process or network work inside a transaction both exist to keep holds in the
// millisecond range. It measures the current hold, not the waiter's own wait:
// background work may legitimately wait behind a long run of user writes.
const StuckWait = 20 * time.Second

// WriterQueueCapacity is the one bound on queued writer tickets, shared by every
// class; the holder is not counted. A user class may borrow any free ticket, so
// a burst of sign-ins is limited by the pool and not by a per-class share.
const WriterQueueCapacity = 99

// OwnerQueueShare is how many queued tickets one owner keeps when the pool is
// full. Below capacity an owner may queue more; dispatch still rotates between
// owners, so the extra tickets wait their turn rather than jumping the queue.
const OwnerQueueShare = 2

// A waiting maintenance write earns one rung each second, up to the
// background-media rung, and then wins ties by arrival time. A busy scan
// therefore cannot starve retention or a WAL reset. No background write is
// ever promoted past a waiting user write.
const GateAgePromotionInterval = time.Second

// ErrWriterCapacity is a write the gate refused before it began: the pool held
// WriterQueueCapacity user tickets and none of them could make room. Nothing
// was written, so the caller may retry the same request later.
var ErrWriterCapacity = errors.New("dbwork: the database writer queue is full")

// Gate preserves SQLite's one-writer physical governor while using the class
// ladder for semantic ordering. A waiting security fence is therefore selected
// before every ordinary queued mutation; a transaction that has already started
// is allowed to reach its atomic commit.
//
// The gate is an application-level lock, not a database one. It exists so
// contention is resolved in Go, where priority is expressible, instead of inside
// SQLite, where the loser only learns it lost by receiving SQLITE_BUSY.
//
// Admission is one bounded pool. User tickets (every class above background)
// count against WriterQueueCapacity. Background acquisitions defer before
// admission when users queue, or their supervisor already has two media tickets
// (one maintenance ticket), including its holder. Users borrow background slots
// first. Deferred callers retain their work and wait on the existing gate wake,
// never dropped. Only a pool full of user tickets refuses anything, and then
// it refuses an over-share owner's newest ticket, or a lower class's newest
// ticket, before it refuses the arrival.
type Gate struct {
	mu     sync.Mutex
	active bool
	grant  *ticket
	nextID uint64
	queues [len(classNames)]classQueue
	// Deferred acquisitions retain one small ticket record per waiting caller,
	// using the same owner rotor. Only admitted tickets have a global bound;
	// pending records remain proportional to producer concurrency. Neither queue
	// holds SQL connections, WAL debits or work payloads. Callers keep their work.
	deferred    [len(classNames)]classQueue
	holderOwner string
	// userQueued counts queued tickets of user classes; ownerQueued splits that
	// count by owner. Both exclude the holder.
	userQueued  int
	ownerQueued map[string]int
	notify      chan struct{}
	// maxQueueAge is the longest any waiter of a class has ever queued.
	maxQueueAge map[Class]uint64
	byClass     [len(classNames)]gateClassCounters
	holderClass Class
	heldSince   time.Time
	holderProbe bool
	// Protected by mu. Readiness acquires the real writer but cannot append
	// frames, so its acquisitions must not invalidate a checkpoint copy fence.
	writeGeneration uint64

	stuckLogged bool
	holder      string
	acquired    atomic.Uint64
	waited      atomic.Uint64
	waitNanos   atomic.Uint64
	cancelled   atomic.Uint64
	refused     atomic.Uint64
	busy        atomic.Uint64
	heldNanos   atomic.Uint64
	maxHeld     atomic.Uint64
	// holding mirrors active for readers that must not take mu: the driver's
	// check that a write on the writer connection is inside a gate hold.
	holding atomic.Bool
	// holdAverage is a moving average of hold durations in nanoseconds, under
	// mu. It turns a queue length into the wait a refused caller is told.
	holdAverage uint64

	// WAL recovery (r3 §7 with the lead errata): under hard WAL pressure the
	// checkpoint owner first holds new non-security acquisitions (security
	// fences may still take a bounded number of turns), then pauses every
	// acquisition while it proves the log is copied, and finally takes the one
	// reserved turn that may reset it. All under mu; epoch 0 means none.
	recoveryEpoch  uint64
	holdEpoch      uint64
	pauseEpoch     uint64
	holdSecurity   int
	recoveryTicket *ticket
}

// RecoverySecurityReserve is how many security-fence turns a pressure hold
// still grants before its final pause. A revocation is never kept waiting
// behind a checkpoint for long, and a stream of them cannot defer the reset
// for ever.
const RecoverySecurityReserve = 128

type gateClassCounters struct {
	acquired, queued, cancelled, refused, busy uint64
	waitNanos, heldNanos, maxHeld              uint64
}

// ticket is one queued acquisition. It holds no SQL, payload or connection:
// only its place, its owner and whether capacity took that place away.
type ticket struct {
	wal      *walRequest
	id       uint64
	class    Class
	owner    string
	since    time.Time
	refused  bool
	deferred bool
	// recovery marks the checkpoint owner's reserved turn. It is never in a
	// class queue and is the only ticket a BEGIN pause selects.
	recovery bool
}

// classQueue rotates between owners, first in first out for one owner. The
// front owner supplies the next ticket and moves to the back once served.
type classQueue struct {
	rotation []*ownerQueue
	byOwner  map[string]*ownerQueue
	count    int
}

type ownerQueue struct {
	owner   string
	tickets []*ticket
}

// stuckClock drives the stuck-hold check. Acquire backs it with a timer; the
// regression test drives it by hand so it never has to sleep for StuckWait.
type stuckClock struct {
	C     <-chan time.Time
	reset func(time.Duration)
}

// NewGate returns a gate with no holder.
func NewGate() *Gate {
	return &Gate{ownerQueued: map[string]int{}, maxQueueAge: map[Class]uint64{}, notify: make(chan struct{})}
}

// Acquire blocks until this caller owns the single write slot, ctx ends, or the
// pool refuses it with ErrWriterCapacity. The returned release is idempotent:
// calling it twice is harmless, which is what lets a deferred release sit
// alongside an explicit one on the commit path.
//
// A cancelled waiter removes itself from its queue and wakes the others, so an
// abandoned request can never hold a place in the ladder. The owner is the
// fairness key ctx carries (see WithOwner); work without one shares one queue.
func (g *Gate) Acquire(ctx context.Context, class Class) (func(), error) {
	// The stuck-hold timer is created once, not once per wake. It used to be
	// created inside the loop with a deferred Stop, so every spurious wake left
	// another timer and another deferred call alive until Acquire returned — a
	// leak that only tests could see, and precisely the kind of thing that makes
	// a long soak run report the wrong numbers.
	var stuck stuckClock
	if traceHolders() {
		timer := time.NewTimer(StuckWait)
		defer timer.Stop()
		stuck = stuckClock{C: timer.C, reset: func(d time.Duration) { timer.Reset(d) }}
	}
	return g.acquire(ctx, class, stuck)
}

func (g *Gate) acquire(ctx context.Context, class Class, stuck stuckClock) (func(), error) {
	if !class.Valid() {
		class = ClassInteractive
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	start := time.Now()
	g.mu.Lock()
	g.nextID++
	t := &ticket{id: g.nextID, class: class, owner: ownerFrom(ctx), since: start}
	if class.Background() {
		t.owner = backgroundOwnerFrom(ctx)
	}
	t.wal, _ = ctx.Value(walRequestKey{}).(*walRequest)
	if !class.Background() && g.userQueued >= WriterQueueCapacity && !g.makeRoomLocked(t) {
		g.refuseLocked(t)
		g.mu.Unlock()
		return nil, ErrWriterCapacity
	}
	if class.Background() {
		t.deferred = true
	} else {
		g.makeBackgroundRoomLocked()
	}
	g.enqueueLocked(t)
	g.admitBackgroundLocked()
	if !g.active {
		g.grant = g.pickWinnerLocked()
		g.signalLocked()
	}
	g.mu.Unlock()
	return g.acquireTicket(ctx, t, stuck)
}

// acquireTicket waits for t's grant: the shared loop of an ordinary
// acquisition and the reserved recovery turn.
func (g *Gate) acquireTicket(ctx context.Context, t *ticket, stuck stuckClock) (func(), error) {
	class := t.class
	// Only qualified reservations can refuse on capacity. A failed disk probe
	// or insufficient disk cannot be repaired by checkpointing indefinitely.
	// One timer per waiting ticket preserves bounded refusal without polling SQL.
	var capacity *time.Timer
	var capacityC <-chan time.Time
	if t.wal != nil && t.wal.qualified {
		capacity = time.NewTimer(walCapacityWait)
		capacityC = capacity.C
		defer capacity.Stop()
	}
	g.mu.Lock()
	start := t.since
	queued := false
	for {
		if t.refused {
			// A higher-priority or fairer arrival took this ticket's place. It
			// was removed and counted when that happened.
			g.mu.Unlock()
			return nil, ErrWriterCapacity
		}
		if err := ctx.Err(); err != nil {
			g.abandonWaiterLocked(t)
			g.mu.Unlock()
			return nil, err
		}
		if !g.active && g.grant == t {
			t.wal.take()
			g.removeWaiterLocked(t, true)
			g.grant = nil
			if g.holdEpoch != 0 && t.class == ClassSecurityFence {
				g.holdSecurity++
			}
			g.active = true
			g.holding.Store(true)
			g.holderProbe, _ = ctx.Value(writerProbeKey{}).(bool)
			if !g.holderProbe {
				g.writeGeneration++
			}
			if traceHolders() {
				g.holder = callerStack()
			} else {
				if pc, _, _, ok := runtime.Caller(2); ok {
					if fn := runtime.FuncForPC(pc); fn != nil {
						g.holder = fn.Name()
					}
				}
			}
			g.stuckLogged = false
			held := time.Now()
			var maintenanceSite callSite
			if class == ClassMaintenance {
				maintenanceSite = captureSite(2)
			}
			g.holderClass, g.heldSince, g.holderOwner = class, held, t.owner
			metrics := &g.byClass[class]
			metrics.acquired++
			if queued {
				metrics.queued++
				metrics.waitNanos += uint64(held.Sub(start))
			}
			g.mu.Unlock()
			g.acquired.Add(1)
			if queued {
				g.waited.Add(1)
				g.waitNanos.Add(uint64(time.Since(start)))
			}
			var once sync.Once
			return func() {
				once.Do(func() {
					elapsed := uint64(time.Since(held))
					g.heldNanos.Add(elapsed)
					for {
						current := g.maxHeld.Load()
						if elapsed <= current || g.maxHeld.CompareAndSwap(current, elapsed) {
							break
						}
					}
					g.mu.Lock()
					metrics := &g.byClass[class]
					metrics.heldNanos += elapsed
					if elapsed > metrics.maxHeld {
						metrics.maxHeld = elapsed
					}
					if g.holdAverage == 0 {
						g.holdAverage = elapsed
					} else {
						g.holdAverage = g.holdAverage - g.holdAverage/8 + elapsed/8
					}
					g.active = false
					g.holding.Store(false)
					g.holderProbe = false
					g.holderClass, g.heldSince, g.holderOwner = 0, time.Time{}, ""
					g.grant = g.pickWinnerLocked()
					g.holder = ""
					g.signalLocked()
					g.mu.Unlock()
					// Resolve and log only a slow maintenance holder, after the
					// gate is free. No query text, bound values or authority
					// descriptors enter this local operator diagnostic.
					if class == ClassMaintenance && time.Duration(elapsed) > SlowReadThreshold {
						function, file, line, _ := maintenanceSite.resolve()
						log.Printf("Maintenance writer held the gate for %s at %s:%d (%s)", time.Duration(elapsed).Round(time.Millisecond), file, line, function)
					}
				})
			}, nil
		}
		queued = true
		notify := g.notify
		g.mu.Unlock()
		select {
		case <-capacityC:
			g.mu.Lock()
			if t.refused || ctx.Err() != nil {
				continue
			}
			if !t.wal.ready() {
				g.removeWaiterLocked(t, false)
				g.refuseLocked(t)
				g.admitBackgroundLocked()
				if !g.active {
					g.grant = g.pickWinnerLocked()
				}
				g.signalLocked()
				g.mu.Unlock()
				return nil, ErrWriterCapacity
			}
			capacity.Reset(walCapacityWait)
		case <-stuck.C:
			// A hold that outlasts StuckWait is a bug, not load: something is
			// holding the writer across work that does not belong inside a
			// transaction, or has returned without committing or rolling back. Say
			// so with the holder's own stack rather than letting the server look
			// merely slow. A long wait behind many short holds is not that, so
			// check the hold itself and look again when it could first qualify.
			g.mu.Lock()
			if !t.refused && g.active && time.Since(g.heldSince) >= StuckWait {
				holder := g.holder
				g.abandonWaiterLocked(t)
				g.mu.Unlock()
				panic(fmt.Sprintf("dbwork: the write gate has been held for more than %s while a %s writer waits.\nHolder:\n%s", StuckWait, class, holder))
			}
			next := StuckWait
			if g.active {
				next -= time.Since(g.heldSince)
			}
			if stuck.reset != nil {
				stuck.reset(next)
			}
		case <-ctx.Done():
			// The loop's checks run in order: a ticket capacity already took
			// reports that, and only a live one is abandoned.
			g.mu.Lock()
		case <-notify:
			g.mu.Lock()
		}
	}
}

// A canceled or panicking waiter gives up both its queue entry and any grant
// reserved for it. Call before unwinding, while the gate mutex is still held.
func (g *Gate) abandonWaiterLocked(t *ticket) {
	g.removeWaiterLocked(t, false)
	g.admitBackgroundLocked()
	if !g.active {
		g.grant = g.pickWinnerLocked()
	}
	g.signalLocked()
	g.byClass[t.class].cancelled++
	g.cancelled.Add(1)
}

// makeRoomLocked frees one user ticket for arrival when the pool is full. The
// victim is the newest ticket of an owner already over its share, if arrival is
// within its own share, and otherwise the newest ticket of the least important
// class below arrival's. A ticket holding the pending grant is about to run and
// is never chosen. With no such victim the arrival itself is refused.
func (g *Gate) makeRoomLocked(arrival *ticket) bool {
	var victim *ticket
	if arrival.owner == "" || g.ownerQueued[arrival.owner] < OwnerQueueShare {
		for _, class := range Classes() {
			if class.Background() || class.Priority() < arrival.class.Priority() {
				continue
			}
			g.queues[class].each(func(candidate *ticket) {
				if candidate != g.grant && candidate.owner != "" && g.ownerQueued[candidate.owner] > OwnerQueueShare && (victim == nil || candidate.id > victim.id) {
					victim = candidate
				}
			})
		}
	}
	if victim == nil {
		classes := Classes()
		for index := len(classes) - 1; index >= 0 && victim == nil; index-- {
			class := classes[index]
			if class.Background() || class.Priority() <= arrival.class.Priority() {
				continue
			}
			g.queues[class].each(func(candidate *ticket) {
				if candidate != g.grant && (victim == nil || candidate.id > victim.id) {
					victim = candidate
				}
			})
		}
	}
	if victim == nil {
		return false
	}
	g.removeWaiterLocked(victim, false)
	g.refuseLocked(victim)
	g.signalLocked()
	return true
}

func (g *Gate) refuseLocked(t *ticket) {
	t.refused = true
	g.byClass[t.class].refused++
	g.refused.Add(1)
}

// signalLocked wakes every waiter so each can re-evaluate its own position.
// Only one waiter can win, so a targeted handoff would only move the priority
// decision into the releasing goroutine, which does not know the ladder state
// any better than the waiters do.
func (g *Gate) signalLocked() {
	close(g.notify)
	g.notify = make(chan struct{})
}

func (g *Gate) enqueueLocked(t *ticket) {
	queue := &g.queues[t.class]
	if t.deferred {
		queue = &g.deferred[t.class]
	}
	if queue.byOwner == nil {
		queue.byOwner = map[string]*ownerQueue{}
	}
	owner := queue.byOwner[t.owner]
	if owner == nil {
		owner = &ownerQueue{owner: t.owner}
		queue.byOwner[t.owner] = owner
		queue.rotation = append(queue.rotation, owner)
	}
	owner.tickets = append(owner.tickets, t)
	queue.count++
	if !t.class.Background() {
		g.userQueued++
		if t.owner != "" {
			g.ownerQueued[t.owner]++
		}
	}
}

// removeWaiterLocked takes t out of its queue. A served owner moves to the back
// of its class's rotation; an owner with nothing left leaves it.
func (g *Gate) removeWaiterLocked(t *ticket, served bool) {
	if t.recovery {
		if g.recoveryTicket == t {
			g.recoveryTicket = nil
		}
		return
	}
	queue := &g.queues[t.class]
	if t.deferred {
		queue = &g.deferred[t.class]
	}
	owner := queue.byOwner[t.owner]
	if owner == nil {
		return
	}
	index := slices.Index(owner.tickets, t)
	if index < 0 {
		return
	}
	if age := uint64(time.Since(t.since)); age > g.maxQueueAge[t.class] {
		g.maxQueueAge[t.class] = age
	}
	owner.tickets = slices.Delete(owner.tickets, index, index+1)
	queue.count--
	if !t.class.Background() {
		g.userQueued--
		if t.owner != "" {
			if g.ownerQueued[t.owner]--; g.ownerQueued[t.owner] == 0 {
				delete(g.ownerQueued, t.owner)
			}
		}
	}
	position := slices.Index(queue.rotation, owner)
	switch {
	case len(owner.tickets) == 0:
		queue.rotation = slices.Delete(queue.rotation, position, position+1)
		delete(queue.byOwner, t.owner)
	case served:
		queue.rotation = append(slices.Delete(queue.rotation, position, position+1), owner)
	}
}

// backgroundTicketLimit includes the current holder as well as queued tickets.
func backgroundTicketLimit(class Class) int {
	if class == ClassMaintenance {
		return 1
	}
	return 2
}

func (g *Gate) backgroundQueuedLocked() int {
	return g.queues[ClassBackgroundMedia].count + g.queues[ClassMaintenance].count
}

// admitBackgroundLocked uses the same per-owner rotor as dispatch. Admission
// stops at user pressure, the global pool bound, or a supervisor's outstanding
// limit. No producer is refused; its existing caller waits on the gate wake.
func (g *Gate) admitBackgroundLocked() {
	if g.userQueued != 0 || g.holdEpoch != 0 || g.pauseEpoch != 0 {
		return
	}
	for g.backgroundQueuedLocked() < WriterQueueCapacity {
		var next *ticket
		now := time.Now()
		for _, class := range []Class{ClassBackgroundMedia, ClassMaintenance} {
			for _, owner := range g.deferred[class].rotation {
				outstanding := 0
				if admitted := g.queues[class].byOwner[owner.owner]; admitted != nil {
					outstanding = len(admitted.tickets)
				}
				if g.active && g.holderClass == class && g.holderOwner == owner.owner {
					outstanding++
				}
				if outstanding >= backgroundTicketLimit(class) {
					continue
				}
				candidate := owner.tickets[0]
				if next == nil || promotedPriority(class, candidate.since, now) < promotedPriority(next.class, next.since, now) ||
					promotedPriority(class, candidate.since, now) == promotedPriority(next.class, next.since, now) && candidate.since.Before(next.since) {
					next = candidate
				}
				break // Within a class, preserve the existing owner rotor.
			}
		}
		if next == nil {
			return
		}
		g.removeWaiterLocked(next, true)
		next.deferred = false
		g.enqueueLocked(next)
	}
}

// A user borrows a background ticket before facing real user capacity. Return
// the newest admission to its supervisor's pending work, ahead of newer work
// for that owner. A pending grant has made no WAL debit and is safe to withdraw.
func (g *Gate) makeBackgroundRoomLocked() {
	if g.userQueued+g.backgroundQueuedLocked() < WriterQueueCapacity {
		return
	}
	var newest *ticket
	for _, class := range []Class{ClassBackgroundMedia, ClassMaintenance} {
		g.queues[class].each(func(t *ticket) {
			if newest == nil || t.id > newest.id {
				newest = t
			}
		})
	}
	if newest == nil {
		return
	}
	g.removeWaiterLocked(newest, false)
	if g.grant == newest {
		g.grant = nil
	}
	newest.deferred = true
	g.enqueueLocked(newest)
	owner := g.deferred[newest.class].byOwner[newest.owner]
	// This was admitted before every still-deferred acquisition of its owner.
	copy(owner.tickets[1:], owner.tickets[:len(owner.tickets)-1])
	owner.tickets[0] = newest
}

// front is the ticket this class would serve next.
func (q *classQueue) front() *ticket {
	if q.count == 0 {
		return nil
	}
	return q.rotation[0].tickets[0]
}

func (q *classQueue) each(visit func(*ticket)) {
	for _, owner := range q.rotation {
		for _, t := range owner.tickets {
			visit(t)
		}
	}
}

func promotedPriority(class Class, since, now time.Time) int {
	priority := class.Priority()
	if class.Background() {
		priority -= int(now.Sub(since) / GateAgePromotionInterval)
		if priority < ClassBackgroundMedia.Priority() {
			priority = ClassBackgroundMedia.Priority()
		}
	}
	return priority
}

// pickWinnerLocked freezes one decision at one instant. If each waiter compares
// priorities separately, an age-promotion boundary can make A see B ahead and
// B see A ahead, leaving a free gate with nobody able to acquire it.
//
// Any waiting user class wins outright, in ladder order. Only when no user
// ticket waits do the background classes compete, by promoted rung and then
// by arrival.
func (g *Gate) pickWinnerLocked() *ticket {
	if g.pauseEpoch != 0 {
		// The final recovery interval: nothing begins but the reserved turn.
		return g.recoveryTicket
	}
	if g.holdEpoch != 0 {
		// Hard WAL pressure: no new writer but a bounded number of security
		// fences until the recovery attempt has had its chance.
		if g.holdSecurity < RecoverySecurityReserve {
			next := g.queues[ClassSecurityFence].front()
			if next != nil && next.wal.ready() {
				return next
			}
			return nil
		}
		return nil
	}
	if g.userQueued > 0 {
		for _, class := range Classes() {
			if class.Background() {
				break
			}
			if next := g.queues[class].front(); next != nil && next.wal.ready() {
				return next
			}
		}
		return nil // A blocked user is never overtaken by background work.
	}
	g.admitBackgroundLocked()
	now := time.Now()
	var selected *ticket
	best := 0
	for _, class := range Classes() {
		if !class.Background() {
			continue
		}
		next := g.queues[class].front()
		if next == nil || !next.wal.ready() {
			continue
		}
		priority := promotedPriority(class, next.since, now)
		if selected == nil || priority < best || priority == best && next.since.Before(selected.since) {
			selected, best = next, priority
		}
	}
	return selected
}

// QueueAges reports, per class, the longest a waiter of that class has ever
// queued and how long the oldest one currently waiting has been there. The
// second is the live signal; the first is the one that survives the moment.
func (g *Gate) QueueAges() (peak map[string]uint64, current map[string]uint64) {
	g.mu.Lock()
	defer g.mu.Unlock()
	peak, current = map[string]uint64{}, map[string]uint64{}
	for class, age := range g.maxQueueAge {
		if age > 0 {
			peak[class.String()] = age / uint64(time.Millisecond)
		}
	}
	now := time.Now()
	for _, class := range Classes() {
		oldest := time.Duration(0)
		visit := func(t *ticket) {
			if age := now.Sub(t.since); age > oldest {
				oldest = age
			}
		}
		g.queues[class].each(visit)
		g.deferred[class].each(visit)
		if oldest > 0 {
			current[class.String()] = uint64(oldest / time.Millisecond)
		}
	}
	return peak, current
}

// ActiveOrWaiting reports whether any writer holds or wants the slot. It is the
// "is anyone writing?" signal exclusive maintenance consults before taking the
// database for itself.
func (g *Gate) ActiveOrWaiting() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.active {
		return true
	}
	for _, class := range Classes() {
		if g.queues[class].count+g.deferred[class].count > 0 {
			return true
		}
	}
	return false
}

// Waiting returns all waiting acquisitions per class, including pre-admission
// background waits. Admission separates those from actual queued tickets.
func (g *Gate) Waiting() map[string]int {
	g.mu.Lock()
	defer g.mu.Unlock()
	out := map[string]int{}
	for _, class := range Classes() {
		if count := g.queues[class].count + g.deferred[class].count; count > 0 {
			out[class.String()] = count
		}
	}
	return out
}

// AdmissionStats is the state of the bounded ticket pool. It is kept out of
// GateStats, which is part of the published admin contract, until the typed
// refusal lands with its own contract change.
type AdmissionStats struct {
	Capacity         int `json:"capacity"`
	UserQueued       int `json:"userQueued"`
	BackgroundQueued int `json:"backgroundQueued"`
	// Deferred is background acquisitions waiting before ticket admission.
	// They are still waiting; none of their work has been dropped.
	Deferred int `json:"deferred"`
	// Refused counts tickets refused before effects by pool capacity or by a
	// qualified WAL reservation deadline, in total and by class.
	Refused        uint64            `json:"refused"`
	RefusedByClass map[string]uint64 `json:"refusedByClass"`
	// BusyAfterAdmission counts SQLITE_BUSY or SQLITE_LOCKED met by a write
	// that already held the gate: an outside opener or a discipline defect,
	// never ordinary contention.
	BusyAfterAdmission        uint64            `json:"busyAfterAdmission"`
	BusyAfterAdmissionByClass map[string]uint64 `json:"busyAfterAdmissionByClass"`
	// RecoveryHold and RecoveryPause are a WAL recovery attempt in force: new
	// writers (then every acquisition) wait for its reserved reset turn.
	RecoveryHold  bool `json:"recoveryHold"`
	RecoveryPause bool `json:"recoveryPause"`
}

// Admission snapshots the ticket pool.
func (g *Gate) Admission() AdmissionStats {
	g.mu.Lock()
	defer g.mu.Unlock()
	out := AdmissionStats{Capacity: WriterQueueCapacity, UserQueued: g.userQueued, Refused: g.refused.Load(), BusyAfterAdmission: g.busy.Load(), RefusedByClass: map[string]uint64{}, BusyAfterAdmissionByClass: map[string]uint64{}, RecoveryHold: g.holdEpoch != 0, RecoveryPause: g.pauseEpoch != 0}
	for _, class := range Classes() {
		if class.Background() {
			out.BackgroundQueued += g.queues[class].count
			out.Deferred += g.deferred[class].count
		}
		if refused := g.byClass[class].refused; refused > 0 {
			out.RefusedByClass[class.String()] = refused
		}
		if busy := g.byClass[class].busy; busy > 0 {
			out.BusyAfterAdmissionByClass[class.String()] = busy
		}
	}
	return out
}

// errRecoveryEnded reports a recovery step whose hold or pause has already
// ended (or belongs to another owner).
var errRecoveryEnded = errors.New("dbwork: WAL recovery hold ended")

// enterPressureHold starts a recovery attempt: new acquisitions other than a
// bounded number of security fences stay queued, unselected and unbegun. Only
// the checkpoint owner calls it, and it must call endRecovery with the epoch.
// A grant already reserved for a ticket the hold now excludes is withdrawn; the
// holder of the gate, if any, finishes normally.
func (g *Gate) enterPressureHold() (uint64, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.recoveryEpoch != 0 {
		return 0, errors.New("dbwork: a WAL recovery attempt is already in progress")
	}
	g.nextID++
	g.recoveryEpoch, g.holdEpoch, g.holdSecurity = g.nextID, g.nextID, 0
	if g.grant != nil && g.grant.class != ClassSecurityFence {
		g.grant = nil
	}
	if !g.active && g.grant == nil {
		g.grant = g.pickWinnerLocked()
		g.signalLocked()
	}
	return g.recoveryEpoch, nil
}

// enterBeginPause turns the hold into the final pause: no acquisition of any
// class is selected except the reserved recovery turn.
func (g *Gate) enterBeginPause(epoch uint64) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if epoch == 0 || g.recoveryEpoch != epoch {
		return errRecoveryEnded
	}
	g.pauseEpoch = epoch
	if g.grant != nil && !g.grant.recovery {
		g.grant = nil
	}
	return nil
}

// endRecovery ends the hold and the pause of epoch, and lets the queue run.
// It is idempotent and ignores another owner's epoch.
func (g *Gate) endRecovery(epoch uint64) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if epoch == 0 || g.recoveryEpoch != epoch {
		return
	}
	g.recoveryEpoch, g.holdEpoch, g.pauseEpoch, g.holdSecurity = 0, 0, 0, 0
	if g.recoveryTicket != nil {
		// Its acquisition sees the pause end and gives up (acquireRecoveryTurn).
		g.recoveryTicket.refused = true
		g.recoveryTicket = nil
	}
	if !g.active {
		g.grant = g.pickWinnerLocked()
	}
	g.signalLocked()
}

// waitIdle waits until no one holds the gate, or ctx ends. During a hold or
// pause no new holder can start, so this is the current holder finishing.
func (g *Gate) waitIdle(ctx context.Context) error {
	for {
		g.mu.Lock()
		if !g.active {
			g.mu.Unlock()
			return nil
		}
		notify := g.notify
		g.mu.Unlock()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-notify:
		}
	}
}

// acquireRecoveryTurn takes the single reserved turn of a BEGIN pause. It is
// the only acquisition the pause selects, and like any acquisition it moves
// the write generation exactly once, which is what the checkpoint's
// generation==copyFence+1 proof expects.
func (g *Gate) acquireRecoveryTurn(ctx context.Context, epoch uint64) (func(), error) {
	g.mu.Lock()
	if epoch == 0 || g.pauseEpoch != epoch || g.recoveryTicket != nil {
		g.mu.Unlock()
		return nil, errRecoveryEnded
	}
	g.nextID++
	t := &ticket{id: g.nextID, class: ClassMaintenance, since: time.Now(), recovery: true}
	g.recoveryTicket = t
	if !g.active && g.grant == nil {
		g.grant = t
		g.signalLocked()
	}
	g.mu.Unlock()
	release, err := g.acquireTicket(ctx, t, stuckClock{})
	if err != nil && errors.Is(err, ErrWriterCapacity) {
		// endRecovery withdrew the turn; it was never a capacity refusal.
		return nil, errRecoveryEnded
	}
	return release, err
}

// RecoveryState reports whether a WAL recovery hold or pause is in force.
func (g *Gate) RecoveryState() (hold, pause bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.holdEpoch != 0, g.pauseEpoch != 0
}

// Held reports whether some caller holds the gate now. It takes no lock.
func (g *Gate) Held() bool { return g.holding.Load() }

// Retry-After bounds for a refused write (F §3): never less than a second, and
// never more than thirty however deep the queue.
const (
	minWriterRetry = time.Second
	maxWriterRetry = 30 * time.Second
)

// RetryAfter is how long a refused write should wait before it is sent again:
// the time the queue ahead of it would take to drain at the recent average
// hold, clamped to 1-30 s. A full pool of 99 short writes rounds up to the
// one-second floor; the same pool behind slow storage asks for longer.
func (g *Gate) RetryAfter() time.Duration {
	g.mu.Lock()
	queued := g.userQueued
	if g.active {
		queued++
	}
	average := g.holdAverage
	g.mu.Unlock()
	wait := time.Duration(uint64(queued) * average)
	return min(max(wait, minWriterRetry), maxWriterRetry)
}

// RetryAfterSeconds is RetryAfter in the whole seconds an HTTP Retry-After
// header carries, rounded up.
func (g *Gate) RetryAfterSeconds() int {
	wait := g.RetryAfter()
	return int((wait + time.Second - 1) / time.Second)
}

// noteBusyAfterAdmission counts a lock error met while holding the gate.
func (g *Gate) noteBusyAfterAdmission(class Class) {
	if !class.Valid() {
		class = ClassInteractive
	}
	g.busy.Add(1)
	g.mu.Lock()
	g.byClass[class].busy++
	g.mu.Unlock()
}

// GateStats is the observable state of the write gate.
type GateStats struct {
	Active         bool                      `json:"active"`
	Acquired       uint64                    `json:"acquired"`
	Queued         uint64                    `json:"queued"`
	Cancelled      uint64                    `json:"cancelled"`
	QueueWaitMilli uint64                    `json:"queueWaitMillis"`
	HeldMilli      uint64                    `json:"heldMillis"`
	MaxHeldMilli   uint64                    `json:"maxHeldMillis"`
	Waiting        map[string]int            `json:"waiting"`
	ByClass        map[string]GateClassStats `json:"byClass"`
	// MaxQueueAgeMilli is the longest wait ever seen per class, and
	// QueueAgeMilli the age of the oldest waiter in each class right now. A
	// ladder without aging can starve its bottom rung; these are how that would
	// be noticed rather than guessed at.
	MaxQueueAgeMilli map[string]uint64 `json:"maxQueueAgeMillis"`
	QueueAgeMilli    map[string]uint64 `json:"queueAgeMillis"`
}

// GateClassStats distinguishes a maintenance hold from an interactive or
// playback write without recording SQL, arguments or caller identity.
type GateClassStats struct {
	Active           bool   `json:"active"`
	Acquired         uint64 `json:"acquired"`
	Queued           uint64 `json:"queued"`
	Cancelled        uint64 `json:"cancelled"`
	QueueWaitMilli   uint64 `json:"queueWaitMillis"`
	HeldMilli        uint64 `json:"heldMillis"`
	MaxHeldMilli     uint64 `json:"maxHeldMillis"`
	CurrentHeldMilli uint64 `json:"currentHeldMillis"`
}

// Stats snapshots the gate counters.
func (g *Gate) Stats() GateStats {
	g.mu.Lock()
	active := g.active
	byClass := make(map[string]GateClassStats, len(classNames)-1)
	for _, class := range Classes() {
		entry := g.byClass[class]
		value := GateClassStats{
			Active:         active && g.holderClass == class,
			Acquired:       entry.acquired,
			Queued:         entry.queued,
			Cancelled:      entry.cancelled,
			QueueWaitMilli: entry.waitNanos / uint64(time.Millisecond),
			HeldMilli:      entry.heldNanos / uint64(time.Millisecond),
			MaxHeldMilli:   entry.maxHeld / uint64(time.Millisecond),
		}
		if value.Active {
			value.CurrentHeldMilli = uint64(time.Since(g.heldSince) / time.Millisecond)
		}
		byClass[class.String()] = value
	}
	g.mu.Unlock()
	peak, current := g.QueueAges()
	return GateStats{
		Active:           active,
		Acquired:         g.acquired.Load(),
		Queued:           g.waited.Load(),
		Cancelled:        g.cancelled.Load(),
		QueueWaitMilli:   g.waitNanos.Load() / uint64(time.Millisecond),
		HeldMilli:        g.heldNanos.Load() / uint64(time.Millisecond),
		MaxHeldMilli:     g.maxHeld.Load() / uint64(time.Millisecond),
		Waiting:          g.Waiting(),
		ByClass:          byClass,
		MaxQueueAgeMilli: peak,
		QueueAgeMilli:    current,
	}
}

// ResetPeak forgets the longest hold seen so far. A measurement window that
// begins after a fixture has been built must not inherit that fixture's own
// transactions: a load test reports the longest hold *during the run*, and a
// peak carried over from setup is a number about nothing.
func (g *Gate) ResetPeak() {
	g.maxHeld.Store(0)
	g.mu.Lock()
	clear(g.maxQueueAge)
	for class := range g.byClass {
		g.byClass[class].maxHeld = 0
	}
	g.mu.Unlock()
}

// checkpointCopyFence captures whether PASSIVE starts with no writer in
// flight, and the acquisition generation a reset must still follow directly.
// The active bit matters: an already acquired writer can commit during copying
// without changing the acquisition counter again.
func (g *Gate) checkpointCopyFence() (uint64, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.writeGeneration, !g.active || g.holderProbe
}

// traceHolders reports whether the gate records who holds it. Capturing a stack
// on every acquisition would be a measurable cost on the write path, so it is
// enabled only where the answer is worth it: tests.
func traceHolders() bool { return strict() }

func callerStack() string {
	buffer := make([]byte, 4096)
	return string(buffer[:runtime.Stack(buffer, false)])
}

// StuckHolder reports a long hold even when no writer is waiting. It logs once
// per hold, and the watchdog uses the returned error for its health state.
func (g *Gate) StuckHolder(threshold time.Duration) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.active || time.Since(g.heldSince) < threshold {
		return nil
	}
	err := fmt.Errorf("database writer %s (%s) has held the write gate for %s", g.holderClass, g.holder, time.Since(g.heldSince).Round(time.Millisecond))
	if !g.stuckLogged {
		log.Printf("%v", err)
		g.stuckLogged = true
	}
	return err
}
