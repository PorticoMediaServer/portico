package writer

func init() {
	registerCommandFinishGuard(commandFinishGuardFamily{
		name: "ip_entity_job",
		resultTypes: []string{
			"ipEntityJobCommandResult", "ipEntityJobCommand", "ipEntityJobLookup",
			"ipEntityJobOwner", "ipEntityJobCandidate", "ipEntityJobReceipt",
			"ipEntityJobSubjectFacts", "ipEntityJobCurrentFacts", "ipEntityJobResult",
		},
		bypasses: []string{
			`func applyIPEntityClaim(ctx context.Context)(out ipEntityJobCommandResult,err error){return out,nil}`,
			`func applyIPEntityRenew(ctx context.Context)(out ipEntityJobCommandResult,err error){return out,nil}`,
			`func applyIPEntityFinish(ctx context.Context)(out ipEntityJobCommandResult,err error){return out,nil}`,
		},
	})
}
