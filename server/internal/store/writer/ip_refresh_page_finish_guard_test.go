package writer

func init() {
	registerCommandFinishGuard(commandFinishGuardFamily{
		name: "ip_refresh_page",
		resultTypes: []string{
			"ipRefreshPageCommandResult", "ipRefreshPageLookup", "ipRefreshPageCommand",
			"ipRefreshPageOwner", "ipRefreshPageCandidate", "ipRefreshPageResult",
		},
		prefixes: []string{"ipRefreshPage"},
	})
}
