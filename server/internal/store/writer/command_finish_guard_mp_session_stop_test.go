package writer

func init() {
	registerCommandFinishGuard(commandFinishGuardFamily{name: "mp-session-stop", resultTypes: []string{"mpSessionStopAuthorization", "mpSessionStopValue"}, names: []string{"requireMPSessionParentDrained", "authorizeMPSessionStop", "authorizeMPSessionExpiry", "mpSessionStopTime", "applyMPSessionStop", "nextMPSessionAccountStop", "nextMPSessionExpiry"}, bypasses: []string{`func requireMPSessionParentDrained(ctx context.Context)(err error){return nil}`}})
}
