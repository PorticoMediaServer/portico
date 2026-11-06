package writer

import (
	"strings"
	"unicode/utf8"
)

// These are inert, detached observations for ONE source-receipt removal. They
// neither attest native rows nor supply disclosure, ACK, a receipt codec, SQL,
// a registry entry, or an executable resource profile. W resolves original
// history before its eventual fresh handler obtains/rechecks these facts.
type wcReclaimOwner struct {
	playbackID                         string
	incarnation                        [16]byte
	lastSequence, acknowledgedSequence int64
	afterSequence, rows, variableBytes int64
	due                                bool
	dueMS                              int64
}

type wcReclaimKey struct {
	playbackID  string
	incarnation [16]byte
	sequence    int64
}

type wcReclaimChild struct {
	key           wcReclaimKey
	variableBytes int64 // actual persisted native bytes, not a caller's estimate
}

// Each bounded first-child probe is qualified by its FULL owner and exact
// range. Its nextSequence must be the native indexed ORDER BY sequence LIMIT 1
// observation, or zero for absence; preparation does not attest that query.
type wcReclaimProbe struct {
	playbackID                string
	incarnation               [16]byte
	afterSequence, throughACK int64
	nextSequence              int64 // zero iff the observed range is empty
}

// Slot presence is separate from RemovedCount. Native kind/NULL/count/codec/result
// digest validation remains W-owned; no newer slot may be decoded as an older
// requested receipt. Full equality below freezes the observed OLD context.
type wcReclaimSlot struct {
	present                       bool
	reference                     ReceiptReference
	resultDigest                  [32]byte
	floor, oldCursor              int64
	removedSequence, removedCount int64
	removedBytes, nextCursor      int64
	noNext                        bool
	appliedMS                     int64
}

type wcReclaimInput struct {
	owner                    wcReclaimOwner
	command                  CommandIdentity
	expectedOldSlot, oldSlot wcReclaimSlot
	firstProbe               wcReclaimProbe // (OLD cursor, ACK], before selecting child
	child                    *wcReclaimChild
	probe                    wcReclaimProbe // (selected child, ACK], or OLD cursor if absent
}

type wcReclaimPrepared struct {
	remove                       bool
	key                          wcReclaimKey
	removedVariableBytes         int64
	afterSequence, remainingRows int64
	remainingVariableBytes       int64
	noNext                       bool
}

// prepareWCReclaim checks the fresh, single-child transition without mutating
// its inputs. It does NOT classify history, perform the deletion, verify the
// supplied native byte observation, charge the replacement slot, or enqueue
// work. Those effects must co-commit on W's same turn after CHANGE-008 adoption.
func prepareWCReclaim(in wcReclaimInput) (wcReclaimPrepared, error) {
	zero := wcReclaimPrepared{}
	o := in.owner
	if o.playbackID == "" || len(o.playbackID) > wcMaxPlaybackIDBytes ||
		!utf8.ValidString(o.playbackID) || strings.IndexByte(o.playbackID, 0) >= 0 ||
		o.incarnation == [16]byte{} || in.command.OperationID == [16]byte{} || in.command.Sequence <= 0 {
		return zero, invalid("reclaim_identity")
	}
	if o.lastSequence < 0 || o.acknowledgedSequence < 0 ||
		o.acknowledgedSequence > o.lastSequence || o.afterSequence < 0 ||
		o.afterSequence > o.acknowledgedSequence || o.rows < 0 || o.variableBytes < 0 ||
		(o.rows == 0) != (o.variableBytes == 0) || o.dueMS < 0 || !o.due && o.dueMS != 0 {
		return zero, ErrResolveRequired
	}
	if !o.due {
		return zero, ErrConflict // no fresh idle identity just to rewrite the slot
	}
	if in.command.Incarnation != o.incarnation {
		return zero, ErrConflict
	}
	if o.lastSequence != o.acknowledgedSequence {
		return zero, ErrResolveRequired
	}
	// Both operands are nonnegative; subtract before adding at MaxInt64.
	if in.command.Sequence <= o.lastSequence || in.command.Sequence-o.lastSequence != 1 {
		return zero, ErrConflict
	}
	if in.oldSlot != in.expectedOldSlot {
		return zero, ErrConflict
	}
	if !validWCReclaimSlot(in.oldSlot, o) {
		return zero, ErrResolveRequired
	}
	if !validWCReclaimProbe(in.firstProbe, o, o.afterSequence) {
		return zero, ErrConflict
	}
	rows, bytes, cursor := o.rows, o.variableBytes, o.afterSequence
	var key wcReclaimKey
	var debit int64
	if c := in.child; c != nil {
		if c.key.playbackID != o.playbackID || c.key.incarnation != o.incarnation ||
			c.key.sequence <= cursor || c.key.sequence > o.acknowledgedSequence ||
			c.key.sequence != in.firstProbe.nextSequence {
			return zero, ErrConflict
		}
		if rows == 0 || c.variableBytes <= 0 || c.variableBytes > bytes {
			return zero, ErrResolveRequired
		}
		key, debit, cursor = c.key, c.variableBytes, c.key.sequence
		rows, bytes = rows-1, bytes-c.variableBytes
	} else if in.firstProbe.nextSequence != 0 {
		return zero, ErrResolveRequired
	}
	p := in.probe
	if !validWCReclaimProbe(p, o, cursor) {
		return zero, ErrConflict
	}
	noNext := p.nextSequence == 0
	// With last==ACK every surviving source child is below this floor. A
	// range/debt disagreement must not clear due or manufacture an idle result.
	if (rows == 0) != (bytes == 0) || noNext != (rows == 0) || in.child == nil && !noNext {
		return zero, ErrResolveRequired
	}
	if in.child != nil {
		key.playbackID = strings.Clone(key.playbackID)
	}
	return wcReclaimPrepared{
		remove: in.child != nil, key: key, removedVariableBytes: debit,
		afterSequence: cursor, remainingRows: rows, remainingVariableBytes: bytes, noNext: noNext,
	}, nil
}

func validWCReclaimProbe(p wcReclaimProbe, o wcReclaimOwner, cursor int64) bool {
	return p.playbackID == o.playbackID && p.incarnation == o.incarnation &&
		p.afterSequence == cursor && p.throughACK == o.acknowledgedSequence &&
		(p.nextSequence == 0 || p.nextSequence > cursor && p.nextSequence <= o.acknowledgedSequence)
}

func validWCReclaimSlot(s wcReclaimSlot, owner wcReclaimOwner) bool {
	if !s.present {
		return s == (wcReclaimSlot{}) && owner.afterSequence == 0
	}
	i := s.reference.Identity
	if i.Incarnation != owner.incarnation || i.Sequence <= 0 || i.Sequence > owner.acknowledgedSequence ||
		i.OperationID == [16]byte{} || s.reference.RequestDigest == (RequestDigest{}) ||
		s.resultDigest == [32]byte{} || s.floor < 0 || s.floor >= i.Sequence ||
		s.oldCursor < 0 || s.oldCursor > s.floor || s.appliedMS < 0 || s.nextCursor != owner.afterSequence ||
		!s.noNext && owner.rows == 0 {
		return false
	}
	switch s.removedCount {
	case 0:
		return s.removedSequence == 0 && s.removedBytes == 0 && s.nextCursor == s.oldCursor && s.noNext
	case 1:
		return s.removedSequence > s.oldCursor && s.removedSequence <= s.floor &&
			s.removedBytes > 0 && s.nextCursor == s.removedSequence
	default:
		return false
	}
}
