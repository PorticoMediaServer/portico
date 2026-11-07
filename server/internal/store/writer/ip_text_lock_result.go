package writer

import (
	"strings"
	"unicode/utf8"
)

// Written result wire v1: common result prefix, raw uint16 version1, LP purpose,
// identity16/U64sequence/operation16/request32, then owner/entity/revision/
// subjectRevision/appliedMS as U64, LPkind/field/value/automatic, four booleans
// (valuePresent,automaticPresent,locked,changed), nullable U64 history ID.
// Shared canonicalResultDigest/requestEncoder remain the sole production codec.
func ipTextLockResultDigest(i CommandIdentity, q RequestDigest, v ipTextLockResult) (ResultDigest, error) {
	return canonicalResultDigest(i, q, v)
}

func preflightIPTextLockResult(v ipTextLockResult) error {
	if v.OwnerID <= 0 || v.EntityID <= 0 || v.AcceptedRevision <= 0 || v.SubjectRevision <= 0 || v.AppliedMS < 0 ||
		v.Kind == "" || v.Field == "" || v.HistoryID.Present != v.Changed || v.HistoryID.Present && v.HistoryID.Value <= 0 || !v.HistoryID.Present && v.HistoryID.Value != 0 ||
		!v.ValuePresent && v.Value != "" || !v.AutomaticPresent && v.Automatic != "" || v.Locked && !v.ValuePresent {
		return ErrInvalid
	}
	remaining := maxCanonicalResultBytes - 256
	for _, s := range []string{v.Kind, v.Field, v.Value, v.Automatic} {
		if len(s) > remaining || !utf8.ValidString(s) || strings.IndexByte(s, 0) >= 0 {
			return ErrInvalid
		}
		remaining -= len(s)
	}
	return nil
}

func writeIPTextLockResult(e *requestEncoder, i CommandIdentity, q RequestDigest, v ipTextLockResult) error {
	e.bytes([]byte(canonicalResultCodec))
	e.bytes([]byte{0, 1})
	e.text("ip_field_lock")
	e.bytes(i.Incarnation[:])
	e.number(uint64(i.Sequence))
	e.bytes(i.OperationID[:])
	e.bytes(q[:])
	return writeIPTextResultPayload(e, v)
}
func writeIPTextResultPayload(e *requestEncoder, v ipTextLockResult) error {
	for _, n := range []int64{v.OwnerID, v.EntityID, v.AcceptedRevision, v.SubjectRevision, v.AppliedMS} {
		e.number(uint64(n))
	}
	for _, s := range []string{v.Kind, v.Field, v.Value, v.Automatic} {
		e.text(s)
	}
	for _, v := range []bool{v.ValuePresent, v.AutomaticPresent, v.Locked, v.Changed} {
		e.flag(v)
	}
	e.flag(v.HistoryID.Present)
	if v.HistoryID.Present {
		e.number(uint64(v.HistoryID.Value))
	}
	return e.err
}
