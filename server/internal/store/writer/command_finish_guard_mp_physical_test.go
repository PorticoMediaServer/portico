package writer

func init() {
	registerCommandFinishGuard(commandFinishGuardFamily{
		name: "mp-physical", resultTypes: []string{"mpPhysicalValue", "mpPhysicalObservation", "mpPhysicalRelease"},
		names:    []string{"consumeMPPhysicalObservation", "verifyMPPhysicalRelease"},
		bypasses: []string{`func consumeMPPhysicalObservation(ctx context.Context)(out mpPhysicalValue,err error){return out,nil}`},
	})
}
