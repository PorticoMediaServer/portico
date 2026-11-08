package writer

func init() {
	registerCommandFinishGuard(commandFinishGuardFamily{
		name: "mp-physical", resultTypes: []string{"mpPhysicalValue", "mpPhysicalObservation"},
		names:    []string{"consumeMPPhysicalObservation"},
		bypasses: []string{`func consumeMPPhysicalObservation(ctx context.Context)(out mpPhysicalValue,err error){return out,nil}`},
	})
}
