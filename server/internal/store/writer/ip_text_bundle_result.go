package writer

import (
	"unicode/utf8"
)

// The saved result is a finite typed schema, never a JSON or generic payload.
// It carries the complete ordered field cohort with each original before/after
// change, the one owner/subject revision and the exact history pin.
const ipTextEditResultFixedBytes = 4096

func ipTextEditResultDigest(identity CommandIdentity, request RequestDigest, value ipTextEditResult) (ResultDigest, error) {
	return canonicalResultDigest(identity, request, ipTextEditSavedResult{IPFieldEdit, value})
}

// preflightIPTextEditResult is the owner's closed canonicalResultDigest hook.
func preflightIPTextEditResult(v ipTextEditSavedResult) error {
	if v.Kind != IPFieldEdit {
		return ErrInvalid
	}
	r := v.Value
	if r.OwnerID <= 0 || (r.EntityID > 0) == (r.PersonID > 0) || r.EntityID < 0 || r.PersonID < 0 ||
		r.Revision <= 0 || r.SubjectRevision <= 0 || r.AppliedMS < 0 ||
		len(r.Fields) == 0 || len(r.Fields) > ipTextMaxFields {
		return ErrInvalid
	}
	remaining := maxCanonicalResultBytes - ipTextEditResultFixedBytes
	last := ""
	changed := false
	for _, f := range r.Fields {
		if string(f.Field) <= last {
			return ErrInvalid
		}
		last = string(f.Field)
		c := f.Change
		s := c.State
		if c.EffectiveChanged && !c.Changed || s.Locked && !s.Effective.Present ||
			!s.Effective.Present && s.Effective.Value != "" || !s.Automatic.Present && s.Automatic.Value != "" {
			return ErrInvalid
		}
		for _, text := range []string{string(f.Field), s.Effective.Value, s.Automatic.Value} {
			if len(text) > remaining || !utf8.ValidString(text) {
				return ErrInvalid
			}
			remaining -= len(text)
		}
		changed = changed || c.Changed
	}
	if r.History.Present != changed || !r.History.Present && r.History.Value != 0 || r.History.Present && r.History.Value <= 0 {
		return ErrInvalid
	}
	return nil
}

func writeIPTextEditResult(e *requestEncoder, identity CommandIdentity, request RequestDigest, v ipTextEditSavedResult) error {
	e.bytes([]byte(canonicalResultCodec))
	e.bytes([]byte{0, 1}) // typed result version, independent of CommandID ordinals
	e.text("ip_text_edit")
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
	e.number(uint64(len(r.Fields)))
	for _, f := range r.Fields {
		e.text(string(f.Field))
		e.text(f.Change.State.Effective.Value)
		e.text(f.Change.State.Automatic.Value)
		for _, b := range []bool{
			f.Change.State.Effective.Present, f.Change.State.Automatic.Present, f.Change.State.Locked,
			f.Change.Changed, f.Change.EffectiveChanged,
		} {
			e.flag(b)
		}
	}
	return e.err
}

func ipTextEditCommandName(kind CommandID) string {
	switch kind {
	case IPFieldEdit:
		return "field_edit"
	case IPFieldLock:
		return "field_lock"
	case IPFieldRestore:
		return "field_restore"
	}
	return ""
}
