package api

// The owner's list of tasks.
func init() {
	safeToRepeat(map[string]string{
		"POST /v1/admin/jobs/{id}/retry": "runs a failed task again; a repeat finds it waiting or running and joins it (the kinds run again from here are never Distinct)",
	})
}
