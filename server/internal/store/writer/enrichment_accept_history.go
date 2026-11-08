package writer

// decodeEnrichAcceptReceipt reads the closed ENRICH ACCEPT result back from the
// ONE immutable receipt projection. It never reconstructs a result from current
// heads, the mutable owner row or a caller-supplied value. Command kind, exact
// identity, coupled NULL shape and W's canonical result digest must all agree.
func decodeEnrichAcceptReceipt(values []any, q EnrichmentReceiptRequest, found receiptFacts) (*enrichAcceptResult, error) {
	if len(values) != savedSealColumnCount || !found.exists || found.kind != EnrichAccept ||
		found.identity.command != q.Receipt.Identity || found.identity.digest != q.Receipt.RequestDigest {
		return nil, ErrConflict
	}
	n := savedSealNativeRow{values: values}
	out := enrichAcceptResult{
		ProviderOperationID:     n.text(savedSealOwner),
		EntityID:                n.integer(savedSealEntity),
		AcceptedCatalogRevision: n.integer(savedSealAcceptedRevision),
		AppliedMS:               n.integer(savedSealAppliedMS),
		AfterItem:               n.text(savedSealAfterItem),
		ImageID:                 n.integer(savedSealImage),
		ImageRevision:           n.integer(savedSealImageRevision),
		RetainedCount:           n.integer(savedSealResultCount),
	}
	savedDigest := n.blob(savedSealResultDigest, 32)
	if n.err != nil {
		return nil, ErrResolveRequired
	}
	// An accept row publishes provider credits, never a source publication, and
	// carries no seal-only fields. A seal row relabeled as accept would fail here
	// before any of its retained TEXT bytes became a result.
	if values[savedSealPublication] != nil || values[savedSealVersion] != nil || values[savedSealEvidence] != nil ||
		values[savedSealSubject] != nil || values[savedSealParser] != nil || values[savedSealAlgorithm] != nil ||
		values[savedSealVerifiedRows] != nil || values[savedSealCanonicalBytes] != nil || values[savedSealRetainedBytes] != nil {
		return nil, ErrResolveRequired
	}
	if out.ProviderOperationID != q.ProviderOperationID || out.EntityID != q.EntityID || found.resultCount != out.RetainedCount {
		return nil, ErrConflict
	}
	if err := preflightEnrichAcceptResult(out); err != nil {
		return nil, ErrResolveRequired
	}
	digest, err := enrichAcceptResultDigest(q.Receipt.Identity, q.Receipt.RequestDigest, out)
	if err != nil || [32]byte(digest) != found.resultDigest {
		return nil, ErrConflict
	}
	for i := range digest {
		if digest[i] != savedDigest[i] {
			return nil, ErrConflict
		}
	}
	return &out, nil
}
