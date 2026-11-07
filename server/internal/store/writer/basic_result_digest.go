package writer

import "unicode/utf8"

// Only W's canonical encoder hashes this projection. Outcome and Digest are
// publication metadata and are deliberately absent from the saved wire.
func basicResultDigest(identity CommandIdentity, request RequestDigest, result BasicResult) (ResultDigest, error) {
	return canonicalResultDigest(identity, request, result)
}

func preflightBasicResult(v BasicResult) error {
	c := v.Continuation
	switch v.Purpose {
	case BasicPurposeStage:
		// Shape-pin/freeze return family1/after0; prefence returns family0
		// and an actual positive native owner ID. No copy cursor is selected.
		if c.NextFamily < 0 || c.NextFamily > 1 || c.AfterEntity < 0 || (c.NextFamily == 1 && c.AfterEntity != 0) {
			return invalid("basic_result_shape")
		}
		c.NextFamily, c.AfterEntity = 0, 0
	case BasicPurposeSeal: // Seal still requires the exact zero continuation.
	case BasicPurposeAccept:
		return ErrNotImplemented // activation's actual continuation/cohort map is not selected
	default:
		return invalid("basic_result_purpose")
	}
	if v.AcceptedRunRevision <= 0 || v.AppliedMS < 0 || v.ChangeID <= 0 || v.ExpectedEpoch <= 0 ||
		v.ResultCount != 0 || v.Members == nil || len(v.Members) != 0 || c != (BasicContinuation{}) {
		return invalid("basic_result_shape")
	}
	// Complete fixed wire plus ALL native TEXT lengths precede UTF-8 walks,
	// hashing and byte conversions. SourceRevision stays arbitrary native TEXT;
	// it is not parsed or constrained to an assumed digest format.
	remaining := maxCanonicalResultBytes - basicResultFixedBytes(v.Purpose)
	texts := [...]string{v.RunID, v.SourceID, v.SourceIncarnation, v.SourceRevision}
	for _, s := range texts {
		if s == "" || len(s) > remaining {
			return invalid("basic_result_bytes")
		}
		remaining -= len(s)
	}
	for _, s := range texts {
		if !utf8.ValidString(s) {
			return invalid("basic_result_utf8")
		}
	}
	return nil
}

func basicResultFixedBytes(purpose BasicPurpose) int {
	// Domain/version; three framed tags; four framed native TEXT values;
	// original incarnation/sequence/op/request; four header integers;
	// one framed cursor + four copy integers + three framed keys + after_entity;
	// result_count and explicit complete cohort length. FIRST has no members.
	return len(canonicalResultCodec) + 2 + 3*8 + len("scan_basic") + len(purpose) + len("applied") +
		4*8 + 16 + 8 + 16 + 32 + 4*8 + 8 + 4*8 + 3*8 + 8 + 2*8
}

func writeBasicResult(e *requestEncoder, identity CommandIdentity, request RequestDigest, v BasicResult) error {
	e.bytes([]byte(canonicalResultCodec))
	e.bytes([]byte{0, 1})
	e.text("scan_basic")
	e.text(string(v.Purpose))
	e.text("applied")
	e.text(v.RunID)
	e.bytes(identity.Incarnation[:])
	e.number(uint64(identity.Sequence))
	e.bytes(identity.OperationID[:])
	e.bytes(request[:])
	e.number(uint64(v.AcceptedRunRevision))
	e.number(uint64(v.AppliedMS))
	e.text(v.SourceID)
	e.text(v.SourceIncarnation)
	e.text(v.SourceRevision)
	e.number(uint64(v.ChangeID))
	e.number(uint64(v.ExpectedEpoch))
	c := v.Continuation
	e.text(c.NextCursor)
	e.number(uint64(c.NextFamily))
	e.number(uint64(c.NextCopyPhase))
	e.number(uint64(c.NextCopyID))
	e.number(uint64(c.NextCopyAux))
	e.text(c.NextCopyKey)
	e.text(c.NextCopyKey2)
	e.text(c.NextCopyKey3)
	e.number(uint64(c.AfterEntity))
	e.number(uint64(v.ResultCount))
	e.number(uint64(len(v.Members)))
	return e.err
}
