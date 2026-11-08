package writer

import (
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"testing"
	"testing/synctest"
)

// Values-only frozen FINAL source projection. It neither supplies native proof
// nor represents the accepting domain's image/current-authority verification.
func enrichAcceptSourceInput(t *testing.T, begin *effectiveStageBeginCommand) *enrichAcceptCommand {
	t.Helper()
	input := &enrichAcceptCommand{valid: true, originalBegin: begin, identity: begin.identity}
	input.identity.Sequence++
	input.identity.OperationID[0] ^= 0x80
	input.request = enrichAcceptRequest{variant: "accept", beginIdentity: begin.identity, beginRequest: begin.digest, owner: begin.request.owner, entity: begin.request.entity, producer: begin.request.producer, rows: []effectiveCreditTuple{}}
	input.request.owner.entityID = begin.request.entity.id
	input.request.owner.incarnation = input.identity.Incarnation
	var err error
	input.digest, err = canonicalRequestDigest(EnrichAccept, 1, input.identity, input.request)
	if err != nil {
		t.Fatal(err)
	}
	return input
}

func TestEnrichAcceptSourceInputBindsDistinctFinalOriginal(t *testing.T) {
	begin, _, _, _, _ := effectiveLineageValueFixture()
	begin.identity.Sequence = 12
	begin.digest[0] = 1
	input := enrichAcceptSourceInput(t, begin)
	if err := validateEnrichAcceptSourceInput(input); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*enrichAcceptCommand){
		func(c *enrichAcceptCommand) { c.identity.Sequence = begin.identity.Sequence },
		func(c *enrichAcceptCommand) { c.request.beginRequest[0] ^= 1 },
		func(c *enrichAcceptCommand) { c.request.beginIdentity.OperationID[0] ^= 1 },
		func(c *enrichAcceptCommand) { c.request.owner.provider = "different" },
		func(c *enrichAcceptCommand) { c.request.entity.incarnation[0] ^= 1 },
		func(c *enrichAcceptCommand) { c.request.producer.Evidence.Digest = "different" },
		func(c *enrichAcceptCommand) { c.digest = begin.digest },
	} {
		c := *input
		change(&c)
		if err := validateEnrichAcceptSourceInput(&c); err == nil {
			t.Fatal("FINAL accepted a different original source projection")
		}
	}
}

func TestEnrichAcceptSourceNativeUsesFinalGrantAndRequest(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w, f := nativeEnrichmentSealsFixture(t)
		begin := effectiveNativeLineageProjection(t, f)
		input := enrichAcceptSourceInput(t, begin)
		turn, a := effectiveSourceNativeTurn(t, w, commandMemoryBudget{8 << 20, 8 << 20}, EnrichAccept)
		proof, err := verifyEnrichAcceptSource(t.Context(), turn, input)
		if err != nil || proof == nil {
			t.Fatal("actual source lineage failed under FINAL grant", err)
		}
		if turn.transaction.descriptor.ID != EnrichAccept {
			t.Fatal("FINAL borrowed BEGIN descriptor")
		}
		if err := checkEnrichAcceptVerifiedSource(t.Context(), turn, input, proof); err != nil {
			t.Fatal(err)
		}
		copy := *input
		if err := checkEnrichAcceptVerifiedSource(t.Context(), turn, &copy, proof); !errors.Is(err, ErrInvalid) {
			t.Fatal("copied FINAL borrowed proof", err)
		}
		input.digest[0] ^= 1
		if err := checkEnrichAcceptVerifiedSource(t.Context(), turn, input, proof); !errors.Is(err, ErrInvalid) {
			t.Fatal("changed FINAL digest borrowed proof", err)
		}
		input.digest[0] ^= 1
		if _, err := verifyEffectiveSource(t.Context(), turn, begin); !errors.Is(err, ErrInvalid) {
			t.Fatal("BEGIN wrapper admitted FINAL grant", err)
		}
		if err := checkEffectiveVerifiedSource(t.Context(), turn, begin, proof.native); !errors.Is(err, ErrInvalid) {
			t.Fatal("BEGIN checker admitted FINAL proof", err)
		}
		if _, err := turn.transaction.rollback(nil); err != nil {
			t.Fatal(err)
		}
		if err := checkEnrichAcceptVerifiedSource(t.Context(), turn, input, proof); err == nil {
			t.Fatal("retired FINAL retained proof")
		}
		if a.Usage() != ([4]governor.Amounts{}) {
			t.Fatal("FINAL proof retained grant")
		}
	})
}

func TestEffectiveSourceGrantKeyBindsTurnJobToOriginalOperation(t *testing.T) {
	input := &effectiveStageBeginCommand{request: effectiveStageBeginRequest{ownerID: "operation-one"}}
	turnFor := func(purpose CommandID, custodyPurpose CommandID, domain CommandID, id string) *commandTurn {
		return &commandTurn{transaction: &transaction{descriptor: Descriptor{ID: purpose}, custody: &governedTurn{purpose: custodyPurpose,
			expected: governor.Request{Key: governor.JobKey{Domain: uint16(domain), ID: id}}}}}
	}
	for _, purpose := range []CommandID{EffectiveStage, EnrichAccept} {
		if err := requireEffectiveSourceGrantKey(turnFor(purpose, purpose, purpose, "operation-one"), input); err != nil {
			t.Fatal("same-job grant refused", purpose, err)
		}
		for name, turn := range map[string]*commandTurn{
			"other_operation": turnFor(purpose, purpose, purpose, "operation-two"),
			"other_domain":    turnFor(purpose, purpose, BasicAccept, "operation-one"),
			"other_custody":   turnFor(purpose, BasicAccept, purpose, "operation-one"),
			"ungoverned":      {transaction: &transaction{descriptor: Descriptor{ID: purpose}}},
		} {
			if err := requireEffectiveSourceGrantKey(turn, input); !errors.Is(err, ErrInvalid) {
				t.Fatal("grant for another job admitted source proof", purpose, name, err)
			}
		}
	}
	if err := requireEffectiveSourceGrantKey(turnFor(EffectiveStage, EffectiveStage, EffectiveStage, ""), &effectiveStageBeginCommand{}); !errors.Is(err, ErrInvalid) {
		t.Fatal("empty original operation matched an empty grant key", err)
	}
}
