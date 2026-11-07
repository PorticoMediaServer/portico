package writer

import (
	"errors"
	"strings"
	"unicode/utf8"
)

// This is a retained driver reference, never a live source/lease witness. Its
// original operation is separate from a STEP's per-command operation nonce.
type iaRetirementReference struct {
	kind, target      string
	libraryID         int64
	libraryKey        string
	originalOperation [16]byte
	identity          CommandIdentity
	request           RequestDigest
	purpose           CommandID
}

type iaRetirementOwner struct {
	exists                        bool
	original                      iaRetirementOriginal
	incarnation                   [16]byte
	last, acknowledged, reclaimed int64
	retainedCount, retainedBytes  int64
}

type iaRetirementReceipt struct {
	exists               bool
	identity             receiptIdentity
	purpose              CommandID
	version, resultCount int64
	result               iaRetirementResult
	digest               ResultDigest
	retainedBytes        int64
}

type iaRetirementHistory struct {
	decision receiptDecision
	result   iaRetirementResult
	digest   ResultDigest
}

// Pure decoding/classification only. Current conjunctive owner/disclosure
// authority and native byte admission MUST precede fetching these same-turn
// facts. No SQL, lease validation, ACK, effects, commit truth or Applied token
// is available here. Original owner loss never becomes fresh birth permission.
func validateIARetirementReference(q iaRetirementReference) error {
	if _, err := iaRetirementKindName(q.purpose); err != nil {
		return err
	}
	if (q.kind != "library" && q.kind != "folder") || q.target == "" || q.libraryID <= 0 || q.libraryKey == "" ||
		q.originalOperation == ([16]byte{}) || q.identity.Incarnation == ([16]byte{}) || q.identity.OperationID == ([16]byte{}) || q.identity.Sequence <= 0 ||
		q.kind == "library" && q.target != q.libraryKey ||
		q.purpose == IARetireLibrary && q.kind != "library" || q.purpose == IARetireSource && q.kind != "folder" ||
		q.purpose != IARetireStep && q.identity.Sequence != 1 || q.purpose == IARetireStep && q.identity.Sequence <= 1 {
		return invalid("ia_retirement_reference")
	}
	remaining := maxCanonicalRequestBytes
	for _, text := range []string{q.kind, q.target, q.libraryKey} {
		if len(text) > remaining {
			return invalid("ia_retirement_reference_bytes")
		}
		remaining -= len(text)
	}
	for _, text := range []string{q.kind, q.target, q.libraryKey} {
		if !utf8.ValidString(text) || strings.IndexByte(text, 0) >= 0 {
			return invalid("ia_retirement_reference_text")
		}
	}
	return nil
}

func classifyIARetirementHistory(q iaRetirementReference, owner iaRetirementOwner, header iaRetirementReceipt) (iaRetirementHistory, error) {
	if err := validateIARetirementReference(q); err != nil {
		return iaRetirementHistory{}, err
	}
	expected := receiptIdentity{command: q.identity, digest: q.request}
	o := receiptOwnerFacts{exists: owner.exists, incarnation: owner.incarnation, lastSequence: owner.last, acknowledgedSequence: owner.acknowledged}
	if !owner.exists || owner.incarnation != q.identity.Incarnation {
		_, err := classifyReceipt(expected, q.purpose, o, receiptFacts{})
		return iaRetirementHistory{}, err
	}
	if owner.original.libraryID != q.libraryID || owner.original.libraryKey != q.libraryKey {
		return iaRetirementHistory{}, ErrDisclosureDenied
	}
	if owner.original.kind != q.kind || owner.original.target != q.target || owner.original.operationID != q.originalOperation {
		return iaRetirementHistory{}, ErrConflict
	}
	if owner.reclaimed < 0 || owner.reclaimed > owner.acknowledged || owner.retainedCount < 0 || owner.retainedBytes < 0 ||
		(owner.retainedCount == 0) != (owner.retainedBytes == 0) || owner.last < owner.reclaimed || owner.retainedCount > owner.last-owner.reclaimed {
		return iaRetirementHistory{}, ErrResolveRequired
	}
	// A retained BEGIN anchor also detects a conflicting original retry after
	// the scalar header is ACKed and physically reclaimed. Never re-create it.
	if q.identity.Sequence == 1 && (q.identity.OperationID != owner.original.operationID || q.request != owner.original.requestDigest || q.purpose == IARetireStep) {
		return iaRetirementHistory{}, ErrConflict
	}
	found := receiptFacts{exists: header.exists, identity: header.identity, kind: header.purpose, resultCount: header.resultCount, resultDigest: header.digest}
	decision, err := classifyReceipt(expected, q.purpose, o, found)
	if err != nil {
		return iaRetirementHistory{}, err
	}
	if decision != receiptReplay {
		// In particular, do not inspect a partial/corrupt result below the ACK
		// floor. W's classifier checked any retained identity conflict first.
		return iaRetirementHistory{decision: decision}, nil
	}
	if header.version != 1 || header.resultCount != 0 || header.result.original != owner.original || owner.retainedCount < 1 {
		return iaRetirementHistory{}, ErrResolveRequired
	}
	var result any
	switch q.purpose {
	case IARetireLibrary:
		result = iaRetireLibraryResult{header.result}
	case IARetireSource:
		result = iaRetireSourceResult{header.result}
	case IARetireStep:
		result = iaRetireStepResult{header.result}
	}
	digest, err := canonicalResultDigest(q.identity, q.request, result)
	if err != nil {
		return iaRetirementHistory{}, errors.Join(ErrResolveRequired, err)
	}
	retained, err := iaRetirementRetainedBytes(q.purpose, header.result)
	if err != nil || retained != header.retainedBytes || retained > owner.retainedBytes || digest != header.digest {
		return iaRetirementHistory{}, ErrResolveRequired
	}
	return iaRetirementHistory{decision: receiptReplay, result: header.result, digest: digest}, nil
}

// A bounded exact receipt deletion is the only refund. ACK alone never calls
// this function. Deletion and these owner deltas must share one W transaction;
// failure of either leaves the old ledger intact after rollback.
func iaRetirementReclaimDebt(owner iaRetirementOwner, sequence, retainedBytes int64) (iaRetirementOwner, error) {
	if !owner.exists || owner.reclaimed < 0 || owner.acknowledged > owner.last || owner.reclaimed > owner.acknowledged ||
		sequence <= owner.reclaimed || sequence > owner.acknowledged || sequence-owner.reclaimed != 1 ||
		retainedBytes <= 0 || owner.retainedCount < 1 || owner.retainedBytes < retainedBytes {
		return iaRetirementOwner{}, ErrResolveRequired
	}
	next := owner
	next.reclaimed, next.retainedCount, next.retainedBytes = sequence, owner.retainedCount-1, owner.retainedBytes-retainedBytes
	if (next.retainedCount == 0) != (next.retainedBytes == 0) {
		return iaRetirementOwner{}, ErrResolveRequired
	}
	return next, nil
}
