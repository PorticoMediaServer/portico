package writer

// IA-19 is its own release family: both accepting bodies and their historical
// replays carry caller-lifetime results even though the current-authority entry
// finishes internally. The shared AST guard must cover both by type name.
func init() {
	registerCommandFinishGuard(commandFinishGuardFamily{
		name:        "ia_mark_missing",
		resultTypes: []string{"iaMissingAcceptOutcome", "iaMissingHistory"},
		bypasses: []string{
			`func acceptIAMissing(ctx context.Context)(out iaMissingAcceptOutcome,err error){return out,nil}`,
			`func lookupIAMissing(ctx context.Context)(out iaMissingHistory,err error){return out,nil}`,
		},
	})
	registerCommandFinishGuard(commandFinishGuardFamily{
		name:        "ia_finish_rescan",
		resultTypes: []string{"iaFinishRescanAcceptOutcome", "iaFinishRescanHistory"},
		bypasses: []string{
			`func acceptIAFinishRescan(ctx context.Context)(out iaFinishRescanAcceptOutcome,err error){return out,nil}`,
			`func lookupIAFinishRescan(ctx context.Context)(out iaFinishRescanHistory,err error){return out,nil}`,
		},
	})
}
