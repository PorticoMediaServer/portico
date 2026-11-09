package writer

import (
	"errors"
	"unicode/utf8"
)

// ipEntitySealResult is the COMPLETE immutable saved result of one nonmovie
// entity evidence seal. Every field is derived from the original request and
// its complete manifest, so a replay never reconstructs it from mutable build,
// evidence, lease or provider state. It pins the exact evidence/source tuple
// and the one manifest commitment; it never accepts a source or publishes.
type ipEntitySealResult struct {
	OperationID, EvidenceID, BuildID, Provider, SubjectKind, ProviderID, ParserVersion string
	Language, Region, RequestShape, ObservedAt                                         string
	ObservationDigest                                                                  [32]byte
	Lease                                                                              string
	SubjectID, BuildGeneration, OwnerGeneration, ContentRevision, SealVersion          int64
	SealRows, SealBytes, LeaseUntilMS, AppliedMS                                       int64
	Commitment                                                                         [32]byte
}

const ipEntitySealResultFixedBytes = 4096

func ipEntitySealResultDigest(id CommandIdentity, request RequestDigest, v ipEntitySealResult) (ResultDigest, error) {
	return canonicalResultDigest(id, request, v)
}

func preflightIPEntitySealResult(v ipEntitySealResult) error {
	remaining := maxCanonicalResultBytes - ipEntitySealResultFixedBytes
	for _, s := range [...]string{
		v.OperationID, v.EvidenceID, v.BuildID, v.Provider, v.SubjectKind, v.ProviderID,
		v.ParserVersion, v.Language, v.Region, v.RequestShape, v.ObservedAt, v.Lease,
	} {
		if len(s) > remaining || !utf8.ValidString(s) {
			return ErrInvalid
		}
		remaining -= len(s)
	}
	if len(v.OperationID) != 32 || !ipEvidenceText(v.EvidenceID, ipEvidenceIDMax) ||
		!ipEvidenceText(v.BuildID, ipEvidenceIDMax) || !ipPolicyProviderCode(v.Provider) ||
		!ipEvidenceSubjectKind(v.SubjectKind) || !ipEvidenceText(v.ProviderID, ipEvidenceIDMax) ||
		!ipEvidenceText(v.ParserVersion, ipEvidenceTextMax) || len(v.Language) == 0 ||
		len(v.Region) == 0 || !ipEvidenceText(v.RequestShape, ipEvidenceTextMax) ||
		!ipEvidenceText(v.ObservedAt, 64) || len(v.Lease) != 32 ||
		v.SubjectID <= 0 || v.BuildGeneration <= 0 || v.OwnerGeneration <= 0 ||
		v.ContentRevision <= 0 || v.SealVersion != ipSealVersion || v.SealRows <= 0 ||
		v.SealBytes <= 0 || v.LeaseUntilMS <= 0 || v.AppliedMS < 0 ||
		!ipSealDigestSet(v.ObservationDigest) || !ipSealDigestSet(v.Commitment) {
		return ErrInvalid
	}
	return nil
}

func writeIPEntitySealResult(e *requestEncoder, id CommandIdentity, request RequestDigest, v ipEntitySealResult) error {
	e.bytes([]byte(canonicalResultCodec))
	e.bytes([]byte{0, 1})
	e.text("ip_entity_seal")
	e.text("applied")
	e.bytes(id.Incarnation[:])
	e.number(uint64(id.Sequence))
	e.bytes(id.OperationID[:])
	e.bytes(request[:])
	e.text(v.OperationID)
	e.text(v.EvidenceID)
	e.text(v.BuildID)
	e.text(v.Provider)
	e.text(v.SubjectKind)
	e.text(v.ProviderID)
	e.text(v.ParserVersion)
	e.text(v.Language)
	e.text(v.Region)
	e.text(v.RequestShape)
	e.text(v.ObservedAt)
	e.bytes(v.ObservationDigest[:])
	e.text(v.Lease)
	e.number(uint64(v.SubjectID))
	e.number(uint64(v.BuildGeneration))
	e.number(uint64(v.OwnerGeneration))
	e.number(uint64(v.ContentRevision))
	e.number(uint64(v.SealVersion))
	e.number(uint64(v.SealRows))
	e.number(uint64(v.SealBytes))
	e.number(uint64(v.LeaseUntilMS))
	e.number(uint64(v.AppliedMS))
	e.bytes(v.Commitment[:])
	if e.err != nil {
		return errors.Join(invalid("ip_entity_seal_result"), e.err)
	}
	return nil
}
