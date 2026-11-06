package writer

import (
	"errors"
	"unicode/utf8"
)

const evidenceBeginFixedBytes = 223 // complete v1 wire excluding two TEXT payloads

// evidenceBeginResult is the complete immutable EvidenceStage BEGIN receipt
// projection. Header row zero is materialized; NextOrdinal is the next row.
// Caller-preallocated evidence/build identities stay in the original request,
// never in publication/image slots. This result proves no source seal.
type evidenceBeginResult struct {
	ProviderOperationID     string
	EntityID                int64
	ObservedCatalogRevision int64
	AppliedMS               int64
	AfterItem               string
	NextOrdinal             int64
	PublicationID           *string
	ImageID                 *int64
	ImageRevision           *int64
	ResultCount             int64
	MemberCohortLength      int64
}

func evidenceBeginResultDigest(identity CommandIdentity, request RequestDigest, result evidenceBeginResult) (ResultDigest, error) {
	return canonicalResultDigest(identity, request, result)
}

func preflightEvidenceBeginResult(v evidenceBeginResult) error {
	if v.ProviderOperationID == "" || v.EntityID <= 0 || v.ObservedCatalogRevision <= 0 || v.AppliedMS < 0 ||
		v.NextOrdinal != 1 || v.PublicationID != nil || v.ImageID != nil || v.ImageRevision != nil ||
		v.ResultCount != 1 || v.MemberCohortLength != 0 {
		return invalid("evidence_begin_result")
	}
	// Subtraction checks the COMPLETE prospective wire without overflow, UTF-8
	// scans, copying or hashing. Native TEXT bytes are not normalized or trimmed.
	remaining := maxCanonicalResultBytes - evidenceBeginFixedBytes
	if len(v.ProviderOperationID) > remaining || len(v.AfterItem) > remaining-len(v.ProviderOperationID) ||
		!utf8.ValidString(v.ProviderOperationID) || !utf8.ValidString(v.AfterItem) {
		return invalid("canonical_result_preflight")
	}
	return nil
}

func writeEvidenceBeginResult(e *requestEncoder, identity CommandIdentity, request RequestDigest, v evidenceBeginResult) error {
	e.bytes([]byte(canonicalResultCodec))
	e.bytes([]byte{0, 1})
	e.text("evidence_stage")
	e.text("begin")
	e.text("applied")
	e.text(v.ProviderOperationID)
	e.bytes(identity.Incarnation[:])
	e.number(uint64(identity.Sequence))
	e.bytes(identity.OperationID[:])
	e.bytes(request[:])
	e.number(uint64(v.EntityID))
	e.number(uint64(v.ObservedCatalogRevision))
	e.number(uint64(v.AppliedMS))
	e.text(v.AfterItem)
	e.number(uint64(v.NextOrdinal))
	e.bytes([]byte{0, 0, 0}) // required NULL publication, image and image revision
	e.number(uint64(v.ResultCount))
	e.number(uint64(v.MemberCohortLength))
	if e.err != nil {
		return errors.Join(invalid("evidence_begin_result"), e.err)
	}
	return nil
}
