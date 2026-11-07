package writer

import (
	"errors"
	"strings"
	"unicode/utf8"
)

// This detached historical reference cannot prove current authority, source
// observation, birth permission, resource admission, or a terminal outcome.
// Original configuration history has exactly sequence1; GC uses105 separately
// on the SAME actual owner command stream, never a borrowed removal/scan slot.
type iaConfigurationReference struct {
	original iaConfigurationReceiptOriginal
	identity CommandIdentity
	request  RequestDigest
}

type iaConfigurationOwner struct {
	exists             bool
	original           iaConfigurationReceiptOriginal
	incarnation        [16]byte
	last, acknowledged int64
	cursor             iaReceiptCursor
	rows, bytes        int64
}

type iaConfigurationReceipt struct {
	exists               bool
	identity             receiptIdentity
	purpose              CommandID
	version, resultCount int64
	digest               ResultDigest
	headerBytes          int64
	result               iaConfigurationResult
	sourceBytes          []int64
}

type iaConfigurationHistory struct {
	decision receiptDecision
	result   iaConfigurationResult
	digest   ResultDigest
}

func validateIAConfigurationReference(q iaConfigurationReference) error {
	o := q.original
	if q.identity.Sequence != 1 || q.identity.Incarnation == ([16]byte{}) || q.identity.OperationID == ([16]byte{}) ||
		o.operationID != q.identity.OperationID || o.requestDigest != q.request || o.libraryID <= 0 || o.libraryKey == "" ||
		o.purpose != IAConfigureLibrary && o.purpose != IAConfigureSource || (o.purpose == IAConfigureSource) != (o.sourceID != "") {
		return invalid("ia_configuration_reference")
	}
	remaining := maxCanonicalRequestBytes
	for _, s := range []string{o.libraryKey, o.sourceID} {
		if len(s) > remaining {
			return invalid("ia_configuration_reference_bytes")
		}
		remaining -= len(s)
	}
	for _, s := range []string{o.libraryKey, o.sourceID} {
		if !utf8.ValidString(s) || strings.IndexByte(s, 0) >= 0 {
			return invalid("ia_configuration_reference_text")
		}
	}
	return nil
}

// CURRENT conjunctive owner/disclosure authority and actual native byte
// admission precede history fetching. Original conflicts precede ACK; below
// ACK, no partial or physically reclaimed result is decoded or reconstructed.
// An absent original owner is ResolveRequired, never fresh birth permission.
func classifyIAConfigurationHistory(q iaConfigurationReference, o iaConfigurationOwner, h iaConfigurationReceipt) (iaConfigurationHistory, error) {
	zero := iaConfigurationHistory{}
	decision, err := classifyIAConfigurationMetadata(q, o, h)
	if err != nil {
		return zero, err
	}
	if decision != receiptReplay {
		return iaConfigurationHistory{decision: decision}, nil
	}
	return decodeIAConfigurationHistory(q, o, h)
}

// Scalar identity/ACK classification is separate so a native adapter can avoid
// fetching names, roots and child cohorts once the original result is retired.
func classifyIAConfigurationMetadata(q iaConfigurationReference, o iaConfigurationOwner, h iaConfigurationReceipt) (receiptDecision, error) {
	if err := validateIAConfigurationReference(q); err != nil {
		return 0, err
	}
	owner := receiptOwnerFacts{exists: o.exists, incarnation: o.incarnation, lastSequence: o.last, acknowledgedSequence: o.acknowledged}
	if err := checkIAConfigurationOwnerScope(q, o); err != nil {
		return 0, err
	}
	found := receiptFacts{exists: h.exists, identity: h.identity, kind: h.purpose, resultCount: h.resultCount, resultDigest: h.digest}
	return classifyReceipt(receiptIdentity{command: q.identity, digest: q.request}, q.original.purpose, owner, found)
}

func checkIAConfigurationOwnerScope(q iaConfigurationReference, o iaConfigurationOwner) error {
	if o.exists && o.incarnation == q.identity.Incarnation {
		if o.original.libraryID != q.original.libraryID || o.original.libraryKey != q.original.libraryKey {
			return ErrDisclosureDenied
		}
		if o.original != q.original {
			return ErrConflict
		}
	}
	return nil
}

func decodeIAConfigurationHistory(q iaConfigurationReference, o iaConfigurationOwner, h iaConfigurationReceipt) (iaConfigurationHistory, error) {
	zero := iaConfigurationHistory{}
	// Any cleanup advances the ACK floor first. A replayable original result
	// must still have its complete untouched cohort and exact full data ledger.
	if o.last != 1 || o.acknowledged != 0 || o.cursor != (iaReceiptCursor{0, -1}) ||
		h.version != 1 || h.result.original != o.original || h.result.resultCount != h.resultCount ||
		h.resultCount <= 0 || h.resultCount > iaConfigurationMaxSources || len(h.sourceBytes) != len(h.result.sources) || o.rows != 1+h.resultCount || o.bytes <= 0 {
		return zero, ErrResolveRequired
	}
	var value any = iaConfigureLibraryResult{h.result}
	if q.original.purpose == IAConfigureSource {
		value = iaConfigureSourceResult{h.result}
	}
	digest, err := canonicalResultDigest(q.identity, q.request, value)
	if err != nil || digest != h.digest {
		return zero, errors.Join(ErrResolveRequired, err)
	}
	headerBytes, err := iaConfigurationHeaderRetainedBytes(q.original.purpose, h.result)
	if err != nil || headerBytes != h.headerBytes || headerBytes > o.bytes {
		return zero, ErrResolveRequired
	}
	remaining := o.bytes - headerBytes
	for i, s := range h.result.sources {
		n, err := iaConfigurationSourceRetainedBytes(q.original.purpose, s)
		if err != nil || n != h.sourceBytes[i] || n > remaining {
			return zero, ErrResolveRequired
		}
		remaining -= n
	}
	if remaining != 0 {
		return zero, ErrResolveRequired
	}
	// Own the returned immutable cohort; neither decoded receipt slices nor
	// future caller mutations may overwrite a previously resolved result.
	result := h.result
	result.sources = append([]iaConfigurationSourceResult(nil), h.result.sources...)
	return iaConfigurationHistory{decision: receiptReplay, result: result, digest: digest}, nil
}
