package dbwork

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

type admissionResult struct {
	label string
	err   error
}

// queueTicket starts one waiter and returns once the gate counts it. Its
// result arrives on results; acquired waiters hold until hold is closed, so a
// test controls when the next one runs.
func queueTicket(t *testing.T, g *Gate, class Class, owner, label string, hold <-chan struct{}, results chan<- admissionResult) context.CancelFunc {
	t.Helper()
	before := 0
	for _, count := range g.Waiting() {
		before += count
	}
	refused := g.Admission().Refused
	ctx, cancel := context.WithCancel(WithOwner(context.Background(), owner))
	go func() {
		release, err := g.Acquire(ctx, class)
		if err == nil {
			<-hold
			release()
		}
		results <- admissionResult{label: label, err: err}
	}()
	// A ticket that displaces another at a full pool leaves the count where it
	// was; the refusal it caused is the sign that it is queued.
	deadline := time.Now().Add(5 * time.Second)
	for {
		total := 0
		for _, count := range g.Waiting() {
			total += count
		}
		if total > before || g.Admission().Refused > refused {
			return cancel
		}
		if time.Now().After(deadline) {
			t.Fatalf("ticket %s never queued: %v", label, g.Waiting())
		}
		time.Sleep(time.Millisecond)
	}
}

func closedChannel() chan struct{} {
	c := make(chan struct{})
	close(c)
	return c
}

// fillPool queues WriterQueueCapacity interactive tickets with distinct
// owners behind a held gate.
func fillPool(t *testing.T, g *Gate, results chan<- admissionResult) {
	t.Helper()
	for i := 0; i < WriterQueueCapacity; i++ {
		queueTicket(t, g, ClassInteractive, fmt.Sprintf("o%d", i), fmt.Sprintf("fill-%d", i), closedChannel(), results)
	}
}

func TestPoolHoldsNinetyNineQueuedPlusTheHolder(t *testing.T) {
	g := NewGate()
	hold, err := g.Acquire(context.Background(), ClassInteractive)
	if err != nil {
		t.Fatal(err)
	}
	results := make(chan admissionResult, 2*WriterQueueCapacity)
	fillPool(t, g, results)
	if stats := g.Admission(); stats.UserQueued != WriterQueueCapacity || stats.Refused != 0 {
		t.Fatalf("pool not full as expected: %+v", stats)
	}
	// The hundredth queued user write, of the same class and a fresh owner,
	// has nothing it may displace.
	if _, err := g.Acquire(WithOwner(context.Background(), "late"), ClassInteractive); !errors.Is(err, ErrWriterCapacity) {
		t.Fatalf("a write beyond the pool returned %v", err)
	}
	if !Overloaded(ErrWriterCapacity) || Classify(fmt.Errorf("save: %w", ErrWriterCapacity)) != KindCapacity {
		t.Fatal("a capacity refusal is not classified as overload")
	}
	// Background work is never refused: it waits outside the users' share.
	queueTicket(t, g, ClassBackgroundMedia, "", "background", closedChannel(), results)
	stats := g.Admission()
	if stats.Refused != 1 || stats.RefusedByClass[ClassInteractive.String()] != 1 || stats.BackgroundQueued != 0 || stats.Deferred != 1 {
		t.Fatalf("admission accounting after refusal: %+v", stats)
	}
	hold()
	served := 0
	for i := 0; i < WriterQueueCapacity+1; i++ {
		result := <-results
		if result.err != nil {
			t.Fatalf("%s failed: %v", result.label, result.err)
		}
		if result.label == "background" && served != WriterQueueCapacity {
			t.Fatalf("background ran at position %d, ahead of queued users", served)
		}
		served++
	}
	if g.ActiveOrWaiting() {
		t.Fatal("pool did not drain")
	}
}

