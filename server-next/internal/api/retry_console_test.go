package api

func init() {
	safeToRepeat(map[string]string{
		"POST /v1/admin/updates/check":               "only looks: nothing is installed",
		"POST /v1/admin/data-folder/permissions/fix": "a folder already private has nothing to change: a repeat answers the same and records nothing",
	})
}
