package writer

import (
	"context"
	"errors"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
)

type scanSubmission uint8

const (
	scanPrepared scanSubmission = iota + 1
	scanSubmitting
	scanNoGrant
	scanFrameBound
	scanFrameRejected // actual owned frame retained for cleanup, never effect authority
)

// One bounded original tracks authority/physical exclusion, never resource
// amounts. The existing governor grant and frame remain sole physical owners.
// All fields are private and accessed under ScanDriver.mu through its closed
// invocation; no producer supplies a completion or "old work gone" boolean.
type scanDriverWork struct {
	driver      *ScanDriver
	purpose     CommandID
	original    ScanReceiptRequest
	generation  int64
	preparation *scanPreparation
	submission  scanSubmission
	frame       *transaction
}

func (d *ScanDriver) prepareWork(ctx context.Context, purpose CommandID, original ScanReceiptRequest, generation int64, p *scanPreparation) (out *scanDriverWork, err error) {
	lifetime := ctx
	if d != nil && d.self == d {
		lifetime = d.lifetime
	}
	defer func() { out, err = finishCommand(ctx, lifetime, out, err) }()
	if err = d.current(); err != nil {
		return nil, err
	}
	_, basicErr := basicPurposeForCommand(purpose)
	basic := basicErr == nil
	if purpose != InventoryCreate && purpose != InventoryClaim && purpose != InventoryAccept && !basic {
		return nil, ErrInvalid
	}
	if original.RunID == "" || original.LibraryID <= 0 || original.LibraryKey == "" || original.Receipt.Identity.Sequence <= 0 || generation < 0 || p == nil || d.preparation != p || p.self != p ||
		p.opening != d.opening || p.runtime != d.writer.effective || p.expected.Key.Domain != uint16(purpose) || p.expected.Key.ID != original.RunID ||
		len(p.expected.Footprint) != 1 || p.expected.Footprint[0].Library != original.LibraryID {
		return nil, ErrInvalid
	}
	// Dedicated Basic work keeps the constructor's exact original holding.
	// History/native Inventory preparation cannot be borrowed for a Basic turn.
	if basic && (generation <= 0 || p.mode != scanBasicPreparation || p.expected.Footprint[0].Domain != uint16(purpose)) {
		return nil, ErrInvalid
	}
	if err = p.current(); err != nil {
		return nil, err
	}
	if d.work != nil {
		return nil, ErrBusy
	}
	out = &scanDriverWork{driver: d, purpose: purpose, original: original, generation: generation, preparation: p, submission: scanPrepared}
	d.work = out
	return out, nil
}

// Mark before Try, so a panic or unknown admission result cannot turn a
// submitted original back into a fresh one. The caller holds driver.mu.
func (w *scanDriverWork) submitting() error {
	if w == nil || w.driver == nil || w.driver.work != w || w.submission != scanPrepared {
		return ErrInvalid
	}
	if err := w.driver.current(); err != nil {
		return err
	}
	if err := w.preparation.current(); err != nil {
		return err
	}
	w.submission = scanSubmitting
	return nil
}

// Only an actual closed Try refusal proves no grant was obtained. A zero or
// inconsistent Decision is unresolved. There is no timer-based inference.
func (w *scanDriverWork) refused(decision governor.Decision) error {
	if w == nil || w.driver == nil || w.driver.work != w || w.submission != scanSubmitting || decision.Grant != nil || decision.Refusal == nil {
		return ErrResolveRequired
	}
	w.submission = scanNoGrant
	return nil
}

func (w *scanDriverWork) bindFrame(tx *transaction) error {
	if w == nil || w.driver == nil || w.driver.self != w.driver || w.driver.opening == nil || w.preparation == nil || w.driver.work != w || w.submission != scanSubmitting || tx == nil || tx.ctx == nil || tx.writer != w.driver.opening.executor || tx.descriptor.ID != w.purpose {
		return ErrInvalid
	}
	c, ok := tx.custody.(*governedTurn)
	if !ok || c.a != w.driver.writer.effective.authority || c.expected.Key.Domain != uint16(w.purpose) || c.expected.Key.ID != w.original.RunID || c.expected.Owner != w.driver.writer.effective.owner {
		return ErrInvalid
	}
	if err := c.a.VerifyComposition(c.g, w.driver.writer.effective.wal); err != nil {
		return err
	}
	w.frame = tx
	w.preparation.mu.Lock()
	w.preparation.frame = tx
	w.preparation.mu.Unlock()
	w.submission = scanFrameRejected
	deadline, ok := tx.ctx.Deadline()
	if tx.done || !tx.begun || !ok || deadline.After(w.preparation.expected.Deadline) ||
		len(c.expected.Footprint) != 1 || c.expected.Footprint[0].Library != w.original.LibraryID || c.expected.Footprint[0].Domain != uint16(w.purpose) {
		return ErrInvalid
	}
	if err := tx.ctx.Err(); err != nil {
		return err
	}
	if err := w.preparation.current(); err != nil {
		return err
	}
	w.submission = scanFrameBound
	return nil
}

func (w *scanDriverWork) physicallyReturned() bool {
	if w == nil {
		return false
	}
	if w.preparation != nil {
		w.preparation.mu.Lock()
		retired := w.preparation.retired
		w.preparation.mu.Unlock()
		if !retired {
			return false
		}
	}
	return w.nativeReturned()
}

// Close must not refund frozen preparation memory while its submitted native
// frame can still read it. This excludes only native/frame custody; preparation
// retirement remains independently required by physicallyReturned above.
func (w *scanDriverWork) nativeReturned() bool {
	if w == nil {
		return false
	}
	switch w.submission {
	case scanPrepared, scanNoGrant:
		return true
	case scanFrameBound, scanFrameRejected:
		tx := w.frame
		return tx != nil && tx.done && tx.connectionReturned && tx.retirementComplete && tx.cleanupComplete
	default:
		return false
	}
}

// excludeOldWork is called with driver.mu held on the SAME real claim turn.
// Expiry/cancellation alone never excludes anything. The current native owner
// floor settles the actual prior sequence; unknown COMMIT also needs its saved
// original receipt/floor resolved before this point. The SC claim then restates
// the exact full vector and bumps generation/revision/sequence atomically.
func (d *ScanDriver) excludeOldWork(ctx context.Context, turn *commandTurn, before scanRunSnapshot) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	if err = d.current(); err != nil {
		return err
	}
	if turn == nil || turn.transaction == nil || turn.transaction.writer != d.opening.executor || turn.transaction.descriptor.ID != InventoryClaim {
		return ErrInvalid
	}
	r := d.writer.effective
	if r.fenced || r.executor.fenced || r.executor.ambiguous {
		return ErrResolveRequired
	}
	actual, err := readCheckedScanRunSnapshot(ctx, turn, before.run.scope.runID)
	if err != nil || actual != before {
		return errors.Join(ErrConflict, err)
	}
	if actual.run.acknowledgedSequence != actual.run.lastSequence {
		return ErrResolveRequired
	}
	if w := d.work; w != nil {
		if w.original.RunID != before.run.scope.runID || w.original.Receipt.Identity.Incarnation != before.run.scope.commandIncarnation || w.generation != before.run.leaseGeneration {
			return ErrConflict
		}
		if !w.physicallyReturned() {
			return ErrBusy
		}
		if w.submission == scanFrameBound || w.submission == scanFrameRejected {
			if w.frame.result.state == commitUnknown || w.original.Receipt.Identity.Sequence > actual.run.acknowledgedSequence {
				return ErrResolveRequired
			}
		}
		d.work = nil
	}
	return nil
}
