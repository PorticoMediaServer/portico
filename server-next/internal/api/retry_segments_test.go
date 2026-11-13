package api

func init() {
	safeToRepeat(map[string]string{
		"POST /v1/admin/segments/redetect":             "Marks current measurements unread and joins existing detection jobs, keeping owner decisions.",
		"POST /v1/admin/segments/retry":                "Only unreadable measurements are marked unread; repeated calls join existing jobs.",
		"POST /v1/admin/segments/titles/{id}/approve":  "Approves only unapproved markers; a repeat leaves approved markers as they are.",
		"POST /v1/admin/segments/titles/{id}/redetect": "Marks measurements unread and joins the library's existing detection job; owner decisions are kept.",
		"POST /v1/items/{id}/analysis/markers":         "Requires requestId; the same request is recorded with the marker change and answered without applying twice.",
	})
}
