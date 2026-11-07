package workload

import (
	"context"
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/store/writer"
	"time"
)

type ProgressReport struct {
	Sequence                         int64
	PositionSeconds, DurationSeconds float64
	State                            string
}

// The callback returns source report data, never authority, completion, SQL,
// physical evidence or a floor. Only the opaque driver can seal that report.
type ProgressBinding struct {
	Writer *writer.Writer
	Driver *writer.WCProgressDriver
	Report func(context.Context, Offer) (ProgressReport, error)
}

func progressAction(binding ProgressBinding, drain bool) Action {
	return func(ctx context.Context, offer Offer, emit func(Observation) error) error {
		if binding.Writer == nil || binding.Driver == nil || !drain && binding.Report == nil {
			return preparationError(emit, ErrWCComposition)
		}
		var report ProgressReport
		var err error
		if !drain {
			report, err = binding.Report(ctx, offer)
			if err != nil {
				return preparationError(emit, err)
			}
		}
		start := time.Now()
		var command writer.ProgressCommand
		if drain {
			command, err = binding.Driver.Drain(ctx)
		} else {
			command, err = binding.Driver.Capture(ctx, report.Sequence, report.PositionSeconds, report.DurationSeconds, report.State)
		}
		end := time.Now()
		if err != nil {
			raw := observedCall("SNAPSHOT", start, end)
			raw.CallError = err.Error()
			return errors.Join(err, emit(raw))
		}
		reference, err := writer.ProgressReference(command)
		if err != nil {
			return preparationError(emit, err)
		}
		before, err := writer.ProgressPreparationSnapshot(command)
		if err != nil {
			return preparationError(emit, err)
		}
		raw := capture("SNAPSHOT", writer.ProgressAccept, writer.ReceiptOutcome{Evidence: before.Evidence}, before, nil, start, end)
		raw.Outcome = nil
		if err = emit(raw); err != nil {
			return err
		}
		start = time.Now()
		var result writer.ProgressResult
		if drain {
			result, err = binding.Driver.ExecuteCurrentDrain(ctx, command)
		} else {
			result, err = binding.Writer.AcceptProgress(ctx, binding.Driver.Disclosure(), command)
		}
		if outputErr := emit(captureRequested(reference.Receipt, "ACCEPT", writer.ProgressAccept, result.Outcome, result, err, start, time.Now())); outputErr != nil {
			return errors.Join(err, outputErr)
		}
		if err != nil {
			return err
		}
		if result.Outcome.State != writer.Applied || result.Outcome.Replayed || result.Outcome.Reference != reference.Receipt {
			return errors.New("progress fresh result mismatches original request")
		}
		start = time.Now()
		replay, err := binding.Writer.ResolveProgress(ctx, binding.Driver.Disclosure(), reference)
		if outputErr := emit(captureRequested(reference.Receipt, "RESOLVE", writer.ProgressAccept, replay.Outcome, replay, err, start, time.Now())); outputErr != nil {
			return errors.Join(err, outputErr)
		}
		if err != nil {
			return err
		}
		if replay.Outcome.State != writer.Applied || !replay.Outcome.Replayed || replay.Outcome.Reference != reference.Receipt || replay.Value != result.Value || replay.Digest != result.Digest {
			return errors.New("progress saved result mismatch")
		}
		start = time.Now()
		ack, err := binding.Writer.AckProgress(ctx, binding.Driver.Disclosure(), reference)
		if outputErr := emit(captureAck(reference.Receipt, writer.ProgressAccept, ack, err, start, time.Now())); outputErr != nil {
			return errors.Join(err, outputErr)
		}
		if err != nil {
			return err
		}
		if ack.AcknowledgedThrough != reference.Receipt.Identity.Sequence {
			return errors.New("progress ACK floor mismatch")
		}
		start = time.Now()
		after, err := binding.Writer.CurrentProgress(ctx, binding.Driver.Disclosure())
		raw = capture("SNAPSHOT", writer.ProgressAccept, writer.ReceiptOutcome{Evidence: after.Evidence}, after, err, start, time.Now())
		raw.Outcome = nil
		if outputErr := emit(raw); outputErr != nil {
			return errors.Join(err, outputErr)
		}
		if err != nil {
			return err
		}
		if after.Last != reference.Receipt.Identity.Sequence || after.Acknowledged != after.Last || after.ReceiptRows != before.ReceiptRows+1 || !after.LastEvent.Valid || after.LastEvent.Int64 <= before.LastEvent.Int64 {
			return errors.New("progress current snapshot lost result debt or committed events")
		}
		if !drain && (!after.Pending || after.PlayedMS != before.PlayedMS) {
			return errors.New("capture lost pending or credited time before drain")
		}
		if drain && (!result.Value.CreditedMs.Valid || after.PlayedMS-before.PlayedMS != result.Value.CreditedMs.Int64 || after.Pending != (result.Value.Finished.Int64 == 0) || after.Viewer != before.Viewer || after.Observation != before.Observation+1) {
			return errors.New("drain snapshot mismatches exact native result")
		}
		return nil
	}
}
