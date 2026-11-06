package writer

import (
	"bytes"
	"errors"
	"unicode/utf8"
)

// scanRunOriginalScope is the retained native context, not current source or
// lease authority. Library IDs and keys, source generation and incarnation,
// source root and observed directory identity retain their separate meanings.
type scanRunOriginalScope struct {
	RunID, LibraryKey           string
	LibraryID                   int64
	SourceID, SourceIncarnation string
	SourceGeneration            int64
	RootIdentity                string
	ResolvedRoot                []byte
	FollowSymlinks              bool
	PolicyRevision              int64
	ScanMode                    string
	CreateOperationID           [16]byte
	CreateRequestDigest         RequestDigest
}

// Each field below is an immutable native receipt echo, except Original, which
// comes from the retained run's immutable creation group. No field is recovered
// from a mutable current lease, source, directory or job after receipt lookup.
type scanRunLifecycleResult struct {
	Original                             scanRunOriginalScope
	AcceptedRunRevision, AppliedMS       int64
	LeaseOwner, LeaseGeneration          int64
	LeaseExpiresMS, AcknowledgedSequence int64
	Discovered, Processed                int64
	RootIdentity, RootRevision           string
	RootCursor                           string
	RootPages, RootSkipped               int64
}

// Distinct concrete variants are selected by the closed dispatcher/decoder.
// There is no caller-selected kind, generic zero-member Inventory result, or
// exported constructor. These values grant no execution or disclosure authority.
type inventoryCreateResult struct{ facts scanRunLifecycleResult }
type inventoryClaimResult struct{ facts scanRunLifecycleResult }

func inventoryCreateResultDigest(identity CommandIdentity, request RequestDigest, v inventoryCreateResult) (ResultDigest, error) {
	return canonicalResultDigest(identity, request, v)
}

func inventoryClaimResultDigest(identity CommandIdentity, request RequestDigest, v inventoryClaimResult) (ResultDigest, error) {
	return canonicalResultDigest(identity, request, v)
}

// Fixed bytes include every LP prefix, scalar, identity, explicit NULL and
// zero cohort. The command name's byte length is added separately below.
const scanLifecycleResultFixedBytes = len(canonicalResultCodec) + 2 +
	3*8 + len("run_lifecycle") + len("applied") +
	10*8 + 16 + 8 + 16 + 32 +
	8 + 8 + 1 + 8 + 16 + 32 +
	10*8 + 1 + 8 + 2*8

