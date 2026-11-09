package writer

func init() {
	registerCommandFinishGuard(commandFinishGuardFamily{
		name:        "mg-capture",
		resultTypes: []string{"mgCaptureAcceptance", "mgCaptureFacts", "mgCaptureRow", "mgCaptureResult"},
		names:       []string{"mgCaptureTime"},
		bypasses: []string{
			`func acceptMGCaptureClaim(ctx context.Context)(out mgCaptureAcceptance,err error){return out,nil}`,
			`func acceptMGCaptureRenew(ctx context.Context)(out mgCaptureAcceptance,err error){return out,nil}`,
			`func acceptMGCaptureCheckpoint(ctx context.Context)(out mgCaptureAcceptance,err error){return out,nil}`,
			`func mgReadCapture(ctx context.Context)(out mgCaptureRow,err error){return out,nil}`,
			`func mgClaimCapture(ctx context.Context)(out mgCaptureFacts,err error){return out,nil}`,
			`func mgRenewCapture(ctx context.Context)(out mgCaptureFacts,err error){return out,nil}`,
			`func mgCheckpointCapture(ctx context.Context)(out mgCaptureFacts,err error){return out,nil}`,
			`func mgCaptureWrite(ctx context.Context)(out mgCaptureFacts,err error){return out,nil}`,
			`func mgCaptureTime(ctx context.Context)(value int64,err error){return 1,nil}`,
		},
	})
}
