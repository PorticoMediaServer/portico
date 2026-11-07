package writer

import (
	"errors"
	"testing"
)

// Native-value matching only: these synthetic structs deliberately do not
// supply SQL, decoder provenance, a turn/grant or a verification capability.
func effectiveLineageValueFixture() (*effectiveStageBeginCommand, effectiveEvidenceBodyPin, effectivePublicationBodyPin, effectivePinnedSealReceipt, effectivePinnedSealReceipt) {
	refs := effectiveSourceReferenceInput()
	refs.evidence.receipt.Identity.Incarnation[0] = 1
	refs.source.receipt.Identity.Incarnation[0] = 2
	refs.evidence.result[0], refs.source.result[0] = 3, 4
	c := &effectiveStageBeginCommand{valid: true, sourceReferences: &refs}
	c.identity.Incarnation = refs.source.receipt.Identity.Incarnation
	c.request.ownerID, c.request.evidenceID, c.request.publicationID = refs.source.owner, "native-evidence", "00000000000000000000000000000001"
	c.request.entity.id = 41
	c.request.owner.provider, c.request.owner.acknowledgedSequence, c.request.owner.generation = "tmdb", 11, 9
	c.request.producer.Evidence = effectiveBeginEvidence{SubjectID: 197, Provider: "tmdb", Kind: "movie", ID: "812", Language: "en", Shape: "shape", Digest: "observation"}
	c.request.header.sourceManifest[0] = 7
	ef := sealResultFacts{ProviderOperationID: refs.evidence.owner, EntityID: 13, SealVersion: 1, EvidenceID: c.request.evidenceID,
		SubjectID: 197, ParserVersion: "parser", Provider: "tmdb", SubjectKind: "movie", ProviderID: "812", Language: "en", RequestShape: "shape", ObservationDigest: "observation", OwnerGeneration: 5, VerifiedRows: 1}
	sf := ef
	sf.ProviderOperationID, sf.EntityID, sf.OwnerGeneration, sf.VerifiedRows = refs.source.owner, 41, 8, 0
	sf.PublicationID = EvidenceText{Value: c.request.publicationID, Present: true}
	sf.UpstreamOwner = EvidenceText{Value: refs.evidence.owner, Present: true}
	sf.UpstreamIncarnation = sealOptionalIncarnation{Value: refs.evidence.receipt.Identity.Incarnation, Present: true}
	sf.UpstreamSequence = EvidenceInteger{Value: refs.evidence.receipt.Identity.Sequence, Present: true}
	sf.Commitment = c.request.header.sourceManifest
	e := effectivePinnedSealReceipt{saved: savedSealReceipt{identity: receiptIdentity{refs.evidence.receipt.Identity, refs.evidence.receipt.RequestDigest}, kind: EvidenceSeal, resultDigest: refs.evidence.result, facts: ef}, proof: effectiveSealProofState{revision: 1, pinCount: 1}}
	s := effectivePinnedSealReceipt{saved: savedSealReceipt{identity: receiptIdentity{refs.source.receipt.Identity, refs.source.receipt.RequestDigest}, kind: CreditSourceSeal, resultDigest: refs.source.result, facts: sf}, proof: effectiveSealProofState{revision: 1, pinCount: 1}}
	ebody := effectiveEvidenceBodyPin{evidence: c.request.evidenceID, subject: 197, parser: "parser", provider: "tmdb", ref: effectiveSourceSealRef{refs.evidence.owner, refs.evidence.receipt.Identity.Incarnation, refs.evidence.receipt.Identity.Sequence, EvidenceSeal}}
	pub := effectivePublicationBodyPin{publication: c.request.publicationID, entity: 41, provider: "tmdb", ref: effectiveSourceSealRef{refs.source.owner, refs.source.receipt.Identity.Incarnation, refs.source.receipt.Identity.Sequence, CreditSourceSeal}}
	return c, ebody, pub, e, s
}

func TestEffectiveSourceLineageValuesPreserveForeignEvidence(t *testing.T) {
	c, eb, sb, e, s := effectiveLineageValueFixture()
	if err := effectiveSourceLineageMatches(c, eb, sb, e, s); err != nil {
		t.Fatal("historical E owner/entity or old source generation was rebased", err)
	}
	s.proof.reclaimDueMS = EvidenceInteger{Value: 1000, Present: true}
	if err := effectiveSourceLineageMatches(c, eb, sb, e, s); err != nil {
		t.Fatal("a reclaim wake replaced actual pin custody", err)
	}
	s.proof.pinCount = 0
	if err := effectiveSourceLineageMatches(c, eb, sb, e, s); !errors.Is(err, ErrResolveRequired) {
		t.Fatal("unpinned proof became a dependency", err)
	}
}

func TestEffectiveSourceLineageValuesBindProofAndManifest(t *testing.T) {
	for _, name := range []string{"original", "upstream", "body_pin", "manifest"} {
		t.Run(name, func(t *testing.T) {
			c, eb, sb, e, s := effectiveLineageValueFixture()
			switch name {
			case "original":
				e.saved.identity.digest[0] ^= 1
			case "upstream":
				s.saved.facts.UpstreamSequence.Value++
			case "body_pin":
				sb.ref.sequence++
			case "manifest":
				c.request.header.sourceManifest[0] ^= 1
			}
			if err := effectiveSourceLineageMatches(c, eb, sb, e, s); err == nil {
				t.Fatal("a different native proof supplied this original")
			}
		})
	}
}
