package writer

func init() {
	registerCommandFinishGuard(commandFinishGuardFamily{
		name:        "mg-tuner-retire",
		resultTypes: []string{"mgTunerMaintenance", "mgTunerRetireResult", "mgTunerReconcileResult"},
		names:       []string{"nextMGTunerDebt"},
		bypasses: []string{
			`func openMGTunerMaintenance(ctx context.Context)(out *mgTunerMaintenance,err error){return out,nil}`,
			`func retireMGTuner(ctx context.Context)(out mgTunerRetireResult,err error){return out,nil}`,
			`func reconcileMGTuners(ctx context.Context)(out mgTunerReconcileResult,err error){return out,nil}`,
			`func nextMGTunerDebt(ctx context.Context)(out mgTunerDebt,err error){return out,nil}`,
		},
	})
}
