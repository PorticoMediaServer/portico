package api

// Reading.
func init() {
	safeToRepeat(map[string]string{
		"POST /v1/annotations:batch": "each change names its annotation and the later edit wins: a repeat changes nothing",
	})
}
