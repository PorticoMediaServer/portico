package scanbench

import (
	"context"

	"github.com/PorticoMediaServer/portico/server/internal/store/qual/scanclock"

	"github.com/PorticoMediaServer/portico/server/internal/store/writer"
)

// LifecycleTurn binds the harness to W's real opaque issuer operation. No
// factory, fixture authority, profile, scope, clock or owner is supplied here.
// On every error retain this object and retry only Resume on this SAME original.
// A process restart uses W.OpenRun with the saved original; it cannot restore
// this in-memory lease or reconstruct first-visibility timing.
type LifecycleObservation struct {
	Original                             writer.ScanRunReference
	AcceptedRunRevision, LeaseGeneration int64
}

type LifecycleTurn struct {
	operation                                                               *writer.ScanRunOperation
	result                                                                  *writer.ScanRunResult
	journal                                                                 *ReceiptJournal
	reference                                                               writer.ScanRunReference
	value                                                                   writer.ScanRunSnapshot
	lease                                                                   *writer.ScanRunLease
	saved, snapshotted, submitted, consumed, released, acknowledged, closed bool
	observed                                                                LifecycleObservation
	journalErr                                                              error
}

func PrepareCreate(ctx context.Context, driver *writer.ScanDriver, input writer.ScanRunInput, journal *ReceiptJournal) (*LifecycleTurn, error) {
	op, err := driver.PrepareCreate(ctx, input)
	if err != nil {
		return nil, err
	}
	return retainLifecycle(op, journal)
}

func PrepareClaim(ctx context.Context, driver *writer.ScanDriver, original writer.ScanRunReference, journal *ReceiptJournal) (*LifecycleTurn, error) {
	op, err := driver.PrepareClaim(ctx, original)
	if err != nil {
		return nil, err
	}
	return retainLifecycle(op, journal)
}

func retainLifecycle(op *writer.ScanRunOperation, journal *ReceiptJournal) (*LifecycleTurn, error) {
	a := &LifecycleTurn{operation: op, journal: journal}
	ref, err := op.Reference()
	if err != nil {
		return a, err
	}
	a.reference = ref
	// A failed save leaves the preparation physically owned. Caller Close
	// joins it; no Apply or replacement command is attempted by this function.
	if err := journal.save(ref.Scan, ref.Purpose, nil); err != nil {
		return a, err
	}
	a.saved = true
	return a, nil
}

func (a *LifecycleTurn) Original() writer.ScanRunReference {
	if a == nil {
		return writer.ScanRunReference{}
	}
	return a.reference
}

// Resume submits at most once, then resolves only the original operation.
// Before ACK it durably consumes the actual snapshot and closes that result.
// Claim Lease performs W's ACTUAL post-ACK read; historical snapshot fields are
// never converted into a capability. All outputs remain zero until Close joins.
func (a *LifecycleTurn) Resume(ctx context.Context) (LifecycleObservation, *writer.ScanRunLease, error) {
	if a == nil || a.operation == nil || a.reference == (writer.ScanRunReference{}) || !a.saved || ctx == nil || a.closed {
		return LifecycleObservation{}, nil, writer.ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return LifecycleObservation{}, nil, err
	}
	if a.result == nil {
		var err error
		if !a.submitted {
			a.submitted = true
			a.result, err = a.operation.Apply(ctx)
		} else {
			a.result, err = a.operation.Resolve(ctx)
		}
		if err != nil {
			a.result = nil
			return LifecycleObservation{}, nil, err
		}
		if a.result == nil {
			return LifecycleObservation{}, nil, writer.ErrResolveRequired
		}
	}
	if a.journalErr != nil {
		return LifecycleObservation{}, nil, a.journalErr
	}
	if !a.snapshotted {
		value, err := a.result.Snapshot(ctx)
		if err != nil {
			a.journalErr = err
			return LifecycleObservation{}, nil, err
		}
		// ResultRetired carries no consumable body. Never synthesize Applied
		// or a lease from it, or from nil-error/zero-state output.
		if value.Outcome.State != writer.Applied || value.Outcome.Reference != a.reference.Scan.Receipt || value.RunID != a.reference.Scan.RunID || value.AcceptedRunRevision <= 0 || value.Digest == (writer.ResultDigest{}) {
			return LifecycleObservation{}, nil, writer.ErrResolveRequired
		}
		a.value, a.snapshotted = value, true
	}
	if !a.consumed {
		if err := a.journal.save(a.reference.Scan, a.reference.Purpose, &a.value); err != nil {
			a.journalErr = err
			return LifecycleObservation{}, nil, err
		}
		a.observed = LifecycleObservation{Original: a.reference, AcceptedRunRevision: a.value.AcceptedRunRevision, LeaseGeneration: a.value.LeaseGeneration}
		a.value = writer.ScanRunSnapshot{} // no borrowed result strings survive consumer release
		a.consumed = true
	}
	if !a.released {
		if err := a.result.Close(); err != nil {
			return LifecycleObservation{}, nil, err
		}
		a.released = true
	}
	if !a.acknowledged {
		ack, err := a.operation.Ack(ctx)
		if err != nil {
			return LifecycleObservation{}, nil, err
		}
		if ack.AcknowledgedThrough != a.reference.Scan.Receipt.Identity.Sequence {
			return LifecycleObservation{}, nil, writer.ErrResolveRequired
		}
		a.acknowledged = true
	}
	if a.reference.Purpose == writer.InventoryClaim && a.lease == nil {
		lease, err := a.operation.Lease(ctx)
		if err != nil {
			return LifecycleObservation{}, nil, err
		}
		a.lease = lease
	}
	if err := a.operation.Close(ctx); err != nil {
		return LifecycleObservation{}, nil, err
	}
	a.closed, a.result, a.operation = true, nil, nil
	return a.observed, a.lease, nil
}

// Close abandons this caller's turn, joins through W and retains handles after
// any error. It does not ACK an unconsumed result or authorize a replacement.
func (a *LifecycleTurn) Close(ctx context.Context) error {
	if a == nil || a.closed {
		return nil
	}
	if a.result != nil && !a.released {
		if err := a.result.Close(); err != nil {
			return err
		}
		a.released = true
	}
	if err := a.operation.Close(ctx); err != nil {
		return err
	}
	a.closed, a.result, a.operation = true, nil, nil
	return nil
}

// PrepareN1 uses only W's post-ACK opaque lease and saved claim reference.
// Snapshot/observation remain on W's original funded preparation. No child
// directory/EOF/frontier entry or completion is inferred by this first target.
func PrepareN1(ctx context.Context, driver *writer.ScanDriver, lease *writer.ScanRunLease, claim writer.ScanRunReference, directory []byte, options FilenameOptions, manifest scanclock.Manifest, journal *ReceiptJournal) (*N1Attempt, error) {
	preparation, err := driver.PrepareInventory(ctx, lease, claim, directory)
	if err != nil {
		return nil, err
	}
	a, err := ObserveN1(ctx, preparation, options, manifest)
	if err != nil {
		// Preserve the preparation so the caller can join physical retirement.
		return &N1Attempt{preparation: preparation, blocked: err}, err
	}
	if err := journal.save(a.Original(), writer.InventoryAccept, nil); err != nil {
		a.blocked = err
		return a, err
	}
	return a, nil
}
