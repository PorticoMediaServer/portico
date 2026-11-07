package writer

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

// Complete conservative use union of the closed bounded copy operations.
// No worker-supplied IDs, Work amounts or invented physical budgets enter it.
func basicCopyStageMaxUses(id stmt.ID) int {
	switch id {
	case basicStageHeader, basicStageAsset, basicStageWitness, basicProbeAsset:
		return 2
	case basicAcceptOwners, basicAcceptEntity, basicAcceptSubject, basicAcceptVersions, basicAcceptStreams, basicAcceptBreadth:
		return 2
	case basicCleanupStreamRetained, basicCleanupStreamDelete, basicCleanupBreadthRetained, basicCleanupBreadthClear, basicCleanupChapterRetained, basicCleanupChapterDelete, basicCleanupSubtitleGapAfter, basicCleanupSubtitleExact, basicCleanupSubtitleExactEmbedded, basicCleanupSubtitleRetained, basicCleanupSubtitleDelete, basicCleanupSubtitleDeleteEmbedded, basicSubtitleLanguageRead, basicSubtitleLanguageCAS, basicSubtitleLanguageDelete:
		return 8
	case basicAcceptTargets, basicAcceptFamilies, basicAcceptSuccessor, basicAcceptAssetShape, basicAcceptShape, basicAcceptFreeze, basicPrefenceHeader, basicPrefenceAssetCAS, basicPrefenceLinkCAS, basicPrefenceChangeCAS, basicCopyFactScope, basicCopyFactPresent, basicCopyFactInsert, basicCopyFactUpdate, basicCopyStreamScope, basicCopyStreamStaged, basicCopyStreamPresent, basicCopyStreamInsert, basicCopyStreamUpdate, basicCopyBreadthStaged, basicCopyBreadthParent, basicCopyBreadthInsert, basicCopyBreadthUpdate, basicCopyChapterScope, basicCopyChapterStaged, basicCopyChapterPresent, basicCopyChapterInsert, basicCopyChapterUpdate, basicCopyChapterFactScope, basicCopyChapterFactStaged, basicCopyChapterFactPresent, basicCopyChapterFactInsert, basicCopyChapterFactUpdate, basicCopySubtitleEvidenceScope, basicCopySubtitleEvidenceStaged, basicCopySubtitleEvidencePresent, basicCopySubtitleEvidenceInsert, basicCopySubtitleEvidenceUpdate, basicCopySubtitleFactScope, basicCopySubtitleFactStaged, basicCopySubtitleFactPresent, basicCopySubtitleFactInsert, basicCopySubtitleFactUpdate, basicCopySubtitleUpdateEmbedded, basicCopySidecarStaged, basicCopySidecarID, basicCopySidecarKey, basicCopySidecarInsert, basicCopySidecarUpdate, basicCopySubtitleStaged, basicCopySubtitleID, basicCopySubtitleKey, basicCopySubtitleInsert, basicCopySubtitleUpdate, basicCopySubtitleSourceSidecar, basicCopySubtitleSourceStream, basicSubtitleLanguageInsert, basicCleanupBreadthFirst, basicCleanupBreadthAfter, basicCleanupChapterFirst, basicCleanupChapterAfter, basicCleanupStreamFirst, basicCleanupStreamAfter, basicCopyPositionCAS, basicCleanupSubtitleGapFirst, basicCleanupSubtitleMoreFirst, basicCleanupSubtitleMoreAfter:
		return 1
	}
	return 0
}
func basicCopyRecipeDefinitions() []stmt.Definition {
	var defs []stmt.Definition
	for _, group := range [][]stmt.Definition{basicStreamAcceptDefinitions(), basicPrefenceDefinitions(), basicCopyFactDefinitions(), basicCopyStreamDefinitions(), basicCopyChapterDefinitions(), basicCopyMediaFactsDefinitions(), basicCopySubtitleFactDefinitions(), basicCopySubtitlesDefinitions(), basicSubtitleLanguageDefinitions(), basicCopyCleanupMediaDefinitions(), basicCopyStreamCleanupDefinitions(), basicCopyStepDefinitions(), basicCopySubtitleCleanupDefinitions()} {
		defs = append(defs, group...)
	}
	return defs
}

func basicStageSealRowBound(id stmt.ID) uint64 {
	switch id {
	case basicBeginFirstEntityAssets, basicStageWitness, basicAcceptOwners:
		return 2
	case basicAcceptVersions:
		return MaxVersions + 1
	case basicAcceptStreams, basicAcceptBreadth:
		return 3*MaxStreamsPerClass + 1
	case basicAcceptFamilies:
		return 19
	case basicCleanupStreamFirst, basicCleanupStreamAfter, basicCleanupBreadthFirst, basicCleanupBreadthAfter, basicCleanupChapterFirst, basicCleanupChapterAfter:
		return 9
	}
	for family := int64(2); family <= 9; family++ {
		if id == basicMediaRowsID(family) {
			return uint64(basicMediaFamilyLimit(family) + 1)
		}
	}
	return 1
}
