package dbwork

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func reservationFixture(t *testing.T) *walLedger {
	t.Helper()
	l := newWALLedger(filepath.Join(t.TempDir(), "state.sqlite"), 4096)
	l.native = true
	l.free = 1 << 40
	l.probeOK = true
	return l
}

func TestWALReservationsUseActualPageSizeAndExactSourcePlan(t *testing.T) {
	for _, size := range []int64{1024, 4096, 65536} {
		l := reservationFixture(t)
		l.pageSize = size
		ctx := WithPlan(context.Background(), WritePlan{Name: BackupHoldPhasePlanName, Pages: 4})
		r := walContextForLedger(ctx, l, ClassInteractive, backupHoldPhaseSQL).Value(walRequestKey{}).(*walRequest)
		if !r.qualified || r.bytes != 32+4*(size+24) {
			t.Fatalf("page size %d: %+v", size, r)
		}
		wrong := walContextForLedger(ctx, l, ClassInteractive, `DELETE FROM backup_blob_holds`).Value(walRequestKey{}).(*walRequest)
		if wrong.qualified {
			t.Fatal("a plan name qualified a different statement")
		}
		l.native = false
		unknown := walContextForLedger(ctx, l, ClassInteractive, backupHoldPhaseSQL).Value(walRequestKey{}).(*walRequest)
		if unknown.qualified {
			t.Fatal("unsupported I/O counted as qualified")
		}
	}
}

func TestWALReservationsKeepFailureDebitsAndMeasuredSpills(t *testing.T) {
	l := reservationFixture(t)
	r := &walRequest{ledger: l, name: "test", bytes: 65536, growth: 4096, qualified: true, class: ClassInteractive}
	r.take()
	// Failed BEGIN/rollback do not have a refund API. A short write also
	// retains the full debit, and rollback spill beyond it adds its cost.
	r.measured(4096, true)
	if got := l.snapshot().DebitedBytes; got != 65536 {
		t.Fatal(got)
	}
	r.take()
	r.measured(100000, true)
	s := l.snapshot()
	if s.DebitedBytes != 65536+100000 || s.Overruns != 1 || !l.blocked["test"] {
		t.Fatalf("%+v", s)
	}
	r.measured(0, false)
	if l.snapshot().UnknownIO != 1 {
		t.Fatal("unknown native accounting represented as zero")
	}
}

func TestWALReservationsReclaimOnlyAtVerifiedEmptyReset(t *testing.T) {
	l := reservationFixture(t)
	l.stats.DebitedBytes = walHardBytes
	l.stats.AdmissionBytes = walHardBytes
	if err := os.WriteFile(l.path+"-wal", make([]byte, 4096), 0600); err != nil {
		t.Fatal(err)
	}
	l.probe()
	if l.snapshot().DebitedBytes != walHardBytes {
		t.Fatal("probe refunded reservations")
	}
	l.reset()
	if l.snapshot().Resets != 0 {
		t.Fatal("nonempty file reclaimed reservations")
	}
	if err := os.Truncate(l.path+"-wal", 0); err != nil {
		t.Fatal(err)
	}
	l.probe()
	if l.snapshot().DebitedBytes != walHardBytes {
		t.Fatal("file shrink alone refunded")
	}
	l.reset()
	if s := l.snapshot(); s.DebitedBytes != 0 || s.Resets != 1 {
		t.Fatalf("%+v", s)
	}
}

func TestWALPressureRetainsUserTicketAndNeverRunsBackgroundAhead(t *testing.T) {
	l := reservationFixture(t)
	l.stats.DebitedBytes = walHardBytes
	l.stats.AdmissionBytes = walHardBytes
	g := NewGate()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	holder, err := g.Acquire(ctx, ClassInteractive)
	if err != nil {
		t.Fatal(err)
	}
	user := &walRequest{ledger: l, bytes: 32768, qualified: true, class: ClassInteractive}
	results := make(chan admissionResult, 2)
	go func() {
		release, err := g.Acquire(context.WithValue(ctx, walRequestKey{}, user), ClassInteractive)
		if err == nil {
			release()
		}
		results <- admissionResult{label: "user", err: err}
	}()
	deadline := time.Now().Add(time.Second)
	for g.Waiting()[ClassInteractive.String()] != 1 {
		if time.Now().After(deadline) {
			t.Fatal("user not queued")
		}
		time.Sleep(time.Millisecond)
	}
	queueTicket(t, g, ClassBackgroundMedia, "", "background", closedChannel(), results)
	holder()
	expectNoResult(t, results, 20*time.Millisecond)
	if user.ledger.snapshot().QualifiedWrites != 0 {
		t.Fatal("blocked ticket debited before selection")
	}
	// Simulate the checkpoint owner's verified reset; gate wake selects the
	// retained user before the already queued background ticket.
	l.reset()
	g.mu.Lock()
	g.grant = g.pickWinnerLocked()
	g.signalLocked()
	g.mu.Unlock()
	expectResult(t, results, "user")
	expectResult(t, results, "background")
}

