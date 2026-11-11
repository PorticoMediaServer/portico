package api

// Browsing and a person's own state on titles.
func init() {
	safeToRepeat(map[string]string{
		"POST /v1/items/state:undo": "uses up its undo offer: a repeat is refused",
	})
}
