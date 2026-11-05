package writer

import (
	"errors"
	"fmt"
)

// CommandIdentity is captured once for a command and survives retries/restarts.
// Its native owner is a separate, typed domain field. These opaque bytes are
// not UUID strings, hashes of owner IDs, or permission/lease capabilities.
type CommandIdentity struct {
	Incarnation [16]byte
	Sequence    int64
	OperationID [16]byte
}

// RequestDigest is a versioned canonical digest of the complete typed request,
// recomputed by its writer constructor. Possessing a digest authorizes nothing.
type RequestDigest [32]byte

var (
	ErrConflict         = errors.New("writer command identity or digest conflict")
	ErrOwnerUnavailable = errors.New("writer receipt owner unavailable")
)

type receiptIdentity struct {
	command CommandIdentity
	digest  RequestDigest
}

// Facts are decoded by finite domain SQL scoped to the exact native owner.
// Current disclosure authority is checked before looking up/serving a result;
// fresh source/job/lease/cursor CAS is checked only after receiptFresh.
type receiptOwnerFacts struct {
	exists               bool
	incarnation          [16]byte
	lastSequence         int64
	acknowledgedSequence int64
}

type receiptFacts struct {
	exists       bool
	identity     receiptIdentity
	kind         CommandID
	resultCount  int64
	resultDigest [32]byte
}

type receiptDecision uint8

const (
	receiptFresh receiptDecision = iota + 1
	receiptReplay
	receiptRetired
	receiptResolveRequired
	receiptConflict
)

// classifyReceipt cannot declare Applied: replay still requires complete typed
// result/count/digest validation. It performs no SQL, current-input CAS, ACK,
// result reconstruction or effect retry. In this initial protocol a predecessor
// must be durably acknowledged separately before admitting the next sequence.
func classifyReceipt(expected receiptIdentity, kind CommandID, owner receiptOwnerFacts, found receiptFacts) (receiptDecision, error) {
	if expected.command.Sequence <= 0 {
		return receiptConflict, invalid("command_sequence")
	}
	if _, err := Describe(kind); err != nil {
		return receiptConflict, err
	}
	if !owner.exists || owner.incarnation != expected.command.Incarnation {
		return receiptResolveRequired, errors.Join(ErrResolveRequired, ErrOwnerUnavailable)
	}
	if owner.lastSequence < 0 || owner.acknowledgedSequence < 0 ||
		owner.acknowledgedSequence > owner.lastSequence || owner.lastSequence-owner.acknowledgedSequence > 1 {
		return receiptResolveRequired, fmt.Errorf("%w: invalid receipt owner counters", ErrResolveRequired)
	}
	if found.exists && (found.identity != expected || found.kind != kind) {
		return receiptConflict, ErrConflict
	}
	sequence := expected.command.Sequence
	// An ACKed cohort may already be partially reclaimed. Never decode or
	// serve it as Applied, even while its header still physically exists.
	if sequence <= owner.acknowledgedSequence {
		return receiptRetired, nil
	}
	if sequence <= owner.lastSequence {
		if !found.exists || found.resultCount < 0 {
			return receiptResolveRequired, fmt.Errorf("%w: incomplete unacknowledged receipt proof", ErrResolveRequired)
		}
		return receiptReplay, nil
	}
	if found.exists {
		return receiptResolveRequired, fmt.Errorf("%w: receipt ahead of its owner", ErrResolveRequired)
	}
	// Subtraction avoids wrapping last+1 at MaxInt64. Both operands are
	// positive here, and sequence > nonnegative last was proved above.
	if sequence-owner.lastSequence != 1 {
		return receiptConflict, ErrConflict
	}
	if owner.lastSequence != owner.acknowledgedSequence {
		return receiptResolveRequired, fmt.Errorf("%w: predecessor requires durable acknowledgement", ErrResolveRequired)
	}
	return receiptFresh, nil
}
