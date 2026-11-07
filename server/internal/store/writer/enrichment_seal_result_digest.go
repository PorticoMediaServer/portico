package writer

import "errors"

const maxSealRetainedBytes = 8876032 // adopted009 per-proof layout bound, not RAM

type sealOptionalIncarnation struct {
	Value   [16]byte
	Present bool
}

// Exact immutable native seal receipt projection. Mutable proof pin/revision,
// cleanup/debt state and current body/head rows never enter historical results.
// These facts alone are neither native proof nor disclosure/custody authority.
type sealResultFacts struct {
	ProviderOperationID                                                                                 string
	EntityID, ObservedCatalogRevision, AppliedMS                                                        int64
	AfterItem                                                                                           string
	NextOrdinal                                                                                         int64
	PublicationID                                                                                       EvidenceText
	ImageID, ImageRevision                                                                              EvidenceInteger
	ResultCount, MemberCohortLength, SealVersion                                                        int64
	EvidenceID                                                                                          string
	SubjectID                                                                                           int64
	ParserVersion, Provider, SubjectKind, ProviderID, Language, Region, RequestShape, ObservationDigest string
	OwnerGeneration                                                                                     int64
	BuildID                                                                                             EvidenceText
	BuildGeneration                                                                                     EvidenceInteger
	ContentRevision                                                                                     int64
	Algorithm                                                                                           string
	Commitment                                                                                          [32]byte
	VerifiedRows, CanonicalBytes, RetainedBytes                                                         int64
	UpstreamOwner                                                                                       EvidenceText
	UpstreamIncarnation                                                                                 sealOptionalIncarnation
	UpstreamSequence                                                                                    EvidenceInteger
}

type evidenceSealResult struct{ facts sealResultFacts }
type creditSourceSealResult struct{ facts sealResultFacts }

func evidenceSealResultDigest(i CommandIdentity, q RequestDigest, v evidenceSealResult) (ResultDigest, error) {
	return canonicalResultDigest(i, q, v)
}
func creditSourceSealResultDigest(i CommandIdentity, q RequestDigest, v creditSourceSealResult) (ResultDigest, error) {
	return canonicalResultDigest(i, q, v)
}

func preflightEnrichmentSealResult(kind CommandID, v sealResultFacts) error {
	fixed := uint64(410)
	if kind == CreditSourceSeal {
		fixed = 439
	} else if kind != EvidenceSeal {
		return invalid("seal_result_kind")
	}
	// Complete native TEXT occurrences, including each repeated value. This is
	// only this saved result, not the complete retained owner/capture/pin census.
	n, err := CheckSealControlStrings([]string{v.ProviderOperationID, v.AfterItem, v.PublicationID.Value, v.EvidenceID, v.ParserVersion, v.Provider, v.SubjectKind, v.ProviderID, v.Language, v.Region, v.RequestShape, v.ObservationDigest, v.BuildID.Value, v.Algorithm, v.UpstreamOwner.Value})
	if err != nil {
		return err
	}
	if n > maxCanonicalResultBytes-fixed {
		return invalid("canonical_result_preflight")
	}
	if !nonblank(v.ProviderOperationID) || v.EntityID <= 0 || v.ObservedCatalogRevision <= 0 || v.AppliedMS < 0 || v.ResultCount != 0 || v.MemberCohortLength != 0 ||
		v.ImageID != (EvidenceInteger{}) || v.ImageRevision != (EvidenceInteger{}) || v.SealVersion != 1 || !nonblank(v.EvidenceID) || v.SubjectID <= 0 ||
		!normalizedNamespace(v.ParserVersion, v.Provider, v.SubjectKind, v.ProviderID, v.RequestShape) || !normalizedHex(v.ObservationDigest, 64) ||
		v.OwnerGeneration <= 0 || v.ContentRevision <= 0 || v.CanonicalBytes <= 0 || v.CanonicalBytes > maxCanonicalRequestBytes || v.RetainedBytes <= 0 || v.RetainedBytes > maxSealRetainedBytes || v.NextOrdinal != v.VerifiedRows {
		return invalid("seal_result")
	}
	if kind == EvidenceSeal {
		if v.PublicationID != (EvidenceText{}) || !v.BuildID.Present || !nonblank(v.BuildID.Value) || !v.BuildGeneration.Present || v.BuildGeneration.Value <= 0 ||
			v.UpstreamOwner != (EvidenceText{}) || v.UpstreamIncarnation != (sealOptionalIncarnation{}) || v.UpstreamSequence != (EvidenceInteger{}) ||
			v.VerifiedRows < 1 || v.VerifiedRows > 1+MaxCastCredits+MaxCrewCredits || v.Algorithm != NormalizedEvidenceAlgorithm {
			return invalid("evidence_seal_result")
		}
	} else if !v.PublicationID.Present || !normalizedHex(v.PublicationID.Value, 32) || v.BuildID != (EvidenceText{}) || v.BuildGeneration != (EvidenceInteger{}) ||
		!v.UpstreamOwner.Present || !nonblank(v.UpstreamOwner.Value) || !v.UpstreamIncarnation.Present || !v.UpstreamSequence.Present || v.UpstreamSequence.Value <= 0 ||
		v.VerifiedRows < 0 || v.VerifiedRows > MaxCastCredits+MaxCrewCredits || v.Algorithm != NormalizedSourceAlgorithm {
		return invalid("credit_source_seal_result")
	}
	return nil
}