func TestFullPoolRefusesTheLeastImportantNewestTicketForAHigherClass(t *testing.T) {
	g := NewGate()
	hold, err := g.Acquire(context.Background(), ClassInteractive)
	if err != nil {
		t.Fatal(err)
	}
	results := make(chan admissionResult, 2*WriterQueueCapacity)
	for i := 0; i < WriterQueueCapacity-1; i++ {
		queueTicket(t, g, ClassInteractive, fmt.Sprintf("o%d", i), fmt.Sprintf("fill-%d", i), closedChannel(), results)
	}
	queueTicket(t, g, ClassForegroundTransfer, "transfer-owner", "transfer", closedChannel(), results)
	// A fence at a full pool takes the transfer's place: it is the least
	// important user ticket, and the newest of its class.
	queueTicket(t, g, ClassSecurityFence, "fence-owner", "fence", closedChannel(), results)
	if result := <-results; result.label != "transfer" || !errors.Is(result.err, ErrWriterCapacity) {
		t.Fatalf("displaced %s with %v, want the transfer refused", result.label, result.err)
	}
	// The next fence displaces the newest interactive ticket below it.
	queueTicket(t, g, ClassSecurityFence, "another", "fence-2", closedChannel(), results)
	if result := <-results; result.label != fmt.Sprintf("fill-%d", WriterQueueCapacity-2) || !errors.Is(result.err, ErrWriterCapacity) {
		t.Fatalf("displaced %s with %v, want the newest interactive ticket", result.label, result.err)
	}
	// An interactive arrival has nothing less important to displace.
	if _, err := g.Acquire(WithOwner(context.Background(), "late"), ClassInteractive); !errors.Is(err, ErrWriterCapacity) {
		t.Fatalf("an interactive write displaced an equal ticket: %v", err)
	}
	hold()
	for i := 0; i < WriterQueueCapacity; i++ {
		result := <-results
		if result.err != nil {
			t.Fatalf("%s failed: %v", result.label, result.err)
		}
		if (i < 2) != (result.label == "fence" || result.label == "fence-2") {
			t.Fatalf("%s was served at position %d", result.label, i)
		}
	}
}

func TestFullPoolRefusesAnOverShareOwnerFirst(t *testing.T) {
	g := NewGate()
	hold, err := g.Acquire(context.Background(), ClassInteractive)
	if err != nil {
		t.Fatal(err)
	}
	results := make(chan admissionResult, 2*WriterQueueCapacity)
	// Below capacity one owner may queue past its share.
	for i := 0; i < 3; i++ {
		queueTicket(t, g, ClassInteractive, "busy-device", fmt.Sprintf("busy-%d", i), closedChannel(), results)
	}
	for i := 3; i < WriterQueueCapacity; i++ {
		queueTicket(t, g, ClassInteractive, fmt.Sprintf("o%d", i), fmt.Sprintf("fill-%d", i), closedChannel(), results)
	}
	// A fresh owner at a full pool displaces the over-share owner's newest
	// ticket, not anybody else's.
	queueTicket(t, g, ClassInteractive, "fresh", "fresh", closedChannel(), results)
	if result := <-results; result.label != "busy-2" || !errors.Is(result.err, ErrWriterCapacity) {
		t.Fatalf("displaced %s with %v, want busy-2", result.label, result.err)
	}
	// The busy owner is now at its share, so its next write is the one refused.
	if _, err := g.Acquire(WithOwner(context.Background(), "busy-device"), ClassInteractive); !errors.Is(err, ErrWriterCapacity) {
		t.Fatalf("an owner at its share displaced another owner: %v", err)
	}
	if g.Admission().UserQueued != WriterQueueCapacity {
		t.Fatalf("pool count drifted: %+v", g.Admission())
	}
	hold()
	for i := 0; i < WriterQueueCapacity; i++ {
		if result := <-results; result.err != nil {
			t.Fatalf("%s failed: %v", result.label, result.err)
		}
	}
}

func TestCancellationFreesExactlyOneTicket(t *testing.T) {
	g := NewGate()
	hold, err := g.Acquire(context.Background(), ClassInteractive)
	if err != nil {
		t.Fatal(err)
	}
	results := make(chan admissionResult, 2*WriterQueueCapacity)
	cancels := make([]context.CancelFunc, 0, WriterQueueCapacity)
	for i := 0; i < WriterQueueCapacity; i++ {
		cancels = append(cancels, queueTicket(t, g, ClassInteractive, "shared", fmt.Sprintf("fill-%d", i), closedChannel(), results))
	}
	cancels[40]()
	if result := <-results; result.label != "fill-40" || !errors.Is(result.err, context.Canceled) {
		t.Fatalf("cancellation reported %s: %v", result.label, result.err)
	}
	if stats := g.Admission(); stats.UserQueued != WriterQueueCapacity-1 {
		t.Fatalf("cancellation freed %d tickets", WriterQueueCapacity-stats.UserQueued)
	}
	// Exactly one place opened: the next write fits and the one after does not.
	queueTicket(t, g, ClassInteractive, "shared", "replacement", closedChannel(), results)
	if _, err := g.Acquire(WithOwner(context.Background(), "shared"), ClassInteractive); !errors.Is(err, ErrWriterCapacity) {
		t.Fatalf("a second write fit into one freed ticket: %v", err)
	}
	hold()
	for i := 0; i < WriterQueueCapacity; i++ {
		if result := <-results; result.err != nil {
			t.Fatalf("%s failed: %v", result.label, result.err)
		}
	}
	if g.ActiveOrWaiting() || g.Admission().UserQueued != 0 {
		t.Fatalf("tickets left behind: %+v", g.Admission())
	}
}

