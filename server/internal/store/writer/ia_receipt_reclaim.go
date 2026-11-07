package writer

import (
	"strings"
	"unicode/utf8"
)

// Original configuration namespace facts come from the actual retained owner,
// never from mutable source rows. The original request digest binds its full
// observed roots, source facts and actor. GC does not return those source facts.
type iaConfigurationReceiptOriginal struct {
	operationID          [16]byte
	requestDigest        RequestDigest
	purpose              CommandID
	libraryID            int64
	libraryKey, sourceID string
}

// Ordinal is -1 when no configuration child has been removed, and for every
// retirement/header cursor. A cursor tracks physical data, not GC commands.
type iaReceiptCursor struct{ sequence, ordinal int64 }

type iaReceiptReclaimResult struct {
	capturedACK                     int64
	old, next                       iaReceiptCursor
	removedKind                     string // none, source, header; source belongs only to105
	removedSequence, removedOrdinal int64  // 0/-1 represent absent native keys
	removedCount, removedBytes      int64
	rows, bytes                     int64 // surviving data only, excluding the retained fixed slot
	noNext                          bool
	appliedMS                       int64
}

type iaConfigurationReceiptReclaimResult struct {
	original iaConfigurationReceiptOriginal
	facts    iaReceiptReclaimResult
}
type iaRetirementReceiptReclaimResult struct {
	original iaRetirementOriginal
	facts    iaReceiptReclaimResult
}

func iaReceiptReclaimKindName(kind CommandID) (string, error) {
	switch kind {
	case IAConfigurationReceiptReclaim:
		return "configuration_receipt_reclaim", nil
	case IARetirementReceiptReclaim:
		return "retirement_receipt_reclaim", nil
	default:
		return "", invalid("ia_receipt_reclaim_kind")
	}
}

func validIAReceiptReclaimResult(kind CommandID, id CommandIdentity, v iaReceiptReclaimResult) bool {
	if kind != IAConfigurationReceiptReclaim && kind != IARetirementReceiptReclaim ||
		id.Incarnation == ([16]byte{}) || id.OperationID == ([16]byte{}) || id.Sequence <= 1 ||
		v.capturedACK < 1 || v.capturedACK >= id.Sequence || id.Sequence-v.capturedACK != 1 ||
		v.old.sequence < 0 || v.old.sequence > v.capturedACK || v.next.sequence < v.old.sequence || v.next.sequence > v.capturedACK ||
		v.rows < 0 || v.bytes < 0 || (v.rows == 0) != (v.bytes == 0) || v.noNext != (v.rows == 0) || v.appliedMS < 0 {
		return false
	}
	config := kind == IAConfigurationReceiptReclaim
	if config {
		if v.old.sequence > 1 || v.next.sequence > 1 || v.old.ordinal < -1 || v.next.ordinal < -1 ||
			v.old.sequence == 1 && v.old.ordinal != -1 || v.next.sequence == 1 && v.next.ordinal != -1 {
			return false
		}
	} else if v.old.ordinal != -1 || v.next.ordinal != -1 {
		return false
	}
	switch v.removedKind {
	case "none":
		return v.removedCount == 0 && v.removedSequence == 0 && v.removedOrdinal == -1 && v.removedBytes == 0 && v.next == v.old && v.noNext
	case "source":
		return config && v.removedCount == 1 && v.removedBytes > 0 && v.removedSequence == 1 && v.old.sequence == 0 && v.next.sequence == 0 &&
			v.removedOrdinal >= 0 && v.removedOrdinal > v.old.ordinal && v.removedOrdinal-v.old.ordinal == 1 && v.next.ordinal == v.removedOrdinal && !v.noNext
	case "header":
		if v.removedCount != 1 || v.removedBytes <= 0 || v.removedOrdinal != -1 || v.removedSequence <= v.old.sequence ||
			v.removedSequence > v.capturedACK || v.next.sequence != v.removedSequence || v.next.ordinal != -1 {
			return false
		}
		return !config || v.old.sequence == 0 && v.next.sequence == 1 && v.noNext
	default:
		return false
	}
}

// Complete wire overhead, excluding only the original scope TEXT payloads.
// Optional key scalars have explicit presence bits; -1 ordinals retain their
// two's-complement uint64 encoding, without becoming an absent native field.
func iaReceiptReclaimWireFixed(kind CommandID, v iaReceiptReclaimResult) int {
	name, _ := iaReceiptReclaimKindName(kind)
	n := len(canonicalResultCodec) + 2 + 3*8 + len(name) + len("receipt_reclaim") + len("applied") + 16 + 8 + 16 + 32
	if kind == IAConfigurationReceiptReclaim {
		n += 81 + 108
	} else {
		n += 113 + 92
	}
	n += len(v.removedKind)
	if v.removedCount == 1 {
		n += 8
	}
	if v.removedKind == "source" {
		n += 8
	}
	return n
}

// Validate the whole prospective wire envelope before scanning any variable
// scope text, copying it, or hashing the first payload.
func iaReceiptReclaimTextPreflight(fixed int, texts ...string) error {
	remaining := maxCanonicalResultBytes - fixed
	for _, text := range texts {
		if len(text) > remaining {
			return invalid("ia_receipt_reclaim_result_bytes")
		}
		remaining -= len(text)
	}
	for _, text := range texts {
		if !utf8.ValidString(text) || strings.IndexByte(text, 0) >= 0 {
			return invalid("ia_receipt_reclaim_result_text")
		}
	}
	return nil
}

