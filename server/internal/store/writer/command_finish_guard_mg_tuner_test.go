package writer

func init() {
	registerCommandFinishGuard(commandFinishGuardFamily{
		name:        "mg-tuner",
		resultTypes: []string{"mgTunerAllocation"},
		names:       []string{"mgTunerTime"},
		bypasses: []string{
			`func readMGTuner(ctx context.Context)(out mgTunerAllocation,err error){return out,nil}`,
			`func acquireMGTuner(ctx context.Context)(out mgTunerAllocation,err error){return out,nil}`,
			`func renewMGTuner(ctx context.Context)(out mgTunerAllocation,err error){return out,nil}`,
			`func mgTunerTime(ctx context.Context)(value int64,err error){return 1,nil}`,
		},
	})
}
