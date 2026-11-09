package writer

// IA-18 is its own release family: the accepting body and its historical replay
// carry caller-lifetime results even though the current-authority entry finishes
// internally. The shared AST guard must cover both by type name.
func init() {
	registerCommandFinishGuard(commandFinishGuardFamily{
		name:        "ia_coverage_verify",
		resultTypes: []string{"iaCoverageAcceptOutcome", "iaCoverageVerifyHistory"},
		bypasses: []string{
			`func acceptIACoverageVerify(ctx context.Context)(out iaCoverageAcceptOutcome,err error){return out,nil}`,
			`func lookupIACoverageVerify(ctx context.Context)(out iaCoverageVerifyHistory,err error){return out,nil}`,
		},
	})
}
