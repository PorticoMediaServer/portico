package writer

import (
	"errors"
	"reflect"
	"testing"
)

func iaRetirementHistoryFixture(t *testing.T) (iaRetirementReference, iaRetirementOwner, iaRetirementReceipt) {
	t.Helper()
	id, request, result := iaRetirementResultFixture()
	digest, err := canonicalResultDigest(id, request, iaRetireSourceResult{result})
	if err != nil {
		t.Fatal(err)
	}
	retained, err := iaRetirementRetainedBytes(IARetireSource, result)
	if err != nil {
		t.Fatal(err)
	}
	q := iaRetirementReference{kind: "folder", target: result.original.target, libraryID: result.original.libraryID,
		libraryKey: result.original.libraryKey, originalOperation: result.original.operationID, identity: id, request: request, purpose: IARetireSource}
	owner := iaRetirementOwner{exists: true, original: result.original, incarnation: id.Incarnation, last: 1, retainedCount: 1, retainedBytes: retained}
	header := iaRetirementReceipt{exists: true, identity: receiptIdentity{id, request}, purpose: IARetireSource,
		version: 1, result: result, digest: digest, retainedBytes: retained}
	return q, owner, header
}

func TestIARetirementHistoryReplayAndAckFloor(t *testing.T) {
	q, owner, header := iaRetirementHistoryFixture(t)
	before := owner
	got, err := classifyIARetirementHistory(q, owner, header)
	if err != nil || got.decision != receiptReplay || got.result != header.result || got.digest != header.digest || owner != before {
		t.Fatal("incomplete replay", got, err)
	}
	// Mutable source health/root/lease/retirement state intentionally have no
	// fields in this history API: they cannot filter saved original history.
	owner.acknowledged = 1
	corrupt := header
	corrupt.result, corrupt.digest, corrupt.version, corrupt.retainedBytes = iaRetirementResult{}, ResultDigest{}, 0, 0
	for _, h := range []iaRetirementReceipt{corrupt, {}} {
		got, err = classifyIARetirementHistory(q, owner, h)
		if err != nil || got.decision != receiptRetired || got.result != (iaRetirementResult{}) || got.digest != (ResultDigest{}) {
			t.Fatal("ACKed result decoded", got, err)
		}
	}
	// An existing nonce/digest conflict precedes the floor, exactly as W does.
	corrupt.identity.digest[1]++
	got, err = classifyIARetirementHistory(q, owner, corrupt)
	if !errors.Is(err, ErrConflict) || !reflect.DeepEqual(got, iaRetirementHistory{}) {
		t.Fatal("floor hid retained conflict", got, err)
	}
	owner.reclaimed, owner.retainedCount, owner.retainedBytes = 1, 0, 0
	q.request[0]++
	got, err = classifyIARetirementHistory(q, owner, iaRetirementReceipt{})
	if !errors.Is(err, ErrConflict) || got != (iaRetirementHistory{}) {
		t.Fatal("reclaimed BEGIN lost original anchor", got, err)
	}
}

func TestIARetirementHistoryRejectsProofGaps(t *testing.T) {
	q, owner, header := iaRetirementHistoryFixture(t)
	for _, mutate := range []func(*iaRetirementReceipt){
		func(v *iaRetirementReceipt) { v.exists = false }, func(v *iaRetirementReceipt) { v.version++ },
		func(v *iaRetirementReceipt) { v.resultCount = 1 }, func(v *iaRetirementReceipt) { v.result.original.resolvedRoot += "other" },
		func(v *iaRetirementReceipt) { v.result.appliedMS++ }, func(v *iaRetirementReceipt) { v.result.original.rootIdentity += "x" },
		func(v *iaRetirementReceipt) { v.retainedBytes-- }, func(v *iaRetirementReceipt) { v.digest[31]++ },
	} {
		bad := header
		mutate(&bad)
		got, err := classifyIARetirementHistory(q, owner, bad)
		if !errors.Is(err, ErrResolveRequired) || got != (iaRetirementHistory{}) {
			t.Fatal("incomplete retained proof escaped", got, err)
		}
	}
	for _, mutate := range []func(*iaRetirementOwner){
		func(v *iaRetirementOwner) { v.exists = false }, func(v *iaRetirementOwner) { v.incarnation[0]++ },
		func(v *iaRetirementOwner) { v.last = 2 }, func(v *iaRetirementOwner) { v.acknowledged = 2 },
		func(v *iaRetirementOwner) { v.reclaimed = 1 }, func(v *iaRetirementOwner) { v.retainedBytes-- },
		func(v *iaRetirementOwner) { v.retainedCount = 0 },
	} {
		bad := owner
		mutate(&bad)
		got, err := classifyIARetirementHistory(q, bad, header)
		if !errors.Is(err, ErrResolveRequired) || got != (iaRetirementHistory{}) {
			t.Fatal("missing/corrupt owner became birth", got, err)
		}
	}
	for _, mutate := range []func(*iaRetirementReference){
		func(v *iaRetirementReference) { v.libraryID++ }, func(v *iaRetirementReference) { v.libraryKey += "other" },
	} {
		bad := q
		mutate(&bad)
		got, err := classifyIARetirementHistory(bad, owner, header)
		if !errors.Is(err, ErrDisclosureDenied) || got != (iaRetirementHistory{}) {
			t.Fatal("cross-library result disclosed", got, err)
		}
	}
	q.originalOperation[0]++
	if got, err := classifyIARetirementHistory(q, owner, header); !errors.Is(err, ErrConflict) || got != (iaRetirementHistory{}) {
		t.Fatal("wrong original operation", got, err)
	}
}

func TestIARetirementHistoryFreshStepAndDebt(t *testing.T) {
	q, owner, header := iaRetirementHistoryFixture(t)
	before := owner
	owner.acknowledged = 1
	if owner.retainedBytes != before.retainedBytes || owner.retainedCount != before.retainedCount {
		t.Fatal("ACK refunded debt")
	}
	q.identity.Sequence, q.identity.OperationID, q.purpose = 2, [16]byte{3}, IARetireStep
	got, err := classifyIARetirementHistory(q, owner, iaRetirementReceipt{})
	if err != nil || got.decision != receiptFresh || got.result != (iaRetirementResult{}) {
		t.Fatal("fresh STEP classification", got, err)
	}
	// Unacknowledged predecessor must be consumed before a new sequence.
	unacked := owner
	unacked.acknowledged = 0
	if got, err := classifyIARetirementHistory(q, unacked, iaRetirementReceipt{}); !errors.Is(err, ErrResolveRequired) || got != (iaRetirementHistory{}) {
		t.Fatal("pending predecessor bypassed", got, err)
	}
	// Reclaim captured receipt1 only; no gap, no unacknowledged refund, no
	// fabricated header byte amount, and no mutation of the original input.
	next, err := iaRetirementReclaimDebt(owner, 1, header.retainedBytes)
	if err != nil || next.reclaimed != 1 || next.retainedCount != 0 || next.retainedBytes != 0 || owner.retainedCount != 1 {
		t.Fatal("exact reclaim delta", next, err)
	}
	for _, tc := range []struct{ sequence, bytes int64 }{{0, header.retainedBytes}, {2, header.retainedBytes}, {1, 0}, {1, header.retainedBytes - 1}, {1, header.retainedBytes + 1}} {
		got, err := iaRetirementReclaimDebt(owner, tc.sequence, tc.bytes)
		if !errors.Is(err, ErrResolveRequired) || got != (iaRetirementOwner{}) {
			t.Fatal("unproved refund", tc, got, err)
		}
	}
}
