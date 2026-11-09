package writer

func init() {
	registerCommandFinishGuard(commandFinishGuardFamily{name: "uo_birth_job", resultTypes: []string{"uoBirthJobLookup"}, bypasses: []string{
		`func lookupUOBirthJob(ctx context.Context)(out *uoBirthJobLookup,err error){return out,nil}`,
	}})
}
