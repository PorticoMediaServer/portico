package writer

// IA-11 is its own release family: the typed episode outcome and the historical
// replay carry caller-lifetime results even though the current-authority entry
// finishes internally. The shared AST guard must cover both by type name.
func init() {
	registerCommandFinishGuard(commandFinishGuardFamily{
		name:        "ia_episode",
		resultTypes: []string{"iaEpisodeAcceptOutcome", "iaEpisodeHistory"},
		bypasses: []string{
			`func acceptIAEpisodeInventory(ctx context.Context)(out iaEpisodeAcceptOutcome,err error){return out,nil}`,
			`func lookupIAEpisodeInventory(ctx context.Context)(out iaEpisodeHistory,err error){return out,nil}`,
		},
	})
}
