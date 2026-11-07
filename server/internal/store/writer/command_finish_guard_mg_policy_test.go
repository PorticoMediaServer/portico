package writer

func init() {
	registerCommandFinishGuard(commandFinishGuardFamily{
		name:        "mg_dvr_policy",
		resultTypes: []string{"mgDVRDefaultsSnapshot", "mgDVRStorageSnapshot"},
		bypasses: []string{
			`func readMGDVRDefaults(ctx context.Context)(out mgDVRDefaultsSnapshot,err error){return out,nil}`,
			`func setMGDVRStorage(ctx context.Context)(out mgDVRStorageSnapshot,err error){return out,nil}`,
		},
	})
}
