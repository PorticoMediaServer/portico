package writer

// IA-13 is its own release family: the typed audiobook outcome and the
// historical replay carry caller-lifetime results even though the
// current-authority entry finishes internally. The shared AST guard must cover
// both by type name.
func init() {
	registerCommandFinishGuard(commandFinishGuardFamily{
		name:        "ia_audiobook",
		resultTypes: []string{"iaBookAcceptOutcome", "iaBookHistory"},
		bypasses: []string{
			`func acceptIABookInventory(ctx context.Context)(out iaBookAcceptOutcome,err error){return out,nil}`,
			`func lookupIABookInventory(ctx context.Context)(out iaBookHistory,err error){return out,nil}`,
		},
	})
}
