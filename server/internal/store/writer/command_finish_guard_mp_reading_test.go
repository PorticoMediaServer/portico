package writer

func init() {
	registerCommandFinishGuard(commandFinishGuardFamily{name: "mp-reading", resultTypes: []string{"mpReadingIntentValue", "mpReadingOriginal", "mpReadingSavedPosition"}, names: []string{"mpReadingIntent", "readMPReadingOriginal", "selectMPReadingIntentSource", "readMPReadingSaved"}, bypasses: []string{`func bypass(ctx context.Context)(out mpReadingOriginal,err error){return out,nil}`}})
}
