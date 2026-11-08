package writer

func init() {
	registerCommandFinishGuard(commandFinishGuardFamily{
		name: "mp-worker", resultTypes: []string{"mpWorkerResult", "mpWorkerOwnerFacts", "mpQueueRepeatValue"},
		names:    []string{"checkMPWorkerNativeDisclosure", "composeMPRepeat", "executeMPRepeat", "applyMPRepeat", "runMPRepeat", "mpWorkerReadOwner"},
		bypasses: []string{`func executeMPRepeat(ctx context.Context)(out mpWorkerResult,err error){return out,nil}`, `func checkMPWorkerNativeDisclosure(ctx context.Context)(err error){return nil}`},
	})
}
