package api

func init() {
	safeToRepeat(map[string]string{
		"POST /v1/admin/updates/check": "only looks: nothing is installed",
	})
}
