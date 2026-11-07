package writer

// Detached facts only. Native NULL/type checks, current authority, actual byte
// admission and one W transaction remain required at the owning issuer. These
// values cannot attest that a point query, deletion, ACK or COMMIT happened.
type iaReceiptReclaimReference struct {
	purpose       CommandID
	identity      CommandIdentity
	request       RequestDigest
	configuration iaConfigurationReceiptOriginal
	retirement    iaRetirementOriginal
}

type iaReceiptReclaimSlot struct {
	present              bool
	identity             CommandIdentity
	request              RequestDigest
	purpose              CommandID
	version, resultCount int64
	facts                iaReceiptReclaimResult
	digest               ResultDigest
	retainedBytes        int64
}

type iaReceiptReclaimOwner struct {
	exists             bool
	incarnation        [16]byte
	configuration      iaConfigurationReceiptOriginal
	retirement         iaRetirementOriginal
	last, acknowledged int64
	cursor             iaReceiptCursor
	rows, bytes        int64
	due                bool
	dueMS              int64
	slot               iaReceiptReclaimSlot
}

func iaReceiptReclaimValue(q iaReceiptReclaimReference, facts iaReceiptReclaimResult) any {
	if q.purpose == IAConfigurationReceiptReclaim {
		return iaConfigurationReceiptReclaimResult{q.configuration, facts}
	}
	return iaRetirementReceiptReclaimResult{q.retirement, facts}
}

func validateIAReceiptReclaimReference(q iaReceiptReclaimReference) error {
	if q.purpose != IAConfigurationReceiptReclaim && q.purpose != IARetirementReceiptReclaim || q.identity.Sequence <= 1 ||
		q.purpose == IAConfigurationReceiptReclaim && q.retirement != (iaRetirementOriginal{}) ||
		q.purpose == IARetirementReceiptReclaim && q.configuration != (iaConfigurationReceiptOriginal{}) {
		return invalid("ia_receipt_reclaim_reference")
	}
	// A reference has no saved/future result. Reuse the closed original-scope
	// validation with a structurally empty scalar, without querying any source.
	empty := iaReceiptReclaimResult{capturedACK: q.identity.Sequence - 1, old: iaReceiptCursor{0, -1}, next: iaReceiptCursor{0, -1}, removedKind: "none", removedOrdinal: -1, noNext: true}
	return preflightCanonicalResult(q.identity, q.request, iaReceiptReclaimValue(q, empty))
}

func iaReceiptReclaimScopeMatches(q iaReceiptReclaimReference, o iaReceiptReclaimOwner) bool {
	return q.configuration == o.configuration && q.retirement == o.retirement
}

// Original result resolution PRECEDES fresh due/cursor/probe checks. At/below
// ACK do not decode the old scalar; a later slot never supplies an older result.
// A replay proves the exact saved post-state/digest/charge and has zero effects.
func classifyIAReceiptReclaim(q iaReceiptReclaimReference, o iaReceiptReclaimOwner) (receiptDecision, error) {
	decision, err := classifyIAReceiptReclaimMetadata(q, o)
	if err != nil || decision != receiptReplay {
		return decision, err
	}
	s := o.slot
	if s.version != 1 || s.resultCount != 1 || s.facts.next != o.cursor || s.facts.rows != o.rows || s.facts.bytes != o.bytes || s.facts.noNext == o.due {
		return receiptResolveRequired, ErrResolveRequired
	}
	digest, err := canonicalResultDigest(q.identity, q.request, iaReceiptReclaimValue(q, s.facts))
	if err != nil || digest != s.digest {
		return receiptResolveRequired, ErrResolveRequired
	}
	charge, err := iaReceiptReclaimSlotBytes(q.purpose, s.facts)
	if err != nil || charge != s.retainedBytes {
		return receiptResolveRequired, ErrResolveRequired
	}
	return receiptReplay, nil
}

// Only fixed identity metadata is inspected before the ACK decision. Native
// adapters must not decode an ACKed slot body or a newer slot as older output.
func classifyIAReceiptReclaimMetadata(q iaReceiptReclaimReference, o iaReceiptReclaimOwner) (receiptDecision, error) {
	if err := validateIAReceiptReclaimReference(q); err != nil {
		return receiptConflict, err
	}
	owner := receiptOwnerFacts{exists: o.exists, incarnation: o.incarnation, lastSequence: o.last, acknowledgedSequence: o.acknowledged}
	if o.exists && o.incarnation == q.identity.Incarnation && !iaReceiptReclaimScopeMatches(q, o) {
		return receiptConflict, ErrConflict
	}
	s := o.slot
	var found receiptFacts
	if s.present && s.identity.Sequence == q.identity.Sequence {
		found = receiptFacts{exists: true, identity: receiptIdentity{command: s.identity, digest: s.request}, kind: s.purpose, resultCount: s.resultCount, resultDigest: s.digest}
	}
	return classifyReceipt(receiptIdentity{command: q.identity, digest: q.request}, q.purpose, owner, found)
}

// One exact unit returned by an indexed full-owner probe. A configuration
// header includes its ORIGINAL source result count, to prove all ordinals were
// consumed before header deletion. No all-purpose child row is introduced.
type iaReceiptReclaimUnit struct {
	kind                     string // empty iff the probe found no physical data
	sequence, ordinal, bytes int64
	sourceCount              int64 // configuration header only
}

type iaReceiptReclaimProbe struct {
	incarnation       [16]byte
	originalOperation [16]byte
	after             iaReceiptCursor
	throughACK        int64
	unit              iaReceiptReclaimUnit
}

type iaReceiptReclaimInput struct {
	reference       iaReceiptReclaimReference
	owner           iaReceiptReclaimOwner
	expectedOldSlot iaReceiptReclaimSlot
	first, after    iaReceiptReclaimProbe
	appliedMS       int64
}

