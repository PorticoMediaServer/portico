package writer

// Values-only validation after the protected native readers and the existing
// saved-result decoder. This function neither reads SQL nor mints the private
// same-turn verification. It never classifies an ACKed dependency as Applied.
func effectiveSourceLineageMatches(c *effectiveStageBeginCommand, evidence effectiveEvidenceBodyPin, publication effectivePublicationBodyPin, e, s effectivePinnedSealReceipt) error {
	if c == nil || !c.valid || c.sourceReferences == nil {
		return &Refusal{Code: RefusalUnqualified}
	}
	refs := *c.sourceReferences
	if err := validateEffectiveSourceReferences(refs); err != nil {
		return err
	}
	for _, v := range []struct {
		want effectiveBeginSealReference
		got  effectivePinnedSealReceipt
	}{{refs.evidence, e}, {refs.source, s}} {
		if v.got.saved.kind != v.want.purpose || v.got.saved.facts.ProviderOperationID != v.want.owner ||
			v.got.saved.identity != (receiptIdentity{v.want.receipt.Identity, v.want.receipt.RequestDigest}) ||
			v.got.saved.resultDigest != v.want.result || v.got.saved.facts.SealVersion != int64(v.want.version) {
			return ErrResolveRequired
		}
		// A reclaim wake is not permission or proof invalidation. Actual live
		// pin custody and an untouched cleanup cursor are required separately.
		if v.got.proof.revision <= 0 || v.got.proof.pinCount <= 0 || v.got.proof.cleanupAfterSequence != 0 {
			return ErrResolveRequired
		}
	}
	ekey := effectiveSourceSealRef{refs.evidence.owner, refs.evidence.receipt.Identity.Incarnation, refs.evidence.receipt.Identity.Sequence, EvidenceSeal}
	skey := effectiveSourceSealRef{refs.source.owner, refs.source.receipt.Identity.Incarnation, refs.source.receipt.Identity.Sequence, CreditSourceSeal}
	r, ef, sf := c.request, e.saved.facts, s.saved.facts
	p := r.producer.Evidence
	if refs.source.owner != r.ownerID || refs.source.receipt.Identity.Incarnation != c.identity.Incarnation ||
		refs.source.receipt.Identity.Sequence > r.owner.acknowledgedSequence {
		return ErrConflict
	}
	if evidence.ref != ekey || publication.ref != skey || evidence.evidence != r.evidenceID || publication.publication != r.publicationID ||
		evidence.subject != ef.SubjectID || evidence.parser != ef.ParserVersion || evidence.provider != ef.Provider ||
		publication.entity != r.entity.id || publication.count != sf.VerifiedRows || publication.provider != sf.Provider {
		return ErrConflict
	}
	if sf.ProviderOperationID != r.ownerID || sf.EntityID != r.entity.id || sf.PublicationID != (EvidenceText{Value: r.publicationID, Present: true}) ||
		ef.EvidenceID != r.evidenceID || sf.EvidenceID != ef.EvidenceID || sf.SubjectID != ef.SubjectID || sf.ParserVersion != ef.ParserVersion ||
		sf.Provider != ef.Provider || sf.SubjectKind != ef.SubjectKind || sf.ProviderID != ef.ProviderID || sf.Language != ef.Language ||
		sf.Region != ef.Region || sf.RequestShape != ef.RequestShape || sf.ObservationDigest != ef.ObservationDigest ||
		sf.UpstreamOwner != (EvidenceText{Value: ekey.owner, Present: true}) ||
		sf.UpstreamIncarnation != (sealOptionalIncarnation{Value: ekey.incarnation, Present: true}) ||
		sf.UpstreamSequence != (EvidenceInteger{Value: ekey.sequence, Present: true}) || ef.VerifiedRows != sf.VerifiedRows+1 {
		return ErrConflict
	}
	// E's original catalogue entity/owner/generation may be foreign: global
	// evidence reuse keeps that immutable provenance. The current source and
	// current Effective owner are independently bound above and by the caller.
	if ef.SubjectID != p.SubjectID || ef.Provider != p.Provider || ef.SubjectKind != p.Kind || ef.ProviderID != p.ID ||
		ef.Language != p.Language || ef.Region != p.Region || ef.RequestShape != p.Shape || ef.ObservationDigest != p.Digest ||
		sf.Provider != r.owner.provider || sf.Commitment != r.header.sourceManifest {
		return ErrConflict
	}
	return nil
}
