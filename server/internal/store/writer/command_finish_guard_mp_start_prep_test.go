package writer

func init() {
	registerCommandFinishGuard(commandFinishGuardFamily{
		name: "mp-start-prep", resultTypes: []string{"mpStartPrepValue", "mpStartPrepOwner", "mpStartPrepResult"},
		names: []string{"applyMPStartPrepBegin", "applyMPStartPrepClaim", "applyMPStartPrepSeal", "captureMPStartPrepOwner",
			"checkMPStartPrepCurrency", "checkMPStartPrepDisclosure", "checkMPStartPrepScope", "composeMPStartPrep",
			"executeMPStartPrep", "mpStartPrepBeginCurrency", "mpStartPrepBeginDisclosure", "mpStartPrepBeginDuplicate",
			"mpStartPrepLoadPayload", "mpStartPrepReadReceipt", "mpStartPrepWriteReceipt",
			"readMPStartPrepDisclosedOwner", "readMPStartPrepExecution", "readMPStartPrepOwner", "runMPStartPrep"},
		bypasses: []string{
			`func executeMPStartPrep(ctx context.Context)(out mpStartPrepResult,historical bool,err error){return out,false,nil}`,
			`func mpStartPrepWriteReceipt(ctx context.Context)(out ResultDigest,err error){return out,nil}`,
			`func checkMPStartPrepDisclosure(ctx context.Context)(err error){return nil}`,
		},
	})
}
