package writer

import (
	"context"
	"errors"
	"math"
)

// This is a private entry into W's SAME dispatcher. W alone supplies its opaque
// current ScanDisclosure. No public driver/permission factory is introduced.
func (r *effectiveRuntime) runBasic(ctx context.Context, w *Writer, disclosure *ScanDisclosure, in basicInvocation, ack bool) (value BasicResult, acknowledged AckResult, err error) {
	defer func() {
		// Driver unlock/physical cleanup happens first. Its independently canceled
		// authority/preparation lifetime must still redact the outer publication.
		scan := &inventoryInvocation{disclosure: disclosure, request: in.request.Scan, basic: &in}
		err = errors.Join(err, r.currentRunDisclosure(w, nil, EnrichmentReceiptRequest{}, scan), basicWorkCurrent(scan))
		v, e := finishCommand(ctx, ctx, commandCompletion{output: commandOutput{basic: value}, ack: acknowledged}, err)
		value, acknowledged, err = v.output.basic, v.ack, e
	}()
	if r == nil || w == nil || w.effective != r {
		return value, acknowledged, ErrInvalid
	}
	// This entry owns driver.mu; trusted callers enter after releasing any
	// preparation lock. Protect existing opaque work for submission and the
	// physical return. Close can still cancel its independent lifetime first.
	if disclosure != nil && disclosure.scope != nil && disclosure.scope.driver != nil {
		d := disclosure.scope.driver
		if !d.mu.TryLock() {
			return value, acknowledged, ErrBusy
		}
		defer d.mu.Unlock()
	}
	if _, err = in.validate(); err != nil {
		return value, acknowledged, err
	}
	if ack && in.fresh() {
		return value, acknowledged, ErrInvalid
	}
	out, acknowledged, err := r.runCommand(ctx, w, nil, EnrichmentReceiptRequest{}, nil, ack,
		&inventoryInvocation{disclosure: disclosure, request: in.request.Scan, basic: &in})
	return out.basic, acknowledged, err
}

func (r *effectiveRuntime) runBasicTurn(ctx context.Context, w *Writer, turn *commandTurn, scan *inventoryInvocation, ack bool, acknowledged *AckResult) (value BasicResult, historical bool, err error) {
	defer func() {
		v, e := finishCommand(ctx, commandLifetime(turn), struct {
			value      BasicResult
			historical bool
		}{value, historical}, err)
		value, historical, err = v.value, v.historical, e
	}()
	if scan == nil || scan.basic == nil || acknowledged == nil {
		return value, false, ErrInvalid
	}
	in := scan.basic
	lookup, err := lookupBasic(ctx, turn, in.request)
	// Cancellation never replaces current authority. Do this AFTER lookup and
	// BEFORE either fresh effects or historical publication/ACK.
	err = errors.Join(err, r.currentRunDisclosure(w, nil, EnrichmentReceiptRequest{}, scan), turn.checkLifetimes(ctx))
	if err != nil {
		return value, false, err
	}
	if ack {
		acknowledged.AcknowledgedThrough, err = acknowledgeBasic(ctx, turn, in.request, lookup)
		return value, true, err
	}
	value = basicHistoricalResult(in.request, lookup)
	if lookup.decision != receiptFresh {
		return value, true, nil
	}
	if !in.fresh() {
		return value, false, nil // honest Pending original resolution
	}
	if err = checkBasicFreshWork(in); err != nil {
		return value, false, err
	}
	if in.accept != nil {
		if r.basicAcceptPlan == nil {
			return value, false, ErrNotImplemented
		}
		if err = checkBasicAcceptBounds(in.accept, r.basicAcceptPlan.basicAcceptBounds); err != nil {
			return value, false, err
		}
	}
	if (in.stage != nil && r.basicStagePlan == nil) || (in.seal != nil && r.basicSealPlan == nil) {
		return value, false, ErrNotImplemented
	}
	value, err = in.apply(ctx, turn)
	if err != nil {
		return value, false, err
	}
	if err = validateBasicFreshResult(in, value); err != nil {
		return value, false, err
	}
	return value, false, nil // physical COMMIT alone may set Applied
}

func basicHistoricalResult(q BasicReceiptRequest, lookup basicReceiptLookup) BasicResult {
	value := BasicResult{Outcome: ReceiptOutcome{Reference: q.Scan.Receipt}}
	switch lookup.decision {
	case receiptFresh:
		value.Outcome.State = Pending
	case receiptRetired:
		value.Outcome.State = ResultRetired
	case receiptReplay:
		value = lookup.result // complete historically decoded native projection
		value.Outcome = ReceiptOutcome{Reference: q.Scan.Receipt, State: Applied, Replayed: true}
	}
	return value
}

func validateBasicFreshResult(in *basicInvocation, value BasicResult) error {
	if _, err := in.validate(); err != nil || !in.fresh() || value.Outcome != (ReceiptOutcome{}) {
		return ErrConflict
	}
	original, _ := in.freshOriginal()
	source, _ := in.freshSource()
	if original.run.revision == math.MaxInt64 || value.Purpose != in.request.Purpose || value.RunID != in.request.Scan.RunID ||
		value.AcceptedRunRevision != original.run.revision+1 || value.SourceID != source.sourceID || value.SourceIncarnation != source.incarnation ||
		value.SourceRevision != source.objectRevision || value.ExpectedEpoch != source.expectedEpoch || value.ChangeID <= 0 ||
		(source.changeID != 0 && value.ChangeID != source.changeID) {
		return ErrConflict
	}
	digest, err := basicResultDigest(in.request.Scan.Receipt.Identity, in.request.Scan.Receipt.RequestDigest, value)
	if err != nil || digest != value.Digest {
		return errors.Join(ErrConflict, err)
	}
	return nil
}
