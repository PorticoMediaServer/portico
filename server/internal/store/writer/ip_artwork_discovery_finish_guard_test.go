package writer

func init() {
	registerCommandFinishGuard(commandFinishGuardFamily{
		name: "ip_artwork_discovery",
		resultTypes: []string{
			"ipArtworkDiscoveryCommandResult", "ipArtworkDiscoverySavedResult", "ipArtworkDiscoveryOutcome",
		},
		names: []string{
			"ipArtworkDiscoveryLookup", "ipArtworkDiscoveryFresh", "ipArtworkDiscoveryConsent",
			"ipArtworkDiscoveryStageRead", "ipArtworkDiscoveryKeys", "ipArtworkDiscoveryDecode",
			"ipArtworkDiscoveryClaim", "ipArtworkDiscoveryContinue", "ipArtworkDiscoveryPageBody",
			"ipArtworkDiscoveryRecord",
		},
		bypasses: []string{
			`func applyIPArtworkDiscovery(ctx context.Context)(out ipArtworkDiscoveryCommandResult,err error){return out,nil}`,
			`func ipArtworkDiscoveryLookup(ctx context.Context)(out ipArtworkDiscoveryOutcome,err error){return out,nil}`,
		},
	})
}
