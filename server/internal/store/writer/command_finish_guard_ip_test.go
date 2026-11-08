package writer

func init() {
	registerCommandFinishGuard(commandFinishGuardFamily{
		name: "ip_entry", resultTypes: []string{"ipCommandEntry", "ipEntryAuthorization"}, names: []string{"checkFreshSubject", "checkFreshProvider"},
		bypasses: []string{`func enterIPCommand(ctx context.Context)(out *ipCommandEntry,err error){return out,nil}`, `func authorizeIPEntryOwner(ctx context.Context)(out *ipEntryAuthorization,err error){return out,nil}`},
	})
}
