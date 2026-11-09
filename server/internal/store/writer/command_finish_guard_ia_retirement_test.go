package writer

func init() {
	registerCommandFinishGuard(commandFinishGuardFamily{
		name: "ia_retirement_unit", resultTypes: []string{"iaRetirementUnitEntry", "iaRetirementUnitResult", "mpGuidePinOccurrence", "mpGuidePinReclaimEffect"},
		names:    []string{"checkIARetirementUnit", "checkIARetirementAsset", "iaRetirementPoint", "requireIACataloguePersonalReferencesAbsent", "readIARetirementSessionBytes", "requireMPSessionParentDrained", "reclaimMPReleasedGuidePin", "selectMPGuidePinOccurrence"},
		bypasses: []string{`func enterIARetirementUnit(ctx context.Context)(out *iaRetirementUnitEntry,err error){return out,nil}`, `func retireIAMediaParentUnit(ctx context.Context)(out iaRetirementUnitResult,err error){return out,nil}`},
	})
}