func validIAReceiptReclaimProbe(p iaReceiptReclaimProbe, q iaReceiptReclaimReference, o iaReceiptReclaimOwner, after iaReceiptCursor) bool {
	original := q.retirement.operationID
	if q.purpose == IAConfigurationReceiptReclaim {
		original = q.configuration.operationID
	}
	if p.incarnation != o.incarnation || p.originalOperation != original || p.after != after || p.throughACK != o.acknowledged {
		return false
	}
	u := p.unit
	if u.kind == "" {
		return u == (iaReceiptReclaimUnit{})
	}
	if u.bytes <= 0 || u.sequence <= 0 || u.sequence > o.acknowledged {
		return false
	}
	if q.purpose == IARetirementReceiptReclaim {
		return u.kind == "header" && u.sequence > after.sequence && u.ordinal == -1 && u.sourceCount == 0
	}
	if u.sequence != 1 || after.sequence != 0 {
		return false
	}
	switch u.kind {
	case "source":
		return u.sourceCount == 0 && u.ordinal >= 0 && u.ordinal < iaConfigurationMaxSources && u.ordinal > after.ordinal && u.ordinal-after.ordinal == 1
	case "header":
		return u.ordinal == -1 && u.sourceCount > 0 && u.sourceCount <= iaConfigurationMaxSources && after.ordinal >= 0 && u.sourceCount-after.ordinal == 1
	default:
		return false
	}
}

// Pure fresh transition, called only after original-reference resolution. The
// issuer must delete the selected actual unit and CAS ALL old scope/slot/debt/
// due/cursor facts into this result on the same W transaction. Any zero-row CAS
// rolls back the deletion. Native slot allocation is independently admitted;
// an old ACKed slot is not advance credit for the new physical allocation.
func prepareIAReceiptReclaimUnit(in iaReceiptReclaimInput) (iaReceiptReclaimResult, error) {
	q, o := in.reference, in.owner
	zero := iaReceiptReclaimResult{}
	decision, err := classifyIAReceiptReclaim(q, o)
	if err != nil {
		return zero, err
	}
	if decision != receiptFresh {
		return zero, ErrConflict
	}
	if !o.due || o.dueMS < 0 || in.appliedMS < o.dueMS || o.cursor.sequence < 0 || o.cursor.sequence > o.acknowledged ||
		o.rows <= 0 || o.bytes <= 0 || in.expectedOldSlot != o.slot {
		return zero, ErrResolveRequired
	}
	if o.slot.present {
		s := o.slot
		if s.identity.Sequence > o.acknowledged || s.identity.Sequence <= 1 || s.identity.Incarnation != o.incarnation ||
			s.identity.OperationID == q.identity.OperationID || s.facts.next != o.cursor || s.version != 1 || s.resultCount != 1 {
			return zero, ErrResolveRequired
		}
		if s.identity.Sequence == o.last && (s.facts.rows != o.rows || s.facts.bytes != o.bytes || s.facts.noNext == o.due) {
			return zero, ErrResolveRequired
		}
		oldq := q
		oldq.identity, oldq.request, oldq.purpose = s.identity, s.request, s.purpose
		digest, e := canonicalResultDigest(s.identity, s.request, iaReceiptReclaimValue(oldq, s.facts))
		charge, ce := iaReceiptReclaimSlotBytes(s.purpose, s.facts)
		if e != nil || ce != nil || digest != s.digest || charge != s.retainedBytes || s.purpose != q.purpose {
			return zero, ErrResolveRequired
		}
	} else if o.slot != (iaReceiptReclaimSlot{}) || o.cursor != (iaReceiptCursor{0, -1}) {
		return zero, ErrResolveRequired
	}
	if q.purpose == IAConfigurationReceiptReclaim && (o.rows > iaConfigurationMaxSources+1 || !o.slot.present && o.last != 1) {
		return zero, ErrResolveRequired
	}
	if !validIAReceiptReclaimProbe(in.first, q, o, o.cursor) {
		return zero, ErrConflict
	}
	u := in.first.unit
	v := iaReceiptReclaimResult{capturedACK: o.acknowledged, old: o.cursor, next: o.cursor, removedKind: "none", removedOrdinal: -1, rows: o.rows, bytes: o.bytes, appliedMS: in.appliedMS}
	if u.kind != "" {
		if o.rows < 1 || u.bytes > o.bytes {
			return zero, ErrResolveRequired
		}
		v.removedKind, v.removedSequence, v.removedOrdinal, v.removedCount, v.removedBytes = u.kind, u.sequence, u.ordinal, 1, u.bytes
		v.rows--
		v.bytes -= u.bytes
		if u.kind == "source" {
			v.next.ordinal = u.ordinal
		} else {
			v.next = iaReceiptCursor{u.sequence, -1}
		}
	}
	return v, nil
}

// Validate all original/OLD facts before a native child deletion. The complete
// transition additionally requires a post-delete physical probe in the same W
// transaction; a failed final proof rolls the deletion back.
func prepareIAReceiptReclaim(in iaReceiptReclaimInput) (iaReceiptReclaimResult, error) {
	v, err := prepareIAReceiptReclaimUnit(in)
	if err != nil {
		return iaReceiptReclaimResult{}, err
	}
	q, o := in.reference, in.owner
	zero := iaReceiptReclaimResult{}
	u := in.first.unit
	if !validIAReceiptReclaimProbe(in.after, q, o, v.next) {
		return zero, ErrConflict
	}
	v.noNext = in.after.unit.kind == ""
	if !validIAReceiptReclaimResult(q.purpose, q.identity, v) || u.kind == "" && !v.noNext {
		return zero, ErrResolveRequired
	}
	return v, nil
}
