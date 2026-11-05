package writer

import (
	"errors"
	"math"
	"testing"
)

func receiptFixture() (receiptIdentity, receiptOwnerFacts, receiptFacts) {
	identity := receiptIdentity{command: CommandIdentity{Incarnation: [16]byte{1}, Sequence: 1, OperationID: [16]byte{2}}, digest: RequestDigest{3}}
	owner := receiptOwnerFacts{exists: true, incarnation: identity.command.Incarnation}
	found := receiptFacts{exists: true, identity: identity, kind: InventoryAccept, resultCount: 1, resultDigest: [32]byte{4}}
	return identity, owner, found
}

func TestReceiptDecisions(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*receiptIdentity, *receiptOwnerFacts, *receiptFacts)
		want   receiptDecision
		err    error
	}{
		{"first", func(_ *receiptIdentity, _ *receiptOwnerFacts, r *receiptFacts) { r.exists = false }, receiptFresh, nil},
		{"replay", func(_ *receiptIdentity, o *receiptOwnerFacts, _ *receiptFacts) { o.lastSequence = 1 }, receiptReplay, nil},
		{"missing unacknowledged proof", func(_ *receiptIdentity, o *receiptOwnerFacts, r *receiptFacts) { o.lastSequence, r.exists = 1, false }, receiptResolveRequired, ErrResolveRequired},
		{"receipt ahead of owner", func(_ *receiptIdentity, _ *receiptOwnerFacts, _ *receiptFacts) {}, receiptResolveRequired, ErrResolveRequired},
		{"retired missing header", func(_ *receiptIdentity, o *receiptOwnerFacts, r *receiptFacts) {
			o.lastSequence, o.acknowledgedSequence, r.exists = 1, 1, false
		}, receiptRetired, nil},
		{"retired partial result", func(_ *receiptIdentity, o *receiptOwnerFacts, r *receiptFacts) {
			o.lastSequence, o.acknowledgedSequence, r.resultCount = 1, 1, -1
		}, receiptRetired, nil},
		{"different digest", func(_ *receiptIdentity, o *receiptOwnerFacts, r *receiptFacts) {
			o.lastSequence, r.identity.digest[0] = 1, 99
		}, receiptConflict, ErrConflict},
		{"different operation", func(_ *receiptIdentity, o *receiptOwnerFacts, r *receiptFacts) {
			o.lastSequence, r.identity.command.OperationID[0] = 1, 99
		}, receiptConflict, ErrConflict},
		{"different kind", func(_ *receiptIdentity, o *receiptOwnerFacts, r *receiptFacts) {
			o.lastSequence, r.kind = 1, BasicAccept
		}, receiptConflict, ErrConflict},
		{"different sequence", func(_ *receiptIdentity, o *receiptOwnerFacts, r *receiptFacts) {
			o.lastSequence, r.identity.command.Sequence = 1, 2
		}, receiptConflict, ErrConflict},
		{"different receipt incarnation", func(_ *receiptIdentity, o *receiptOwnerFacts, r *receiptFacts) {
			o.lastSequence, r.identity.command.Incarnation[0] = 1, 99
		}, receiptConflict, ErrConflict},
		{"invalid count", func(_ *receiptIdentity, o *receiptOwnerFacts, r *receiptFacts) { o.lastSequence, r.resultCount = 1, -1 }, receiptResolveRequired, ErrResolveRequired},
		{"owner missing with old header", func(_ *receiptIdentity, o *receiptOwnerFacts, _ *receiptFacts) { o.exists = false }, receiptResolveRequired, ErrOwnerUnavailable},
		{"owner recycled", func(_ *receiptIdentity, o *receiptOwnerFacts, _ *receiptFacts) { o.incarnation[0] = 99 }, receiptResolveRequired, ErrOwnerUnavailable},
		{"negative last", func(_ *receiptIdentity, o *receiptOwnerFacts, _ *receiptFacts) { o.lastSequence = -1 }, receiptResolveRequired, ErrResolveRequired},
		{"negative floor", func(_ *receiptIdentity, o *receiptOwnerFacts, _ *receiptFacts) { o.acknowledgedSequence = -1 }, receiptResolveRequired, ErrResolveRequired},
		{"floor ahead", func(_ *receiptIdentity, o *receiptOwnerFacts, _ *receiptFacts) { o.acknowledgedSequence = 1 }, receiptResolveRequired, ErrResolveRequired},
		{"multiple unresolved", func(_ *receiptIdentity, o *receiptOwnerFacts, _ *receiptFacts) { o.lastSequence = 2 }, receiptResolveRequired, ErrResolveRequired},
		{"zero sequence", func(e *receiptIdentity, _ *receiptOwnerFacts, _ *receiptFacts) { e.command.Sequence = 0 }, receiptConflict, ErrInvalid},
		{"negative sequence", func(e *receiptIdentity, _ *receiptOwnerFacts, _ *receiptFacts) { e.command.Sequence = -1 }, receiptConflict, ErrInvalid},
		{"sequence gap", func(e *receiptIdentity, _ *receiptOwnerFacts, r *receiptFacts) {
			e.command.Sequence, r.exists = 2, false
		}, receiptConflict, ErrConflict},
	} {
		t.Run(test.name, func(t *testing.T) {
			expected, owner, found := receiptFixture()
			test.change(&expected, &owner, &found)
			got, err := classifyReceipt(expected, InventoryAccept, owner, found)
			if got != test.want || !errors.Is(err, test.err) {
				t.Fatalf("got %v/%v want %v/%v", got, err, test.want, test.err)
			}
		})
	}
}

