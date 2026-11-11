package api

func init() {
	safeToRepeat(map[string]string{
		"POST /v1/items/state:batch": "Sets state values with edit ages; repeating the same batch adds nothing, as for the single-item PUT.",
	})
}
