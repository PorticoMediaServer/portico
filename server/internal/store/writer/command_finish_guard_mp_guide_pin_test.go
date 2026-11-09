package writer

func init() {
	registerCommandFinishGuard(commandFinishGuardFamily{name: "mp-guide-pin", resultTypes: []string{"mpGuidePinEffect", "mpGuidePinReclaimEffect", "mpGuidePinOccurrence"}, names: []string{"installMPExternalGuidePin", "releaseMPExternalGuidePin", "reclaimMPReleasedGuidePin", "selectMPGuidePinOccurrence"}, bypasses: []string{`func bypass(ctx context.Context)(out mpGuidePinEffect,err error){return out,nil}`}})
}
