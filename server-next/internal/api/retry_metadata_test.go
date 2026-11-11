package api

// Metadata: fixing a match, the editor and History.
func init() {
	safeToRepeat(map[string]string{
		"POST /v1/admin/items/{id}/metadata/refresh": "a repeat asks the provider again, as a second press of Refresh would",
		"POST /v1/admin/items/{id}/merge":            "the title merged away is gone after the first, so a repeat is not found and changes nothing",
		"POST /v1/admin/versions/{id}/split":         "the version is its title's only one after the first, so a repeat is refused and changes nothing",
		"POST /v1/admin/items/{id}/metadata/history/{entryId}/undo": "Undo puts back only what still holds the change's value, " +
			"so a repeat puts nothing more back (it adds one more History line)",
	})
}
