package writer

func init() {
	registerCommandFinishGuard(commandFinishGuardFamily{
		name:        "mg-recording",
		resultTypes: []string{"mgRecordingAcceptance", "mgRecordingIntentFacts", "mgRecordingIntentResult", "mgRecordingIntentRow", "mgRecordingReservation"},
		names:       []string{"mgRecordingReadback"},
		bypasses: []string{
			`func acceptMGRecordingSchedule(ctx context.Context)(out mgRecordingAcceptance,err error){return out,nil}`,
			`func acceptMGRecordingUpdate(ctx context.Context)(out mgRecordingAcceptance,err error){return out,nil}`,
			`func acceptMGRecordingCancel(ctx context.Context)(out mgRecordingAcceptance,err error){return out,nil}`,
			`func acceptMGRecordingKeep(ctx context.Context)(out mgRecordingAcceptance,err error){return out,nil}`,
		},
	})
}
