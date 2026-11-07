package writer

import (
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

func TestBasicControlCompositionFiniteOperationUses(t *testing.T) {
	// Columns are BEGIN, StageFact and SealStream. This is the finite domain
	// inventory, not W admission, authority/history/ACK or terminal work.
	want := map[stmt.ID][3]int{
		basicControlRun:             {1, 1, 1},
		basicControlSource:          {1, 1, 1},
		basicControlActive:          {1, 1, 1},
		basicControlObject:          {1, 1, 1},
		basicControlLibrary:         {1, 1, 1},
		basicControlPolicy:          {1, 1, 1},
		basicControlJob:             {1, 1, 1},
		basicControlAsset:           {1, 1, 1},
		basicControlPostState:       {1, 1, 1},
		basicControlRunCAS:          {1, 1, 1},
		basicControlReceipt:         {1, 1, 1},
		basicControlChange:          {1, 2, 1}, // StageFact checks before and after the effect.
		basicStageHeader:            {0, 1, 1},
		basicStageAsset:             {1, 1, 1},
		basicStageWitness:           {0, 1, 1},
		basicStageFamily:            {0, 1, 1},
		basicStageBudget:            {1, 1, 0},
		basicStageBudgetCAS:         {1, 1, 0},
		basicStageStreamInsert:      {0, 1, 0},
		basicStageFamilyCAS:         {0, 1, 0},
		basicSealStreamFact:         {0, 0, 1},
		basicSealFamilyCAS:          {0, 0, 1},
		basicBeginExisting:          {1, 0, 0},
		basicBeginFirstAsset:        {1, 0, 0},
		basicBeginFirstOwners:       {1, 0, 0},
		basicBeginFirstEntityAssets: {1, 0, 0},
		basicBeginChangeInsert:      {1, 0, 0},
		basicBeginWitnessInsert:     {1, 0, 0},
		basicBeginFamiliesInsert:    {9, 0, 0}, // One literal insert for each of nine manifests.
	}
	defs := basicControlCommandDefinitions()
	if len(defs) != len(want) {
		t.Fatalf("composed %d definitions, want %d", len(defs), len(want))
	}
	seen := make(map[stmt.ID]bool, len(defs))
	for _, def := range defs {
		expected, ok := want[def.ID]
		if !ok || seen[def.ID] {
			t.Fatalf("unsupported or duplicate composition ID %q", def.ID)
		}
		seen[def.ID] = true
		if def.SQL == "" || (def.Kind != stmt.Read && def.Kind != stmt.Write) {
			t.Fatalf("composition ID %q lost its executable definition", def.ID)
		}
		got := [3]int{basicStageCommandMaxUses(basicStageBeginInput, def.ID), basicStageCommandMaxUses(basicStageFactInput, def.ID), basicSealCommandMaxUses(basicSealStreamInput, def.ID)}
		if got != expected {
			t.Errorf("%s uses BEGIN/StageFact/SealStream = %v, want %v", def.ID, got, expected)
		}
	}
	for id := range want {
		if !seen[id] {
			t.Errorf("missing finite composition ID %q", id)
		}
	}

	for _, id := range []stmt.ID{"", "scan.basic_stage.unsupported", basicProbeOldPresence} {
		if got := [3]int{basicStageCommandMaxUses(basicStageBeginInput, id), basicStageCommandMaxUses(basicStageFactInput, id), basicSealCommandMaxUses(basicSealStreamInput, id)}; got != ([3]int{}) {
			t.Errorf("unsupported ID %q acquired uses %v", id, got)
		}
	}
	// Even common run/receipt statements confer no uses on unknown operations.
	for _, def := range defs {
		for _, operation := range []basicStageOperation{0, 255} {
			if got := basicStageCommandMaxUses(operation, def.ID); got != 0 {
				t.Errorf("Stage operation %d acquired %d uses of %s", operation, got, def.ID)
			}
		}
		for _, operation := range []basicSealOperation{0, 255} {
			if got := basicSealCommandMaxUses(operation, def.ID); got != 0 {
				t.Errorf("Seal operation %d acquired %d uses of %s", operation, got, def.ID)
			}
		}
	}
}
