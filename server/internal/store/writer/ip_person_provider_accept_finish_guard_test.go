package writer

func init() {
	registerCommandFinishGuard(commandFinishGuardFamily{
		name: "ip_person_accept",
		resultTypes: []string{
			"ipPersonAcceptCommand", "ipPersonAcceptCommandResult",
			"ipPersonDeltaCommand", "ipPersonDeltaCommandResult",
		},
		names: []string{
			"applyIPPersonAccept", "applyIPPersonDelta",
			"ipPersonAcceptFresh", "ipPersonDeltaFresh",
			"ipPersonAcceptedLookupReceipt", "ipPersonSealedObservationRead",
			"ipPersonAcceptSearchRead", "ipPersonAcceptWritePublication",
			"ipPersonAcceptSourceWrite", "ipPersonAcceptPersonWrite",
			"ipPersonAcceptFactsWrite", "ipPersonAcceptRetainProof",
			"ipPersonAcceptAdvanceOwner", "ipPersonAcceptRecord",
			"ipPersonDeltaPriorHeadRead", "ipPersonDeltaPriorEvidenceRead",
			"ipPersonDeltaPriorSealRead", "ipPersonDeltaNoOp",
		},
		bypasses: []string{
			`func applyIPPersonAccept(ctx context.Context)(out ipPersonAcceptCommandResult,err error){return out,nil}`,
			`func applyIPPersonDelta(ctx context.Context)(out ipPersonDeltaCommandResult,err error){return out,nil}`,
			`func ipPersonAcceptFresh(ctx context.Context)(out ipPersonAcceptResult,err error){return out,nil}`,
			`func ipPersonDeltaFresh(ctx context.Context)(out ipPersonDeltaResult,err error){return out,nil}`,
			`func ipPersonDeltaNoOp(ctx context.Context)(noOp bool,err error){return false,nil}`,
		},
	})
}
