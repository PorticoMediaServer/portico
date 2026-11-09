package writer

// IA-16 is its own release family: both accepting bodies and both historical
// replays carry caller-lifetime results even though the current-authority entry
// finishes internally. The shared AST guard must cover all four by type name.
func init() {
	registerCommandFinishGuard(commandFinishGuardFamily{
		name:        "ia_extra_membership",
		resultTypes: []string{"iaExtraAcceptOutcome", "iaExtraHistory", "iaMembershipAcceptOutcome", "iaMembershipHistory"},
		bypasses: []string{
			`func acceptIAExtraInventory(ctx context.Context)(out iaExtraAcceptOutcome,err error){return out,nil}`,
			`func lookupIAExtraInventory(ctx context.Context)(out iaExtraHistory,err error){return out,nil}`,
			`func acceptIAMembership(ctx context.Context)(out iaMembershipAcceptOutcome,err error){return out,nil}`,
			`func lookupIAMembership(ctx context.Context)(out iaMembershipHistory,err error){return out,nil}`,
		},
	})
}
