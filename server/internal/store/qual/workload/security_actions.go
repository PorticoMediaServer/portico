package workload

import (
	"context"
	"errors"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/writer"
)

// Target supplies the ID of one already-existing family. It supplies neither
// authority nor a constructor; the sealed driver checks CURRENT native scope.
type SecurityBinding struct {
	Writer *writer.Writer
	Driver *writer.WCSecurityDriver
	Target func(context.Context, Offer) (string, error)
}

func securityAction(b SecurityBinding) Action {
	return func(ctx context.Context, offer Offer, emit func(Observation) error) error {
		if b.Writer == nil || b.Driver == nil || b.Target == nil {
			return preparationError(emit, ErrWCComposition)
		}
		family, err := b.Target(ctx, offer)
		if err != nil {
			return preparationError(emit, err)
		}
		start := time.Now()
		command, err := b.Driver.PrepareRevoke(ctx, family)
		end := time.Now()
		if err != nil {
			raw := observedCall("SNAPSHOT", start, end)
			raw.CallError = err.Error()
			return errors.Join(err, emit(raw))
		}
		q, err := writer.SecurityReference(command)
		if err != nil {
			return preparationError(emit, err)
		}
		before, err := writer.SecurityPreparationSnapshot(command)
		if err != nil {
			return preparationError(emit, err)
		}
		raw := capture("SNAPSHOT", writer.SecurityAccept, writer.ReceiptOutcome{Evidence: before.Evidence}, before, nil, start, end)
		raw.Outcome = nil
		if err = emit(raw); err != nil {
			return err
		}
		start = time.Now()
		result, err := b.Writer.AcceptSecurity(ctx, b.Driver.Disclosure(), command)
		if e := emit(captureRequested(q.Receipt, "ACCEPT", writer.SecurityAccept, result.Outcome, result, err, start, time.Now())); e != nil {
			return errors.Join(err, e)
		}
		if err != nil {
			return err
		}
		if result.Outcome.State != writer.Applied || result.Outcome.Replayed || result.Outcome.Reference != q.Receipt {
			return errors.New("security original mismatch")
		}
		start = time.Now()
		saved, err := b.Writer.ResolveSecurity(ctx, b.Driver.Disclosure(), q)
		if e := emit(captureRequested(q.Receipt, "RESOLVE", writer.SecurityAccept, saved.Outcome, saved, err, start, time.Now())); e != nil {
			return errors.Join(err, e)
		}
		if err != nil {
			return err
		}
		if saved.Outcome.State != writer.Applied || !saved.Outcome.Replayed || saved.Outcome.Reference != q.Receipt || saved.Value != result.Value || saved.Digest != result.Digest {
			return errors.New("security saved result mismatch")
		}
		start = time.Now()
		ack, err := b.Writer.AckSecurity(ctx, b.Driver.Disclosure(), q)
		if e := emit(captureAck(q.Receipt, writer.SecurityAccept, ack, err, start, time.Now())); e != nil {
			return errors.Join(err, e)
		}
		if err != nil {
			return err
		}
		if ack.AcknowledgedThrough != q.Receipt.Identity.Sequence {
			return errors.New("security ACK floor mismatch")
		}
		start = time.Now()
		after, err := b.Writer.CurrentSecurity(ctx, b.Driver.Disclosure(), family)
		raw = capture("SNAPSHOT", writer.SecurityAccept, writer.ReceiptOutcome{Evidence: after.Evidence}, after, err, start, time.Now())
		raw.Outcome = nil
		if e := emit(raw); e != nil {
			return errors.Join(err, e)
		}
		if err != nil {
			return err
		}
		if before.Revoked != 0 || after.Revoked != 1 || after.Last != q.Receipt.Identity.Sequence || after.Acknowledged != after.Last || !after.Reason.Valid || after.Reason.String != "admin_revoke" || !after.RevokedAt.Valid || after.RevokedAt.String != result.Value.RevokedAt || !after.TeardownDue.Valid || after.TeardownDue.Int64 != result.Value.AppliedMS || !after.LastEvent.Valid || after.LastEvent.Int64 <= before.LastEvent.Int64 {
			return errors.New("security deny, retained scalar, teardown debt or devices hint missing")
		}
		return nil
	}
}