func preflightScanRunLifecycleResult(kind CommandID, identity CommandIdentity, request RequestDigest, v scanRunLifecycleResult) error {
	var name string
	switch kind {
	case InventoryCreate:
		name = "inventory_create"
		if identity.Sequence != 1 || v.AcceptedRunRevision != 1 || v.AcknowledgedSequence != 0 ||
			v.LeaseOwner != 0 || v.LeaseGeneration != 0 || v.LeaseExpiresMS != 0 ||
			v.Discovered != 0 || v.Processed != 0 || v.RootCursor != "" || v.RootPages != 0 || v.RootSkipped != 0 ||
			v.Original.CreateOperationID != identity.OperationID || v.Original.CreateRequestDigest != request {
			return invalid("inventory_create_result")
		}
	case InventoryClaim:
		name = "inventory_claim"
		if identity.Sequence <= 1 || v.AcceptedRunRevision <= 1 || v.AcknowledgedSequence != identity.Sequence-1 ||
			v.LeaseOwner <= 0 || v.LeaseGeneration <= 0 || v.LeaseExpiresMS <= v.AppliedMS {
			return invalid("inventory_claim_result")
		}
	default:
		return invalid("scan_lifecycle_result_kind")
	}
	o := v.Original
	if o.LibraryID <= 0 || o.SourceGeneration <= 0 || o.PolicyRevision <= 0 || v.AppliedMS < 0 ||
		v.Discovered < 0 || v.Processed < 0 || v.RootPages < 0 || v.RootSkipped < 0 || v.RootSkipped > v.RootPages {
		return invalid("scan_lifecycle_result")
	}
	// Inspect the COMPLETE byte envelope before UTF-8, path scans or hashing.
	// In particular a late root cursor cannot hide an oversized result behind
	// a cheap prefix. No intermediate encoding buffer or root conversion exists.
	remaining := maxCanonicalResultBytes - scanLifecycleResultFixedBytes - len(name)
	texts := [...]string{o.RunID, o.LibraryKey, o.SourceID, o.SourceIncarnation, o.RootIdentity,
		o.ScanMode, v.RootIdentity, v.RootRevision, v.RootCursor}
	for i, s := range texts {
		if (i != len(texts)-1 && s == "") || len(s) > remaining {
			return invalid("scan_lifecycle_result_bytes")
		}
		remaining -= len(s)
	}
	if len(o.ResolvedRoot) == 0 || len(o.ResolvedRoot) > remaining {
		return invalid("scan_lifecycle_result_bytes")
	}
	for _, s := range texts {
		if !utf8.ValidString(s) {
			return invalid("scan_lifecycle_result_utf8")
		}
	}
	if !utf8.Valid(o.ResolvedRoot) || bytes.IndexByte(o.ResolvedRoot, 0) >= 0 {
		return invalid("scan_lifecycle_result_root")
	}
	switch o.ScanMode {
	case "first-import", "incremental", "integrity", "full":
	default:
		return invalid("scan_lifecycle_result_mode")
	}
	return nil
}

func writeScanRunLifecycleResult(e *requestEncoder, kind CommandID, identity CommandIdentity, request RequestDigest, v scanRunLifecycleResult) error {
	var name string
	switch kind {
	case InventoryCreate:
		name = "inventory_create"
	case InventoryClaim:
		name = "inventory_claim"
	default:
		return invalid("scan_lifecycle_result_kind")
	}
	o := v.Original
	e.bytes([]byte(canonicalResultCodec))
	e.bytes([]byte{0, 1})
	e.text(name)
	e.text("run_lifecycle")
	e.text("applied")
	e.text(o.RunID)
	e.bytes(identity.Incarnation[:])
	e.number(uint64(identity.Sequence))
	e.bytes(identity.OperationID[:])
	e.bytes(request[:])
	e.number(uint64(o.LibraryID))
	e.text(o.LibraryKey)
	e.text(o.SourceID)
	e.number(uint64(o.SourceGeneration))
	e.text(o.SourceIncarnation)
	e.text(o.RootIdentity)
	e.number(uint64(len(o.ResolvedRoot)))
	e.bytes(o.ResolvedRoot)
	e.flag(o.FollowSymlinks)
	e.number(uint64(o.PolicyRevision))
	e.text(o.ScanMode)
	e.bytes(o.CreateOperationID[:])
	e.bytes(o.CreateRequestDigest[:])
	e.number(uint64(v.AcceptedRunRevision))
	e.number(uint64(v.AppliedMS))
	e.number(uint64(v.LeaseOwner))
	e.number(uint64(v.LeaseGeneration))
	e.number(uint64(v.LeaseExpiresMS))
	e.number(uint64(v.AcknowledgedSequence))
	e.number(uint64(v.Discovered))
	e.number(uint64(v.Processed))
	e.text(v.RootIdentity)
	e.text(v.RootRevision)
	e.text(v.RootCursor)
	e.number(uint64(v.RootPages))
	e.number(uint64(v.RootSkipped))
	e.bytes([]byte{0}) // source_revision is required SQL NULL for lifecycle
	e.text("")         // next_cursor is unused; root cursor has its own named slot
	e.number(0)        // result_count
	e.number(0)        // no member cohort
	if e.err != nil {
		return errors.Join(invalid("scan_lifecycle_result"), e.err)
	}
	return nil
}
