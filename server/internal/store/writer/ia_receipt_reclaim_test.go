package writer

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"math"
	"strings"
	"testing"
)

func iaReclaimFixture(config bool) (iaReceiptReclaimReference, iaReceiptReclaimOwner) {
	q := iaReceiptReclaimReference{purpose: IARetirementReceiptReclaim, identity: CommandIdentity{Incarnation: [16]byte{1}, OperationID: [16]byte{3}, Sequence: 2}, request: RequestDigest{4}}
	_, _, source := iaRetirementResultFixture()
	q.retirement = source.original
	if config {
		q.purpose = IAConfigurationReceiptReclaim
		q.retirement = iaRetirementOriginal{}
		q.configuration = iaConfigurationReceiptOriginal{operationID: [16]byte{2}, requestDigest: RequestDigest{5}, purpose: IAConfigureSource, libraryID: 7, libraryKey: "library", sourceID: "source"}
	}
	o := iaReceiptReclaimOwner{exists: true, incarnation: q.identity.Incarnation, configuration: q.configuration, retirement: q.retirement, last: 1, acknowledged: 1, cursor: iaReceiptCursor{0, -1}, rows: 1, bytes: 238, due: true, dueMS: 100}
	return q, o
}

func iaReclaimProbe(q iaReceiptReclaimReference, o iaReceiptReclaimOwner, after iaReceiptCursor, u iaReceiptReclaimUnit) iaReceiptReclaimProbe {
	op := q.retirement.operationID
	if q.purpose == IAConfigurationReceiptReclaim {
		op = q.configuration.operationID
	}
	return iaReceiptReclaimProbe{incarnation: o.incarnation, originalOperation: op, after: after, throughACK: o.acknowledged, unit: u}
}

func iaReclaimSave(t *testing.T, q iaReceiptReclaimReference, o iaReceiptReclaimOwner, v iaReceiptReclaimResult) iaReceiptReclaimOwner {
	t.Helper()
	digest, err := canonicalResultDigest(q.identity, q.request, iaReceiptReclaimValue(q, v))
	if err != nil {
		t.Fatal(err)
	}
	charge, err := iaReceiptReclaimSlotBytes(q.purpose, v)
	if err != nil {
		t.Fatal(err)
	}
	o.last, o.cursor, o.rows, o.bytes, o.due = q.identity.Sequence, v.next, v.rows, v.bytes, !v.noNext
	o.slot = iaReceiptReclaimSlot{present: true, identity: q.identity, request: q.request, purpose: q.purpose, version: 1, resultCount: 1, facts: v, digest: digest, retainedBytes: charge}
	return o
}

// Independent wire oracle: no production encoder, preflight, charge constant or
// reflection codec. Every field is encoded explicitly with its native presence.
func iaReclaimWire(q iaReceiptReclaimReference, v iaReceiptReclaimResult) []byte {
	var b bytes.Buffer
	n := func(v int64) { _ = binary.Write(&b, binary.BigEndian, v) }
	s := func(v string) { n(int64(len(v))); b.WriteString(v) }
	f := func(v bool) {
		if v {
			b.WriteByte(1)
		} else {
			b.WriteByte(0)
		}
	}
	b.WriteString("portico-writer-result-sha256-v1\x00")
	b.Write([]byte{0, 1})
	config := q.purpose == IAConfigurationReceiptReclaim
	if config {
		s("configuration_receipt_reclaim")
	} else {
		s("retirement_receipt_reclaim")
	}
	s("receipt_reclaim")
	s("applied")
	b.Write(q.identity.Incarnation[:])
	n(q.identity.Sequence)
	b.Write(q.identity.OperationID[:])
	b.Write(q.request[:])
	if config {
		o := q.configuration
		n(int64(o.purpose))
		b.Write(o.operationID[:])
		b.Write(o.requestDigest[:])
		n(o.libraryID)
		s(o.libraryKey)
		f(o.purpose == IAConfigureSource)
		s(o.sourceID)
	} else {
		o := q.retirement
		s(o.kind)
		s(o.target)
		n(o.libraryID)
		s(o.libraryKey)
		s(o.resolvedRoot)
		f(o.kind == "folder")
		s(o.sourceIncarnation)
		n(o.sourceGeneration)
		s(o.rootIdentity)
		b.Write(o.operationID[:])
		b.Write(o.requestDigest[:])
	}
	n(v.capturedACK)
	n(v.old.sequence)
	f(config)
	if config {
		n(v.old.ordinal)
	}
	s(v.removedKind)
	f(v.removedCount == 1)
	if v.removedCount == 1 {
		n(v.removedSequence)
	}
	f(v.removedKind == "source")
	if v.removedKind == "source" {
		n(v.removedOrdinal)
	}
	n(v.removedCount)
	n(v.removedBytes)
	n(v.next.sequence)
	if config {
		n(v.next.ordinal)
	}
	n(v.rows)
	n(v.bytes)
	f(v.noNext)
	n(v.appliedMS)
	n(1)
	n(0)
	return b.Bytes()
}

