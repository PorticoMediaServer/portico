package writer

func init() {
	registerCommandFinishGuard(commandFinishGuardFamily{
		name:        "mg-personal",
		resultTypes: []string{"mgPersonalAcceptance", "mgPersonalRootAuthorization", "mgPersonalPreferenceResult", "mgPersonalReminderResult"},
		names:       []string{"mgPersonalRootObserve"},
		bypasses: []string{
			`func acceptMGPersonalPreference(ctx context.Context)(out mgPersonalAcceptance,err error){return out,nil}`,
			`func acceptMGPersonalReminder(ctx context.Context)(out mgPersonalAcceptance,err error){return out,nil}`,
			`func acceptMGPersonalReminderDelete(ctx context.Context)(out mgPersonalAcceptance,err error){return out,nil}`,
			`func authorizeUSCurrentSession(ctx context.Context)(out mgPersonalRootAuthorization,err error){return out,nil}`,
		},
	})
}
