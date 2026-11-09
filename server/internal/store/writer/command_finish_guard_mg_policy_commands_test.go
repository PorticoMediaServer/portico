package writer

// The two MG-18 accepting commands share the personal-root adapter but keep
// their own release result and bypass controls. The existing mg_dvr_policy
// family covers the leaf effects; this family covers the accepting wrappers so a
// new success return cannot silently skip the shared finish.
func init() {
	registerCommandFinishGuard(commandFinishGuardFamily{
		name:        "mg_dvr_policy_commands",
		resultTypes: []string{"mgDVRPolicyAcceptance"},
		bypasses: []string{
			`func acceptMGSetDVRDefaults(ctx context.Context)(out mgDVRPolicyAcceptance,err error){return out,nil}`,
			`func acceptMGSetDVRStoragePolicy(ctx context.Context)(out mgDVRPolicyAcceptance,err error){return out,nil}`,
		},
	})
}
