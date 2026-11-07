package workload

import (
	"context"
	"errors"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/writer"
)

type ReclaimBinding struct {
	Writer *writer.Writer
	Driver *writer.WCReclaimDriver
}

func Reclaim(bindings ...ReclaimBinding) Action {
	if len(bindings) != 1 {
		return wcGatedAction(WCIntent{Phase: "RECLAIM", Purpose: writer.ProgressReceiptReclaim, Class: governor.Background, Durability: writer.Normal, Variant: "progress_receipt_reclaim"})
	}
	b := bindings[0]
	return func(ctx context.Context, _ Offer, emit func(Observation) error) error {
		if b.Writer == nil || b.Driver == nil {
			return preparationError(emit, ErrWCComposition)
		}
		start := time.Now()
		command, err := b.Driver.Prepare(ctx)
		end := time.Now()
		if err != nil {
			raw := observedCall("SNAPSHOT", start, end)
			raw.CallError = err.Error()
			return errors.Join(err, emit(raw))
		}
		q, err := writer.ReclaimReference(command)
		if err != nil {
			return preparationError(emit, err)
		}
		before, err := writer.ReclaimPreparationSnapshot(command)
		if err != nil {
			return preparationError(emit, err)
		}
		raw := capture("SNAPSHOT", writer.ProgressReceiptReclaim, writer.ReceiptOutcome{Evidence: before.Evidence}, before, nil, start, end)
		raw.Outcome = nil
		if err = emit(raw); err != nil {
			return err
		}
		start = time.Now()
		result, err := b.Writer.AcceptReclaim(ctx, b.Driver.Disclosure(), command)
		if e := emit(captureRequested(q.Receipt, "ACCEPT", writer.ProgressReceiptReclaim, result.Outcome, result, err, start, time.Now())); e != nil {
			return errors.Join(err, e)
		}
		if err != nil {
			return err
		}
		if result.Outcome.State != writer.Applied || result.Outcome.Replayed || result.Outcome.Reference != q.Receipt {
			return errors.New("reclaim original mismatch")
		}
		start = time.Now()
		saved, err := b.Writer.ResolveReclaim(ctx, b.Driver.Disclosure(), q)
		if e := emit(captureRequested(q.Receipt, "RESOLVE", writer.ProgressReceiptReclaim, saved.Outcome, saved, err, start, time.Now())); e != nil {
			return errors.Join(err, e)
		}
		if err != nil {
			return err
		}
		if saved.Outcome.State != writer.Applied || !saved.Outcome.Replayed || saved.Outcome.Reference != q.Receipt || saved.Value != result.Value || saved.Digest != result.Digest {
			return errors.New("reclaim saved result mismatch")
		}
		start = time.Now()
		ack, err := b.Writer.AckReclaim(ctx, b.Driver.Disclosure(), q)
		if e := emit(captureAck(q.Receipt, writer.ProgressReceiptReclaim, ack, err, start, time.Now())); e != nil {
			return errors.Join(err, e)
		}
		if err != nil {
			return err
		}
		if ack.AcknowledgedThrough != q.Receipt.Identity.Sequence {
			return errors.New("reclaim ACK floor mismatch")
		}
		start = time.Now()
		after, err := b.Writer.CurrentReclaim(ctx, b.Driver.Disclosure())
		raw = capture("SNAPSHOT", writer.ProgressReceiptReclaim, writer.ReceiptOutcome{Evidence: after.Evidence}, after, err, start, time.Now())
		raw.Outcome = nil
		if e := emit(raw); e != nil {
			return errors.Join(err, e)
		}
		if err != nil {
			return err
		}
		if after.Last != q.Receipt.Identity.Sequence || after.Acknowledged != after.Last || after.ReceiptRows != before.ReceiptRows-result.Value.RemovedCount || after.ReceiptBytes != before.ReceiptBytes-result.Value.RemovedVariableBytes || after.AfterSequence != result.Value.ResultAfterSequence || !after.SlotPresent || after.Slot != result.Value || after.LastRemovedChildPresent || after.Due.Valid == result.Value.ResultNoNext || after.Due.Valid && after.Due != before.Due || after.LastEvent != before.LastEvent {
			return errors.New("reclaim physical child, debit, scalar or debt mismatch")
		}
		return nil
	}
}
