package writer

import (
	"errors"
	"fmt"
)

// Exact saved-native projection order. SQL qualification and the byte-length
// point are owned separately; these offsets neither declare nor execute SQL.
// All 50 native schema columns are accounted for: 44 inputs below; mutable
// proof_revision, proof_pin_count, proof_reclaim_due_ms and
// proof_cleanup_after_sequence excluded; generated upstream_evidence_kind and
// source_dependency_kind excluded. The latter are native-generated from the
// corresponding owner NULL tags, rather than independently saved result facts.
// This decoder neither observes nor validates mutable proof lifecycle state.
const (
	savedSealOwner                 = iota // provider_operation_id
	savedSealIncarnation                  // command_incarnation
	savedSealSequence                     // command_sequence
	savedSealOperation                    // operation_id
	savedSealRequest                      // request_digest
	savedSealKind                         // command_kind
	savedSealAcceptedRevision             // accepted_revision
	savedSealAppliedMS                    // applied_ms
	savedSealEntity                       // entity_id
	savedSealAfterItem                    // after_item
	savedSealAfterOrdinal                 // after_ordinal
	savedSealPublication                  // publication_id
	savedSealImage                        // effective_image_id
	savedSealImageRevision                // effective_image_revision
	savedSealResultCount                  // result_count
	savedSealResultDigest                 // result_digest
	savedSealVersion                      // seal_version
	savedSealEvidence                     // seal_evidence_id
	savedSealSubject                      // seal_subject_id
	savedSealParser                       // seal_parser_version
	savedSealProvider                     // seal_provider
	savedSealSubjectKind                  // seal_subject_kind
	savedSealProviderID                   // seal_provider_id
	savedSealLanguage                     // seal_language
	savedSealRegion                       // seal_region
	savedSealRequestShape                 // seal_request_shape
	savedSealObservation                  // seal_observation_digest
	savedSealOwnerGeneration              // seal_owner_generation
	savedSealBuild                        // seal_build_id
	savedSealBuildGeneration              // seal_build_generation
	savedSealContentRevision              // seal_content_revision
	savedSealAlgorithm                    // seal_algorithm
	savedSealCommitment                   // seal_commitment
	savedSealVerifiedRows                 // seal_verified_rows
	savedSealCanonicalBytes               // seal_canonical_bytes
	savedSealRetainedBytes                // seal_retained_bytes
	savedSealUpstreamOwner                // upstream_evidence_owner
	savedSealUpstreamIncarnation          // upstream_evidence_incarnation
	savedSealUpstreamSequence             // upstream_evidence_sequence
	savedSealDependencyOwner              // source_dependency_owner
	savedSealDependencyIncarnation        // source_dependency_incarnation
	savedSealDependencySequence           // source_dependency_sequence
	savedSealDependencyPublication        // source_dependency_publication_id
	savedSealDependencyProvider           // source_dependency_provider
	savedSealColumnCount
)

// Immutable decoded history only. A valid digest supplies neither a current
// native proof nor disclosure, source, lease, custody or publication authority.
type savedSealReceipt struct {
	identity     receiptIdentity
	kind         CommandID
	resultDigest ResultDigest
	facts        sealResultFacts
}

