package writer

import "encoding/hex"

// These projections consume ACTUAL native mutation/RETURNING facts. They do
// not observe a clock, allocate an ID, infer provenance or return an Outcome.
// The enclosing W frame alone verifies authority/proof, stores receipt+last,
// and publishes a result after its physical commit/finish protocol.
func projectEvidenceSealResult(scope sealOwnerScope, root evidenceSealRoot, actual sealMutationFacts, content sealReceiptContent) (evidenceSealResult, ResultDigest, error) {
	if root.evidenceID != content.header.ID || root.subjectID != content.header.SubjectID || root.parser != content.header.ParserVersion ||
		root.revision <= 0 || root.revision == 9223372036854775807 || actual.revision != root.revision+1 || root.rows != actual.next ||
		root.buildGeneration <= 0 || root.buildID == "" || content.manifest.algorithm != NormalizedEvidenceAlgorithm {
		return evidenceSealResult{}, ResultDigest{}, ErrConflict
	}
	facts, err := projectSealNativeFacts(scope, actual, content)
	if err != nil {
		return evidenceSealResult{}, ResultDigest{}, err
	}
	facts.BuildID = EvidenceText{Value: root.buildID, Present: true}
	facts.BuildGeneration = EvidenceInteger{Value: root.buildGeneration, Present: true}
	result := evidenceSealResult{facts}
	digest, err := evidenceSealResultDigest(scope.identity, scope.request, result)
	if err != nil {
		return evidenceSealResult{}, ResultDigest{}, err
	}
	return result, digest, nil
}

func projectSourceSealResult(scope sealOwnerScope, root sourceSealRoot, actual sealMutationFacts, content sealReceiptContent) (creditSourceSealResult, ResultDigest, error) {
	if root.evidenceID != content.header.ID || root.subjectID != content.header.SubjectID || root.parser != content.header.ParserVersion ||
		scope.sourceRevision <= 0 || scope.sourceRevision == 9223372036854775807 || actual.revision != scope.sourceRevision+1 || root.next != root.expected || root.next != actual.next ||
		root.manifest != content.manifest || content.manifest.algorithm != NormalizedSourceAlgorithm {
		return creditSourceSealResult{}, ResultDigest{}, ErrConflict
	}
	facts, err := projectSealNativeFacts(scope, actual, content)
	if err != nil {
		return creditSourceSealResult{}, ResultDigest{}, err
	}
	facts.PublicationID = EvidenceText{Value: root.publicationID, Present: true}
	facts.UpstreamOwner = EvidenceText{Value: root.upstreamOwner, Present: true}
	facts.UpstreamIncarnation = sealOptionalIncarnation{Value: root.upstreamIncarnation, Present: true}
	facts.UpstreamSequence = EvidenceInteger{Value: root.upstreamSequence, Present: true}
	result := creditSourceSealResult{facts}
	digest, err := creditSourceSealResultDigest(scope.identity, scope.request, result)
	if err != nil {
		return creditSourceSealResult{}, ResultDigest{}, err
	}
	return result, digest, nil
}

func projectSealNativeFacts(scope sealOwnerScope, actual sealMutationFacts, content sealReceiptContent) (sealResultFacts, error) {
	h, m := content.header, content.manifest
	if h.Provider != scope.provider || h.ProviderID != scope.selectedID || actual.observedCatalogRevision != scope.entityRevision || actual.afterItem != scope.afterItem ||
		actual.next != m.rows || actual.appliedMS < 0 || !scope.valid() {
		return sealResultFacts{}, ErrConflict
	}
	return sealResultFacts{
		ProviderOperationID: scope.ownerID, EntityID: scope.entityID, ObservedCatalogRevision: actual.observedCatalogRevision,
		AppliedMS: actual.appliedMS, AfterItem: actual.afterItem, NextOrdinal: actual.next,
		SealVersion: m.version, EvidenceID: h.ID, SubjectID: h.SubjectID, ParserVersion: h.ParserVersion,
		Provider: h.Provider, SubjectKind: h.SubjectKind, ProviderID: h.ProviderID, Language: h.Language, Region: h.Region,
		RequestShape: h.RequestShape, ObservationDigest: hex.EncodeToString(h.ObservationDigest[:]),
		OwnerGeneration: scope.generation, ContentRevision: actual.revision, Algorithm: m.algorithm,
		Commitment: [32]byte(m.commitment), VerifiedRows: m.rows, CanonicalBytes: m.bytes, RetainedBytes: content.retainedBytes,
	}, nil
}
