package api

// Signing in with a Portico Account, the link to Portico Services and its notes.
func init() {
	safeToRepeat(map[string]string{
		"POST /v1/auth/portico/challenge": "makes nothing: a challenge nobody answers is not kept",
		"POST /v1/auth/portico/exchange": "a sign-in note works once; after a lost answer the person signs in again, " +
			"and an invitation it used has already made their account, so nothing is made twice",
		"POST /v1/hosted/notices":             "a note is applied only when newer: a repeat changes nothing and is acknowledged the same",
		"POST /v1/hosted/approvals/{id}/poll": "changes nothing",
		"POST /v1/admin/portico/link":         "a lost answer leaves an approval nobody holds, which expires",
		"POST /v1/admin/portico/link/finish":  "an approval works once: a repeat is refused and the link it made stands",
	})
}
