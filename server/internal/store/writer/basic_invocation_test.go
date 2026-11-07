package writer

import (
	"errors"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

func TestBasicInvocationIsClosedAndKeepsOriginalReference(t *testing.T) {
	q := BasicReceiptRequest{Purpose: BasicPurposeStage, Scan: ScanReceiptRequest{RunID: "saved-run", LibraryID: 7, LibraryKey: "library",
		Receipt: ReceiptReference{Identity: CommandIdentity{Sequence: 19}}}}
	command := &basicStageCommand{identity: q.Scan.Receipt.Identity, digest: q.Scan.Receipt.RequestDigest, valid: true,
		request: basicStageRequest{purpose: BasicPurposeStage, original: basicOriginalScope{scope: inventoryScope{runID: q.Scan.RunID, libraryID: 7, libraryKey: "library"}}}}
	good := basicInvocation{request: q, stage: command}
	if kind, err := good.validate(); err != nil || kind != BasicStage || !good.fresh() {
		t.Fatal(kind, err)
	}
	for _, in := range []basicInvocation{
		{request: BasicReceiptRequest{}},
		{request: BasicReceiptRequest{Purpose: BasicPurposeAccept}},
		{request: q, stage: &basicStageCommand{}},
		{request: q, stage: command, seal: &basicSealCommand{}},
		{request: BasicReceiptRequest{Purpose: BasicPurposeSeal, Scan: q.Scan}, stage: command},
		{request: func() BasicReceiptRequest { v := q; v.Scan.Receipt.RequestDigest[0]++; return v }(), stage: command},
	} {
		if _, err := in.validate(); !errors.Is(err, ErrInvalid) {
			t.Fatal("foreign/mutated invocation accepted", in, err)
		}
	}
	// Original history carries no current fresh lease or source observation.
	for _, purpose := range []BasicPurpose{BasicPurposeStage, BasicPurposeSeal, BasicPurposeAccept} {
		in := basicInvocation{request: BasicReceiptRequest{Scan: q.Scan, Purpose: purpose}}
		if _, err := in.validate(); err != nil || in.fresh() || in.leaseExpiresMS() != 0 {
			t.Fatal(in, err)
		}
	}
}

func TestBasicPurposeRecipesCoverEveryImplementedOperation(t *testing.T) {
	// Independent finite effect inventories: Stage's largest prefix and the
	// nine manifests must be reserved BEFORE BEGIN, not charged as one use.
	for _, purpose := range []CommandID{BasicStage, BasicSeal} {
		defs := basicEffectsDefinitions(purpose)
		seen := make(map[stmt.ID]bool, len(defs))
		for _, d := range defs {
			if seen[d.ID] || basicEffectsMaxUses(purpose, d.ID) == 0 {
				t.Fatal(purpose, d.ID)
			}
			seen[d.ID] = true
		}
		for _, d := range basicControlDefinitions() {
			if !seen[d.ID] {
				t.Fatal("missing common control effect", purpose, d.ID)
			}
		}
		if basicEffectsMaxUses(purpose, scanReceiptReclaimHeader) != 0 || basicEffectsMaxUses(purpose, scanInventoryObject) != 0 {
			t.Fatal("foreign effects entered Basic purpose")
		}
	}
	if basicEffectsMaxUses(BasicStage, basicBeginFamiliesInsert) != 9 || basicEffectsMaxUses(BasicStage, basicMediaChapterInsert) != 8 ||
		basicEffectsMaxUses(BasicSeal, basicBeginFamiliesInsert) != 0 || basicEffectsMaxUses(BasicSeal, basicMediaChapterInsert) != 0 ||
		basicEffectsMaxUses(BasicAccept, basicControlRunCAS) != 0 {
		t.Fatal("closed maximum-use inventory widened")
	}
}
