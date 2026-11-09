package writer

// IA-17 is its own release family: the accepting body and its historical replay
// carry caller-lifetime results even though the current-authority entry finishes
// internally. The shared AST guard must cover both by type name.
func init() {
	registerCommandFinishGuard(commandFinishGuardFamily{
		name:        "ia_reconcile_object",
		resultTypes: []string{"iaReconcileAcceptOutcome", "iaReconcileObjectHistory"},
		bypasses: []string{
			`func acceptIAReconcileObject(ctx context.Context)(out iaReconcileAcceptOutcome,err error){return out,nil}`,
			`func lookupIAReconcileObject(ctx context.Context)(out iaReconcileObjectHistory,err error){return out,nil}`,
		},
	})
}
