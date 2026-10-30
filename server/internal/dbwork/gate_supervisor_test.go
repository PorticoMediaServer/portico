package dbwork

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/supervise"
)

// Capture the actual context that Loop delivers, rather than manufacturing the
// gate's owner string. A request owner must not split one supervisor's allowance.
func supervisedGateContext(ctx context.Context, name string) context.Context {
	var life context.Context
	supervise.Loop(ctx, name, func(ctx context.Context) { life = ctx })
	return life
}

type supervisorHold struct {
	label   string
	release func()
}

type supervisorHarness struct {
	t       *testing.T
	g       *Gate
	ctx     context.Context
	started chan supervisorHold
	errors  chan error
	wg      sync.WaitGroup
}

func newSupervisorHarness(t *testing.T, g *Gate) *supervisorHarness {
	ctx, cancel := context.WithCancel(context.Background())
	h := &supervisorHarness{t: t, g: g, ctx: ctx, started: make(chan supervisorHold, 256), errors: make(chan error, 256)}
	t.Cleanup(func() { cancel(); h.wg.Wait() })
	return h
}

func (h *supervisorHarness) start(name string, class Class, label string) context.CancelFunc {
	h.t.Helper()
	ctx, cancel := context.WithCancel(supervisedGateContext(WithOwner(h.ctx, label), name))
	h.wg.Add(1)
	go func() {
		defer h.wg.Done()
		release, err := h.g.Acquire(ctx, class)
		if err != nil {
			h.errors <- err
			return
		}
		hold, done := make(chan struct{}), make(chan struct{})
		var once sync.Once
		h.started <- supervisorHold{label: label, release: func() { once.Do(func() { close(hold) }); <-done }}
		select {
		case <-hold:
		case <-ctx.Done():
		}
		release()
		close(done)
	}()
	return cancel
}

func (h *supervisorHarness) next(label string) supervisorHold {
	h.t.Helper()
	select {
	case hold := <-h.started:
		if hold.label != label {
			h.t.Fatalf("served %s, want %s", hold.label, label)
		}
		return hold
	case err := <-h.errors:
		h.t.Fatalf("acquisition failed: %v", err)
	case <-time.After(5 * time.Second):
		h.t.Fatalf("%s did not acquire", label)
	}
	return supervisorHold{}
}

