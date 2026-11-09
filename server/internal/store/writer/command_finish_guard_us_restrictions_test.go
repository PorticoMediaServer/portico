package writer

// US restrictions dispatch releases its results through finishCommand before the
// physical terminal, because transaction.commit/rollback cancel the frame
// lifetime: a lifetime-based deferred finish afterwards would report a
// committed command as canceled. runUSRestrictionsDispatch therefore keeps its
// explicit pre-terminal lifetime release and adds the shared dispatcher's
// final deferred publication on the CALLER's context, and its bound helpers
// are selected here.
// Every bypass below is a REAL unguarded release that this family's selectors
// must report exactly once.
func init() {
	registerCommandFinishGuard(commandFinishGuardFamily{
		name: "us_restrictions_dispatch",
		// The deferred-recheck helpers (lookupUSRestrictions, lookupUSRestrictionsCustody,
		// storeUSRestrictionsResult, acknowledgeUSRestrictionsAfterAuthority) run the
		// finish before their denial replacement, a shape the shared AST guard cannot
		// express; US-06/US-07 leave the identical shape unselected. The selectors below
		// cover every straight-line release and the complete result payloads.
		resultTypes: []string{
			"usRestrictionsSavedResult", "usRestrictionsEffect", "usRestrictionsBefore",
			"usRestrictionsQuotaFacts", "usRestrictionsOwnerFacts",
		},
		prefixes: []string{"readUSRestrictions"},
		names:    []string{"executeUSRestrictionsInvocation"},
		bypasses: []string{
			`func restrictionsBypass(ctx context.Context)(out usRestrictionsSavedResult,err error){return out,nil}`,
			`func effectBypass(ctx context.Context)(out usRestrictionsEffect,err error){return out,nil}`,
			`func readUSRestrictionsBypass(ctx context.Context)(value int64,err error){return 1,nil}`,
		},
	})
}
