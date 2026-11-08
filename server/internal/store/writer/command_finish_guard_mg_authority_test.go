package writer

func init() {
	registerCommandFinishGuard(commandFinishGuardFamily{name: "mg-authority", resultTypes: []string{"mgActorAuthorization", "mgSourceFrame", "mgSourceAcquisition", "mgSourceOriginalDenial"}, names: []string{"requireFreshHead"}, bypasses: []string{`func openMGSourceFrame(ctx context.Context)(out *mgSourceFrame,err error){return out,nil}`}})
}