func TestIAReceiptReclaimCanonicalWireAndExactCharge(t *testing.T) {
	for _, config := range []bool{false, true} {
		q, _ := iaReclaimFixture(config)
		v := iaReceiptReclaimResult{capturedACK: 1, old: iaReceiptCursor{0, -1}, next: iaReceiptCursor{1, -1}, removedKind: "header", removedSequence: 1, removedOrdinal: -1, removedCount: 1, removedBytes: 238, noNext: true, appliedMS: 100}
		cases := []iaReceiptReclaimResult{v}
		if config {
			v.old.ordinal = 0
			cases = []iaReceiptReclaimResult{v}
			source := v
			source.old.ordinal = -1
			source.next = iaReceiptCursor{0, 0}
			source.removedKind, source.removedOrdinal = "source", 0
			source.noNext = false
			source.rows, source.bytes = 1, 195
			cases = append(cases, source)
		}
		empty := v
		empty.old = empty.next
		empty.removedKind, empty.removedCount, empty.removedBytes, empty.removedSequence = "none", 0, 0, 0
		cases = append(cases, empty)
		for _, v := range cases {
			wire := iaReclaimWire(q, v)
			var actual iaResultRecorder
			if err := encodeCanonicalResult(&requestEncoder{h: &actual}, q.identity, q.request, iaReceiptReclaimValue(q, v)); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(wire, actual.Bytes()) {
				t.Fatal("fixed-slot result wire drift")
			}
			digest, err := canonicalResultDigest(q.identity, q.request, iaReceiptReclaimValue(q, v))
			if err != nil || digest != sha256.Sum256(wire) {
				t.Fatal("digest differs from independent wire", err)
			}
			variable := len(q.retirement.kind) + len(q.retirement.target) + len(q.retirement.libraryKey) + len(q.retirement.resolvedRoot) + len(q.retirement.sourceIncarnation) + len(q.retirement.rootIdentity)
			if config {
				variable = len(q.configuration.libraryKey) + len(q.configuration.sourceID)
			}
			if len(wire) != iaReceiptReclaimWireFixed(q.purpose, v)+variable {
				t.Fatal("whole envelope bound differs from wire")
			}
			charge, err := iaReceiptReclaimSlotBytes(q.purpose, v)
			want := int64(216)
			if !config && v.removedCount == 0 {
				want = 206
			}
			if config {
				want = 235
				if v.removedKind == "source" {
					want = 243
				}
				if v.removedKind == "none" {
					want = 225
				}
			}
			if err != nil || charge != want {
				t.Fatal("logical native payload charge differs", charge, want, err)
			}
		}
	}
}

