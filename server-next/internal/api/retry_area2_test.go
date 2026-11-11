package api

// Setup, remote access and the kind of sign-in.
func init() {
	safeToRepeat(map[string]string{
		"POST /v1/setup/portico":                   "saves nothing: a repeat asks for another approval address",
		"POST /v1/setup/portico/finish":            "happens once: a repeat is told the server is set up",
		"POST /v1/setup/finish":                    "sets Remote security: a repeat sets it again",
		"POST /v1/admin/remote-access/route-check": "changes nothing",
		"POST /v1/admin/remote-access/check":       "asks the router and reads the certificate again; changes no setting",
	})
}
