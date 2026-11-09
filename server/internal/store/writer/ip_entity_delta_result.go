package writer

import (
	"errors"
	"math"
	"unicode/utf8"
)

// ipEntityDeltaResult is the COMPLETE immutable saved result of one typed
// provider delta replacement. Every field is derived from the original request,
// its exact verified NEW source cohort and the saved receipt header, so a replay
// never reconstructs it from mutable publication, subject-provider, entity or
// search state. The result names the new accepted publication head (or the
// retained old head for an exact no-op), the exact old head it replaced and the
// retained upstream proof keys.
type ipEntityDeltaResult struct {
	OperationID, PublicationID, PreviousPublicationID            string
	EvidenceID, PreviousEvidenceID                               string
	SetID, PreviousSetID                                         string
	Provider, SubjectKind, ProviderID, ParserVersion, ObservedAt string
	Lease                                                        string
	EntityID, OwnerGeneration, ContentRevision                   int64
	AcceptedRevision, SelectionRevision, SearchJobID             int64
	FieldCount, ExternalCount, TermCount, CreditCount            int64
	LeaseUntilMS, AppliedMS                                      int64
	NoOp                                                         bool
}

const ipEntityDeltaResultFixedBytes = 4096

func ipEntityDeltaResultDigest(id CommandIdentity, request RequestDigest, v ipEntityDeltaResult) (ResultDigest, error) {
	return canonicalResultDigest(id, request, v)
}

func preflightIPEntityDeltaResult(v ipEntityDeltaResult) error {
	remaining := maxCanonicalResultBytes - ipEntityDeltaResultFixedBytes
	for _, s := range [...]string{
		v.OperationID, v.PublicationID, v.PreviousPublicationID, v.EvidenceID,
		v.PreviousEvidenceID, v.SetID, v.PreviousSetID, v.Provider, v.SubjectKind,
		v.ProviderID, v.ParserVersion, v.ObservedAt, v.Lease,
	} {
		if len(s) > remaining || !utf8.ValidString(s) {
			return ErrInvalid
		}
		remaining -= len(s)
	}
	if len(v.OperationID) != 32 || !ipEntityAcceptText(v.PublicationID, ipEntityAcceptIDMax) ||
		len(v.PublicationID) == 0 ||
		!ipEntityAcceptText(v.PreviousPublicationID, ipEntityAcceptIDMax) ||
		len(v.PreviousPublicationID) == 0 ||
		!ipEvidenceText(v.EvidenceID, ipEvidenceIDMax) || len(v.EvidenceID) == 0 ||
		!ipEvidenceText(v.PreviousEvidenceID, ipEvidenceIDMax) ||
		len(v.PreviousEvidenceID) == 0 ||
		!ipEntityAcceptOptionalID(v.SetID) || !ipEntityAcceptOptionalID(v.PreviousSetID) ||
		v.EvidenceID == v.PreviousEvidenceID ||
		!ipPolicyProviderCode(v.Provider) || !ipEntityAcceptSubjectKind(v.SubjectKind) ||
		!ipEvidenceText(v.ProviderID, ipEvidenceIDMax) || len(v.ProviderID) == 0 ||
		!ipEvidenceText(v.ParserVersion, ipEvidenceTextMax) || len(v.ParserVersion) == 0 ||
		!ipEvidenceText(v.ObservedAt, 64) || len(v.ObservedAt) == 0 || len(v.Lease) != 32 ||
		v.EntityID <= 0 || v.OwnerGeneration <= 0 || v.ContentRevision <= 0 ||
		v.AcceptedRevision <= 0 || v.SelectionRevision <= 0 || v.SelectionRevision == math.MaxInt64 ||
		v.SearchJobID <= 0 || v.FieldCount <= 0 || v.FieldCount > ipEntityAcceptFieldsMax ||
		v.ExternalCount < 0 || v.ExternalCount > ipEvidenceFamilyBound("external") ||
		v.TermCount < 0 || v.TermCount > ipEvidenceFamilyBound("term") ||
		v.CreditCount < 0 || v.CreditCount > ipEvidenceFamilyBound("credit") ||
		v.LeaseUntilMS <= 0 || v.AppliedMS < 0 {
		return ErrInvalid
	}
	if v.SetID != "" && v.SubjectKind != "series" && v.SubjectKind != "season" {
		return ErrInvalid
	}
	if v.PreviousSetID != "" && v.SubjectKind != "series" && v.SubjectKind != "season" {
		return ErrInvalid
	}
	// The head identity is exact: an unchanged observation retains the old
	// publication head, a new observation publishes a different one.
	if v.NoOp != (v.PublicationID == v.PreviousPublicationID) {
		return ErrInvalid
	}
	return nil
}

func writeIPEntityDeltaResult(e *requestEncoder, id CommandIdentity, request RequestDigest, v ipEntityDeltaResult) error {
	e.bytes([]byte(canonicalResultCodec))
	e.bytes([]byte{0, 1})
	e.text("ip_entity_delta")
	e.text("applied")
	e.bytes(id.Incarnation[:])
	e.number(uint64(id.Sequence))
	e.bytes(id.OperationID[:])
	e.bytes(request[:])
	e.text(v.OperationID)
	e.text(v.PublicationID)
	e.text(v.PreviousPublicationID)
	e.text(v.EvidenceID)
	e.text(v.PreviousEvidenceID)
	e.text(v.SetID)
	e.text(v.PreviousSetID)
	e.text(v.Provider)
	e.text(v.SubjectKind)
	e.text(v.ProviderID)
	e.text(v.ParserVersion)
	e.text(v.ObservedAt)
	e.text(v.Lease)
	e.number(uint64(v.EntityID))
	e.number(uint64(v.OwnerGeneration))
	e.number(uint64(v.ContentRevision))
	e.number(uint64(v.AcceptedRevision))
	e.number(uint64(v.SelectionRevision))
	e.number(uint64(v.SearchJobID))
	e.number(uint64(v.FieldCount))
	e.number(uint64(v.ExternalCount))
	e.number(uint64(v.TermCount))
	e.number(uint64(v.CreditCount))
	e.number(uint64(v.LeaseUntilMS))
	e.number(uint64(v.AppliedMS))
	e.flag(v.NoOp)
	if e.err != nil {
		return errors.Join(invalid("ip_entity_delta_result"), e.err)
	}
	return nil
}
