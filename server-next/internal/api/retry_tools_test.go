package api

// Outside tools.
func init() {
	safeToRepeat(map[string]string{
		"POST /v1/mcp": "an assistant's tools only read or set a state (mark watched)",
	})
}
