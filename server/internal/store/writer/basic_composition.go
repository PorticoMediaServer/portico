package writer

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

// Finite domain inventory only. W adds current authority/history/ACK and the
// physical terminal on its sole dispatcher. No numerical Work is invented here.
func basicControlCommandDefinitions() []stmt.Definition {
	defs := append(basicControlDefinitions(), basicCommandStateDefinitions()...)
	defs = append(defs, basicStreamStageDefinitions()...)
	return append(defs, basicBeginDefinitions()...)
}

func basicStageCommandMaxUses(operation basicStageOperation, id stmt.ID) int {
	if operation == basicStageRetireInput {
		if uses := basicRetireFirstMaxUses(id); uses != 0 {
			return uses
		}
		if uses := basicRetirePrefixMaxUses(id); uses != 0 {
			return uses
		}
		if id == basicReclaimMediaHeader || id == basicStageFamily || id == basicStageBudget || id == basicStageBudgetCAS {
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
		if id == basicControlObject || id == basicControlAsset {
			return 0
		}
		return basicControlCommonMaxUses(id)
	}
	if operation != basicStageBeginInput && operation != basicStageFactInput {
		return 0
	}
	if n := basicControlCommonMaxUses(id); n != 0 {
		return n
	}
	if operation == basicStageBeginInput {
		switch id {
		case basicControlChange, basicStageAsset, basicStageBudget, basicStageBudgetCAS, basicBeginExisting, basicBeginChangeInsert, basicBeginWitnessInsert, basicBeginFirstAsset, basicBeginFirstOwners, basicBeginFirstEntityAssets:
			return 1
		case basicBeginFamiliesInsert:
			return 9
		}
	} else {
		switch id {
		case basicControlChange:
			return 2 // before effect and bounded native post-cursor proof
		case basicStageHeader, basicStageAsset, basicStageWitness, basicStageFamily, basicStageBudget, basicStageBudgetCAS, basicStageStreamInsert, basicStageFamilyCAS:
			return 1
		}
	}
	return 0
}

func basicSealCommandMaxUses(operation basicSealOperation, id stmt.ID) int {
	if operation != basicSealStreamInput {
		return 0
	}
	if n := basicControlCommonMaxUses(id); n != 0 {
		return n
	}
	switch id {
	case basicControlChange, basicStageHeader, basicStageAsset, basicStageWitness, basicStageFamily, basicSealStreamFact, basicSealFamilyCAS:
		return 1
	}
	return 0
}

func basicControlCommonMaxUses(id stmt.ID) int {
	switch id {
	case basicControlRun, basicControlSource, basicControlActive, basicControlObject, basicControlLibrary, basicControlPolicy, basicControlJob, basicControlAsset,
		basicControlPostState, basicControlRunCAS, basicControlReceipt:
		return 1
	}
	return 0
}
