package writer

import (
	"errors"
	"strings"
	"unicode/utf8"
)

// The original removal driver is a retained namespace anchor. Its source
// fields are absent for library retirement. No current library/source row,
// lease, authority, or result reconstructed from a mutable cursor belongs here.
type iaRetirementOriginal struct {
	kind, target                    string
	libraryID                       int64
	libraryKey, resolvedRoot        string
	sourceIncarnation, rootIdentity string
	sourceGeneration                int64
	operationID                     [16]byte
	requestDigest                   RequestDigest
}

type iaRetirementResult struct {
	original                        iaRetirementOriginal
	acceptedRevision, appliedMS     int64
	phase, entityKind, entityCursor int64
	objectCursor, state             string
	walkRemoved, walkKept           bool
}

// The dispatcher chooses concrete variants. A caller cannot relabel a generic
// scalar result as a successful security command. Count/cohort are always zero;
// the scalar header still consumes one retained receipt and its actual bytes.
type iaRetireLibraryResult struct{ facts iaRetirementResult }
type iaRetireSourceResult struct{ facts iaRetirementResult }
type iaRetireStepResult struct{ facts iaRetirementResult }

func iaRetirementKindName(kind CommandID) (string, error) {
	switch kind {
	case IARetireLibrary:
		return "retire_library", nil
	case IARetireSource:
		return "retire_source", nil
	case IARetireStep:
		return "retire_step", nil
	default:
		return "", invalid("ia_retirement_result_kind")
	}
}

// Wire overhead includes every fixed blob, LP prefix, scalar and boolean.
// This is separate from the native retained payload accounting below.
const iaRetirementResultFixedBytes = len(canonicalResultCodec) + 2 +
	3*8 + len("retirement") + len("applied") +
	16 + 8 + 16 + 32 + // command identity/request
	6*8 + 8 + 8 + 16 + 32 + 1 + // original TEXT prefixes, library/source scalars, original identity, source presence
	5*8 + 2*8 + 2 + 2*8 // result scalars, cursor/state prefixes, flags, zero count/cohort

func preflightIARetirementResult(kind CommandID, identity CommandIdentity, request RequestDigest, v iaRetirementResult) error {
	name, err := iaRetirementKindName(kind)
	if err != nil {
		return err
	}
	o := v.original
	if identity.Incarnation == ([16]byte{}) || identity.OperationID == ([16]byte{}) || identity.Sequence <= 0 ||
		o.operationID == ([16]byte{}) || o.libraryID <= 0 || v.acceptedRevision <= 0 || v.appliedMS < 0 ||
		v.phase < 0 || v.phase > 5 || v.entityKind < 0 || v.entityCursor < 0 ||
		(v.state != "retiring" && v.state != "complete") || (v.state == "complete" && v.phase != 5) {
		return invalid("ia_retirement_result")
	}
	if o.kind != "library" && o.kind != "folder" || o.target == "" || o.libraryKey == "" || o.resolvedRoot == "" ||
		o.kind == "library" && (o.target != o.libraryKey || o.sourceIncarnation != "" || o.sourceGeneration != 0 || o.rootIdentity != "") ||
		o.kind == "folder" && (o.sourceIncarnation == "" || o.sourceGeneration <= 0 || o.rootIdentity == "") {
		return invalid("ia_retirement_result_original")
	}
	if kind != IARetireStep {
		want := "library"
		if kind == IARetireSource {
			want = "folder"
		}
		if o.kind != want || identity.Sequence != 1 || identity.OperationID != o.operationID || request != o.requestDigest ||
			v.phase != 0 || v.entityKind != 0 || v.entityCursor != 0 || v.objectCursor != "" ||
			v.walkRemoved || v.walkKept || v.state != "retiring" {
			return invalid("ia_retirement_begin_result")
		}
	} else if identity.Sequence <= 1 || identity.OperationID == o.operationID {
		return invalid("ia_retirement_step_result")
	}
	remaining := maxCanonicalResultBytes - iaRetirementResultFixedBytes - len(name)
	texts := [...]string{o.kind, o.target, o.libraryKey, o.resolvedRoot, o.sourceIncarnation, o.rootIdentity, v.objectCursor, v.state}
	// Inspect the complete byte envelope before any UTF-8/NUL scan or hashing.
	for _, text := range texts {
		if len(text) > remaining {
			return invalid("ia_retirement_result_bytes")
		}
		remaining -= len(text)
	}
	for _, text := range texts {
		if !utf8.ValidString(text) || strings.IndexByte(text, 0) >= 0 {
			return invalid("ia_retirement_result_text")
		}
	}
	return nil
}

func writeIARetirementResult(e *requestEncoder, kind CommandID, identity CommandIdentity, request RequestDigest, v iaRetirementResult) error {
	name, err := iaRetirementKindName(kind)
	if err != nil {
		return err
	}
	o := v.original
	e.bytes([]byte(canonicalResultCodec))
	e.bytes([]byte{0, 1})
	e.text(name)
	e.text("retirement")
	e.text("applied")
	e.bytes(identity.Incarnation[:])
	e.number(uint64(identity.Sequence))
	e.bytes(identity.OperationID[:])
	e.bytes(request[:])
	e.text(o.kind)
	e.text(o.target)
	e.number(uint64(o.libraryID))
	e.text(o.libraryKey)
	e.text(o.resolvedRoot)
	// Explicit presence distinguishes source facts from library NULLs, rather
	// than inventing a synthetic source witness for a library retirement.
	e.flag(o.kind == "folder")
	e.text(o.sourceIncarnation)
	e.number(uint64(o.sourceGeneration))
	e.text(o.rootIdentity)
	e.bytes(o.operationID[:])
	e.bytes(o.requestDigest[:])
	e.number(uint64(v.acceptedRevision))
	e.number(uint64(v.appliedMS))
	e.number(uint64(v.phase))
	e.number(uint64(v.entityKind))
	e.number(uint64(v.entityCursor))
	e.text(v.objectCursor)
	e.flag(v.walkRemoved)
	e.flag(v.walkKept)
	e.text(v.state)
	e.number(0) // result_count
	e.number(0) // no member cohort
	if e.err != nil {
		return errors.Join(invalid("ia_retirement_result"), e.err)
	}
	return nil
}

// Exact logical native scalar payload, matching IA-SCHEMA-001. This excludes
// wire framing, SQLite pages and Go memory. ACK never subtracts this debt;
// only deletion of the acknowledged exact receipt can refund it.
func iaRetirementRetainedBytes(kind CommandID, v iaRetirementResult) (int64, error) {
	name, err := iaRetirementKindName(kind)
	if err != nil {
		return 0, err
	}
	n := int64(200)
	for _, text := range []string{name, v.original.kind, v.original.target, v.objectCursor, v.state} {
		if int64(len(text)) > int64(maxCanonicalResultBytes)-n {
			return 0, invalid("ia_retirement_retained_bytes")
		}
		n += int64(len(text))
	}
	return n, nil
}
