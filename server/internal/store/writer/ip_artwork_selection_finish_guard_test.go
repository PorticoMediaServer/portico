package writer

func init() {
	registerCommandFinishGuard(commandFinishGuardFamily{
		name: "ip_artwork_selection",
		resultTypes: []string{
			"ipArtworkSelectionCommandResult", "ipArtworkSelectionSavedResult",
			"ipArtworkSelectionOutcome", "ipArtworkSelectionResult",
		},
		names: []string{
			"ipArtworkSelectionLookup", "ipArtworkSelectionRead", "ipArtworkSelectionSource",
			"ipArtworkSelectionApply", "ipArtworkSelectionHistory", "ipArtworkSelectionRecord",
		},
		bypasses: []string{
			`func applyIPArtworkSelection(ctx context.Context)(out ipArtworkSelectionCommandResult,err error){return out,nil}`,
			`func ipArtworkSelectionApply(ctx context.Context)(out ipArtworkSelectionResult,err error){return out,nil}`,
		},
	})
}
