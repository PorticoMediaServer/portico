package api

// Notices and problem reports.
func init() {
	safeToRepeat(map[string]string{
		"POST /v1/notifications/read-all": "sets the state",
	})
	retryAreas = append(retryAreas, func(th *theatre) []retryCase {
		return []retryCase{
			{route: "POST /v1/reports", body: ReportCreate{Kind: "problem", Message: "No sound"}, other: ReportCreate{Kind: "problem", Message: "Other"},
				count: func() int { return th.named("/v1/reports", "message", "No sound") }},
			{route: "POST /v1/admin/notifications", body: ServerMessage{Audience: "everyone", Message: "Back at six"},
				other: ServerMessage{Audience: "everyone", Message: "Back at seven"},
				count: func() int { return th.named("/v1/notifications?view=all", "body", "Back at six") }},
		}
	})
}
