package writer

func init() {
	registerCommandFinishGuard(commandFinishGuardFamily{name: "uo_birth_session", resultTypes: []string{"uoBirthSessionLookup"}, bypasses: []string{
		`func lookupUOBirthSession(ctx context.Context)(out *uoBirthSessionLookup,err error){return out,nil}`,
		`func persistUOBirthSession(ctx context.Context)(out ResultDigest,err error){return out,nil}`,
	}})
}
