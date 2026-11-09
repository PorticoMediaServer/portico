package writer

func init() {
	registerCommandFinishGuard(commandFinishGuardFamily{
		name:        "uo_public_reading",
		resultTypes: []string{"uoPublicReadingCheck", "usCurrentSessionAuthorization"},
		bypasses: []string{
			`func checkUOPublicReadingReceipt(ctx context.Context)(out uoPublicReadingCheck,err error){return out,nil}`,
			`func authorizeUSCurrentSession(ctx context.Context)(out *usCurrentSessionAuthorization,err error){return out,nil}`,
		},
	})
}
