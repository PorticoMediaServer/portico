package writer

func init() {
	registerCommandFinishGuard(commandFinishGuardFamily{
		name: "ia_entry", resultTypes: []string{"iaCommandEntry", "iaEntryAuthorization"},
		names: []string{"checkFreshSource", "recheck"}, bypasses: []string{
			`func enterIACommand(ctx context.Context)(out *iaCommandEntry,err error){return out,nil}`,
			`func authorizeIAEntryOwner(ctx context.Context)(out *iaEntryAuthorization,err error){return out,nil}`,
		},
	})
}
