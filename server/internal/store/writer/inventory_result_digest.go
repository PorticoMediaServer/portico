package writer

import (
	"errors"
	"unicode/utf8"
)

const inventoryResultHeaderBytes = 239 // complete v1 wire excluding five TEXT payloads
const inventoryResultMemberBytes = 64  // complete member wire excluding ObjectID bytes

// inventoryAcceptResult is the immutable receipt projection for ordinary inputs.
// Native owner/source/asset echoes are proved by the accepting command. Encoding
// these values neither reconstructs current rows nor attests Basic/publication.
type inventoryAcceptResult struct {
	RunID                          string
	AcceptedRunRevision, AppliedMS int64
	SourceID, SourceIncarnation    string
	SourceRevision, NextCursor     string
	ResultCount                    int64
	Members                        []inventoryResultMember
}

type inventoryResultMember struct {
	InputOrdinal, ChildKind, ChildOrdinal                int64
	ObjectID                                             string
	LibraryID, SubjectID, AssetID, AcceptedAssetRevision int64
}

func inventoryAcceptResultDigest(identity CommandIdentity, request RequestDigest, result inventoryAcceptResult) (ResultDigest, error) {
	return canonicalResultDigest(identity, request, result)
}

func preflightInventoryAcceptResult(v inventoryAcceptResult) error {
	if v.AcceptedRunRevision <= 0 || v.AppliedMS < 0 || v.ResultCount <= 0 || v.ResultCount != int64(len(v.Members)) {
		return invalid("inventory_result_shape")
	}
	// Bound the entire cohort before iteration, multiplication or UTF-8 work.
	// This is codec preparation only; N>1 wire shape is not N>1 admission.
	remaining := maxCanonicalResultBytes - inventoryResultHeaderBytes
	if len(v.Members) > remaining/inventoryResultMemberBytes {
		return invalid("inventory_result_bytes")
	}
	remaining -= len(v.Members) * inventoryResultMemberBytes
	texts := [...]string{v.RunID, v.SourceID, v.SourceIncarnation, v.SourceRevision, v.NextCursor}
	for n, s := range texts {
		if n < len(texts)-1 && s == "" {
			return invalid("inventory_result_text")
		}
		if len(s) > remaining {
			return invalid("inventory_result_bytes")
		}
		remaining -= len(s)
	}
	for ordinal, m := range v.Members {
		if m.InputOrdinal != int64(ordinal) || m.ChildKind != 0 || m.ChildOrdinal != 0 || m.ObjectID == "" ||
			m.LibraryID <= 0 || m.SubjectID <= 0 || m.AssetID <= 0 || m.AcceptedAssetRevision <= 0 {
			return invalid("inventory_result_member")
		}
		if len(m.ObjectID) > remaining {
			return invalid("inventory_result_bytes")
		}
		remaining -= len(m.ObjectID)
	}
	// Only after COMPLETE lengths, count/order and native ranges pass may any
	// string be scanned. A late oversized member cannot cause early UTF-8/hash
	// work or a large temporary copy. Embedded NUL and exact Unicode stay intact.
	for _, s := range texts {
		if !utf8.ValidString(s) {
			return invalid("inventory_result_utf8")
		}
	}
	for _, m := range v.Members {
		if !utf8.ValidString(m.ObjectID) {
			return invalid("inventory_result_utf8")
		}
	}
	return nil
}

func writeInventoryAcceptResult(e *requestEncoder, identity CommandIdentity, request RequestDigest, v inventoryAcceptResult) error {
	e.bytes([]byte(canonicalResultCodec))
	e.bytes([]byte{0, 1})
	e.text("inventory_accept")
	e.text("ordinary_batch")
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
	e.text(v.NextCursor)
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
	if e.err != nil {
		return errors.Join(invalid("inventory_result"), e.err)
	}
	return nil
}
