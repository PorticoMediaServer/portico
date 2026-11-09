package writer

func init() {
	registerCommandFinishGuard(commandFinishGuardFamily{name: "uo_birth_start", resultTypes: []string{"uoBirthStartLookup", "uoRootCurrentProof"}, bypasses: []string{
		`func lookupUOBirthStart(ctx context.Context)(out *uoBirthStartLookup,err error){return out,nil}`,
		`func bindUSCurrentUORoot(ctx context.Context)(out *uoRootCurrentProof,err error){return out,nil}`,
	}})
}