func preflightIAConfigurationReceiptReclaim(id CommandIdentity, v iaConfigurationReceiptReclaimResult) error {
	o := v.original
	if !validIAReceiptReclaimResult(IAConfigurationReceiptReclaim, id, v.facts) || o.operationID == ([16]byte{}) ||
		o.libraryID <= 0 || o.libraryKey == "" || o.purpose != IAConfigureLibrary && o.purpose != IAConfigureSource ||
		(o.purpose == IAConfigureSource) != (o.sourceID != "") || id.OperationID == o.operationID {
		return invalid("ia_configuration_receipt_reclaim_result")
	}
	return iaReceiptReclaimTextPreflight(iaReceiptReclaimWireFixed(IAConfigurationReceiptReclaim, v.facts), o.libraryKey, o.sourceID)
}

func preflightIARetirementReceiptReclaim(id CommandIdentity, v iaRetirementReceiptReclaimResult) error {
	o := v.original
	if !validIAReceiptReclaimResult(IARetirementReceiptReclaim, id, v.facts) || o.operationID == ([16]byte{}) || o.libraryID <= 0 ||
		o.libraryKey == "" || o.resolvedRoot == "" || id.OperationID == o.operationID ||
		o.kind != "library" && o.kind != "folder" ||
		o.kind == "library" && (o.target != o.libraryKey || o.sourceIncarnation != "" || o.sourceGeneration != 0 || o.rootIdentity != "") ||
		o.kind == "folder" && (o.target == "" || o.sourceIncarnation == "" || o.sourceGeneration <= 0 || o.rootIdentity == "") {
		return invalid("ia_retirement_receipt_reclaim_result")
	}
	return iaReceiptReclaimTextPreflight(iaReceiptReclaimWireFixed(IARetirementReceiptReclaim, v.facts), o.kind, o.target, o.libraryKey, o.resolvedRoot, o.sourceIncarnation, o.rootIdentity)
}

func writeIAReceiptReclaimPrefix(e *requestEncoder, kind CommandID, id CommandIdentity, request RequestDigest) error {
	name, err := iaReceiptReclaimKindName(kind)
	if err != nil {
		return err
	}
	e.bytes([]byte(canonicalResultCodec))
	e.bytes([]byte{0, 1})
	e.text(name)
	e.text("receipt_reclaim")
	e.text("applied")
	e.bytes(id.Incarnation[:])
	e.number(uint64(id.Sequence))
	e.bytes(id.OperationID[:])
	e.bytes(request[:])
	return nil
}

func writeIAReceiptReclaimFacts(e *requestEncoder, kind CommandID, v iaReceiptReclaimResult) error {
	e.number(uint64(v.capturedACK))
	e.number(uint64(v.old.sequence))
	config := kind == IAConfigurationReceiptReclaim
	e.flag(config)
	if config {
		e.number(uint64(v.old.ordinal))
	}
	e.text(v.removedKind)
	e.flag(v.removedCount == 1)
	if v.removedCount == 1 {
		e.number(uint64(v.removedSequence))
	}
	e.flag(v.removedKind == "source")
	if v.removedKind == "source" {
		e.number(uint64(v.removedOrdinal))
	}
	e.number(uint64(v.removedCount))
	e.number(uint64(v.removedBytes))
	e.number(uint64(v.next.sequence))
	if config {
		e.number(uint64(v.next.ordinal))
	}
	e.number(uint64(v.rows))
	e.number(uint64(v.bytes))
	e.flag(v.noNext)
	e.number(uint64(v.appliedMS))
	e.number(1) // one scalar fixed result; no source receipt row is created
	e.number(0) // no result cohort
	return e.err
}

func writeIAConfigurationReceiptReclaim(e *requestEncoder, id CommandIdentity, request RequestDigest, v iaConfigurationReceiptReclaimResult) error {
	if err := writeIAReceiptReclaimPrefix(e, IAConfigurationReceiptReclaim, id, request); err != nil {
		return err
	}
	o := v.original
	e.number(uint64(o.purpose))
	e.bytes(o.operationID[:])
	e.bytes(o.requestDigest[:])
	e.number(uint64(o.libraryID))
	e.text(o.libraryKey)
	e.flag(o.purpose == IAConfigureSource)
	e.text(o.sourceID)
	return writeIAReceiptReclaimFacts(e, IAConfigurationReceiptReclaim, v.facts)
}

func writeIARetirementReceiptReclaim(e *requestEncoder, id CommandIdentity, request RequestDigest, v iaRetirementReceiptReclaimResult) error {
	if err := writeIAReceiptReclaimPrefix(e, IARetirementReceiptReclaim, id, request); err != nil {
		return err
	}
	o := v.original
	e.text(o.kind)
	e.text(o.target)
	e.number(uint64(o.libraryID))
	e.text(o.libraryKey)
	e.text(o.resolvedRoot)
	e.flag(o.kind == "folder")
	e.text(o.sourceIncarnation)
	e.number(uint64(o.sourceGeneration))
	e.text(o.rootIdentity)
	e.bytes(o.operationID[:])
	e.bytes(o.requestDigest[:])
	return writeIAReceiptReclaimFacts(e, IARetirementReceiptReclaim, v.facts)
}

// Logical payload charge of the ACTUAL fixed slot in IA-SCHEMA-001. Its virtual
// meter stores no additional payload. Replacing an ACKed slot reserves this new
// charge before subtracting the old charge; ACK by itself refunds nothing.
func iaReceiptReclaimSlotBytes(kind CommandID, v iaReceiptReclaimResult) (int64, error) {
	name, err := iaReceiptReclaimKindName(kind)
	if err != nil {
		return 0, err
	}
	n := int64(176 + len(name) + len(v.removedKind))
	if kind == IAConfigurationReceiptReclaim {
		n += 16
	}
	if v.removedCount == 1 {
		n += 8
	}
	if v.removedKind == "source" {
		n += 8
	}
	return n, nil
}
