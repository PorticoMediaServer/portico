package scanbench

import (
	"context"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/qual/scanclock"
	"github.com/PorticoMediaServer/portico/server/internal/store/scan"
	"github.com/PorticoMediaServer/portico/server/internal/store/writer"
)

// FilenameOptions contains no worker-selected identity root or source key.
type FilenameOptions struct {
	ExplicitSortTitle, MetadataLanguage string
}

// N1Preparation matches W's draft composition: only ObserveInventory/Close
// are concrete in W’s local draft; trusted issuance and enable remain gated.
// This interface grants nothing and has no constructor or runtime registration.
type N1Preparation interface {
	scan.InventoryObservation
	Snapshot(context.Context) (writer.InventoryPreparationSnapshot, error)
	Accept(context.Context, writer.InventoryAcceptCommand) (writer.InventoryResult, error)
}

// ScanResolver is the draft W driver, which retains CURRENT disclosure itself.
// A caller cannot supply a principal, grant, lease, profile or retry identity.
type ScanResolver interface {
	ResolveScan(context.Context, writer.ScanReceiptRequest) (writer.InventoryResult, error)
}

// N1Attempt retains one original command/reference and its timing collector.
// It is single-owner; methods must not run concurrently.
// It supplies no traversal loop or accepted-boundary reducer.
// Its optional journal path consumes the exact result before W's AckScan.
// In particular, nil-error/zero-state is not progress; historical resolution
// cannot reconstruct an earlier first-visible or first-Applied timestamp.
type N1Attempt struct {
	preparation N1Preparation
	prepared    scan.PreparedN1
	original    writer.ScanReceiptRequest
	clock       *scanclock.Clock
	blocked     error // observation/journal failure permits only physical Close
	submitted   bool
	closed      bool
	retired     bool
	consumed    bool
}

// ObserveN1 times this one owned N1 observation, after W's actual Snapshot.
// Earlier W lifecycle root observations are outside this window. The report
// labels that origin explicitly; it cannot supply a whole-scan visibility time.
// On error the caller STILL retains preparation and must Close/join it; no
// deferred release hides pending physical debt. No accepting entry is minted.
func ObserveN1(ctx context.Context, preparation N1Preparation, options FilenameOptions, manifest scanclock.Manifest) (*N1Attempt, error) {
	if ctx == nil || preparation == nil {
		return nil, scan.ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	snapshot, err := preparation.Snapshot(ctx)
	if err != nil {
		return nil, err
	}
	identity, template := snapshot.Identity, snapshot.Input
	if manifest.Run != template.Scope.RunID {
		return nil, scanclock.ErrObservation
	}
	clock, err := scanclock.NewN1Observation(time.Now(), manifest)
	if err != nil {
		return nil, err
	}
	filename := scan.FilenameNaming{IdentityRoot: snapshot.IdentityRoot, ExplicitSortTitle: options.ExplicitSortTitle, MetadataLanguage: options.MetadataLanguage}
	prepared, err := scan.ObserveN1(ctx, preparation, identity, template, filename)
	if err != nil {
		return nil, err
	}
	return &N1Attempt{preparation: preparation, prepared: prepared, original: prepared.Reference(), clock: clock}, nil
}

func (a *N1Attempt) Original() writer.ScanReceiptRequest {
	if a == nil {
		return writer.ScanReceiptRequest{}
	}
	return a.original
}

// Accept submits at most once. The owner must retain ORIGINAL for recovery
// BEFORE calling it; this in-memory harness is not a durable journal or restart
// contract. Errors produce zero
// outputs, retain the same attempt and require SAME-reference ResolveScan.
// Timing rejection never masks or changes an actual successful writer result.
func (a *N1Attempt) Accept(ctx context.Context) (writer.InventoryResult, error) {
	if a == nil || a.preparation == nil || a.clock == nil || ctx == nil || a.submitted || a.closed || a.blocked != nil {
		return writer.InventoryResult{}, scan.ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return writer.InventoryResult{}, err
	}
	a.submitted = true
	result, err := a.preparation.Accept(ctx, a.prepared.Command())
	if err != nil {
		return writer.InventoryResult{}, err
	}
	_ = a.clock.Applied(time.Now(), a.Original(), result, nil)
	return result, nil
}

func (a *N1Attempt) Resolve(ctx context.Context, driver ScanResolver) (writer.InventoryResult, error) {
	if a == nil || ctx == nil || driver == nil || !a.submitted {
		return writer.InventoryResult{}, scan.ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return writer.InventoryResult{}, err
	}
	result, err := driver.ResolveScan(ctx, a.Original())
	if err != nil {
		return writer.InventoryResult{}, err
	}
	// Saved resolution is a boundary for W's consumer, not a reconstruction
	// of first acceptance time. Leave fresh/visibility clocks unmeasured.
	return result, nil
}

func (a *N1Attempt) Close(ctx context.Context) error {
	if a == nil || ctx == nil {
		return scan.ErrInvalid
	}
	if a.retired {
		return nil
	}
	if a.preparation == nil {
		return scan.ErrInvalid
	}
	a.closed = true
	if err := a.preparation.Close(ctx); err != nil {
		return err // pending retains this exact W preparation AND payload
	}
	// The caller has released consumer ownership. Drop native/prepared backing
	// along with W's successful retirement; only fixed recovery/timing values
	// survive. ACK never supplied this release or refunded these bytes.
	a.preparation, a.prepared, a.retired = nil, scan.PreparedN1{}, true
	return nil
}

func (a *N1Attempt) Report() scanclock.Report {
	if a == nil {
		return (*scanclock.Clock)(nil).Report()
	}
	return a.clock.Report()
}

// ScanProtocol is W's agreed N1 driver target. W alone mints the current
// disclosure; historical recovery never obtains a fresh source/root/lease.
type ScanProtocol interface {
	ScanResolver
	AckScan(context.Context, writer.ScanReceiptRequest) (writer.AckResult, error)
}

// SubmitAndConsume saves the exact result before making ACK available. The
// original must already have been journaled by PrepareN1. A failed write does
// not ACK, discard the preparation or advance to another native observation.
func (a *N1Attempt) SubmitAndConsume(ctx context.Context, journal *ReceiptJournal) (writer.InventoryResult, error) {
	value, err := a.Accept(ctx)
	if err != nil {
		return writer.InventoryResult{}, err
	}
	if err := journal.saveInventory(a.Original(), value); err != nil {
		return writer.InventoryResult{}, err
	}
	a.consumed = true
	return value, nil
}

func (a *N1Attempt) ResolveAndConsume(ctx context.Context, driver ScanResolver, journal *ReceiptJournal) (writer.InventoryResult, error) {
	value, err := a.Resolve(ctx, driver)
	if err != nil {
		return writer.InventoryResult{}, err
	}
	if err := journal.saveInventory(a.Original(), value); err != nil {
		return writer.InventoryResult{}, err
	}
	a.consumed = true
	return value, nil
}

func (a *N1Attempt) AckConsumed(ctx context.Context, driver ScanProtocol) (writer.AckResult, error) {
	if a == nil || driver == nil || !a.consumed || ctx == nil {
		return writer.AckResult{}, scan.ErrInvalid
	}
	value, err := driver.AckScan(ctx, a.Original())
	if err != nil {
		return writer.AckResult{}, err
	}
	if value.AcknowledgedThrough != a.Original().Receipt.Identity.Sequence {
		return writer.AckResult{}, writer.ErrResolveRequired
	}
	return value, nil
}
