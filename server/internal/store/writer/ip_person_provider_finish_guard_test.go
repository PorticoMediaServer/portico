package writer

func init() {
	registerCommandFinishGuard(commandFinishGuardFamily{
		name: "ip_person_provider",
		resultTypes: []string{
			"ipPersonCommandResult", "ipPersonCommand", "ipPersonLookup", "ipPersonSealCommand",
			"ipPersonSealCommandResult", "ipPersonSealEvidence",
			"ipPersonOwner", "ipPersonReceipt", "ipPersonBuild", "ipPersonRecoveryResult",
		},
		bypasses: []string{
			`func applyIPPersonClaim(ctx context.Context)(out ipPersonCommandResult,err error){return out,nil}`,
			`func applyIPPersonRenew(ctx context.Context)(out ipPersonCommandResult,err error){return out,nil}`,
			`func applyIPPersonFinish(ctx context.Context)(out ipPersonCommandResult,err error){return out,nil}`,
			`func applyIPPersonEvidenceBegin(ctx context.Context)(out ipPersonCommandResult,err error){return out,nil}`,
			`func applyIPPersonEvidenceAppend(ctx context.Context)(out ipPersonCommandResult,err error){return out,nil}`,
			`func applyIPPersonSeal(ctx context.Context)(out ipPersonSealCommandResult,err error){return out,nil}`,
		},
	})
}
