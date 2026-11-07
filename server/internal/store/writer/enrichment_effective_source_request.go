package writer

import "strings"

// Native body pins alone store the compact owner/incarnation/sequence key.
// The original production request also freezes the complete immutable receipt
// identity and result commitment. Rebinding a reused body ID to a later seal
// must not silently change which dependency this original meant.
type effectiveBeginSealReference struct {
	owner   string
	purpose CommandID
	version uint16
	receipt ReceiptReference
	result  ResultDigest
}

type effectiveBeginSourceReferences struct {
	evidence effectiveBeginSealReference
	source   effectiveBeginSealReference
}

const effectiveNativeSourceRequestVersion uint16 = 2

type effectiveNativeSourceRequest struct {
	variant string
	begin   effectiveStageBeginRequest
	seals   effectiveBeginSourceReferences
}

func validateEffectiveSourceReferences(in effectiveBeginSourceReferences) error {
	for _, v := range []struct {
		ref  effectiveBeginSealReference
		kind CommandID
	}{{in.evidence, EvidenceSeal}, {in.source, CreditSourceSeal}} {
		if v.ref.purpose != v.kind || v.ref.version != 1 || !nonblank(v.ref.owner) || v.ref.receipt.Identity.Sequence <= 0 {
			return invalid("effective_source_reference")
		}
		// This bounds only this occurrence. The complete native proof's owner,
		// captured rows, pins and receipt occurrences are checked separately.
		if _, err := CheckSealControlStrings([]string{v.ref.owner}); err != nil {
			return err
		}
	}
	return nil
}

// A frozen request constructor only: no native read, grant, verified flag or
// authority is issued. The production dispatcher must separately call W's
// same-request/same-turn native lineage verifier before any fresh effect.
// This is intentionally not a silent extension of Effective BEGIN v1.
func newEffectiveStageBeginFromSeals(identity CommandIdentity, in effectiveStageBeginRequest, refs effectiveBeginSourceReferences) (*effectiveStageBeginCommand, error) {
	if err := validateEffectiveSourceReferences(refs); err != nil {
		return nil, err
	}
	if refs.source.owner != in.ownerID || refs.source.receipt.Identity.Incarnation != identity.Incarnation ||
		refs.source.receipt.Identity.Sequence > in.owner.acknowledgedSequence {
		return nil, invalid("effective_source_owner")
	}
	// Check the COMPLETE typed v2 envelope before the legacy constructor owns
	// begin strings or this constructor clones reference owners. The caller
	// exclusively owns these borrowed input values for this synchronous call.
	packet := effectiveNativeSourceRequest{"begin_native_seal_refs_v1", in, refs}
	digest, err := canonicalRequestDigest(EffectiveStage, effectiveNativeSourceRequestVersion, identity, packet)
	if err != nil {
		return nil, err
	}
	c, err := newEffectiveStageBeginCommand(identity, in)
	if err != nil {
		return nil, err
	}
	refs.evidence.owner, refs.source.owner = strings.Clone(refs.evidence.owner), strings.Clone(refs.source.owner)
	c.digest, c.sourceReferences = digest, &refs
	return c, nil
}
