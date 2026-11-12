package api

func init() {
	safeToRepeat(map[string]string{
		"POST /v1/items/state:batch": "Sets state values with edit ages; repeating the same batch adds nothing, as for the single-item PUT.",
		"POST /v1/me/recommendations:reset": "Creates nothing: a repeat is the same reset a moment later, and can only forget what the profile " +
			"played or liked between the two sends, which for a retry is seconds after the press.",
	})
}
