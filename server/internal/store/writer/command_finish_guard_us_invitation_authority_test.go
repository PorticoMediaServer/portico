package writer

func init() {
	registerCommandFinishGuard(commandFinishGuardFamily{name: "us_invitation_authority", resultTypes: []string{"usInvitationAuthorization"}, names: []string{"Recheck"}, bypasses: []string{
		`func authorizeUSInvitationManager(ctx context.Context)(out *usInvitationAuthorization,err error){return out,nil}`,
		`func(a *usInvitationAuthorization) Recheck(ctx context.Context)(err error){return nil}`,
	}})
}
