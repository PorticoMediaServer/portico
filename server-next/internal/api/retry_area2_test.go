package api

// Setup, remote access and the kind of sign-in.
func init() {
	safeToRepeat(map[string]string{
		"POST /v1/setup/portico":        "saves nothing: a repeat asks for another approval address",
		"POST /v1/setup/portico/finish": "happens once: a repeat is told the server is set up",
	})
}
