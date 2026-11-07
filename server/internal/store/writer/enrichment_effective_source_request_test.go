package writer

import (
	"strings"
	"testing"
)

func effectiveSourceReferenceInput() effectiveBeginSourceReferences {
	return effectiveBeginSourceReferences{
		evidence: effectiveBeginSealReference{owner: "original-evidence-owner", purpose: EvidenceSeal, version: 1, receipt: ReceiptReference{Identity: CommandIdentity{Sequence: 7}}},
		source:   effectiveBeginSealReference{owner: "current-source-owner", purpose: CreditSourceSeal, version: 1, receipt: ReceiptReference{Identity: CommandIdentity{Sequence: 11}}},
	}
}

func TestEffectiveSourceReferencesKeepDistinctPurposes(t *testing.T) {
	want := effectiveSourceReferenceInput()
	if err := validateEffectiveSourceReferences(want); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*effectiveBeginSourceReferences){
		func(v *effectiveBeginSourceReferences) { v.evidence.purpose = EvidenceStage },
		func(v *effectiveBeginSourceReferences) { v.source.purpose = CreditStage },
		func(v *effectiveBeginSourceReferences) { v.source.version++ },
		func(v *effectiveBeginSourceReferences) { v.evidence.receipt.Identity.Sequence = 0 },
	} {
		v := want
		change(&v)
		if err := validateEffectiveSourceReferences(v); err == nil {
			t.Fatal("stage alias or malformed native seal reference accepted")
		}
	}
}

func TestEffectiveSourceReferenceByteCapPrecedesPayloadWork(t *testing.T) {
	v := effectiveSourceReferenceInput()
	v.evidence.owner = strings.Repeat("x", MaxSealControlStringBytes+1)
	if err := validateEffectiveSourceReferences(v); err == nil {
		t.Fatal("oversized owner became a native proof reference")
	}
}

func TestEffectiveNativeSourceRequestBindsBothOriginals(t *testing.T) {
	i := CommandIdentity{Sequence: 12}
	packet := effectiveNativeSourceRequest{variant: "begin_native_seal_refs_v1", seals: effectiveSourceReferenceInput()}
	want, err := canonicalRequestDigest(EffectiveStage, effectiveNativeSourceRequestVersion, i, packet)
	if err != nil {
		t.Fatal(err)
	}
	// Codec sensitivity only: the zero begin projection is not a valid command
	// and no native lineage/authority is claimed by this pure input test.
	for _, change := range []func(*effectiveNativeSourceRequest){
		func(v *effectiveNativeSourceRequest) { v.seals.evidence.receipt.RequestDigest[0] ^= 1 },
		func(v *effectiveNativeSourceRequest) { v.seals.source.receipt.Identity.OperationID[0] ^= 1 },
		func(v *effectiveNativeSourceRequest) { v.seals.source.result[0] ^= 1 },
	} {
		v := packet
		change(&v)
		got, err := canonicalRequestDigest(EffectiveStage, effectiveNativeSourceRequestVersion, i, v)
		if err != nil || got == want {
			t.Fatal("exact original seal identity/commitment escaped request digest", got, err)
		}
	}
}
