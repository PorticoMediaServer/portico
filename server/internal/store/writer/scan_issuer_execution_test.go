package writer

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/storage/nativeinventory"
	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
)

// Real opening/native Capture/governor/dispatcher/SC effects. The numerical
// forecasts and virtual clock are protocol inputs, not a production profile.
func scanIssuerRuntime(t *testing.T) *scanDriverFixture {
	t.Helper()
	f, p, ready := scanIssuerRuntimeInputs(t)
	if err := configureScanIssuer(t.Context(), f.driver, p, p, ready, time.Second); err != nil {
		t.Fatal(err)
	}
	return f
}

func scanIssuerRuntimeInputs(t *testing.T) (*scanDriverFixture, effectivePlan, scanPreparationPlan) {
	t.Helper()
	return scanIssuerRuntimePlans(t, newScanDriverFixture(t, scanIssuerRuntimeFixture))
}

func scanIssuerRuntimePlans(t *testing.T, f *scanDriverFixture) (*scanDriverFixture, effectivePlan, scanPreparationPlan) {
	t.Helper()
	p := effectivePlan{recipe: f.recipe, headerSync: Work{WALBytes: 32, Hold: 5 * time.Millisecond}, registration: nil,
		need: f.request.Need, io: f.request.IO, scanLifecycleMemory: commandMemoryBudget{4 << 20, 4 << 20}}
	for _, entry := range f.registry.Manifest() {
		p.steps = append(p.steps, statementStep{entry.ID, Work{16, 2000, 16, 65536, 100 * time.Microsecond}})
	}
	// The same actual complete Capture is reproduced, not a fixture adapter.
	w := f.driver.opening.executor
	// Registry carries no public receipt accessor. Capture's ordinary result is
	// obtained through the existing setup helper on the same owned connection.
	conn, err := w.db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	golden := captureEffectiveFixture(t, conn, w.config, scanLifecycleCommandDefinitions(), "scan_issuer_execution_test.go", "scan_driver_test.go", "scan_run_lifecycle_test.go")
	if err = conn.Close(); err != nil {
		t.Fatal(err)
	}
	p.registration = golden.Receipt()
	readyNeed := f.request.Need
	readyNeed[governor.MemoryBytes] = 8 << 20
	readyNeed[governor.WALBytes] = 0
	readyNeed[governor.IOBytes] = 0
	ready := scanPreparationPlan{need: readyNeed, budget: f.request.Budget, ready: time.Second}
	return f, p, ready
}

