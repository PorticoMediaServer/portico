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

// mgLCControlDispatch is the only entry to the four control purposes. Current
// owner authority is minted and rechecked BEFORE any retained receipt read and
// again before the first effect, exactly like the MG-03 source family. A saved
// original is disclosed without a fresh channel CAS; a retired channel denies
// disclosure of non-retirement control results. Unknown/unprovable state is
// never success.
func mgLCControlDispatch(ctx context.Context, turn *commandTurn, proof *mgActorProof, lifetime context.Context, budget mgAuthorityBudget, purpose CommandID, w mgLCControlWitness, c mgLCControlCommand, in mgLCControlInput) (out mgLCControlSaved, decision receiptDecision, err error) {
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
	// Channel configuration is owner-level. A comparison witness can never
	// stand in for the US owner/main-profile proof.
	if proof == nil || proof.kind != mgActorOwner {
		return mgLCControlSaved{}, receiptConflict, ErrDisclosureDenied
	}
	actor, err := authorizeMGActor(ctx, turn, proof, lifetime, budget)
	if err != nil {
		return mgLCControlSaved{}, receiptConflict, err
	}
	// Current authority precedes ANY receipt owner/payload read, replay included.
	if err = actor.recheck(ctx, turn); err != nil {
		return mgLCControlSaved{}, receiptConflict, err
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
	// The encoded result is bounded before any native write; the post-effect
	// receipt save must never be the first place an oversized body is refused.
	planned, ok := mgLCControlPlannedResult(kind, w, in)
	if !ok {
		return mgLCControlSaved{}, receiptConflict, ErrInvalid
	}
	if len(mgLCControlResultBytes(mgLCControlSaved{command: c, kind: kind, result: planned})) > mgLCBytes {
		return mgLCControlSaved{}, receiptConflict, &Refusal{Code: RefusalCapacity}
	}
	// Recheck immediately before the first effect; the lookup above may have
	// consumed a stale horizon.
	if err = actor.recheck(ctx, turn); err != nil {
		return mgLCControlSaved{}, receiptConflict, err
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
	// Retirement ends the control lifecycle of every earlier saved result. A
	// reorder original spans the whole directory, so ANY member retired since
	// the reorder denies the whole snapshot. A missing or unprovable native row
	// is denial as well: ErrConflict is not evidence that replay is safe.
	ids := []string{decoded.result.channel.id}
	if kind == mgLCControlReorderKind {
		ids = ids[:0]
		for _, ch := range decoded.result.directory {
			ids = append(ids, ch.id)
		}
	}
	for _, id := range ids {
		current, err := readMGLCControlChannel(ctx, turn, id)
		if err != nil {
			if errors.Is(err, ErrConflict) {
				return receiptRetired, saved, nil
			}
			return receiptConflict, saved, err
		}
		if current.removed != 0 && kind != mgLCControlRetireKind {
			return receiptRetired, saved, nil
		}
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
