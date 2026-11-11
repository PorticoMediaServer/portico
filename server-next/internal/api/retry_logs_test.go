package api

// The owner console: messages and the support export.
func init() {
	safeToRepeat(map[string]string{
		"POST /v1/admin/support-export": "changes nothing: the export is made as it is sent and not kept",
	})
}