func TestIAReceiptReclaimPrepareReplayAndAckLifetime(t *testing.T) {
	q, o := iaReclaimFixture(true)
	o.rows, o.bytes = 3, 622
	first := iaReceiptReclaimUnit{kind: "source", sequence: 1, ordinal: 0, bytes: 215}
	after := iaReceiptReclaimUnit{kind: "source", sequence: 1, ordinal: 1, bytes: 212}
	in := iaReceiptReclaimInput{reference: q, owner: o, first: iaReclaimProbe(q, o, o.cursor, first), after: iaReclaimProbe(q, o, iaReceiptCursor{0, 0}, after), appliedMS: 100}
	v, err := prepareIAReceiptReclaim(in)
	if err != nil || v.rows != 2 || v.bytes != 407 {
		t.Fatal("first source transition", v, err)
	}
	o = iaReclaimSave(t, q, o, v)
	if o.bytes+o.slot.retainedBytes != 650 {
		t.Fatal("new slot growth was hidden by deletion")
	}
	// Model a lost return: original reference resolves the saved scalar before
	// any fresh due/probe consideration, even though the deleted child is gone.
	if d, e := classifyIAReceiptReclaim(q, o); e != nil || d != receiptReplay {
		t.Fatal("original lost-return resolution", d, e)
	}
	wrong := q
	wrong.request[0]++
	if _, e := classifyIAReceiptReclaim(wrong, o); !errors.Is(e, ErrConflict) {
		t.Fatal("changed original request replayed", e)
	}
	corrupt := o
	corrupt.slot.facts.removedBytes++
	if _, e := classifyIAReceiptReclaim(q, corrupt); !errors.Is(e, ErrResolveRequired) {
		t.Fatal("corrupt saved result replayed", e)
	}
	next := q
	next.identity.Sequence++
	next.identity.OperationID = [16]byte{6}
	if _, e := classifyIAReceiptReclaim(next, o); !errors.Is(e, ErrResolveRequired) {
		t.Fatal("unacknowledged slot replaced", e)
	}
	// ACK refunds nothing and does not decode an ACKed, partially reclaimed
	// source result. Only its retained scalar metadata can identify a conflict.
	o.acknowledged = o.last
	before := o
	corrupt = o
	corrupt.slot.facts = iaReceiptReclaimResult{}
	if d, e := classifyIAReceiptReclaim(q, corrupt); e != nil || d != receiptRetired {
		t.Fatal("decoded below ACK", d, e)
	}
	if o.rows != before.rows || o.bytes != before.bytes || o.slot != before.slot {
		t.Fatal("ACK changed debt or fixed slot")
	}
	in = iaReceiptReclaimInput{reference: next, owner: o, expectedOldSlot: o.slot, first: iaReclaimProbe(next, o, o.cursor, after), after: iaReclaimProbe(next, o, iaReceiptCursor{0, 1}, iaReceiptReclaimUnit{kind: "header", sequence: 1, ordinal: -1, bytes: 195, sourceCount: 2}), appliedMS: 101}
	v, err = prepareIAReceiptReclaim(in)
	if err != nil || v.rows != 1 || v.bytes != 195 {
		t.Fatal("second source transition", v, err)
	}
	o = iaReclaimSave(t, next, o, v)
	o.acknowledged = o.last
	q = next
	next.identity.Sequence++
	next.identity.OperationID = [16]byte{7}
	in = iaReceiptReclaimInput{reference: next, owner: o, expectedOldSlot: o.slot, first: iaReclaimProbe(next, o, o.cursor, iaReceiptReclaimUnit{kind: "header", sequence: 1, ordinal: -1, bytes: 195, sourceCount: 2}), after: iaReclaimProbe(next, o, iaReceiptCursor{1, -1}, iaReceiptReclaimUnit{}), appliedMS: 102}
	v, err = prepareIAReceiptReclaim(in)
	if err != nil || !v.noNext || v.rows != 0 || v.bytes != 0 {
		t.Fatal("empty-header transition", v, err)
	}
	o = iaReclaimSave(t, next, o, v)
	if o.slot.retainedBytes != 235 || o.due {
		t.Fatal("terminal scalar did not remain charged")
	}
	if d, e := classifyIAReceiptReclaim(q, o); e != nil || d != receiptRetired {
		t.Fatal("newer slot supplied older result", d, e)
	}
}

