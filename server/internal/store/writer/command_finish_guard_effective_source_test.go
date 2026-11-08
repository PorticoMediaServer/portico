package writer

func init() {
	registerCommandFinishGuard(commandFinishGuardFamily{
		name:        "effective_source",
		resultTypes: []string{"effectiveEvidencePinLengths", "effectivePublicationPinLengths", "effectiveReceiptPinLengths", "effectiveEvidenceBodyPin", "effectivePublicationBodyPin", "effectivePinnedSealReceipt", "effectiveSourceByteCoverage", "effectiveVerifiedSource", "enrichAcceptVerifiedSource"},
		names:       []string{"requireEffectiveSourceMemory", "checkEffectiveSourceControlPoints", "checkEffectiveVerifiedSource", "checkNativeEffectiveSource", "checkEnrichAcceptVerifiedSource", "consumeEvidencePinCopy", "consumePublicationPinCopy", "consumeReceiptPinCopy"},
		bypasses: []string{
			`func checkEnrichAcceptVerifiedSource(ctx context.Context,turn *commandTurn,input *enrichAcceptCommand,proof *enrichAcceptVerifiedSource)(err error){return nil}`,
			`func verifyEnrichAcceptSource(ctx context.Context,turn *commandTurn,input *enrichAcceptCommand)(out *enrichAcceptVerifiedSource,err error){return nil,nil}`,
			`func requireEffectiveSourceMemory(ctx context.Context,required uint64)(err error){return nil}`,
			`func checkEffectiveSourceControlPoints(ctx context.Context,turn *commandTurn,c *effectiveStageBeginCommand,e,s effectivePinnedSealReceipt)(err error){return nil}`,
		},
	})
}
