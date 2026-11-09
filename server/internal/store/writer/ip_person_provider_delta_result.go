package writer

import (
	"errors"
	"unicode/utf8"
)

// ipPersonDeltaResult is the COMPLETE immutable saved result of one typed
// person provider delta replacement. Every field is derived from the original
// request, its exact verified NEW sealed cohort and the saved receipt header.
// It names the new accepted person publication head (or the retained old head
// for an exact no-op), the exact old head it replaced, the retained upstream
// source keys and the typed person facts, and it never reads mutable state.
type ipPersonDeltaResult struct {
	OperationID, PublicationID, PreviousPublicationID string
	EvidenceID, PreviousEvidenceID                    string
	Provider, ProviderID, ObservedAt, Lease           string
	Name, Biography, BirthDate, DeathDate, BirthPlace string
	PersonID, PersonRevision, AcceptedRevision        int64
	PreviousSourceRevision, SourceRevision            int64
	OwnerGeneration, ContentRevision                  int64
	SearchJobID, AliasCount, ExternalCount            int64
	LeaseUntilMS, AppliedMS                           int64
	NoOp                                              bool
}

const ipPersonDeltaResultFixedBytes = 4096

func ipPersonDeltaResultDigest(id CommandIdentity, request RequestDigest, v ipPersonDeltaResult) (ResultDigest, error) {
	return canonicalResultDigest(id, request, v)
}

func preflightIPPersonDeltaResult(v ipPersonDeltaResult) error {
	remaining := maxCanonicalResultBytes - ipPersonDeltaResultFixedBytes
	for _, text := range [...]string{v.OperationID, v.PublicationID, v.PreviousPublicationID,
		v.EvidenceID, v.PreviousEvidenceID, v.Provider, v.ProviderID, v.ObservedAt, v.Lease,
		v.Name, v.Biography, v.BirthDate, v.DeathDate, v.BirthPlace} {
		if len(text) > remaining || !utf8.ValidString(text) {
			return ErrInvalid
		}
		remaining -= len(text)
	}
	if len(v.OperationID) != 32 ||
		!ipPersonText(v.PublicationID, ipPersonPointerMax, true) ||
		!ipPersonText(v.PreviousPublicationID, ipPersonPointerMax, true) ||
		!ipPersonText(v.EvidenceID, ipPersonPointerMax, true) ||
		!ipPersonText(v.PreviousEvidenceID, ipPersonPointerMax, true) ||
		v.EvidenceID == v.PreviousEvidenceID ||
		!ipPolicyProviderCode(v.Provider) ||
		!ipPersonText(v.ProviderID, ipPersonProviderIDMax, true) ||
		!ipPersonText(v.ObservedAt, 64, true) || len(v.Lease) != 32 ||
		!ipPersonText(v.Name, ipPersonTextMax, true) ||
		!ipPersonText(v.Biography, ipPersonTextMax, false) ||
		!ipPersonEvidenceDate(EvidenceText{Present: true, Value: v.BirthDate}) ||
		!ipPersonEvidenceDate(EvidenceText{Present: true, Value: v.DeathDate}) ||
		!ipPersonEvidenceOptionalText(EvidenceText{Present: true, Value: v.BirthPlace}) ||
		v.PersonID <= 0 || v.PersonRevision <= 0 || v.AcceptedRevision != v.PersonRevision ||
		v.PreviousSourceRevision <= 0 || v.SourceRevision <= 0 ||
		v.OwnerGeneration <= 0 || v.ContentRevision <= 0 ||
		v.SearchJobID <= 0 || v.AliasCount < 0 || v.AliasCount > ipPersonMaxChildren ||
		v.ExternalCount < 0 || v.ExternalCount > ipPersonMaxChildren ||
		v.AliasCount+v.ExternalCount > ipPersonMaxChildren ||
		v.LeaseUntilMS <= 0 || v.AppliedMS < 0 || v.LeaseUntilMS <= v.AppliedMS {
		return ErrInvalid
	}
	if _, ok := ipPersonTextTime(v.ObservedAt); !ok {
		return ErrInvalid
	}
	// The head identity is exact: an unchanged observation retains the old
	// publication and source revision, a new observation publishes a new head.
	if v.NoOp != (v.PublicationID == v.PreviousPublicationID) {
		return ErrInvalid
	}
	if v.NoOp {
		if v.SourceRevision != v.PreviousSourceRevision {
			return ErrInvalid
		}
	} else if v.SourceRevision != v.PreviousSourceRevision+1 {
		return ErrInvalid
	}
	return nil
}

func writeIPPersonDeltaResult(e *requestEncoder, id CommandIdentity, request RequestDigest, v ipPersonDeltaResult) error {
	e.bytes([]byte(canonicalResultCodec))
	e.bytes([]byte{0, 1})
	e.text("ip_person_delta")
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
	e.text(v.Provider)
	e.text(v.ProviderID)
	e.text(v.ObservedAt)
	e.text(v.Lease)
	e.text(v.Name)
	e.text(v.Biography)
	e.text(v.BirthDate)
	e.text(v.DeathDate)
	e.text(v.BirthPlace)
	e.number(uint64(v.PersonID))
	e.number(uint64(v.PersonRevision))
	e.number(uint64(v.AcceptedRevision))
	e.number(uint64(v.PreviousSourceRevision))
	e.number(uint64(v.SourceRevision))
	e.number(uint64(v.OwnerGeneration))
	e.number(uint64(v.ContentRevision))
	e.number(uint64(v.SearchJobID))
	e.number(uint64(v.AliasCount))
	e.number(uint64(v.ExternalCount))
	e.number(uint64(v.LeaseUntilMS))
	e.number(uint64(v.AppliedMS))
	e.flag(v.NoOp)
	if e.err != nil {
		return errors.Join(invalid("ip_person_delta_result"), e.err)
	}
	return nil
}