func TestIAReceiptReclaimNativeFactRefusals(t *testing.T) {
	q, o := iaReclaimFixture(false)
	in := iaReceiptReclaimInput{reference: q, owner: o, first: iaReclaimProbe(q, o, o.cursor, iaReceiptReclaimUnit{kind: "header", sequence: 1, ordinal: -1, bytes: 238}), after: iaReclaimProbe(q, o, iaReceiptCursor{1, -1}, iaReceiptReclaimUnit{}), appliedMS: 100}
	if _, err := prepareIAReceiptReclaim(in); err != nil {
		t.Fatal(err)
	}
	// IA BEGIN/configuration always retains a scalar data header. Empty
	// unreceipted accounting is not a genesis empty owner or a W exception:
	// never adopt a missing/deleted header as a fresh zero-removal result.
	missing := in
	missing.owner.rows, missing.owner.bytes = 0, 0
	missing.first = iaReclaimProbe(q, missing.owner, missing.owner.cursor, iaReceiptReclaimUnit{})
	missing.after = missing.first
	if _, err := prepareIAReceiptReclaim(missing); !errors.Is(err, ErrResolveRequired) {
		t.Fatal("missing data became a fresh empty result", err)
	}
	for _, change := range []func(*iaReceiptReclaimInput){
		func(v *iaReceiptReclaimInput) { v.owner.due = false },
		func(v *iaReceiptReclaimInput) { v.owner.retirement.resolvedRoot += "/replacement" },
		func(v *iaReceiptReclaimInput) { v.owner.retirement.sourceGeneration++ },
		func(v *iaReceiptReclaimInput) { v.expectedOldSlot.present = true },
		func(v *iaReceiptReclaimInput) { v.first.originalOperation[0]++ },
		func(v *iaReceiptReclaimInput) { v.first.unit.bytes++ },
		func(v *iaReceiptReclaimInput) { v.first.unit.sequence++ },
		func(v *iaReceiptReclaimInput) { v.after.after.sequence = 0 },
		func(v *iaReceiptReclaimInput) { v.owner.rows++ },
		func(v *iaReceiptReclaimInput) { v.owner.bytes++ },
		func(v *iaReceiptReclaimInput) { v.appliedMS = 99 },
		func(v *iaReceiptReclaimInput) { v.reference.identity.Sequence = math.MaxInt64 },
	} {
		bad := in
		change(&bad)
		if _, err := prepareIAReceiptReclaim(bad); err == nil {
			t.Fatal("inconsistent native input admitted", bad)
		}
	}
	q, o = iaReclaimFixture(true)
	o.rows, o.bytes = 3, 622
	initial := iaReceiptReclaimInput{reference: q, owner: o, first: iaReclaimProbe(q, o, o.cursor, iaReceiptReclaimUnit{kind: "source", sequence: 1, ordinal: 0, bytes: 215}), after: iaReclaimProbe(q, o, iaReceiptCursor{0, 0}, iaReceiptReclaimUnit{kind: "source", sequence: 1, ordinal: 1, bytes: 212}), appliedMS: 100}
	accepted, err := prepareIAReceiptReclaim(initial)
	if err != nil {
		t.Fatal(err)
	}
	o = iaReclaimSave(t, q, o, accepted)
	o.acknowledged = o.last
	q.identity.Sequence++
	q.identity.OperationID = [16]byte{6}
	in = iaReceiptReclaimInput{reference: q, owner: o, first: iaReclaimProbe(q, o, o.cursor, iaReceiptReclaimUnit{kind: "header", sequence: 1, ordinal: -1, bytes: 195, sourceCount: 2}), after: iaReclaimProbe(q, o, iaReceiptCursor{1, -1}, iaReceiptReclaimUnit{}), appliedMS: 100}
	in.expectedOldSlot = o.slot
	if _, err := prepareIAReceiptReclaim(in); err == nil {
		t.Fatal("header removed before complete source ordinal proof")
	}
}

