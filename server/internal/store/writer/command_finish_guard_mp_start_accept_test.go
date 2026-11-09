package writer

func init() {
	registerCommandFinishGuard(commandFinishGuardFamily{
		name: "mp-start-accept", resultTypes: []string{"mpStartAcceptValue", "mpStartAcceptResult", "mpStartAcceptOwner"},
		names: []string{"applyMPStartPrepAccept", "applyMPStartPrepRetire", "applyMPStartPrepTimeout",
			"checkMPStartAcceptDisclosure", "checkMPStartAcceptScope", "composeMPStartAccept", "executeMPStartAccept",
			"mpStartAcceptLoadPayload", "mpStartAcceptReadReceipt", "mpStartAcceptWriteReceipt",
			"readMPStartAcceptFresh", "readMPStartAcceptOwner", "runMPStartAccept"},
		bypasses: []string{
			`func executeMPStartAccept(ctx context.Context)(out mpStartAcceptResult,historical bool,err error){return out,false,nil}`,
			`func mpStartAcceptWriteReceipt(ctx context.Context)(out ResultDigest,err error){return out,nil}`,
			`func checkMPStartAcceptDisclosure(ctx context.Context)(err error){return nil}`,
		},
	})
}
