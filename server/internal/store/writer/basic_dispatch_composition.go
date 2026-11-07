package writer

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

func basicCommandDefinitions(purpose CommandID) []stmt.Definition {
	if purpose != BasicStage && purpose != BasicSeal {
		return nil
	}
	all := append(inventoryDisclosureDefinitions(), basicReceiptDefinitions()...)
	all = append(all, scanReceiptDefinitions()...)
	all = append(all, basicControlDefinitions()...)
	all = append(all, basicCommandStateDefinitions()...)
	all = append(all, basicStreamStageDefinitions()...)
	defs := make([]stmt.Definition, 0, len(all))
	for _, d := range all {
		if basicCommandMaxUses(purpose, d.ID) > 0 {
			defs = append(defs, d)
		}
	}
	return defs
}
func basicCommandMaxUses(purpose CommandID, id stmt.ID) int {
	if purpose != BasicStage && purpose != BasicSeal {
		return 0
	}
	switch id {
	case inventoryDisclosureJob, inventoryDisclosureLibrary, scanReceiptOwner, scanReceiptAck, scanReceiptReclaimKey,
		basicReceiptScope, basicReceiptIdentity, basicReceiptHeaderLengths, basicReceiptHeader,
		basicControlRun, basicControlSource, basicControlActive, basicControlObject, basicControlLibrary, basicControlPolicy, basicControlJob, basicControlAsset, basicControlPostState, basicControlRunCAS, basicControlReceipt,
		basicStageHeader, basicStageAsset, basicStageWitness, basicStageFamily:
		return 1
	case basicControlChange:
		if purpose == BasicStage {
			return 2
		}
		return 1
	}
	if purpose == BasicStage {
		switch id {
		case basicStageBudget, basicStageBudgetCAS, basicStageStreamInsert, basicStageFamilyCAS:
			return 1
		}
	}
	if purpose == BasicSeal {
		switch id {
		case basicSealStreamFact, basicSealFamilyCAS:
			return 1
		}
	}
	return 0
}
func basicStageCommandDefinitions(in basicStageRequest) []stmt.Definition {
	if _, err := basicStageSource(in); err != nil {
		return nil
	}
	return basicCommandDefinitions(BasicStage)
}
func basicSealCommandDefinitions(in basicSealRequest) []stmt.Definition {
	if in.purpose != BasicPurposeSeal || in.operation != basicSealStreamInput {
		return nil
	}
	return basicCommandDefinitions(BasicSeal)
}

func basicControlCommandDefinitions() []stmt.Definition {
	defs := append(basicControlDefinitions(), basicCommandStateDefinitions()...)
	return append(defs, basicStreamStageDefinitions()...)
}
