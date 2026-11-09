package writer

func init() {
	registerCommandFinishGuard(commandFinishGuardFamily{
		name:        "search-rearm",
		resultTypes: []string{"searchEntityRearmResult"},
		names:       []string{"rearmSearchEntity"},
		bypasses:    []string{`func rearmSearchEntity(ctx context.Context)(out searchEntityRearmResult,err error){return out,nil}`},
	})
}
