package writer

import (
	"errors"
	"unicode/utf8"
)

// ipPersonAcceptResult is the COMPLETE immutable saved result of one person
// provider acceptance. Every field is derived from the original request, its
// exact verified sealed cohort and the saved receipt header, so a replay never
// reconstructs it from mutable person, source, publication or search state. It
// is deliberately NOT the entity accept result: it names the typed person
// facts, the person/role source slot revision and the alias/external counts.
type ipPersonAcceptResult struct {
	OperationID, PublicationID, EvidenceID            string
	Provider, ProviderID, ObservedAt, Lease           string
	Name, Biography, BirthDate, DeathDate, BirthPlace string
	PersonID, PersonRevision, AcceptedRevision        int64
	SourceRevision, OwnerGeneration, ContentRevision  int64
	SearchJobID, AliasCount, ExternalCount            int64
	LeaseUntilMS, AppliedMS                           int64
}

const ipPersonAcceptResultFixedBytes = 4096

func ipPersonAcceptResultDigest(id CommandIdentity, request RequestDigest, v ipPersonAcceptResult) (ResultDigest, error) {
	return canonicalResultDigest(id, request, v)
}

func preflightIPPersonAcceptResult(v ipPersonAcceptResult) error {
	remaining := maxCanonicalResultBytes - ipPersonAcceptResultFixedBytes
	for _, text := range [...]string{v.OperationID, v.PublicationID, v.EvidenceID, v.Provider,
		v.ProviderID, v.ObservedAt, v.Lease, v.Name, v.Biography, v.BirthDate, v.DeathDate,
		v.BirthPlace} {
		if len(text) > remaining || !utf8.ValidString(text) {
			return ErrInvalid
		}
		remaining -= len(text)
	}
	if len(v.OperationID) != 32 || !ipPersonText(v.PublicationID, ipPersonPointerMax, true) ||
		!ipPersonText(v.EvidenceID, ipPersonPointerMax, true) ||
		!ipPolicyProviderCode(v.Provider) ||
		!ipPersonText(v.ProviderID, ipPersonProviderIDMax, true) ||
		!ipPersonText(v.ObservedAt, 64, true) || len(v.Lease) != 32 ||
		!ipPersonText(v.Name, ipPersonTextMax, true) ||
		!ipPersonText(v.Biography, ipPersonTextMax, false) ||
		!ipPersonEvidenceDate(EvidenceText{Present: true, Value: v.BirthDate}) ||
		!ipPersonEvidenceDate(EvidenceText{Present: true, Value: v.DeathDate}) ||
		!ipPersonEvidenceOptionalText(EvidenceText{Present: true, Value: v.BirthPlace}) ||
		v.PersonID <= 0 || v.PersonRevision <= 0 || v.AcceptedRevision != v.PersonRevision ||
		v.SourceRevision <= 0 || v.OwnerGeneration <= 0 || v.ContentRevision <= 0 ||
		v.SearchJobID <= 0 || v.AliasCount < 0 || v.AliasCount > ipPersonMaxChildren ||
		v.ExternalCount < 0 || v.ExternalCount > ipPersonMaxChildren ||
		v.AliasCount+v.ExternalCount > ipPersonMaxChildren ||
		v.LeaseUntilMS <= 0 || v.AppliedMS < 0 ||
		v.LeaseUntilMS <= v.AppliedMS {
		return ErrInvalid
	}
	if _, ok := ipPersonTextTime(v.ObservedAt); !ok {
		return ErrInvalid
	}
	return nil
}

func writeIPPersonAcceptResult(e *requestEncoder, id CommandIdentity, request RequestDigest, v ipPersonAcceptResult) error {
	e.bytes([]byte(canonicalResultCodec))
	e.bytes([]byte{0, 1})
	e.text("ip_person_accept")
	e.text("applied")
	e.bytes(id.Incarnation[:])
	e.number(uint64(id.Sequence))
	e.bytes(id.OperationID[:])
	e.bytes(request[:])
	e.text(v.OperationID)
	e.text(v.PublicationID)
	e.text(v.EvidenceID)
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
	e.number(uint64(v.SourceRevision))
	e.number(uint64(v.OwnerGeneration))
	e.number(uint64(v.ContentRevision))
	e.number(uint64(v.SearchJobID))
	e.number(uint64(v.AliasCount))
	e.number(uint64(v.ExternalCount))
	e.number(uint64(v.LeaseUntilMS))
	e.number(uint64(v.AppliedMS))
	if e.err != nil {
		return errors.Join(invalid("ip_person_accept_result"), e.err)
	}
	return nil
}
