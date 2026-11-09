package writer

func init() {
	registerCommandFinishGuard(commandFinishGuardFamily{
		name: "ip_artwork_material_publish",
		resultTypes: []string{
			"ipArtworkMaterialPublishCommandResult", "ipArtworkMaterialPublishSavedResult",
			"ipArtworkMaterialPublishOutcome",
		},
		names: []string{
			"ipArtworkMaterialPublishLookup", "ipArtworkMaterialPublishFresh",
			"ipArtworkMaterialPublishIntent", "ipArtworkMaterialPublishBody",
			"ipArtworkMaterialPublishSourceFacts", "ipArtworkMaterialPublishRecord",
		},
		bypasses: []string{
			`func applyIPArtworkMaterialPublish(ctx context.Context)(out ipArtworkMaterialPublishCommandResult,err error){return out,nil}`,
			`func ipArtworkMaterialPublishIntent(ctx context.Context)(err error){return nil}`,
		},
	})
}
