package governor

import (
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

func checkpointSetup(t *testing.T) (Governor, *Authority, *testClock, *WALGate) {
	t.Helper()
	base, _, clock := setup(t)
	cfg := base.(*ledger).config
	wal := gate(t)
	cfg.WAL = wal
	g, a, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	publish(t, wal, walSample(4, 4), clock.now())
	return g, a, clock, wal
}
func checkpointRequest(clock *testClock) Request {
	r := request(clock, Recovery)
	r.Key.ID = "checkpoint"
	r.Need[MemoryBytes] = 16
	r.Need[CPUMilli] = 1
	r.Need[IOBytes] = 16
	r.MaxHold = 5 * time.Millisecond
	r.Budget = UnitBudget{RawRows: 1, VMOps: 100, Pages: 8}
	return r
}
func checkpointWriter(clock *testClock, cl Class) Request {
	r := request(clock, cl)
	r.Writer = true
	r.MaxHold = 5 * time.Millisecond
	r.Need[WALBytes] = 32
	r.Budget = UnitBudget{RawRows: 1, VMOps: 100, Pages: 8}
	return r
}
func checkpointGrant(t *testing.T, a *Authority, r Request) *Grant {
	t.Helper()
	d := a.tryCheckpoint(r)
	if d.Grant == nil || d.Refusal != nil {
		t.Fatal("checkpoint refused", d)
	}
	return d.Grant
}
func checkpointRefused(t *testing.T, a *Authority, r Request, want RefusalCode) {
	t.Helper()
	d := a.tryCheckpoint(r)
	if d.Grant != nil || d.Refusal == nil || d.Refusal.Code != want || d.Refusal.RetryAt.IsZero() {
		t.Fatal("unexpected checkpoint admission", d, want)
	}
}

func TestCheckpointExcludesWritersUntilPhysicalRetirement(t *testing.T) {
	g, a, clock, wal := checkpointSetup(t)
	r := checkpointRequest(clock)
	before := wal.Observation()
	cp := checkpointGrant(t, a, r)
	if err := a.Check(cp, r); err != nil {
		t.Fatal(err)
	}
	during := wal.Observation()
	if during == before {
		t.Fatal("checkpoint admission did not invalidate earlier samples")
	}
	for _, cl := range []Class{Recovery, Foreground, ScanMinimum, Background} {
		refused(t, g, checkpointWriter(clock, cl), Capacity)
	}
	checkpointRefused(t, a, r, Capacity)
	if wal.Publish(before, walSample(4, 4), clock.now()) != ErrWALObservation || wal.Publish(during, walSample(4, 4), clock.now()) != ErrWALObservation {
		t.Fatal("checkpoint accepted a pre/during-call WAL sample")
	}
	// Reads remain concurrent; an unrelated physical return cannot clear the turn.
	for _, cl := range []Class{Recovery, Foreground, ScanMinimum, Background} {
		retire(t, a, grant(t, g, request(clock, cl)))
	}
	refused(t, g, checkpointWriter(clock, Foreground), Capacity)
	g.Cancel(cp)
	if a.Check(cp, r) != ErrGrant || a.Usage()[Recovery] != r.Need {
		t.Fatal("cancel lost checkpoint debt")
	}
	refused(t, g, checkpointWriter(clock, Foreground), Capacity)
	checkpointRefused(t, a, r, Capacity)
	retire(t, a, cp)
	refused(t, g, checkpointWriter(clock, Foreground), FailSafe)
	if wal.Publish(during, walSample(4, 4), clock.now()) != ErrWALObservation {
		t.Fatal("pre-return checkpoint sample revived")
	}
	publish(t, wal, walSample(4, 4), clock.now())
	if wal.ResetCount() != 0 {
		t.Fatal("checkpoint completion invented a reset")
	}
	writer := grant(t, g, checkpointWriter(clock, Foreground))
	checkpointRefused(t, a, r, Capacity)
	g.Cancel(writer)
	checkpointRefused(t, a, r, Capacity)
	retire(t, a, writer)
	// Stale headroom must not prevent a recovery checkpoint itself.
	cp = checkpointGrant(t, a, r)
	retire(t, a, cp)
	if a.Retired(cp) != ErrRetirement || a.Usage() != [4]Amounts{} {
		t.Fatal("duplicate CP return or custody leak")
	}
}

func TestCheckpointExpiryFenceAndForgedReturnKeepTurn(t *testing.T) {
	g, a, clock, wal := checkpointSetup(t)
	r := checkpointRequest(clock)
	cp := checkpointGrant(t, a, r)
	a.EnterStatic()
	clock.ms.Store(2000)
	if a.Check(cp, r) != ErrGrant || a.Recover() {
		t.Fatal("fence/expiry claimed physical exit")
	}
	next := checkpointRequest(clock)
	fake := &Grant{id: cp.id, epoch: cp.epoch, issuer: cp.issuer}
	_, other, _, _ := checkpointSetup(t)
	if a.Retired(fake) != ErrRetirement || other.Retired(cp) != ErrRetirement {
		t.Fatal("forged checkpoint retirement accepted")
	}
	refused(t, g, checkpointWriter(clock, Foreground), Capacity)
	checkpointRefused(t, a, next, Capacity)
	if a.Usage()[Recovery] != r.Need {
		t.Fatal("old checkpoint lost physical debt")
	}
	retire(t, a, cp)
	if !a.Recover() {
		t.Fatal("physical retirement did not unblock recovery")
	}
	refused(t, g, checkpointWriter(clock, Foreground), FailSafe)
	publish(t, wal, walSample(5, 5), clock.now())
	retire(t, a, grant(t, g, checkpointWriter(clock, Foreground)))
}

func TestCheckpointNeedsPrivateQualifiedAdmission(t *testing.T) {
	g, a, clock, wal := checkpointSetup(t)
	base := checkpointRequest(clock)
	changes := []func(*Request){
		func(r *Request) { r.Class = Foreground }, func(r *Request) { r.Writer = true; r.Need[WALBytes] = 1 },
		func(r *Request) { r.Need[WALBytes] = 1 }, func(r *Request) { r.Need[Reader] = 0 },
		func(r *Request) { r.Need[MemoryBytes] = 0 }, func(r *Request) { r.Need[CPUMilli] = 0 }, func(r *Request) { r.Need[IOBytes] = 0 },
		func(r *Request) { r.Budget.RawRows = 0 }, func(r *Request) { r.Budget.VMOps = 0 }, func(r *Request) { r.Budget.Pages = 0 },
		func(r *Request) { r.MaxHold = 0 }, func(r *Request) { r.MaxHold = 26 * time.Millisecond },
	}
	version := wal.Observation()
	for _, change := range changes {
		r := base
		change(&r)
		checkpointRefused(t, a, r, Unqualified)
		if a.Usage() != [4]Amounts{} || wal.Observation() != version {
			t.Fatal("refused checkpoint altered custody/generation")
		}
	}
	oversized := base
	oversized.Need[MemoryBytes] = 129
	checkpointRefused(t, a, oversized, Capacity)
	expired := base
	expired.Deadline = clock.now()
	checkpointRefused(t, a, expired, Deadline)
	ordinary := grant(t, g, base) // An ordinary read vector is NOT a checkpoint capability.
	if wal.Observation() != version {
		t.Fatal("worker selected a private checkpoint turn")
	}
	writer := grant(t, g, checkpointWriter(clock, Foreground))
	retire(t, a, ordinary)
	checkpointRefused(t, a, base, Capacity)
	retire(t, a, writer)
	noWAL, noAuthority, _ := setup(t)
	checkpointRefused(t, noAuthority, base, Unqualified)
	if noWAL == nil {
		t.Fatal("invalid pure-ledger fixture")
	}
}

func TestCheckpointSharedWALGateAndBoundedContention(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		g, a, clock, wal := checkpointSetup(t)
		cfg := g.(*ledger).config
		other, otherAuthority, err := New(cfg)
		if err != nil {
			t.Fatal(err)
		}
		cp := checkpointGrant(t, a, checkpointRequest(clock))
		refused(t, other, checkpointWriter(clock, Foreground), Capacity)
		checkpointRefused(t, otherAuthority, checkpointRequest(clock), Capacity)
		if otherAuthority.Retired(cp) != ErrRetirement {
			t.Fatal("shared gate bypassed ledger identity")
		}
		retire(t, a, cp)
		wal.mu.Lock()
		done := make(chan Decision, 1)
		go func() { done <- a.tryCheckpoint(checkpointRequest(clock)) }()
		synctest.Wait()
		select {
		case d := <-done:
			wal.mu.Unlock()
			t.Fatal("contention became refusal", d)
		default:
		}
		wal.mu.Unlock()
		d := <-done
		if d.Grant == nil || d.Refusal != nil {
			t.Fatal(d)
		}
		retire(t, a, d.Grant)

		if a.Usage() != [4]Amounts{} {
			t.Fatal("contended admission left debt")
		}
	})
}

func TestCheckpointWriterAdmissionRace(t *testing.T) {
	g, a, clock, wal := checkpointSetup(t)
	for i := 0; i < 128; i++ {
		publish(t, wal, walSample(4, 4), clock.now())
		start := make(chan struct{})
		var wg sync.WaitGroup
		wg.Add(2)
		var cp, writer Decision
		go func() { defer wg.Done(); <-start; cp = a.tryCheckpoint(checkpointRequest(clock)) }()
		go func() { defer wg.Done(); <-start; writer = g.Try(checkpointWriter(clock, Foreground)) }()
		close(start)
		wg.Wait()
		if (cp.Grant == nil) == (writer.Grant == nil) {
			t.Fatal("writer/checkpoint overlap or lost reservation", cp, writer)
		}
		if cp.Grant != nil {
			refused(t, g, checkpointWriter(clock, Foreground), Capacity)
			retire(t, a, cp.Grant)
		} else {
			checkpointRefused(t, a, checkpointRequest(clock), Capacity)
			retire(t, a, writer.Grant)
		}
		if a.Usage() != [4]Amounts{} {
			t.Fatal("race leaked resource custody")
		}
	}
}
