package writer

func init() {
	registerCommandFinishGuard(commandFinishGuardFamily{
		name: "ip_entity_evidence",
		resultTypes: []string{
			"ipEntityEvidenceCommandResult", "ipEntityEvidenceCommand",
			"ipEntityEvidenceResult", "ipEntityEvidenceBuild",
		},
		bypasses: []string{
			`func applyIPEntityEvidenceBegin(ctx context.Context)(out ipEntityEvidenceCommandResult,err error){return out,nil}`,
			`func applyIPEntityEvidenceAppend(ctx context.Context)(out ipEntityEvidenceCommandResult,err error){return out,nil}`,
		},
	})
}