func writeEnrichmentSealResult(e *requestEncoder, kind CommandID, i CommandIdentity, q RequestDigest, v sealResultFacts) error {
	name := "evidence_seal"
	if kind == CreditSourceSeal {
		name = "credit_source_seal"
	}
	e.bytes([]byte(canonicalResultCodec))
	e.bytes([]byte{0, 1})
	e.text(name)
	e.text("seal")
	e.text("applied")
	e.text(v.ProviderOperationID)
	e.bytes(i.Incarnation[:])
	e.number(uint64(i.Sequence))
	e.bytes(i.OperationID[:])
	e.bytes(q[:])
	e.number(uint64(v.EntityID))
	e.number(uint64(v.ObservedCatalogRevision))
	e.number(uint64(v.AppliedMS))
	e.text(v.AfterItem)
	e.number(uint64(v.NextOrdinal))
	writeSealResultText(e, v.PublicationID)
	e.bytes([]byte{0, 0})
	e.number(uint64(v.ResultCount))
	e.number(uint64(v.MemberCohortLength))
	e.number(uint64(v.SealVersion))
	e.text(v.EvidenceID)
	e.number(uint64(v.SubjectID))
	for _, s := range []string{v.ParserVersion, v.Provider, v.SubjectKind, v.ProviderID, v.Language, v.Region, v.RequestShape, v.ObservationDigest} {
		e.text(s)
	}
	e.number(uint64(v.OwnerGeneration))
	writeSealResultText(e, v.BuildID)
	writeSealResultInteger(e, v.BuildGeneration)
	e.number(uint64(v.ContentRevision))
	e.text(v.Algorithm)
	e.bytes(v.Commitment[:])
	e.number(uint64(v.VerifiedRows))
	e.number(uint64(v.CanonicalBytes))
	e.number(uint64(v.RetainedBytes))
	writeSealResultText(e, v.UpstreamOwner)
	e.flag(v.UpstreamIncarnation.Present)
	if v.UpstreamIncarnation.Present {
		e.bytes(v.UpstreamIncarnation.Value[:])
	}
	writeSealResultInteger(e, v.UpstreamSequence)
	if e.err != nil {
		return errors.Join(invalid("seal_result_encoding"), e.err)
	}
	return nil
}
func writeSealResultText(e *requestEncoder, v EvidenceText) {
	e.flag(v.Present)
	if v.Present {
		e.text(v.Value)
	}
}
func writeSealResultInteger(e *requestEncoder, v EvidenceInteger) {
	e.flag(v.Present)
	if v.Present {
		e.number(uint64(v.Value))
	}
}
