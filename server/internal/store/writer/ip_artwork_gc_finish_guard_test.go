package writer

func init() {
	registerCommandFinishGuard(commandFinishGuardFamily{
		name: "ip_artwork_gc",
		resultTypes: []string{
			"ipArtworkGCCommandResult", "ipArtworkGCSavedResult", "ipArtworkGCOutcome", "ipArtworkGCResult",
		},
		prefixes: []string{"ipArtworkGC"},
		bypasses: []string{
			`func applyIPArtworkGC(ctx context.Context)(out ipArtworkGCCommandResult,err error){return out,nil}`,
			`func ipArtworkGCLookup(ctx context.Context)(out ipArtworkGCOutcome,err error){return out,nil}`,
		},
	})
}
