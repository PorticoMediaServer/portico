package writer

func init() {
	registerCommandFinishGuard(commandFinishGuardFamily{
		name:        "effective_source",
		resultTypes: []string{"effectiveEvidencePinLengths", "effectivePublicationPinLengths", "effectiveReceiptPinLengths", "effectiveEvidenceBodyPin", "effectivePublicationBodyPin", "effectivePinnedSealReceipt", "effectiveSourceByteCoverage", "effectiveVerifiedSource"},
		names:       []string{"requireEffectiveSourceMemory", "checkEffectiveSourceControlPoints", "checkEffectiveVerifiedSource", "consumeEvidencePinCopy", "consumePublicationPinCopy", "consumeReceiptPinCopy"},
		bypasses: []string{
			`func requireEffectiveSourceMemory(ctx context.Context,required uint64)(err error){return nil}`,
			`func checkEffectiveSourceControlPoints(ctx context.Context,turn *commandTurn,c *effectiveStageBeginCommand,e,s effectivePinnedSealReceipt)(err error){return nil}`,
		},
	})
}
