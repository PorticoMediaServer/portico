package writer

// US invitation create/edit/revoke (550-552) releases its complete result
// through finishCommand before the physical terminal, like every other US
// dispatcher: transaction.commit/rollback cancel the frame lifetime, so a
// lifetime-based deferred finish afterwards would report a committed command as
// canceled. The dispatcher keeps its explicit pre-terminal lifetime release and
// adds the shared dispatcher's final deferred publication on the CALLER's
// context; the helpers whose defers replace a denial (store/lookup/acknowledge)
// carry the same deferred-recheck shape the shared AST guard cannot express and
// are deliberately not selected. Every bypass below is a REAL unguarded release
// of this family's result type.
func init() {
	registerCommandFinishGuard(commandFinishGuardFamily{
		name:        "us_invitation_dispatch",
		resultTypes: []string{"usInvitationDispatchResult"},
		bypasses: []string{
			`func invitationDispatchBypass(ctx context.Context)(out usInvitationDispatchResult,err error){return out,nil}`,
			`func invitationCompletionBypass(ctx context.Context)(out usInvitationDispatchResult,err error){return out,nil}`,
		},
	})
}
