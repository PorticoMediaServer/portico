package writer

// IA-06 is its own release family: the typed configuration outcome and the
// historical replay carry caller-lifetime results even though the IA-05 entry
// finishes internally. The shared AST guard must cover both by type name.
func init() {
	registerCommandFinishGuard(commandFinishGuardFamily{
		name:        "ia_configure",
		resultTypes: []string{"iaConfigureLibraryOutcome", "iaConfigureLibraryHistory"},
		bypasses: []string{
			`func configureIALibrary(ctx context.Context)(out iaConfigureLibraryOutcome,err error){return out,nil}`,
			`func lookupIAConfigureLibrary(ctx context.Context)(out iaConfigureLibraryHistory,err error){return out,nil}`,
		},
	})
}
