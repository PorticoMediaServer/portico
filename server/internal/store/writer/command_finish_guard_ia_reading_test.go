package writer

// IA-15 is its own release family: the typed reading outcome and the historical
// replay carry caller-lifetime results even though the current-authority entry
// finishes internally. The shared AST guard must cover both by type name.
func init() {
	registerCommandFinishGuard(commandFinishGuardFamily{
		name:        "ia_reading",
		resultTypes: []string{"iaReadingAcceptOutcome", "iaReadingHistory"},
		bypasses: []string{
			`func acceptIAReadingInventory(ctx context.Context)(out iaReadingAcceptOutcome,err error){return out,nil}`,
			`func lookupIAReadingInventory(ctx context.Context)(out iaReadingHistory,err error){return out,nil}`,
		},
	})
}