func assertSupervisorPool(t *testing.T, g *Gate, queued, deferred int) {
	t.Helper()
	s := g.Admission()
	if s.BackgroundQueued != queued || s.Deferred != deferred || s.Refused != 0 {
		t.Fatalf("admission = %+v, want background queued=%d deferred=%d", s, queued, deferred)
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.userQueued+g.backgroundQueuedLocked() > WriterQueueCapacity {
		t.Fatal("actual global ticket cap exceeded")
	}
	for _, class := range []Class{ClassBackgroundMedia, ClassMaintenance} {
		if queued == 0 && deferred == 0 {
			for _, q := range []*classQueue{&g.queues[class], &g.deferred[class]} {
				if q.count != 0 || len(q.rotation) != 0 || len(q.byOwner) != 0 {
					t.Fatalf("%s retained owner state: %+v", class, q)
				}
			}
		}
		for _, owner := range g.queues[class].rotation {
			n := len(owner.tickets)
			if g.active && g.holderClass == class && g.holderOwner == owner.owner {
				n++
			}
			if n > backgroundTicketLimit(class) {
				t.Fatalf("%s/%s has %d outstanding", class, owner.owner, n)
			}
		}
	}
}

func TestSupervisorOutstandingCapsIncludeHolderAndCancelBeforeAdmission(t *testing.T) {
	for _, class := range []Class{ClassBackgroundMedia, ClassMaintenance} {
		t.Run(class.String(), func(t *testing.T) {
			g := NewGate()
			h := newSupervisorHarness(t, g)
			h.start("cmd.metadata", class, "held")
			held := h.next("held")
			limit := backgroundTicketLimit(class)
			for i := 1; i < limit; i++ {
				h.start("cmd.metadata", class, fmt.Sprint(i))
				waitForWaiters(t, g, i)
			}
			cancel := h.start("cmd.metadata", class, "deferred")
			waitForWaiters(t, g, limit)
			assertSupervisorPool(t, g, limit-1, 1)
			cancel()
			if err := <-h.errors; !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			assertSupervisorPool(t, g, limit-1, 0)
			held.release()
			for i := 1; i < limit; i++ {
				h.next(fmt.Sprint(i)).release()
			}
			if g.ActiveOrWaiting() {
				t.Fatal("supervisor allowance leaked")
			}
		})
	}
}

func TestSupervisorsRotateFIFOUsingActualOperationKeys(t *testing.T) {
	for _, class := range []Class{ClassBackgroundMedia, ClassMaintenance} {
		t.Run(class.String(), func(t *testing.T) {
			g := NewGate()
			block, err := g.Acquire(context.Background(), ClassInteractive)
			if err != nil {
				t.Fatal(err)
			}
			defer block()
			h := newSupervisorHarness(t, g)
			n := 0
			for _, name := range []string{"cmd.metadata", "cmd.artwork", "cmd.scanner"} {
				for i := 0; i < 4; i++ {
					h.start(name, class, fmt.Sprintf("%s/%d", name, i))
					n++
					waitForWaiters(t, g, n)
				}
			}
			queued := 3 * backgroundTicketLimit(class)
			assertSupervisorPool(t, g, queued, 12-queued)
			block()
			for i := 0; i < 4; i++ {
				for _, name := range []string{"cmd.metadata", "cmd.artwork", "cmd.scanner"} {
					h.next(fmt.Sprintf("%s/%d", name, i)).release()
				}
			}
			assertSupervisorPool(t, g, 0, 0)
		})
	}
}

func TestSupervisorDefersUnderUserPressureAndRecoveryWithoutWALDebit(t *testing.T) {
	g := NewGate()
	block, err := g.Acquire(context.Background(), ClassInteractive)
	if err != nil {
		t.Fatal(err)
	}
	defer block()
	h := newSupervisorHarness(t, g)
	ledger := reservationFixture(t)
	request := &walRequest{ledger: ledger, bytes: 32768, qualified: true, class: ClassBackgroundMedia}
	h.ctx = context.WithValue(h.ctx, walRequestKey{}, request)
	before := ledger.snapshot().QualifiedWrites
	results := make(chan admissionResult, 1)
	queueTicket(t, g, ClassSecurityFence, "user", "fence", closedChannel(), results)
	h.start("cmd.metadata", ClassBackgroundMedia, "background")
	waitForWaiters(t, g, 2)
	assertSupervisorPool(t, g, 0, 1)
	epoch, err := g.enterPressureHold()
	if err != nil {
		t.Fatal(err)
	}
	defer g.endRecovery(epoch)
	block()
	expectResult(t, results, "fence")
	if err = g.enterBeginPause(epoch); err != nil {
		t.Fatal(err)
	}
	assertSupervisorPool(t, g, 0, 1)
	turn, err := g.acquireRecoveryTurn(context.Background(), epoch)
	if err != nil {
		t.Fatal(err)
	}
	turn()
	assertSupervisorPool(t, g, 0, 1)
	if got := ledger.snapshot().QualifiedWrites; got != before {
		t.Fatalf("deferred acquisition debited WAL: %d -> %d", before, got)
	}
	g.endRecovery(epoch)
	h.next("background").release()
	if got := ledger.snapshot().QualifiedWrites; got != before+1 {
		t.Fatalf("acquired background did not debit exactly once: %d -> %d", before, got)
	}
	assertSupervisorPool(t, g, 0, 0)
}

func TestSupervisorActualPoolBoundAndUserBorrowsBackgroundTicket(t *testing.T) {
	g := NewGate()
	block, err := g.Acquire(context.Background(), ClassInteractive)
	if err != nil {
		t.Fatal(err)
	}
	defer block()
	h := newSupervisorHarness(t, g)
	for i := 0; i < WriterQueueCapacity+1; i++ {
		h.start(fmt.Sprintf("test.supervisor.%d", i), ClassBackgroundMedia, fmt.Sprint(i))
		waitForWaiters(t, g, i+1)
	}
	assertSupervisorPool(t, g, WriterQueueCapacity, 1)
	userHold := make(chan struct{})
	results := make(chan admissionResult, 1)
	queueTicket(t, g, ClassInteractive, "user", "user", userHold, results)
	assertSupervisorPool(t, g, WriterQueueCapacity-1, 2)
	block()
	// The user holds the writer; background has no opportunity until it ends.
	g.mu.Lock()
	// A pending user grant or its active hold is the only possible transition.
	backgroundGranted := g.grant != nil && g.grant.class.Background()
	g.mu.Unlock()
	if backgroundGranted {
		t.Fatal("background granted ahead of user")
	}
	close(userHold)
	expectResult(t, results, "user")
	for i := 0; i < WriterQueueCapacity-1; i++ {
		h.next(fmt.Sprint(i)).release()
	}
	// The displaced owner's next turn follows the owner already deferred.
	h.next(fmt.Sprint(WriterQueueCapacity)).release()
	h.next(fmt.Sprint(WriterQueueCapacity - 1)).release()
	assertSupervisorPool(t, g, 0, 0)
}

func TestSupervisorAdmittedCancellationReleasesExactAllowance(t *testing.T) {
	g := NewGate()
	block, err := g.Acquire(context.Background(), ClassInteractive)
	if err != nil {
		t.Fatal(err)
	}
	defer block()
	h := newSupervisorHarness(t, g)
	cancel := h.start("cmd.metadata", ClassBackgroundMedia, "cancelled")
	waitForWaiters(t, g, 1)
	h.start("cmd.metadata", ClassBackgroundMedia, "second")
	waitForWaiters(t, g, 2)
	h.start("cmd.metadata", ClassBackgroundMedia, "third")
	waitForWaiters(t, g, 3)
	assertSupervisorPool(t, g, 2, 1)
	cancel()
	if err := <-h.errors; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	assertSupervisorPool(t, g, 2, 0)
	block()
	h.next("second").release()
	h.next("third").release()
	assertSupervisorPool(t, g, 0, 0)
}

func TestSupervisorCancelledPendingGrantDoesNotDebitWALOrLeakAllowance(t *testing.T) {
	g := NewGate()
	life := supervisedGateContext(context.Background(), "cmd.database-housekeeping")
	ctx, cancel := context.WithCancel(life)
	ledger := reservationFixture(t)
	request := &walRequest{ledger: ledger, bytes: 32768, qualified: true, class: ClassMaintenance}
	first := &ticket{id: 1, class: ClassMaintenance, owner: backgroundOwnerFrom(life), since: time.Now(), wal: request}
	next := &ticket{id: 2, class: ClassMaintenance, owner: first.owner, since: time.Now(), wal: request, deferred: true}
	// Freeze the ordinary handoff immediately before its recipient runs.
	g.mu.Lock()
	g.nextID = 2
	g.enqueueLocked(first)
	g.enqueueLocked(next)
	g.grant = first
	g.mu.Unlock()
	cancel()
	if _, err := g.acquireTicket(ctx, first, stuckClock{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled pending grant: %v", err)
	}
	assertSupervisorPool(t, g, 1, 0)
	if ledger.snapshot().QualifiedWrites != 0 {
		t.Fatal("cancelled grant debited WAL")
	}
	g.mu.Lock()
	selected := g.grant == next
	g.mu.Unlock()
	if !selected {
		t.Fatal("cancelled grant stranded its supervisor's deferred acquisition")
	}
	release, err := g.acquireTicket(life, next, stuckClock{})
	if err != nil {
		t.Fatal(err)
	}
	release()
	if ledger.snapshot().QualifiedWrites != 1 {
		t.Fatal("replacement grant did not debit exactly once")
	}
	assertSupervisorPool(t, g, 0, 0)
}

func TestUnsupervisedBackgroundSharesConservativeAllowance(t *testing.T) {
	g := NewGate()
	block, err := g.Acquire(context.Background(), ClassInteractive)
	if err != nil {
		t.Fatal(err)
	}
	defer block()
	results := make(chan admissionResult, 3)
	for i := 0; i < 3; i++ {
		queueTicket(t, g, ClassBackgroundMedia, fmt.Sprintf("request-owner-%d", i), fmt.Sprint(i), closedChannel(), results)
	}
	assertSupervisorPool(t, g, 2, 1)
	block()
	for i := 0; i < 3; i++ {
		expectResult(t, results, fmt.Sprint(i))
	}
	assertSupervisorPool(t, g, 0, 0)
}

func TestSupervisorRepeatedUserDisplacementPreservesOwnerFIFO(t *testing.T) {
	g := NewGate()
	block, err := g.Acquire(context.Background(), ClassInteractive)
	if err != nil {
		t.Fatal(err)
	}
	defer block()
	h := newSupervisorHarness(t, g)
	// Leave exactly two tickets for metadata and one deferred acquisition of
	// the same supervisor. Two users must return those admissions in FIFO order.
	for i := 0; i < WriterQueueCapacity-2; i++ {
		h.start(fmt.Sprintf("test.supervisor.%d", i), ClassBackgroundMedia, fmt.Sprint(i))
		waitForWaiters(t, g, i+1)
	}
	for i := 0; i < 3; i++ {
		h.start("cmd.metadata", ClassBackgroundMedia, fmt.Sprintf("metadata/%d", i))
		waitForWaiters(t, g, WriterQueueCapacity-1+i)
	}
	assertSupervisorPool(t, g, WriterQueueCapacity, 1)
	results := make(chan admissionResult, 2)
	for i := 0; i < 2; i++ {
		queueTicket(t, g, ClassInteractive, "user", fmt.Sprintf("user/%d", i), closedChannel(), results)
	}
	assertSupervisorPool(t, g, WriterQueueCapacity-2, 3)
	block()
	for i := 0; i < 2; i++ {
		expectResult(t, results, fmt.Sprintf("user/%d", i))
	}
	for i := 0; i < WriterQueueCapacity-2; i++ {
		h.next(fmt.Sprint(i)).release()
	}
	for i := 0; i < 3; i++ {
		h.next(fmt.Sprintf("metadata/%d", i)).release()
	}
	assertSupervisorPool(t, g, 0, 0)
}

func TestSupervisorDeferredAgeAndEveryUserClassKeepsPriority(t *testing.T) {
	g := NewGate()
	block, err := g.Acquire(context.Background(), ClassInteractive)
	if err != nil {
		t.Fatal(err)
	}
	defer block()
	h := newSupervisorHarness(t, g)
	results := make(chan admissionResult, len(Classes()))
	users := 0
	for _, class := range Classes() {
		if !class.Background() {
			queueTicket(t, g, class, "user", class.String(), closedChannel(), results)
			users++
		}
	}
	// Both supervisors arrive under existing user pressure, so neither has an
	// admitted ticket. Their entire pre-admission age must survive until served.
	h.start("cmd.database-housekeeping", ClassMaintenance, "maintenance")
	waitForWaiters(t, g, users+1)
	h.start("cmd.metadata", ClassBackgroundMedia, "media")
	waitForWaiters(t, g, users+2)
	assertSupervisorPool(t, g, 0, 2)
	g.mu.Lock()
	g.deferred[ClassMaintenance].front().since = time.Now().Add(-2 * time.Hour)
	g.deferred[ClassBackgroundMedia].front().since = time.Now().Add(-time.Hour)
	g.mu.Unlock()
	_, current := g.QueueAges()
	if current[ClassMaintenance.String()] < uint64((2*time.Hour)/time.Millisecond) || current[ClassBackgroundMedia.String()] < uint64(time.Hour/time.Millisecond) {
		t.Fatalf("deferred age missing: %v", current)
	}
	block()
	for _, class := range Classes() {
		if !class.Background() {
			expectResult(t, results, class.String())
		}
	}
	// The older maintenance waiter reaches the media rung, never a user rung.
	h.next("maintenance").release()
	h.next("media").release()
	peak, current := g.QueueAges()
	if len(current) != 0 || peak[ClassMaintenance.String()] < uint64((2*time.Hour)/time.Millisecond) || peak[ClassBackgroundMedia.String()] < uint64(time.Hour/time.Millisecond) {
		t.Fatalf("deferred ages lost on admission/drain: peak=%v current=%v", peak, current)
	}
	assertSupervisorPool(t, g, 0, 0)
}

func TestSupervisorPendingGrantPreemptedByEveryUserClassWithoutWALDebit(t *testing.T) {
	for _, class := range Classes() {
		if class.Background() {
			continue
		}
		t.Run(class.String(), func(t *testing.T) {
			g := NewGate()
			ledger := reservationFixture(t)
			life := supervisedGateContext(context.Background(), "cmd.metadata")
			pending := &ticket{id: 1, class: ClassBackgroundMedia, owner: backgroundOwnerFrom(life), since: time.Now(),
				wal: &walRequest{ledger: ledger, bytes: 32768, qualified: true, class: ClassBackgroundMedia}}
			// Freeze the real selection/acquisition boundary. A pending grant is
			// still queued, with no WAL debit or started transaction to protect.
			g.mu.Lock()
			g.nextID = 1
			g.enqueueLocked(pending)
			g.grant = g.pickWinnerLocked()
			g.mu.Unlock()
			user, err := g.Acquire(context.Background(), class)
			if err != nil {
				t.Fatal(err)
			}
			defer user()
			if ledger.snapshot().QualifiedWrites != 0 {
				t.Fatal("preempted background grant debited WAL")
			}
			assertSupervisorPool(t, g, 1, 0)
			user()
			release, err := g.acquireTicket(life, pending, stuckClock{})
			if err != nil {
				t.Fatal(err)
			}
			release()
			if s := ledger.snapshot(); s.QualifiedWrites != 1 || s.DebitedBytes != 32768 {
				t.Fatalf("background grant debit was not exactly once: %+v", s)
			}
			assertSupervisorPool(t, g, 0, 0)
		})
	}
}

func TestSupervisorWALBlockedQueueResumesAfterReservedRecovery(t *testing.T) {
	g := NewGate()
	block, err := g.Acquire(context.Background(), ClassInteractive)
	if err != nil {
		t.Fatal(err)
	}
	defer block()
	h := newSupervisorHarness(t, g)
	ledger := reservationFixture(t)
	ledger.stats.AdmissionBytes = walSoftBytes
	h.ctx = context.WithValue(h.ctx, walRequestKey{}, &walRequest{ledger: ledger, bytes: 32768, qualified: true, class: ClassBackgroundMedia})
	for i := 0; i < 3; i++ {
		h.start("cmd.metadata", ClassBackgroundMedia, fmt.Sprint(i))
		waitForWaiters(t, g, i+1)
	}
	assertSupervisorPool(t, g, 2, 1)
	block()
	g.mu.Lock()
	blocked := !g.active && g.grant == nil
	g.mu.Unlock()
	if !blocked || ledger.snapshot().QualifiedWrites != 0 {
		t.Fatal("WAL-blocked background was granted or debited")
	}
	epoch, err := g.enterPressureHold()
	if err != nil {
		t.Fatal(err)
	}
	defer g.endRecovery(epoch)
	if err := g.enterBeginPause(epoch); err != nil {
		t.Fatal(err)
	}
	turn, err := g.acquireRecoveryTurn(context.Background(), epoch)
	if err != nil {
		t.Fatal(err)
	}
	ledger.reset()
	turn()
	assertSupervisorPool(t, g, 2, 1)
	if ledger.snapshot().QualifiedWrites != 0 {
		t.Fatal("paused background was debited during recovery")
	}
	g.endRecovery(epoch)
	for i := 0; i < 3; i++ {
		h.next(fmt.Sprint(i)).release()
	}
	if s := ledger.snapshot(); s.QualifiedWrites != 3 || s.DebitedBytes != 3*32768 {
		t.Fatalf("resumed background debit mismatch: %+v", s)
	}
	assertSupervisorPool(t, g, 0, 0)
}
