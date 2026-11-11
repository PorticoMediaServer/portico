package api

// Switching the kind of sign-in, moving, change owner and leaving.
func init() {
	safeToRepeat(map[string]string{
		"POST /v1/admin/sign-in/preview":        "changes nothing on this server",
		"POST /v1/admin/sign-in/switch":         "happens once: a repeat is signed out by the switch, or asks for the kind it already has",
		"POST /v1/admin/sign-in/finish":         "sets the state: a repeat finds nobody waiting",
		"POST /v1/admin/members/{id}/move-code": "replaces the person's code; the answer is the only copy either way",
		"POST /v1/admin/sign-in/move-codes":     "replaces everyone's codes; the answer is the only copy either way",
		"POST /v1/admin/owner":                  "happens once: a repeat is refused, the caller is no longer the owner",
		"POST /v1/auth/move/check":              "changes nothing",
		"POST /v1/auth/move/complete":           "uses up its code: a repeat is refused",
		"POST /v1/me/leave":                     "happens once: a repeat is signed out by the first",
	})
}