// decodeSavedSealReceipt accepts exactly one complete saved-native projection:
// TEXT is string, INTEGER is int64, BLOB is []byte, SQL NULL is nil. It never
// coerces types or substitutes a zero value for a required NULL or missing field.
// The caller must first classify the bounded header using classifyReceipt and
// must not call this on a public-floor Retired result. Private pinned lookup is
// separately authorized by its caller. Before payload decode/copy, the caller
// reserves the exact native octet lengths and checks copied lengths in the same
// qualified snapshot. Its saved header/owner/identity/kind must match this output.
// No current mutable body/head values are accepted as historical result facts.
func decodeSavedSealReceipt(values []any) (savedSealReceipt, error) {
	if len(values) != savedSealColumnCount {
		return savedSealReceipt{}, fmt.Errorf("%w: invalid saved seal receipt column count", ErrResolveRequired)
	}
	n := savedSealNativeRow{values: values}
	var out savedSealReceipt
	switch n.text(savedSealKind) {
	case "evidence_seal":
		out.kind = EvidenceSeal
	case "credit_source_seal":
		out.kind = CreditSourceSeal
	default:
		return savedSealReceipt{}, fmt.Errorf("%w: foreign saved seal receipt purpose", ErrResolveRequired)
	}
	// These two purposes cannot carry a source-dependent effective/accept result.
	// Check every raw nullable field, even if other coupled fields are absent.
	for _, column := range []int{savedSealDependencyOwner, savedSealDependencyIncarnation, savedSealDependencySequence, savedSealDependencyPublication, savedSealDependencyProvider} {
		if values[column] != nil {
			return savedSealReceipt{}, fmt.Errorf("%w: foreign saved seal receipt dependency", ErrResolveRequired)
		}
	}
	out.identity.command.Sequence = n.integer(savedSealSequence)
	copy(out.identity.command.Incarnation[:], n.blob(savedSealIncarnation, 16))
	copy(out.identity.command.OperationID[:], n.blob(savedSealOperation, 16))
	copy(out.identity.digest[:], n.blob(savedSealRequest, 32))
	copy(out.resultDigest[:], n.blob(savedSealResultDigest, 32))
	out.facts = sealResultFacts{
		ProviderOperationID:     n.text(savedSealOwner),
		EntityID:                n.integer(savedSealEntity),
		ObservedCatalogRevision: n.integer(savedSealAcceptedRevision),
		AppliedMS:               n.integer(savedSealAppliedMS),
		AfterItem:               n.text(savedSealAfterItem),
		NextOrdinal:             n.integer(savedSealAfterOrdinal),
		PublicationID:           n.optionalText(savedSealPublication),
		ImageID:                 n.optionalInteger(savedSealImage),
		ImageRevision:           n.optionalInteger(savedSealImageRevision),
		ResultCount:             n.integer(savedSealResultCount),
		// Neither seal purpose has a member-result cohort. The closed result
		// preflight below requires native result_count=0 and image fields NULL.
		MemberCohortLength: 0,
		SealVersion:        n.integer(savedSealVersion),
		EvidenceID:         n.text(savedSealEvidence),
		SubjectID:          n.integer(savedSealSubject),
		ParserVersion:      n.text(savedSealParser),
		Provider:           n.text(savedSealProvider),
		SubjectKind:        n.text(savedSealSubjectKind),
		ProviderID:         n.text(savedSealProviderID),
		Language:           n.text(savedSealLanguage),
		Region:             n.text(savedSealRegion),
		RequestShape:       n.text(savedSealRequestShape),
		ObservationDigest:  n.text(savedSealObservation),
		OwnerGeneration:    n.integer(savedSealOwnerGeneration),
		BuildID:            n.optionalText(savedSealBuild),
		BuildGeneration:    n.optionalInteger(savedSealBuildGeneration),
		ContentRevision:    n.integer(savedSealContentRevision),
		Algorithm:          n.text(savedSealAlgorithm),
		VerifiedRows:       n.integer(savedSealVerifiedRows),
		CanonicalBytes:     n.integer(savedSealCanonicalBytes),
		RetainedBytes:      n.integer(savedSealRetainedBytes),
		UpstreamOwner:      n.optionalText(savedSealUpstreamOwner),
		UpstreamSequence:   n.optionalInteger(savedSealUpstreamSequence),
	}
	copy(out.facts.Commitment[:], n.blob(savedSealCommitment, 32))
	if values[savedSealUpstreamIncarnation] != nil {
		out.facts.UpstreamIncarnation.Present = true
		copy(out.facts.UpstreamIncarnation.Value[:], n.blob(savedSealUpstreamIncarnation, 16))
	}
	if n.err != nil {
		return savedSealReceipt{}, n.err
	}
	// The existing closed result codec owns all purpose shape, coupled NULL,
	// namespace, byte/range and complete canonical-wire validation. No alternate
	// codec, classifier, observation digest or normalized commitment is used.
	var digest ResultDigest
	var err error
	switch out.kind {
	case EvidenceSeal:
		digest, err = canonicalResultDigest(out.identity.command, out.identity.digest, evidenceSealResult{out.facts})
	case CreditSourceSeal:
		digest, err = canonicalResultDigest(out.identity.command, out.identity.digest, creditSourceSealResult{out.facts})
	}
	if err != nil {
		return savedSealReceipt{}, errors.Join(ErrResolveRequired, err)
	}
	if digest != out.resultDigest {
		return savedSealReceipt{}, fmt.Errorf("%w: saved seal receipt result digest mismatch", ErrResolveRequired)
	}
	return out, nil
}

// Native type decoding only; it performs no SQL, allocation of payload bytes,
// canonical encoding, receipt classification or capability construction.
type savedSealNativeRow struct {
	values []any
	err    error
}

func (n *savedSealNativeRow) malformed(column int) {
	if n.err == nil {
		n.err = fmt.Errorf("%w: invalid saved seal receipt native column %d", ErrResolveRequired, column)
	}
}

func (n *savedSealNativeRow) text(column int) string {
	v, ok := n.values[column].(string)
	if !ok {
		n.malformed(column)
	}
	return v
}

func (n *savedSealNativeRow) integer(column int) int64 {
	v, ok := n.values[column].(int64)
	if !ok {
		n.malformed(column)
	}
	return v
}

func (n *savedSealNativeRow) blob(column, width int) []byte {
	v, ok := n.values[column].([]byte)
	if !ok || len(v) != width {
		n.malformed(column)
		return nil
	}
	return v
}

func (n *savedSealNativeRow) optionalText(column int) EvidenceText {
	if n.values[column] == nil {
		return EvidenceText{}
	}
	return EvidenceText{Value: n.text(column), Present: true}
}

func (n *savedSealNativeRow) optionalInteger(column int) EvidenceInteger {
	if n.values[column] == nil {
		return EvidenceInteger{}
	}
	return EvidenceInteger{Value: n.integer(column), Present: true}
}
