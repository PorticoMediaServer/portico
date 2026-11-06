package writer

import (
	"database/sql"
	"math"
)

// PrepareEffectiveStageBegin freezes the complete PRE-allocation request using
// the existing writer constructor and canonical codec. It performs no SQL,
// admission, disclosure, receipt resolution or source-proof verification.
// Successful preparation does not permit fresh execution: W must independently
// verify typed stage/seal provenance bound to this SAME complete request digest.
func PrepareEffectiveStageBegin(identity CommandIdentity, input EffectiveStageBeginInput) (EffectiveStageBeginCommand, error) {
	if !effectivePrepareNullableShape(input) {
		return nil, invalid("effective_begin_nullable_input")
	}
	command, err := newEffectiveStageBeginCommand(identity, effectivePrepareRequest(input))
	if err != nil {
		return nil, err
	}
	return command, nil
}

// EffectiveStageBeginReference returns the copied ORIGINAL identity/digest for
// ResolveEffective/AckEffective. No marker is invoked and no request is rebuilt.
// Possessing this reference is not disclosure or source/receipt authority.
func EffectiveStageBeginReference(command EffectiveStageBeginCommand) (ReceiptReference, error) {
	prepared, ok := command.(*effectiveStageBeginCommand)
	if !ok || prepared == nil || !prepared.valid {
		return ReceiptReference{}, invalid("effective_begin_command")
	}
	return ReceiptReference{Identity: prepared.identity, RequestDigest: prepared.digest}, nil
}

func effectivePrepareRequest(in EffectiveStageBeginInput) effectiveStageBeginRequest {
	o, e, h := in.Operation, in.Entity, in.Image
	return effectiveStageBeginRequest{
		variant: "begin", ownerID: in.ProviderOperationID, evidenceID: in.EvidenceID,
		publicationID: in.PublicationID, objectID: in.ObjectID, producer: in.Producer,
		owner: effectiveBeginOwner{
			receiptOwnerFacts: receiptOwnerFacts{o.Exists, o.CommandIncarnation, o.LastCommandSequence, o.AcknowledgedCommandSequence},
			entityID:          o.EntityID, generation: o.Generation, provider: o.Provider, phase: o.Phase, status: o.Status,
			lease: o.Lease, leaseUntil: o.LeaseUntil, entityIncarnation: o.EntityIncarnation, policyIncarnation: o.PolicyIncarnation,
			sourceRevision: o.SourceRevision, identityRevision: o.IdentityRevision, metadataRevision: o.MetadataRevision,
			hierarchyRevision: o.HierarchyRevision, selectionRevision: o.SelectionRevision, descriptiveRevision: o.DescriptiveRevision,
			policyRevision: o.PolicyRevision, consentRevision: o.ConsentRevision, algorithm: o.Algorithm, refreshMode: o.RefreshMode,
			inputDigest: o.InputDigest, selectedID: o.SelectedID, page: o.Page, afterItem: o.AfterItem, imageID: o.CreditImageID,
			queryTitle: o.QueryTitle, queryArtist: o.QueryArtist, queryDigest: o.QueryDigest, queryYear: o.QueryYear,
			manual: o.Manual, reviewSearch: o.ReviewSearch, hintID: o.HintID, hintProblem: o.HintProblem,
			qualification: o.Qualification, episodeOrder: o.EpisodeOrder, setID: o.SetID, setSubjectKind: o.SetSubjectKind, setProviderID: o.SetProviderID,
		},
		entity: effectiveBeginEntity{
			id: e.ID, libraryID: e.LibraryID, kind: e.Kind, retired: e.Retired, revision: e.Revision, incarnation: e.MetadataIncarnation,
			sourceRevision: e.SourceRevision, identityRevision: e.IdentityRevision, metadataRevision: e.MetadataRevision,
			language: e.Language, ownerRevision: e.OwnerRevision, ownerLocked: e.OwnerLocked, imageID: e.CreditImageID,
			tvIncarnation:    effectiveBeginOptionalIncarnation{e.TVIncarnation.Value, e.TVIncarnation.Present},
			tvSourceRevision: e.TVSourceRevision, tvHierarchyRevision: e.TVHierarchyRevision,
			tvSelectionRevision: e.TVSelectionRevision, tvDescriptiveRevision: e.TVDescriptiveRevision,
		},
		header: effectiveBeginHeader{
			entityID: h.EntityID, entityIncarnation: h.EntityIncarnation, sourceRevision: h.SourceRevision,
			identityRevision: h.IdentityRevision, metadataRevision: h.MetadataRevision, ownerRevision: h.OwnerRevision,
			policyIncarnation: h.PolicyIncarnation, policyRevision: h.PolicyRevision, consentRevision: h.ConsentRevision,
			language: h.Language, sourceManifest: h.SourceManifest, declaredInput: h.DeclaredInput,
			expectedManifest: h.ExpectedManifest, expectedCount: h.ExpectedCount, expectedBytes: h.ExpectedBytes,
		},
	}
}

// Native readers produce zero payloads for NULL. Refuse inconsistent caller
// projections instead of silently dropping bytes or hashing two forms of NULL.
func effectivePrepareNullableShape(in EffectiveStageBeginInput) bool {
	o, e, p := in.Operation, in.Entity, in.Producer
	for _, value := range []sql.NullInt64{o.CreditImageID, e.CreditImageID, e.TVSourceRevision, e.TVHierarchyRevision, e.TVSelectionRevision, e.TVDescriptiveRevision, p.Association.CUE} {
		if !value.Valid && value.Int64 != 0 {
			return false
		}
	}
	for _, value := range []sql.NullString{o.SetID, o.SetSubjectKind, o.SetProviderID, p.Selection.Evidence, p.Selection.Set, p.Selection.Publication, p.Selection.Track, p.Asset.InfoOwner, p.Asset.InfoRevision} {
		if !value.Valid && value.String != "" {
			return false
		}
	}
	for _, value := range []sql.NullFloat64{p.Capture.End, p.Association.End} {
		if !value.Valid && math.Float64bits(value.Float64) != 0 {
			return false
		}
	}
	return (e.TVIncarnation.Present || e.TVIncarnation.Value == [16]byte{}) &&
		(p.CreditHead.Exists || (p.CreditHead.PublicationID == "" && p.CreditHead.Revision == 0))
}
