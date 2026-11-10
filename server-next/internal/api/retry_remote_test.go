package api

func init() {
	safeToRepeat(map[string]string{
		"POST /v1/admin/remote-sources/drive-sign-in": "starts a short-lived in-memory sign-in; a repeat only starts another and changes no saved source",
	})
}
