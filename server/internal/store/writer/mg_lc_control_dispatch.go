package writer

import (
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
)

// Accepting frames for 868-871. They resolve the ORIGINAL saved channel result
// before any effect, reauthorize only disclosure on replay, and perform exactly
// one bounded control effect on a fresh turn. The parent owns the actor
// authority, the immutable generation enqueue and the public COMMIT lifecycle;
// this frame supplies the closed identity/request/receipt protocol and the FULL
// native durability boundary for 868/870. No physical release happens here.

// mgLCControlDispatch is the only entry to the four control purposes. A saved
// original is disclosed without a fresh channel CAS; a retired channel denies
// disclosure of non-retirement control results. Unknown/unprovable state is
// never success.
func mgLCControlDispatch(ctx context.Context, turn *commandTurn, purpose CommandID, w mgLCControlWitness, c mgLCControlCommand, in mgLCControlInput) (out mgLCControlSaved, decision receiptDecision, err error) {
	defer func() {
		value, failure := finishCommand(ctx, commandLifetime(turn), struct {
			Saved    mgLCControlSaved
			Decision receiptDecision
		}{out, decision}, err)
		out, decision, err = value.Saved, value.Decision, failure
	}()
	kind, ok := mgLCControlKindFor(purpose)
	if !ok {
		return mgLCControlSaved{}, receiptConflict, ErrInvalid
	}
	if err = mgLCControlPurpose(ctx, turn, purpose); err != nil {
		return mgLCControlSaved{}, receiptConflict, err
	}
	if !c.valid() || c.identity.Incarnation != w.issuer || !w.valid(kind) {
		return mgLCControlSaved{}, receiptConflict, ErrInvalid
	}
	if err = in.valid(kind); err != nil {
		return mgLCControlSaved{}, receiptConflict, err
	}
	// The caller's request digest must be the canonical digest of this complete
	// typed request; a caller-chosen hash is not receipt authority.
	request := mgLCControlRequestDigest(kind, w, in)
	if c.request != request {
		return mgLCControlSaved{}, receiptConflict, ErrConflict
	}
	decision, out, err = lookupMGLCControlReceipt(ctx, turn, kind, c)
	if err != nil {
		return mgLCControlSaved{}, decision, err
	}
	switch decision {
	case receiptReplay:
		return out, decision, nil
	case receiptRetired:
		return mgLCControlSaved{}, decision, ErrDisclosureDenied
	case receiptResolveRequired:
		return mgLCControlSaved{}, decision, ErrResolveRequired
	case receiptConflict:
		return mgLCControlSaved{}, decision, ErrConflict
	case receiptFresh:
	default:
		return mgLCControlSaved{}, receiptConflict, ErrInvalid
	}
	var result mgLCControlResult
	switch kind {
	case mgLCControlSaveKind:
		if in.config == nil {
			return mgLCControlSaved{}, receiptConflict, ErrInvalid
		}
		result, err = saveMGLCControlChannel(ctx, turn, w, *in.config)
	case mgLCControlRegenerateKind:
		result, err = requestMGLCControlRegeneration(ctx, turn, w)
	case mgLCControlRetireKind:
		result, err = retireMGLCControlChannel(ctx, turn, w)
	case mgLCControlReorderKind:
		result, err = reorderMGLCControlChannels(ctx, turn, w, in.order)
	default:
		return mgLCControlSaved{}, receiptConflict, ErrInvalid
	}
	if err != nil {
		return mgLCControlSaved{}, receiptConflict, err
	}
	saved := mgLCControlSaved{command: c, kind: kind, result: result}
	saved.digest = mgLCControlResultDigest(saved)
	if err = saveMGLCControlReceipt(ctx, turn, saved); err != nil {
		return mgLCControlSaved{}, receiptConflict, err
	}
	return saved, receiptFresh, nil
}

// lookupMGLCControlReceipt checks the retained original FIRST. The legacy
// lc_receipts row is the durable result for this channel-control family; there
// is no ACK/reclaim state in that retained table (MG-26 owns receipt reclaim),
// so an absent or already-reclaimed receipt can only surface as a stale-witness
// conflict and is never success.
func lookupMGLCControlReceipt(ctx context.Context, turn *commandTurn, kind mgLCControlKind, c mgLCControlCommand) (decision receiptDecision, saved mgLCControlSaved, err error) {
	defer func() {
		value, failure := finishCommand(ctx, commandLifetime(turn), struct {
			Decision receiptDecision
			Saved    mgLCControlSaved
		}{decision, saved}, err)
		decision, saved, err = value.Decision, value.Saved, failure
	}()
	if !c.valid() || !kind.valid() {
		return receiptConflict, saved, ErrInvalid
	}
	row, err := mgLCControlRow(ctx, turn, mgLCControlReceiptRead, mgLCControlRequestKey(c))
	if err != nil {
		return receiptConflict, saved, err
	}
	var digest, body string
	if err := row.Scan(&digest, &body); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return receiptFresh, saved, nil
		}
		return receiptConflict, saved, err
	}
	raw, err := hex.DecodeString(body)
	if err != nil {
		return receiptResolveRequired, saved, ErrResolveRequired
	}
	decoded, err := decodeMGLCControlResult(raw, c, kind)
	if errors.Is(err, ErrConflict) {
		return receiptConflict, saved, ErrConflict
	}
	if err != nil {
		return receiptResolveRequired, saved, ErrResolveRequired
	}
	if decoded.digest == (ResultDigest{}) || hex.EncodeToString(decoded.digest[:]) != digest {
		return receiptResolveRequired, saved, ErrResolveRequired
	}
	// Retirement ends the control lifecycle of every earlier saved result. The
	// retirement's own original result remains disclosable.
	current, err := readMGLCControlChannel(ctx, turn, decoded.result.channel.id)
	switch {
	case err == nil && current.removed != 0 && kind != mgLCControlRetireKind:
		return receiptRetired, saved, nil
	case err != nil && !errors.Is(err, ErrConflict):
		return receiptConflict, saved, err
	}
	return receiptReplay, decoded, nil
}

// saveMGLCControlReceipt stores the closed canonical result and its result
// digest, then reads both back exactly. Any failure after a native effect leaves
// the whole frame to the caller's rollback.
func saveMGLCControlReceipt(ctx context.Context, turn *commandTurn, saved mgLCControlSaved) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	if !saved.command.valid() || !saved.kind.valid() || saved.digest == (ResultDigest{}) ||
		saved.digest != mgLCControlResultDigest(saved) {
		return ErrInvalid
	}
	body := mgLCControlResultBytes(saved)
	if len(body) > mgLCBytes {
		return ErrCap
	}
	key := mgLCControlRequestKey(saved.command)
	digest := hex.EncodeToString(saved.digest[:])
	encoded := hex.EncodeToString(body)
	if err = mgLCControlOne(ctx, turn, mgLCControlReceiptInsert, key, digest, encoded); err != nil {
		return err
	}
	row, err := mgLCControlRow(ctx, turn, mgLCControlReceiptRead, key)
	if err != nil {
		return err
	}
	var readDigest, readBody string
	if err := row.Scan(&readDigest, &readBody); err != nil {
		return mgLCNoRows(err)
	}
	if readDigest != digest || readBody != encoded {
		return ErrConflict
	}
	return nil
}
