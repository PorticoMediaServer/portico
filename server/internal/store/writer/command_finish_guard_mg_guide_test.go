package writer

func init() {
	registerCommandFinishGuard(commandFinishGuardFamily{
		name:        "mg_guide",
		resultTypes: []string{"mgGuideNative", "mgGuideNativeResult", "mgGuideReceipt", "mgGuideReceiptLookup", "mgGuideFloorLookup"},
		names:       []string{"mgCheckGuideCapacity", "mgCheckGuideReceiptBirth"},
		bypasses: []string{
			`func mgReadGuideNative(ctx context.Context)(out mgGuideNative,err error){return out,nil}`,
			`func mgBeginGuideNative(ctx context.Context)(out mgGuideNativeResult,err error){return out,nil}`,
			`func mgReadGuideReceiptRow(ctx context.Context)(out mgGuideReceipt,err error){return out,nil}`,
			`func mgLookupGuideReceipt(ctx context.Context)(out mgGuideReceiptLookup,err error){return out,nil}`,
			`func mgReadGuideRetirementFloor(ctx context.Context)(out mgGuideFloorLookup,err error){return out,nil}`,
			`func mgCheckGuideCapacity(ctx context.Context)(err error){return nil}`,
			`func mgCheckGuideReceiptBirth(ctx context.Context)(err error){return nil}`,
		},
	})
}
