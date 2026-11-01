package serverreturn

// A80 has three credential-change families. Recovery-code operations are part
// of the existing second-factor flow, not another reauthentication category.
func CredentialPurpose(purpose, target string) bool {
	switch purpose {
	case "password_set_or_change", "mfa_enroll", "mfa_disable", "recovery_codes":
		return target == ""
	case "email_change":
		return len(target) >= 3 && len(target) <= 320
	}
	return false
}
