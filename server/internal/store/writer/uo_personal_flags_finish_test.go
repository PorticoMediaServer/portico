package writer

func init() {
	registerCommandFinishGuard(commandFinishGuardFamily{
		name:        "uo-personal-flags",
		resultTypes: []string{"uoPersonalFlagsEffect"},
		bypasses:    []string{`func bypass(ctx context.Context)(out uoPersonalFlagsEffect,err error){return out,nil}`},
	})
}