func TestOwnersRotateWithinAClassAndStayFIFO(t *testing.T) {
	g := NewGate()
	hold, err := g.Acquire(context.Background(), ClassInteractive)
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	order := []string{}
	results := make(chan admissionResult, 8)
	for _, entry := range [][2]string{{"a", "a1"}, {"a", "a2"}, {"a", "a3"}, {"b", "b1"}, {"c", "c1"}, {"b", "b2"}} {
		owner, label := entry[0], entry[1]
		before := 0
		for _, count := range g.Waiting() {
			before += count
		}
		go func() {
			release, err := g.Acquire(WithOwner(context.Background(), owner), ClassInteractive)
			if err == nil {
				mu.Lock()
				order = append(order, label)
				mu.Unlock()
				release()
			}
			results <- admissionResult{label: label, err: err}
		}()
		waitForWaiters(t, g, before+1)
	}
	hold()
	for i := 0; i < 6; i++ {
		if result := <-results; result.err != nil {
			t.Fatal(result.err)
		}
	}
	want := []string{"a1", "b1", "c1", "a2", "b2", "a3"}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("owner order %v, want %v", order, want)
		}
	}
}

// Many owners and classes arrive, cancel and are refused at once. Whatever the
// interleaving, the pool never exceeds its bound and every ticket is accounted
// for when the dust settles.
func TestAdmissionUnderConcurrentChurn(t *testing.T) {
	g := NewGate()
	var wg sync.WaitGroup
	var peak int
	var peakMu sync.Mutex
	classes := []Class{ClassSecurityFence, ClassEstablishedPlayback, ClassInteractive, ClassForegroundTransfer, ClassBackgroundMedia, ClassMaintenance}
	for worker := 0; worker < 300; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			life := supervisedGateContext(context.Background(), fmt.Sprintf("test.supervisor.%d", worker%17))
			for round := 0; round < 20; round++ {
				ctx := WithOwner(life, fmt.Sprintf("o%d", worker%17))
				class := classes[(worker+round)%len(classes)]
				var cancel context.CancelFunc
				if (worker+round)%5 == 0 {
					ctx, cancel = context.WithTimeout(ctx, time.Duration(worker%3)*time.Millisecond)
				}
				release, err := g.Acquire(ctx, class)
				if cancel != nil {
					cancel()
				}
				if err != nil {
					if class.Background() && errors.Is(err, ErrWriterCapacity) {
						t.Errorf("background acquisition refused: %v", err)
					}
					if !errors.Is(err, ErrWriterCapacity) && !errors.Is(err, context.DeadlineExceeded) {
						t.Error(err)
					}
					continue
				}
				stats := g.Admission()
				queued := stats.UserQueued + stats.BackgroundQueued
				peakMu.Lock()
				peak = max(peak, queued)
				peakMu.Unlock()
				g.mu.Lock()
				for _, background := range []Class{ClassBackgroundMedia, ClassMaintenance} {
					for _, owner := range g.queues[background].rotation {
						n := len(owner.tickets)
						if g.active && g.holderClass == background && g.holderOwner == owner.owner {
							n++
						}
						if n > backgroundTicketLimit(background) {
							t.Errorf("%s/%s has %d outstanding", background, owner.owner, n)
						}
					}
				}
				g.mu.Unlock()
				release()
			}
		}(worker)
	}
	wg.Wait()
	if peak > WriterQueueCapacity {
		t.Fatalf("pool held %d admitted tickets", peak)
	}
	stats := g.Admission()
	if g.ActiveOrWaiting() || stats.UserQueued != 0 || stats.BackgroundQueued != 0 || stats.Deferred != 0 {
		t.Fatalf("tickets left behind: %+v", stats)
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if len(g.ownerQueued) != 0 {
		t.Fatalf("owner counts leaked: %v", g.ownerQueued)
	}
	for _, class := range Classes() {
		for _, q := range []*classQueue{&g.queues[class], &g.deferred[class]} {
			if q.count != 0 || len(q.rotation) != 0 || len(q.byOwner) != 0 {
				t.Fatalf("%s queue leaked: %+v", class, q)
			}
		}
	}
}
