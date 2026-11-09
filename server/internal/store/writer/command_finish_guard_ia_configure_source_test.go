package writer

// IA-07 is its own release family: the typed configure-source outcome and the
// historical replay carry caller-lifetime results even though the IA-05 entry
// finishes internally. The shared AST guard must cover both by type name.
func init() {
	registerCommandFinishGuard(commandFinishGuardFamily{
		name:        "ia_configure_source",
		resultTypes: []string{"iaConfigureSourceOutcome", "iaConfigureSourceHistory"},
		bypasses: []string{
			`func configureIASource(ctx context.Context)(out iaConfigureSourceOutcome,err error){return out,nil}`,
			`func lookupIAConfigureSource(ctx context.Context)(out iaConfigureSourceHistory,err error){return out,nil}`,
		},
	})
}
