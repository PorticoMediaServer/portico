package writer

// IA-12 is its own release family: the typed music outcome and the historical
// replay carry caller-lifetime results even though the current-authority entry
// finishes internally. The shared AST guard must cover both by type name.
func init() {
	registerCommandFinishGuard(commandFinishGuardFamily{
		name:        "ia_music",
		resultTypes: []string{"iaMusicAcceptOutcome", "iaMusicHistory"},
		bypasses: []string{
			`func acceptIAMusicInventory(ctx context.Context)(out iaMusicAcceptOutcome,err error){return out,nil}`,
			`func lookupIAMusicInventory(ctx context.Context)(out iaMusicHistory,err error){return out,nil}`,
		},
	})
}
