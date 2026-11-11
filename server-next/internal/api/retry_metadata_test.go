package api

// Metadata: fixing a match, the editor and History.
func init() {
	safeToRepeat(map[string]string{
		"POST /v1/admin/items/{id}/metadata/refresh": "a repeat asks the provider again, as a second press of Refresh would",
		"POST /v1/admin/items/{id}/merge":            "the title merged away is gone after the first, so a repeat is not found and changes nothing",
		"POST /v1/admin/versions/{id}/split":         "the version is its title's only one after the first, so a repeat is refused and changes nothing",
		"POST /v1/admin/items/{id}/artwork/find":     "a repeat fetches the provider's pictures again and adds none it already has (one row per picture address)",
		"POST /v1/admin/items/{id}/artwork":          "an upload of the same picture is the row it made the first time, used and locked again",
		"POST /v1/admin/items/{id}/metadata/history/{entryId}/undo": "Undo puts back only what still holds the change's value, " +
			"so a repeat puts nothing more back (it adds one more History line)",
		"POST /v1/admin/metadata/settings/opensubtitles/check": "Test connection only asks OpenSubtitles and keeps what it answered in memory; a repeat asks again",
		"POST /v1/admin/items/{id}/lyrics/fetch":               "a repeat asks LRCLIB again and keeps its answer in the song's one lyrics row; pasted lyrics are never replaced",
		"POST /v1/items/{id}/subtitles":                        "a repeat answers the subtitle already kept for this version and result, without downloading it again",
	})
}

// A library's collections.
func init() {
	retryAreas = append(retryAreas, func(th *theatre) []retryCase {
		return []retryCase{
			{route: "POST /v1/admin/libraries/{id}/collections", path: func() string {
				return "/v1/admin/libraries/" + th.libs["movies"].PublicID + "/collections"
			}, body: CollectionCreate{Title: "Retry collection"}, other: CollectionCreate{Title: "Other collection"}},
		}
	})
}
