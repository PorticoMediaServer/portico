package writer

// IA-08 is its own release family: the typed BEGIN outcome and the historical
// replay carry caller-lifetime results even though current owner authority and
// the private history load finish internally. The shared AST guard must cover
// both typed results and the scalar/error helpers by name.
func init() {
	registerCommandFinishGuard(commandFinishGuardFamily{
		name:        "ia_retirement_begin",
		resultTypes: []string{"iaRetireBeginOutcome", "iaRetirementBeginHistory", "iaRetirementOwnerReceipt"},
		names: []string{
			"beginIARetirement", "applyIARetirementBegin", "lookupIARetirementBegin",
			"loadIARetirementOwner", "iaRetirementBeginHeader", "iaRetirementBeginTime",
		},
		bypasses: []string{
			`func beginIARetirement(ctx context.Context)(out iaRetireBeginOutcome,err error){return out,nil}`,
			`func lookupIARetirementBegin(ctx context.Context)(out iaRetirementBeginHistory,err error){return out,nil}`,
			`func loadIARetirementOwner(ctx context.Context)(out iaRetirementOwnerReceipt,err error){return out,nil}`,
			`func iaRetirementBeginHeader(ctx context.Context)(value int64,err error){return 1,nil}`,
		},
	})
}
