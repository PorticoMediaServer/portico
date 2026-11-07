package writer

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/storage/nativeinventory"
	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
)

func applyAndAcknowledgeScanOperation(t *testing.T, op *ScanRunOperation) {
	t.Helper()
	result, err := op.Apply(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err = result.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err = op.Ack(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestScanIssuerCloseRetryPreservesNativeReturnAndPredecessor(t *testing.T) {
	f := scanIssuerRuntime(t)
	d := f.driver
	op, err := d.PrepareCreate(t.Context(), ScanRunInput{7, "lifecycle-library", "lifecycle-new-source", "first-import"})
	if err != nil {
		t.Fatal(err)
	}
	original, err := op.Reference()
	if err != nil {
		t.Fatal(err)
	}
	applyAndAcknowledgeScanOperation(t, op)
	p := op.preparation
	holding, err := p.scope.Acquire()
	if err != nil {
		t.Fatal(err)
	}
	defer holding.Release()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err = op.Close(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal("canceled join did not retain custody", err)
	}
	if op.cleanup == nil || op.cleanup.preparation != p || op.closed || op.create == nil || op.last.Outcome.State != Applied || d.work == nil || d.birth == nil || d.preparation != p || d.operation != op {
		t.Fatal("failed Close lost retry or payload custody")
	}
	if d.writer.effective.authority.Usage() == ([4]governor.Amounts{}) {
		t.Fatal("failed join refunded preparation")
	}
	if d.predecessor != nil {
		t.Fatal("predecessor published before retirement")
	}
	holding.Release()
	if err = op.Close(t.Context()); err != nil {
		t.Fatal("joined Close retry", err)
	}
	if !op.closed || op.cleanup != nil || op.preparation != nil || op.create != nil || d.work != nil || d.birth != nil || d.operation != nil || d.preparation != nil || d.writer.effective.authority.Usage() != ([4]governor.Amounts{}) {
		t.Fatal("successful retry retained roots or authority")
	}
	if d.predecessor == nil || d.predecessor.incarnation != original.Scan.Receipt.Identity.Incarnation || d.predecessor.sequence != 1 || d.predecessor.generation != 0 {
		t.Fatal("retry lost exact predecessor")
	}
	if err = op.Close(t.Context()); err != nil {
		t.Fatal("duplicate Close", err)
	}
	claim, err := d.PrepareClaim(t.Context(), original)
	if err != nil {
		t.Fatal(err)
	}
	applyAndAcknowledgeScanOperation(t, claim)
	if err = claim.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestScanIssuerOtherSettledRunDoesNotInheritPredecessor(t *testing.T) {
	f := scanIssuerRuntime(t)
	d := f.driver
	if _, err := d.opening.executor.db.ExecContext(t.Context(), `INSERT INTO library_sources(id,library_id,configured_root,root,incarnation,root_identity,generation,identity_confirmed) VALUES('lifecycle-other-source','lifecycle-library','/media-other','/media-other','source-incarnation','source-root',23,1)`); err != nil {
		t.Fatal(err)
	}
	// Same existing controlled observer/runner, with no path-specific file row.
	d.observer = nativeinventory.NewObserver(&scanObservationFaultTransport{page: `{"Version":1,"RootIdentity":"source-root","DirectoryIdentity":"actual-directory","DirectoryRevision":"directory-revision","Complete":true,"Entries":[],"Issues":[]}`})
	a, err := d.PrepareCreate(t.Context(), ScanRunInput{7, "lifecycle-library", "lifecycle-new-source", "first-import"})
	if err != nil {
		t.Fatal(err)
	}
	aRef, err := a.Reference()
	if err != nil {
		t.Fatal(err)
	}
	applyAndAcknowledgeScanOperation(t, a)
	if err = a.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	aProof := d.predecessor
	b, err := d.PrepareCreate(t.Context(), ScanRunInput{7, "lifecycle-library", "lifecycle-other-source", "first-import"})
	if err != nil {
		t.Fatal(err)
	}
	applyAndAcknowledgeScanOperation(t, b)
	if err = b.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	bProof := d.predecessor
	if aProof == nil || bProof == nil || aProof.createOperation == bProof.createOperation {
		t.Fatal("different run anchors were not retained")
	}
	// The cache still protects its OWN original; this is not a blanket bypass.
	d.predecessor = aProof
	aProof.generation++
	bad, err := d.PrepareClaim(t.Context(), aRef)
	if err != nil {
		t.Fatal(err)
	}
	if result, err := bad.Apply(t.Context()); !errors.Is(err, ErrConflict) || result != nil {
		t.Fatal("same-run predecessor mismatch was ignored", result, err)
	}
	if err = bad.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	aProof.generation--
	d.predecessor = bProof
	claim, err := d.PrepareClaim(t.Context(), aRef)
	if err != nil {
		t.Fatal(err)
	}
	applyAndAcknowledgeScanOperation(t, claim)
	if err = claim.Close(t.Context()); err != nil {
		t.Fatal("run A was blocked by run B", err)
	}
	if d.predecessor.createOperation != aProof.createOperation || d.predecessor.generation != 1 {
		t.Fatal("current run did not replace the bounded cache")
	}
}

func TestScanIssuerLiveRenewalRefusesStaleGenerationCapability(t *testing.T) {
	f := scanIssuerRuntime(t)
	d := f.driver
	create, err := d.PrepareCreate(t.Context(), ScanRunInput{7, "lifecycle-library", "lifecycle-new-source", "first-import"})
	if err != nil {
		t.Fatal(err)
	}
	createRef, _ := create.Reference()
	applyAndAcknowledgeScanOperation(t, create)
	if err = create.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	claim, err := d.PrepareClaim(t.Context(), createRef)
	if err != nil {
		t.Fatal(err)
	}
	claimRef, _ := claim.Reference()
	applyAndAcknowledgeScanOperation(t, claim)
	oldLease, err := claim.Lease(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err = claim.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	*f.clock = f.clock.Add(time.Millisecond)
	renew, err := d.PrepareClaim(t.Context(), claimRef)
	if err != nil {
		t.Fatal(err)
	}
	renewRef, _ := renew.Reference()
	applyAndAcknowledgeScanOperation(t, renew)
	if err = renew.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if d.lease != oldLease || oldLease.generation != 1 || d.predecessor.generation != 2 {
		t.Fatal("stale capability setup changed")
	}
	*f.clock = f.clock.Add(time.Millisecond)
	stale, err := d.PrepareClaim(t.Context(), renewRef)
	if err != nil {
		t.Fatal(err)
	}
	if result, err := stale.Apply(t.Context()); err == nil || result != nil {
		t.Fatal("old capability authorized a live next generation", result, err)
	}
	if err = stale.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
}
