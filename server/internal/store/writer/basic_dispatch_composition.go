package writer

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

func basicCommandDefinitions(purpose CommandID) []stmt.Definition {
	if purpose != BasicStage && purpose != BasicSeal {
		return nil
	}
	defs := append(inventoryDisclosureDefinitions(), basicReceiptDefinitions()...)
	for _, def := range scanReceiptDefinitions() {
		switch def.ID {
		case scanReceiptOwner, scanReceiptAck, scanReceiptReclaimKey:
			defs = append(defs, def)
		}
	}
	return append(defs, basicEffectsDefinitions(purpose)...)
}

func basicCommandMaxUses(purpose CommandID, id stmt.ID) int {
	if purpose != BasicStage && purpose != BasicSeal {
		return 0
	}
	if uses := basicEffectsMaxUses(purpose, id); uses != 0 {
		return uses
	}
	switch id {
	case inventoryDisclosureJob, inventoryDisclosureLibrary, scanReceiptOwner, scanReceiptAck, scanReceiptReclaimKey,
		basicReceiptScope, basicReceiptIdentity, basicReceiptHeaderLengths, basicReceiptHeader:
		return 1
	}
	return 0 // no reclaim/header delete, lifecycle, Inventory or other variant
}

// One immutable profile per closed purpose reserves the maximum uses over its
// implemented operation variants. It cannot be supplied by a worker/request.
// Numerical Work still comes from trusted composition and real Capture proof.
func basicEffectsDefinitions(purpose CommandID) []stmt.Definition {
	if purpose != BasicStage && purpose != BasicSeal {
		return nil
	}
	all := append(basicControlCommandDefinitions(), basicMediaDefinitions()...)
	if purpose == BasicStage {
		all = append(all, basicRetireFirstDefinitions()...)
		all = append(all, basicRetirePrefixDefinitions()...)
		all = append(all, basicReclaimMediaDefinitions()...)
		all = append(all, basicProbeDefinitions()...)
		all = append(all, basicShapeDefinitions()...)
		all = append(all, basicCopyRecipeDefinitions()...)
	}
	defs := make([]stmt.Definition, 0, len(all))
	for _, def := range all {
		if basicEffectsMaxUses(purpose, def.ID) > 0 {
			defs = append(defs, def)
		}
	}
	return defs
}

func basicEffectsMaxUses(purpose CommandID, id stmt.ID) int {
	if purpose != BasicStage && purpose != BasicSeal {
		return 0
	}
	if uses := basicControlCommonMaxUses(id); uses != 0 {
		return uses
	}
	if purpose == BasicStage {
		if uses := basicRetireFirstMaxUses(id); uses != 0 {
			return uses
		}
		if uses := basicRetirePrefixMaxUses(id); uses != 0 {
			return uses
		}
		if id == basicReclaimMediaHeader {
			return 1
		}
		if id == basicReclaimMediaSubtitleEmbedded {
			return 8
		}
		for family := int64(2); family <= 9; family++ {
			if id == basicReclaimMediaID(family) {
				return 8
			}
		}
		if uses := basicCopyStageMaxUses(id); uses != 0 {
			return uses
		}
		if uses := basicProbeMaxUses(id); uses != 0 {
			return uses
		}
		switch id {
		case basicControlChange, basicStageHeader:
			return 2
		case basicShapeLookup, basicShapeInsert, basicShapeRefCAS, basicShapeHeaderCAS, basicAcceptFamilies:
			return 1
		case basicBeginFamiliesInsert:
			return 9
		case basicStageAsset, basicStageWitness, basicStageFamily, basicStageBudget, basicStageBudgetCAS,
			basicStageStreamInsert, basicStageFamilyCAS, basicBeginExisting, basicBeginChangeInsert, basicBeginWitnessInsert, basicBeginFirstAsset, basicBeginFirstOwners, basicBeginFirstEntityAssets:
			return 1
		}
		if basicMediaInsertFamily(id) != 0 {
			return 8 // bounded raw prefix; never a full-family INSERT loop
		}
		return 0
	}
	switch id {
	case basicStageHeader:
		return 2
	case basicControlChange, basicStageAsset, basicStageWitness, basicStageFamily, basicSealStreamFact, basicSealFamilyCAS:
		return 1
	}
	for family := int64(2); family <= 9; family++ {
		if id == basicMediaRowsID(family) {
			return 1 // one COMPLETE bounded family, including its overflow row
		}
	}
	return 0
}
