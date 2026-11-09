package writer

// IA-09 is its own release family: the typed step outcome and the historical
// replay carry caller-lifetime results even though current owner authority and
// the private driver/receipt load finish internally. The shared AST guard must
// cover both typed results and the scalar/error helpers by name.
func init() {
	registerCommandFinishGuard(commandFinishGuardFamily{
		name:        "ia_retirement_step",
		resultTypes: []string{"iaRetirementStepOutcome", "iaRetirementStepHistory", "iaRetirementStepLoaded", "iaRetirementStepUnit"},
		names: []string{
			"stepIARetirement", "applyIARetirementStep", "lookupIARetirementStep",
			"loadIARetirementStep", "iaRetirementStepHeader", "iaRetirementStepTime",
			"iaRetirementStepVictim", "iaRetirementStepEntityUnit", "iaRetirementStepOne",
			"iaRetirementStepExists",
		},
		bypasses: []string{
			`func stepIARetirement(ctx context.Context)(out iaRetirementStepOutcome,err error){return out,nil}`,
			`func lookupIARetirementStep(ctx context.Context)(out iaRetirementStepHistory,err error){return out,nil}`,
			`func loadIARetirementStep(ctx context.Context)(out iaRetirementStepLoaded,err error){return out,nil}`,
			`func iaRetirementStepVictim(ctx context.Context)(out iaRetirementStepUnit,err error){return out,nil}`,
			`func iaRetirementStepExists(ctx context.Context)(value bool,err error){return true,nil}`,
			`func iaRetirementStepTime(ctx context.Context)(value int64,err error){return 1,nil}`,
		},
	})
}