func TestIAReceiptReclaimRetirementAllowsOnlyProvenDataHoles(t *testing.T) {
	q, o := iaReclaimFixture(false)
	q.identity.Sequence = 3
	o.last, o.acknowledged, o.rows, o.bytes = 2, 2, 2, 481
	in := iaReceiptReclaimInput{reference: q, owner: o, first: iaReclaimProbe(q, o, o.cursor, iaReceiptReclaimUnit{kind: "header", sequence: 1, ordinal: -1, bytes: 238}), after: iaReclaimProbe(q, o, iaReceiptCursor{1, -1}, iaReceiptReclaimUnit{kind: "header", sequence: 2, ordinal: -1, bytes: 243}), appliedMS: 100}
	v, err := prepareIAReceiptReclaim(in)
	if err != nil {
		t.Fatal(err)
	}
	o = iaReclaimSave(t, q, o, v)
	o.acknowledged = o.last
	q.identity.Sequence = 4
	q.identity.OperationID = [16]byte{4}
	in = iaReceiptReclaimInput{reference: q, owner: o, expectedOldSlot: o.slot, first: iaReclaimProbe(q, o, o.cursor, iaReceiptReclaimUnit{kind: "header", sequence: 2, ordinal: -1, bytes: 243}), after: iaReclaimProbe(q, o, iaReceiptCursor{2, -1}, iaReceiptReclaimUnit{}), appliedMS: 101}
	v, err = prepareIAReceiptReclaim(in)
	if err != nil {
		t.Fatal(err)
	}
	o = iaReclaimSave(t, q, o, v)
	o.acknowledged = o.last
	old := o.slot
	// An independently committed/ACKed physical STEP5 retains new data. Its
	// result must not mutate the previously acknowledged GC4 saved post-debt.
	o.last, o.acknowledged, o.rows, o.bytes, o.due, o.dueMS = 5, 5, 1, 243, true, 102
	q.identity.Sequence = 6
	q.identity.OperationID = [16]byte{6}
	in = iaReceiptReclaimInput{reference: q, owner: o, expectedOldSlot: o.slot, first: iaReclaimProbe(q, o, o.cursor, iaReceiptReclaimUnit{kind: "header", sequence: 5, ordinal: -1, bytes: 243}), after: iaReclaimProbe(q, o, iaReceiptCursor{5, -1}, iaReceiptReclaimUnit{}), appliedMS: 102}
	v, err = prepareIAReceiptReclaim(in)
	if err != nil || v.removedSequence != 5 || v.next.sequence != 5 || !v.noNext {
		t.Fatal("GC-only holes were treated as missing data", v, err)
	}
	if old != o.slot || o.slot.facts.rows != 0 || o.slot.facts.bytes != 0 {
		t.Fatal("later STEP rewrote old saved GC output")
	}
	bad := in
	bad.first.unit.sequence = 3
	bad.after.after.sequence = 3
	// Only an actual full-owner probe attests which header comes first. The
	// native issuer must query this fact; detached values confer no SQL proof.
	bad.first.originalOperation[0]++
	if _, err := prepareIAReceiptReclaim(bad); err == nil {
		t.Fatal("cross-original data probe admitted")
	}
}

func TestIAReceiptReclaimCompleteEnvelopeAndScope(t *testing.T) {
	q, _ := iaReclaimFixture(false)
	v := iaReceiptReclaimResult{capturedACK: 1, old: iaReceiptCursor{0, -1}, next: iaReceiptCursor{1, -1}, removedKind: "header", removedSequence: 1, removedOrdinal: -1, removedCount: 1, removedBytes: 238, noNext: true, appliedMS: 100}
	original := iaRetirementReceiptReclaimResult{q.retirement, v}
	before, err := canonicalResultDigest(q.identity, q.request, original)
	if err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*iaRetirementReceiptReclaimResult){
		func(v *iaRetirementReceiptReclaimResult) { v.original.resolvedRoot += "/new" },
		func(v *iaRetirementReceiptReclaimResult) { v.original.rootIdentity += "new" },
		func(v *iaRetirementReceiptReclaimResult) { v.original.sourceGeneration++ },
		func(v *iaRetirementReceiptReclaimResult) { v.original.requestDigest[0]++ },
		func(v *iaRetirementReceiptReclaimResult) { v.facts.removedBytes++ },
		func(v *iaRetirementReceiptReclaimResult) { v.facts.appliedMS++ },
	} {
		changed := original
		change(&changed)
		after, e := canonicalResultDigest(q.identity, q.request, changed)
		if e != nil || after == before {
			t.Fatal("saved source/result fact was not bound", e)
		}
	}
	bad := original
	bad.original.resolvedRoot = strings.Repeat("x", maxCanonicalResultBytes) + "\xff"
	if _, e := canonicalResultDigest(q.identity, q.request, bad); !errors.Is(e, ErrInvalid) {
		t.Fatal("oversized complete envelope admitted", e)
	}
	for _, purpose := range []CommandID{IAConfigurationReceiptReclaim, IARetirementReceiptReclaim} {
		d, e := Describe(purpose)
		if e != nil || d.Durability != Normal {
			t.Fatal("GC descriptor drift", d, e)
		}
	}
}
