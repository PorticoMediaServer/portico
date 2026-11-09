package writer

import "unicode/utf8"

// The saved single-field result is a finite typed schema, never a JSON or
// generic payload. It carries the exact after state, the one owner/subject
// revision pair and the history pin of the original lock or restore.
const ipTextLockResultFixedBytes = 2048

func ipTextLockResultDigest(identity CommandIdentity, request RequestDigest, kind CommandID, value ipTextLockValue) (ResultDigest, error) {
	return canonicalResultDigest(identity, request, ipTextLockSavedResult{kind, value})
}

// preflightIPTextLockResult is the owner's closed canonicalResultDigest hook.
func preflightIPTextLockResult(v ipTextLockSavedResult) error {
	if v.Kind != IPFieldLock && v.Kind != IPFieldRestore {
		return ErrInvalid
	}
	r := v.Value
	if r.OwnerID <= 0 || (r.EntityID > 0) == (r.PersonID > 0) || r.EntityID < 0 || r.PersonID < 0 ||
		r.Revision <= 0 || r.SubjectRevision <= 0 || r.AppliedMS < 0 || len(r.Field) == 0 || len(r.Field) > 64 {
		return ErrInvalid
	}
	c := r.Change
	s := c.State
	if c.EffectiveChanged && !c.Changed || s.Locked && !s.Effective.Present ||
		!s.Effective.Present && s.Effective.Value != "" || !s.Automatic.Present && s.Automatic.Value != "" {
		return ErrInvalid
	}
	if v.Kind == IPFieldLock && !s.Locked || v.Kind == IPFieldRestore && s.Locked {
		return ErrInvalid
	}
	remaining := maxCanonicalResultBytes - ipTextLockResultFixedBytes
	for _, text := range []string{string(r.Field), s.Effective.Value, s.Automatic.Value} {
		if len(text) > remaining || !utf8.ValidString(text) {
			return ErrInvalid
		}
		remaining -= len(text)
	}
	if r.History.Present != c.Changed || !r.History.Present && r.History.Value != 0 || r.History.Present && r.History.Value <= 0 {
		return ErrInvalid
	}
	return nil
}

func writeIPTextLockResult(e *requestEncoder, identity CommandIdentity, request RequestDigest, v ipTextLockSavedResult) error {
	e.bytes([]byte(canonicalResultCodec))
	e.bytes([]byte{0, 1}) // typed result version, independent of CommandID ordinals
	e.text("ip_text_lock")
	e.text(ipTextEditCommandName(v.Kind))
	e.text("applied")
	e.bytes(identity.Incarnation[:])
	e.number(uint64(identity.Sequence))
	e.bytes(identity.OperationID[:])
	e.bytes(request[:])
	r := v.Value
	for _, n := range []int64{r.OwnerID, r.EntityID, r.PersonID, r.Revision, r.SubjectRevision, r.AppliedMS} {
		e.number(uint64(n))
	}
	e.flag(r.History.Present)
	if r.History.Present {
		e.number(uint64(r.History.Value))
	}
	e.text(string(r.Field))
	e.text(r.Change.State.Effective.Value)
	e.text(r.Change.State.Automatic.Value)
	for _, b := range []bool{
		r.Change.State.Effective.Present, r.Change.State.Automatic.Present, r.Change.State.Locked,
		r.Change.Changed, r.Change.EffectiveChanged,
	} {
		e.flag(b)
	}
	return e.err
}
