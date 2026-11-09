package writer

func init() {
	registerCommandFinishGuard(commandFinishGuardFamily{
		name: "ip_artwork_material",
		resultTypes: []string{
			"ipArtworkMaterialCommandResult", "ipArtworkMaterialSavedResult", "ipArtworkMaterialOutcome",
		},
		names: []string{
			"ipArtworkMaterialLookup", "ipArtworkMaterialFreshSource", "ipArtworkMaterialFresh",
			"ipArtworkMaterialIntentMatch", "ipArtworkMaterialManifest", "ipArtworkMaterialSlotKeys",
			"ipArtworkMaterialSlotRead", "ipArtworkMaterialDeclare", "ipArtworkMaterialInstall",
			"ipArtworkMaterialRecord", "ipArtworkMaterialJobDue", "ipArtworkMaterialClaimJob",
			"ipArtworkMaterialJobLive", "ipArtworkMaterialCompleteJob",
		},
		bypasses: []string{
			`func applyIPArtworkMaterial(ctx context.Context)(out ipArtworkMaterialCommandResult,err error){return out,nil}`,
			`func ipArtworkMaterialManifest(ctx context.Context)(total int64,err error){return 0,nil}`,
		},
	})
}