func TestWALCancelledPressureTicketHasNoDebit(t *testing.T) {
	l := reservationFixture(t)
	l.stats.DebitedBytes = walHardBytes
	l.stats.AdmissionBytes = walHardBytes
	g := NewGate()
	r := &walRequest{ledger: l, bytes: 32768, qualified: true, class: ClassInteractive}
	ctx, cancel := context.WithTimeout(context.WithValue(context.Background(), walRequestKey{}, r), 20*time.Millisecond)
	defer cancel()
	_, err := g.Acquire(ctx, ClassInteractive)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	if s := l.snapshot(); s.QualifiedWrites != 0 || s.DebitedBytes != walHardBytes {
		t.Fatalf("%+v", s)
	}
}

func TestWALReservationSurvivesOverlappingHandleReplacement(t *testing.T) {
	db := testDB(t)
	l := ledgerFor(db)
	l.mu.Lock()
	l.stats.DebitedBytes += 12345
	want := l.stats.DebitedBytes
	l.mu.Unlock()
	replacement, err := OpenHandle(l.path, DefaultPolicy())
	if err != nil {
		t.Fatal(err)
	}
	defer replacement.Close()
	if ledgerFor(replacement) != l || WALReservations(replacement).DebitedBytes != want {
		t.Fatal("handle replacement lost the live ledger")
	}
}

func TestWALReservationUsesNativeFilenameAcrossAliases(t *testing.T) {
	db := testDB(t)
	l := ledgerFor(db)
	alias := filepath.Join(t.TempDir(), "alias.sqlite")
	if err := os.Symlink(l.path, alias); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	other, err := OpenHandle(alias, DefaultPolicy())
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	if ledgerFor(other) != l {
		t.Fatal("native filename alias created a second reservation ledger")
	}
}

func TestWALReservationLedgerRetiresWithLastNativeScope(t *testing.T) {
	db := testDB(t)
	l := ledgerFor(db)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	liveWALLedgers.Lock()
	defer liveWALLedgers.Unlock()
	if _, ok := liveWALLedgers.byPath[l.path]; ok {
		t.Fatal("closed handle retained reservation ledger")
	}
}

func TestUnqualifiedShadowReservationDoesNotEnableAdmissionPolicy(t *testing.T) {
	l := reservationFixture(t)
	r := &walRequest{ledger: l, bytes: walHardBytes + 1, class: ClassBackgroundMedia}
	r.take()
	r.measured(4096, true)
	if soft, hard := l.pressure(); soft || hard {
		t.Fatal("unqualified estimate enabled recovery policy")
	}
	r.retained(walHardBytes + 1)
	if _, hard := l.pressure(); !hard {
		t.Fatal("actual WAL extent ignored")
	}
}

func TestWALReservationDiskFailureRefusesWithoutEffect(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	l := reservationFixture(t)
	l.probeOK = false
	g := NewGate()
	r := &walRequest{ledger: l, bytes: 32768, qualified: true, class: ClassInteractive}
	start := time.Now()
	_, e := g.Acquire(context.WithValue(context.Background(), walRequestKey{}, r), ClassInteractive)
	if !errors.Is(e, ErrWriterCapacity) || time.Since(start) < walCapacityWait || time.Since(start) > walCapacityWait+time.Second {
		t.Fatal(e, time.Since(start))
	}
	if s := l.snapshot(); s.QualifiedWrites != 0 || s.DebitedBytes != 0 {
		t.Fatal(s)
	}
	release, e := g.Acquire(context.Background(), ClassInteractive)
	if e != nil {
		t.Fatal(e)
	}
	release()
}

func TestMBPinPagerEnforcementRemainsUnqualified(t *testing.T) {
	l := reservationFixture(t)
	ctx := WithPlan(context.Background(), WritePlan{Name: "catalog-one-mb-pin", Pages: 2048})
	r := walContextForLedger(ctx, l, ClassBackgroundMedia, "").Value(walRequestKey{}).(*walRequest)
	if r.qualified {
		t.Fatal("unproved MB pager profile enabled")
	}
}
