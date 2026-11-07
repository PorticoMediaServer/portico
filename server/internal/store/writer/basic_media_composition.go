package writer

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

// Exact finite subsets for a frozen closed operation. These are domain use
// inventories, never a mutable worker profile, registration or admission grant.
func basicStageCommandDefinitions(in basicStageRequest) []stmt.Definition {
	defs := basicControlCommandDefinitions()
	if in.operation == basicStageMediaInput {
		defs = append(defs, basicMediaDefinitions()...)
	}
	if in.operation == basicStageProbeInput || in.operation == basicStagePinShapeInput {
		defs = append(defs, basicProbeDefinitions()...)
	}
	if in.operation == basicStagePinShapeInput {
		defs = append(defs, basicShapeDefinitions()...)
		for _, def := range basicStreamAcceptDefinitions() {
			if def.ID == basicAcceptFamilies {
				defs = append(defs, def)
			}
		}
	}
	filtered := make([]stmt.Definition, 0, len(defs))
	for _, d := range defs {
		if basicStageRequestMaxUses(in, d.ID) > 0 {
			filtered = append(filtered, d)
		}
	}
	return filtered
}

func basicSealCommandDefinitions(in basicSealRequest) []stmt.Definition {
	defs := basicControlCommandDefinitions()
	if in.operation == basicSealMediaInput {
		defs = append(defs, basicMediaDefinitions()...)
	}
	filtered := make([]stmt.Definition, 0, len(defs))
	for _, d := range defs {
		if basicSealRequestMaxUses(in, d.ID) > 0 {
			filtered = append(filtered, d)
		}
	}
	return filtered
}

func basicStageRequestMaxUses(in basicStageRequest, id stmt.ID) int {
	if in.purpose != BasicPurposeStage {
		return 0
	}
	if in.operation == basicStageProbeInput || in.operation == basicStagePinShapeInput {
		if uses := basicControlCommonMaxUses(id); uses != 0 {
			return uses
		}
		switch id {
		case basicControlChange:
			if in.operation == basicStagePinShapeInput {
				return 1
			}
			return 2
		case basicStageHeader, basicStageAsset, basicStageWitness, basicStageBudget, basicStageBudgetCAS:
			return 1
		}
		if in.operation == basicStagePinShapeInput {
			switch id {
			case basicProbeHeader, basicProbeAsset, basicShapeLookup, basicShapeInsert, basicShapeRefCAS, basicShapeHeaderCAS, basicAcceptFamilies:
				return 1
			}
			return 0
		}
		return basicProbeMaxUses(id)
	}
	if in.operation != basicStageMediaInput {
		return basicStageCommandMaxUses(in.operation, id)
	}
	if in.purpose != BasicPurposeStage || in.start < 0 || in.end <= in.start || in.end-in.start > 8 || in.end > basicRowsCount(in.rows) {
		return 0
	}
	if _, err := basicRowsRequestBytes(in.fact, in.rows); err != nil {
		return 0
	}
	if uses := basicControlCommonMaxUses(id); uses != 0 {
		return uses
	}
	switch id {
	case basicControlChange, basicStageHeader:
		return 2 // before/post revision proof; source header + family-mask proof
	case basicStageAsset, basicStageWitness, basicStageFamily, basicStageBudget, basicStageBudgetCAS, basicStageFamilyCAS:
		return 1
	}
	if basicMediaInsertFamily(id) == in.rows.family {
		return int(in.end - in.start)
	}
	return 0
}

func basicSealRequestMaxUses(in basicSealRequest, id stmt.ID) int {
	if in.purpose != BasicPurposeSeal {
		return 0
	}
	if in.operation != basicSealMediaInput {
		return basicSealCommandMaxUses(in.operation, id)
	}
	if in.purpose != BasicPurposeSeal {
		return 0
	}
	if _, err := basicRowsRequestBytes(in.source, in.rows); err != nil {
		return 0
	}
	if uses := basicControlCommonMaxUses(id); uses != 0 {
		return uses
	}
	switch id {
	case basicStageHeader:
		return 2
	case basicControlChange, basicStageAsset, basicStageWitness, basicStageFamily, basicSealFamilyCAS:
		return 1
	}
	if id == basicMediaRowsID(in.rows.family) {
		return 1
	} // COMPLETE family LIMIT+1
	return 0
}

func basicMediaInsertFamily(id stmt.ID) int64 {
	switch id {
	case basicMediaStreamInsert:
		return 2
	case basicMediaBreadthInsert:
		return 3
	case basicMediaChapterFactInsert:
		return 4
	case basicMediaChapterInsert:
		return 5
	case basicMediaSubtitleFactInsert:
		return 6
	case basicMediaSidecarInsert:
		return 7
	case basicMediaSubtitleInsert:
		return 8
	case basicMediaEvidenceInsert:
		return 9
	default:
		return 0
	}
}