func TestReceiptSequenceAdvanceRequiresDurableAck(t *testing.T) {
	expected, owner, _ := receiptFixture()
	expected.command.Sequence, owner.lastSequence = 2, 1
	if got, err := classifyReceipt(expected, InventoryAccept, owner, receiptFacts{}); got != receiptResolveRequired || !errors.Is(err, ErrResolveRequired) {
		t.Fatalf("advanced with unresolved predecessor: %v %v", got, err)
	}
	owner.acknowledgedSequence = 1
	if got, err := classifyReceipt(expected, InventoryAccept, owner, receiptFacts{}); got != receiptFresh || err != nil {
		t.Fatalf("did not advance after durable ACK: %v %v", got, err)
	}
	owner.lastSequence, owner.acknowledgedSequence = math.MaxInt64-1, math.MaxInt64-1
	expected.command.Sequence = math.MaxInt64
	if got, err := classifyReceipt(expected, InventoryAccept, owner, receiptFacts{}); got != receiptFresh || err != nil {
		t.Fatalf("last legal sequence: %v %v", got, err)
	}
	owner.lastSequence, owner.acknowledgedSequence = math.MaxInt64, math.MaxInt64
	if got, err := classifyReceipt(expected, InventoryAccept, owner, receiptFacts{}); got != receiptRetired || err != nil {
		t.Fatalf("max sequence wrapped/resurrected: %v %v", got, err)
	}
}

func TestReceiptOpaqueBytesAndUnknownPurpose(t *testing.T) {
	expected, owner, found := receiptFixture()
	// Schema identity bytes are opaque, not a UUID/nonzero-number grammar.
	expected.command.Incarnation, owner.incarnation = [16]byte{}, [16]byte{}
	expected.command.OperationID, expected.digest = [16]byte{}, RequestDigest{}
	if got, err := classifyReceipt(expected, InventoryAccept, owner, receiptFacts{}); got != receiptFresh || err != nil {
		t.Fatalf("coerced opaque bytes: %v %v", got, err)
	}
	if got, err := classifyReceipt(expected, CommandID(65535), owner, found); got != receiptConflict || !errors.Is(err, ErrInvalid) {
		t.Fatalf("unknown command authority: %v %v", got, err)
	}
}