func TestScanIssuerCreateClaimAndRetainedResults(t *testing.T) {
	f := scanIssuerRuntime(t)
	d := f.driver
	op, err := d.PrepareCreate(t.Context(), ScanRunInput{LibraryID: 7, LibraryKey: "lifecycle-library", SourceID: "lifecycle-new-source", ScanMode: "first-import"})
	if err != nil {
		t.Fatal("prepare create", err)
	}
	ref, err := op.Reference()
	if err != nil {
		t.Fatal(err)
	}
	result, err := op.Apply(t.Context())
	if err != nil {
		t.Fatal("create", err)
	}
	value, err := result.Snapshot(t.Context())
	if err != nil || value.Outcome.State != Applied || value.AcceptedRunRevision != 1 || value.LeaseOwner != 0 {
		t.Fatal(value, err)
	}
	if _, err = op.Ack(t.Context()); err == nil {
		t.Fatal("ACK before consumer release")
	}
	if err = result.Close(); err != nil {
		t.Fatal(err)
	}
	ack, err := op.Ack(t.Context())
	if err != nil || ack.AcknowledgedThrough != 1 {
		t.Fatal("create ACK", ack, err)
	}
	if d.writer.effective.authority.Usage() == ([4]governor.Amounts{}) {
		t.Fatal("retained original/result reservation refunded before operation Close")
	}
	if err = op.Close(t.Context()); err != nil {
		t.Fatal("create close", err)
	}
	if d.writer.effective.authority.Usage() != ([4]governor.Amounts{}) {
		t.Fatal("create reservation retained after actual release")
	}
	claim, err := d.PrepareClaim(t.Context(), ref)
	if err != nil {
		t.Fatal("prepare claim", err)
	}
	claimRef, err := claim.Reference()
	if err != nil || claimRef.Scan.Receipt.Identity.Sequence != 2 {
		t.Fatal(claimRef, err)
	}
	result, err = claim.Apply(t.Context())
	if err != nil {
		t.Fatal("claim", err)
	}
	value, err = result.Snapshot(t.Context())
	if err != nil || value.LeaseOwner != d.leaseOwner || value.LeaseGeneration != 1 || value.AcceptedRunRevision != 2 || value.AcknowledgedSequence != 1 {
		t.Fatal(value, err)
	}
	if err = result.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err = claim.Lease(t.Context()); err == nil {
		t.Fatal("historical saved ACK issued current lease")
	}
	if ack, err = claim.Ack(t.Context()); err != nil || ack.AcknowledgedThrough != 2 {
		t.Fatal("claim ACK", ack, err)
	}
	lease, err := claim.Lease(t.Context())
	if err != nil || lease.driver != d || lease.generation != 1 {
		t.Fatal("actual lease", lease, err)
	}
	if err = claim.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if d.writer.effective.authority.Usage() != ([4]governor.Amounts{}) {
		t.Fatal("claim reservation leaked")
	}
	// FIRST includes fenced SAME-owner live renewal. The original lease stays
	// historical; only the post-ACK native snapshot issues its new generation.
	early, err := d.PrepareClaim(t.Context(), claimRef)
	if err != nil {
		t.Fatal(err)
	}
	if result, err := early.Apply(t.Context()); !errors.Is(err, ErrBusy) || result != nil {
		t.Fatal("unchanged clock synthesized a lease extension", result, err)
	}
	if err = early.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	*f.clock = f.clock.Add(time.Millisecond)
	renew, err := d.PrepareClaim(t.Context(), claimRef)
	if err != nil {
		t.Fatal("prepare live renewal", err)
	}
	result, err = renew.Apply(t.Context())
	if err != nil {
		t.Fatal("live renewal", err)
	}
	value, err = result.Snapshot(t.Context())
	if err != nil || value.LeaseGeneration != 2 || value.AcceptedRunRevision != 3 || value.LeaseExpiresMS <= lease.expiresMS {
		t.Fatal("live renewal did not advance actual fences", value, err)
	}
	if err = result.Close(); err != nil {
		t.Fatal(err)
	}
	ackBase, ackCancel := context.WithCancel(t.Context())
	defer ackCancel()
	ackCtx := &scanIssuerPublicationContext{Context: ackBase, cancel: ackCancel, installed: func() bool { return renew.acknowledged }}
	if ack, err = renew.Ack(ackCtx); !errors.Is(err, context.Canceled) || ack != (AckResult{}) || renew.acknowledged {
		t.Fatal("discarded ACK authorized progression", ack, err)
	}
	if _, err = renew.Lease(t.Context()); err == nil {
		t.Fatal("failed ACK publication issued a lease")
	}
	if _, err = renew.Ack(t.Context()); err != nil {
		t.Fatal(err)
	}
	leaseBase, leaseCancel := context.WithCancel(t.Context())
	defer leaseCancel()
	leaseCtx := &scanIssuerPublicationContext{Context: leaseBase, cancel: leaseCancel, installed: func() bool { return d.lease != nil && d.lease.generation == 2 }}
	if discarded, err := renew.Lease(leaseCtx); !errors.Is(err, context.Canceled) || discarded != nil || d.lease != nil {
		t.Fatal("discarded lease remained current", discarded, err)
	}
	newLease, err := renew.Lease(t.Context())
	if err != nil || newLease.generation != 2 || !lease.revoked.Load() {
		t.Fatal("old lease survived current native issuance", newLease, err)
	}
	if err = renew.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err = d.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestScanIssuerFailedPreparationCanReleaseAndRetry(t *testing.T) {
	for _, fault := range []string{"missing-source", "invalid-native-page"} {
		t.Run(fault, func(t *testing.T) {
			f := scanIssuerRuntime(t)
			d := f.driver
			in := ScanRunInput{7, "lifecycle-library", "lifecycle-new-source", "first-import"}
			observer := d.observer
			if fault == "missing-source" {
				in.SourceID = "no-source"
			} else {
				d.observer = nativeinventory.NewObserver(&scanObservationFaultTransport{page: `{}`})
			}
			if op, err := d.PrepareCreate(t.Context(), in); err == nil || op != nil {
				t.Fatal("failed preparation published a capability", op, err)
			}
			if err := d.ReleasePending(t.Context()); err != nil {
				t.Fatal(err)
			}
			if d.preparation != nil || d.work != nil || d.birth != nil || d.operation != nil || d.writer.effective.authority.Usage() != ([4]governor.Amounts{}) {
				t.Fatal("failed preparation retained buffers or authority after release")
			}
			d.observer, in.SourceID = observer, "lifecycle-new-source"
			op, err := d.PrepareCreate(t.Context(), in)
			if err != nil {
				t.Fatal("retry after joined release", err)
			}
			if err = d.ReleasePending(t.Context()); err != nil {
				t.Fatal(err)
			}
			if !op.closed || op.preparation != nil || op.create != nil || op.reference != (ScanRunReference{}) || d.operation != nil || d.writer.effective.authority.Usage() != ([4]governor.Amounts{}) {
				t.Fatal("abandoned operation outlived its refunded memory")
			}
		})
	}
}

type scanIssuerPublicationContext struct {
	context.Context
	cancel    context.CancelFunc
	installed func() bool
	fired     atomic.Bool
}

func (c *scanIssuerPublicationContext) Err() error {
	if !c.fired.Load() && c.installed() {
		c.fired.Store(true)
		c.cancel()
	}
	return c.Context.Err()
}

func TestScanIssuerCanceledConfigurationCannotRemainUsable(t *testing.T) {
	f, plan, ready := scanIssuerRuntimeInputs(t)
	d := f.driver
	base, cancel := context.WithCancel(t.Context())
	defer cancel()
	ctx := &scanIssuerPublicationContext{Context: base, cancel: cancel, installed: func() bool { return d.leaseOwner != 0 }}
	err := configureScanIssuer(ctx, d, plan, plan, ready, time.Second)
	if !errors.Is(err, context.Canceled) || !errors.Is(d.current(), ErrClosed) {
		t.Fatal("failed configuration left live issuer authority", err, d.current())
	}
	if d.writer.effective.authority.Usage() != ([4]governor.Amounts{}) {
		t.Fatal("configuration minted an unowned holding")
	}
}
