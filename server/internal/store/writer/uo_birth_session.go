package writer

import (
	"context"
	"fmt"

	"github.com/PorticoMediaServer/portico/server/internal/mutationreceipt"
	"github.com/PorticoMediaServer/portico/server/internal/playbackv1"
)

type uoBirthSessionLookup struct {
	turn     *commandTurn
	root     uoRootLookup
	original uoBirthSessionResult
	saved    bool
}

func (uoBirthSessionLookup) Format(s fmt.State, _ rune) {
	formatUSPreparation(s, "uoBirthSessionLookup")
}

// UO owns this user-root original. The root and command owner are already
// registered; a missing retry owner never becomes a new birth. MP owns the
// actual logical session, independent physical session/incarnation and
// source/queue/device admission in this SAME frame after Fresh. No current row
// is consulted to reconstruct a replay.
func lookupUOBirthSession(ctx context.Context, turn *commandTurn, a *usCurrentSessionAuthorization, q uoRootReference) (out *uoBirthSessionLookup, err error) {
	defer func() { uoPublicReadingPoison(turn, err) }()
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if turn == nil || turn.transaction == nil || turn.transaction.birthSession != nil || q.kind != 710 || !mutationreceipt.ValidRootID(q.root) || !q.validFor(turn) {
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
	out = &uoBirthSessionLookup{turn: turn, root: uoRootLookup{turn: turn, binding: &binding, reference: q}}
	turn.transaction.birthSession = out
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
		out.original, err = decodeUOBirthSession(q.identity, q.request, q.root, payload, digest)
		if err != nil {
			return nil, err
		}
		if root.resultCount != 0 || root.appliedMS != out.original.source.appliedMS {
			return nil, ErrResolveRequired
		}
		out.saved = true
	} else if root.decision == receiptRetired {
		out.saved = true
	} else if root.decision != receiptFresh {
		return nil, ErrConflict
	}
	turn.transaction.birthSession = out
	return out, nil
}

// This finalizer is not a session constructor. MP must create the logical
// session, its independent physical occurrence and the first-response view
// after Fresh. Here the sealed complete original and root counters co-commit;
// ignored failures/unsaved Fresh cannot cross the physical terminal.
func persistUOBirthSession(ctx context.Context, turn *commandTurn, lookup *uoBirthSessionLookup, source uoBirthSessionSource, view playbackv1.SessionView) (out ResultDigest, err error) {
	defer func() { uoPublicReadingPoison(turn, err) }()
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if lookup == nil || lookup.turn != turn || turn == nil || turn.transaction == nil || turn.transaction.birthSession != lookup || lookup.saved || lookup.root.decision != receiptFresh {
		return out, ErrInvalid
	}
	q := lookup.root.reference
	wire, err := sealUOBirthSessionView(view)
	if err != nil {
		return out, err
	}
	v := uoBirthSessionResult{source: source, wire: wire}
	if err = preflightUOBirthSession(v); err != nil {
		return out, err
	}
	if v.source.root != q.root || v.source.logical == q.identity.Incarnation || v.source.physicalIncarnation == q.identity.Incarnation {
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

func (t *transaction) checkUOBirthSessionCommit() error {
	v := t.birthSession
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
	original, err := decodeUOBirthSession(v.root.reference.identity, v.root.reference.request, v.root.reference.root, payload, digest)
	if err != nil || !original.equal(v.original) {
		return ErrConflict
	}
	if v.root.decision == receiptFresh {
		if current.owner.rows != v.root.owner.rows+1 || current.owner.variableBytes != v.root.owner.variableBytes+current.variableBytes || current.owner.revision != v.root.owner.revision+1 {
			return ErrConflict
		}
		if state != "active" {
			return ErrUORootGone
		}
		return checkUOBirthSessionNative(t.ctx, v.turn, v, v.original)
	}
	return nil
}

// The two born lifetimes are proved separately: the logical session row keys
// its own command incarnation, and the physical occurrence keys an independent
// one. Both must carry this identity's root/lifetimes with zero worker counters.
func checkUOBirthSessionNative(ctx context.Context, turn *commandTurn, lookup *uoBirthSessionLookup, v uoBirthSessionResult) error {
	if turn == nil || turn.transaction == nil || lookup == nil || lookup.root.binding == nil || lookup.root.binding.current == nil || lookup.root.binding.current.session == nil {
		return ErrInvalid
	}
	a := lookup.root.binding.current.session
	i := a.proof.witness.issuer
	now := turn.transaction.writer.now().UnixMilli()
	deadline, ok := turn.transaction.ctx.Deadline()
	if !ok || deadline.UnixMilli() > v.source.deadlineMS || v.source.device != a.proof.witness.device || v.source.tokenExpiresMS != a.proof.tokenExpiry.UnixMilli() || v.source.createdMS < turn.transaction.start.UnixMilli() || v.source.appliedMS > now || now >= v.source.deadlineMS || v.source.deadlineMS > a.proof.horizon.UnixMilli() {
		return ErrConflict
	}
	view, err := decodeUOBirthSessionView(v.wire)
	if err != nil || view.ID != v.source.session {
		return ErrConflict
	}
	leaseMS, ok := uoBirthSessionLeaseMS(view)
	if !ok {
		return ErrConflict
	}
	if err = lookup.root.binding.check(ctx, turn); err != nil {
		return err
	}
	if _, err = wc008DraftInteger(ctx, turn, uoBirthSessionLogical, v.source.session, v.source.device, i.account, i.profile, v.source.startKey, v.source.startBody, view.State, v.source.physical, v.source.physicalGeneration, leaseMS, v.source.createdMS, v.source.logical[:], v.source.root, i.memberIncarnation, i.profileLifetime); err != nil {
		return err
	}
	_, err = wc008DraftInteger(ctx, turn, uoBirthSessionPhysical, v.source.physical, v.source.physicalGeneration, v.source.physicalIncarnation[:], v.source.root, i.account, i.profile, i.memberIncarnation, i.profileLifetime)
	return err
}
