package api

// Personal lists and their entries.
func init() {
	retryAreas = append(retryAreas, func(th *theatre) []retryCase {
		var listID string
		return []retryCase{
			{route: "POST /v1/lists", body: ListCreate{Kind: "playlist", Name: "Retry playlist"},
				other: ListCreate{Kind: "playlist", Name: "Other playlist"}, created: func(id string) { listID = id }},
			{route: "POST /v1/lists/{id}/entries", path: func() string { return "/v1/lists/" + listID + "/entries" },
				body: ListAdd{Items: []string{th.eps[0]}}, other: ListAdd{Items: []string{th.eps[1]}}},
		}
	})
}
