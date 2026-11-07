package writer

import (
	"math"
	"unicode/utf8"
)

// Only W's canonical encoder hashes this projection. Outcome and Digest are
// publication metadata and are deliberately absent from the saved wire.
func basicResultDigest(identity CommandIdentity, request RequestDigest, result BasicResult) (ResultDigest, error) {
	return canonicalResultDigest(identity, request, result)
}

func preflightBasicResult(v BasicResult) error {
	c := v.Continuation
	// Account for every fixed field and TEXT byte before inspecting UTF-8 or hashing.
	remaining := maxCanonicalResultBytes - basicResultFixedBytes(v.Purpose)
	if len(v.Members) > remaining/(8*8) {
		return invalid("basic_result_bytes")
	}
	remaining -= len(v.Members) * 8 * 8 // seven integers and one TEXT frame per member
	texts := []string{v.RunID, v.SourceID, v.SourceIncarnation, v.SourceRevision,
		c.NextCursor, c.NextCopyKey, c.NextCopyKey2, c.NextCopyKey3}
	for _, s := range texts {
		if len(s) > remaining {
			return invalid("basic_result_bytes")
		}
		remaining -= len(s)
	}
	for _, m := range v.Members {
		if len(m.ObjectID) > remaining {
			return invalid("basic_result_bytes")
		}
		remaining -= len(m.ObjectID)
	}
	for _, s := range texts {
		if !utf8.ValidString(s) {
			return invalid("basic_result_utf8")
		}
	}
	for _, m := range v.Members {
		if !utf8.ValidString(m.ObjectID) {
			return invalid("basic_result_utf8")
		}
	}
	if v.RunID == "" || v.SourceID == "" || v.SourceIncarnation == "" || v.SourceRevision == "" ||
		v.AcceptedRunRevision <= 0 || v.AppliedMS < 0 || v.ChangeID <= 0 || v.ExpectedEpoch <= 0 || v.Members == nil ||
		c.NextCursor != "" || c.NextCopyKey2 != "" || c.NextCopyKey3 != "" || c.AfterEntity < 0 {
		return invalid("basic_result_shape")
	}
	switch v.Purpose {
	case BasicPurposeStage:
		if v.ResultCount != 0 || len(v.Members) != 0 ||
			(c.NextCopyKey != "" && !(c.NextFamily == 8 && c.NextCopyPhase == 1 && c.NextCopyAux == 1)) {
			return invalid("basic_result_shape")
		}
		switch c.NextFamily {
		case 0, 1:
			if c.NextFamily == 1 && c.AfterEntity != 0 {
				return invalid("basic_result_shape")
			}
			if c.NextCopyPhase != 0 || c.NextCopyID != 0 || c.NextCopyAux != 0 {
				return invalid("basic_result_shape")
			}
		case 2, 3, 5:
			if c.AfterEntity <= 0 || c.NextCopyID < 0 || c.NextCopyPhase < 0 || c.NextCopyPhase > 1 ||
				(c.NextCopyPhase == 0 && c.NextCopyAux != 0) ||
				(c.NextCopyPhase == 1 && ((c.NextCopyID == 0 && c.NextCopyAux != 0) || (c.NextCopyID > 0 && c.NextCopyAux != 1))) {
				return invalid("basic_result_shape")
			}
		case 4, 6, 9:
			if c.AfterEntity <= 0 || c.NextCopyPhase != 0 || c.NextCopyID != 0 || c.NextCopyAux != 0 {
				return invalid("basic_result_shape")
			}
		case 7:
			if c.AfterEntity <= 0 || c.NextCopyPhase != 0 || c.NextCopyID < 0 || c.NextCopyAux != 0 {
				return invalid("basic_result_shape")
			}
		case 8:
			if c.AfterEntity <= 0 || c.NextCopyID < 0 {
				return invalid("basic_result_shape")
			}
			switch c.NextCopyPhase {
			case 0: // input_ordinal remains a numeric prefix, never a text key
				if c.NextCopyAux != 0 {
					return invalid("basic_result_shape")
				}
			case 1: // actual canonical asset_subtitles.id; empty IDs stay distinct from initial
				if c.NextCopyID != 0 || (c.NextCopyAux != 0 && c.NextCopyAux != 1) {
					return invalid("basic_result_shape")
				}
			default:
				return invalid("basic_result_shape")
			}
		default:
			return invalid("basic_result_shape")
		}
	case BasicPurposeSeal:
		if c != (BasicContinuation{}) || v.ResultCount != 0 || len(v.Members) != 0 {
			return invalid("basic_result_shape")
		}
	case BasicPurposeAccept:
		if c.AfterEntity <= 0 {
			return invalid("basic_result_shape")
		}
		c.AfterEntity = 0
		if c != (BasicContinuation{}) || v.ResultCount != 1 || len(v.Members) != 1 || v.ExpectedEpoch == math.MaxInt64 {
			return invalid("basic_result_shape")
		}
		m := v.Members[0]
		if m.InputOrdinal != 0 || m.ChildKind != 0 || m.ChildOrdinal != 0 || m.ObjectID == "" || m.LibraryID <= 0 ||
			m.SubjectID != v.Continuation.AfterEntity || m.AssetID <= 0 || m.AcceptedAssetRevision != v.ExpectedEpoch+1 {
			return invalid("basic_result_shape")
		}
	default:
		return invalid("basic_result_purpose")
	}
	return nil
}

func basicResultFixedBytes(purpose BasicPurpose) int {
	// Domain/version; three framed tags; four framed native TEXT values;
	// original incarnation/sequence/op/request; four header integers;
	// one framed cursor + four copy integers + three framed keys + after_entity;
	// result_count and explicit complete cohort length. Member framing is added in preflight.
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
	for _, m := range v.Members {
		e.number(uint64(m.InputOrdinal))
		e.number(uint64(m.ChildKind))
		e.number(uint64(m.ChildOrdinal))
		e.text(m.ObjectID)
		e.number(uint64(m.LibraryID))
		e.number(uint64(m.SubjectID))
		e.number(uint64(m.AssetID))
		e.number(uint64(m.AcceptedAssetRevision))
	}
	return e.err
}
