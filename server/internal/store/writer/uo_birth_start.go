package writer

import (
	"context"
	"fmt"

	"github.com/PorticoMediaServer/portico/server/internal/mutationreceipt"
)

type uoBirthStartLookup struct {
	turn     *commandTurn
	root     uoRootLookup
	original uoBirthStartResult
	saved    bool
}

func (uoBirthStartLookup) Format(s fmt.State, _ rune) { formatUSPreparation(s, "uoBirthStartLookup") }

// UO owns this user-root original. The root and command owner must already be
// registered; a missing retry owner never becomes a new birth. MP owns actual
// selected-source/physical/process/queue admission and row birth, in this SAME
// frame after Fresh. No source row is consulted to reconstruct a replay.
func lookupUOBirthStart(ctx context.Context, turn *commandTurn, a *usCurrentSessionAuthorization, q uoRootReference) (out *uoBirthStartLookup, err error) {
	defer func() { uoPublicReadingPoison(turn, err) }()
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if turn == nil || turn.transaction == nil || turn.transaction.birthStart != nil || q.kind != 700 || !mutationreceipt.ValidRootID(q.root) || !q.validFor(turn) {
		return nil, ErrInvalid
	}
	proof, err := bindUSCurrentUORoot(ctx, turn, a)
	if err != nil {
		return nil, err
	}
	binding, err := bindUORoot(turn, nil, proof)
	if err != nil {
		return nil, err
	}
	out = &uoBirthStartLookup{turn: turn, root: uoRootLookup{turn: turn, binding: &binding, reference: q}}
	turn.transaction.birthStart = out
	root, err := lookupUORootCommand(ctx, turn, &binding, q)
	if err != nil {
		return nil, err
	}
	out.root = root
	if root.decision == receiptReplay {
		payload, digest, e := readUORootPayload(ctx, turn, root)
		if e != nil {
			return nil, e
		}
		out.original, err = decodeUOBirthStart(q.identity, q.request, q.root, payload, digest)
		if err != nil {
			return nil, err
		}
		if root.resultCount != 0 || root.appliedMS != out.original.appliedMS {
			return nil, ErrResolveRequired
		}
		out.saved = true
	} else if root.decision == receiptRetired {
		out.saved = true
	} else if root.decision != receiptFresh {
		return nil, ErrConflict
	}
	turn.transaction.birthStart = out
	return out, nil
}

// This finalizer is not a preparation/physical-process constructor. MP must
// create the actual selected row and independent worker owner after Fresh.
// Here exact native birth facts, immutable user original, and root counters
// co-commit; ignored failures/unsaved Fresh cannot cross the physical terminal.
func persistUOBirthStart(ctx context.Context, turn *commandTurn, lookup *uoBirthStartLookup, v uoBirthStartResult) (out ResultDigest, err error) {
	defer func() { uoPublicReadingPoison(turn, err) }()
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if lookup == nil || lookup.turn != turn || turn == nil || turn.transaction == nil || turn.transaction.birthStart != lookup || lookup.saved || lookup.root.decision != receiptFresh {
		return out, ErrInvalid
	}
	q := lookup.root.reference
	if err = preflightUOBirthStart(v); err != nil {
		return out, err
	}
	if v.root != q.root || v.worker == q.identity.Incarnation {
		return out, ErrConflict
	}
	out, err = persistUORootCanonical(ctx, turn, lookup.root, v)
	if err != nil {
		return out, err
	}
	lookup.original = v
	lookup.saved = true
	return out, nil
}

func (t *transaction) checkUOBirthStartCommit() error {
	v := t.birthStart
	if v == nil {
		return nil
	}
	if v.turn == nil || v.turn.transaction != t || !v.saved {
		return ErrConflict
	}
	if err := v.root.binding.check(t.ctx, v.turn); err != nil {
		return err
	}
	state, err := v.root.binding.root(t.ctx, v.turn, v.root.reference.root)
	if err != nil {
		return err
	}
	current, err := lookupUORootCommand(t.ctx, v.turn, v.root.binding, v.root.reference)
	if err != nil {
		return err
	}
	if v.root.decision == receiptRetired {
		if current.decision != receiptRetired {
			return ErrConflict
		}
		return nil
	}
	if current.decision != receiptReplay {
		return ErrConflict
	}
	payload, digest, err := readUORootPayload(t.ctx, v.turn, current)
	if err != nil {
		return err
	}
	original, err := decodeUOBirthStart(v.root.reference.identity, v.root.reference.request, v.root.reference.root, payload, digest)
	if err != nil || original != v.original {
		return ErrConflict
	}
	if v.root.decision == receiptFresh {
		if current.owner.rows != v.root.owner.rows+1 || current.owner.variableBytes != v.root.owner.variableBytes+current.variableBytes || current.owner.revision != v.root.owner.revision+1 {
			return ErrConflict
		}
		if state != "active" {
			return ErrUORootGone
		}
		return checkUOBirthStartNative(t.ctx, v.turn, v, v.original)
	}
	return nil
}

func checkUOBirthStartNative(ctx context.Context, turn *commandTurn, lookup *uoBirthStartLookup, v uoBirthStartResult) error {
	if turn == nil || turn.transaction == nil || lookup == nil || lookup.root.binding == nil || lookup.root.binding.current == nil || lookup.root.binding.current.session == nil {
		return ErrInvalid
	}
	a := lookup.root.binding.current.session
	i := a.proof.witness.issuer
	now := turn.transaction.writer.now().UnixMilli()
	deadline, ok := turn.transaction.ctx.Deadline()
	if !ok || deadline.UnixMilli() > v.deadlineMS || v.device != a.proof.witness.device || v.tokenExpiresMS != a.proof.tokenExpiry.UnixMilli() || v.createdMS < turn.transaction.start.UnixMilli() || v.appliedMS > now || now >= v.deadlineMS || v.deadlineMS > a.proof.horizon.UnixMilli() {
		return ErrConflict
	}
	if err := lookup.root.binding.check(ctx, turn); err != nil {
		return err
	}
	_, err := wc008DraftInteger(ctx, turn, uoBirthStartNative, v.preparation, v.device, v.startKey, v.startBody, v.item, v.asset, v.part, v.replaces, v.replacesGeneration, v.tokenExpiresMS, v.createdMS, v.deadlineMS, v.worker[:], lookup.root.reference.root, i.memberIncarnation, i.profileLifetime)
	return err
}

func isUOUserBirthPurpose(kind CommandID) bool {
	switch kind {
	case 700, 710, 740, 750, 761:
		return true
	}
	return false
}
