package api

// The owner's webhooks.
func init() {
	retryAreas = append(retryAreas, func(th *theatre) []retryCase {
		return []retryCase{
			{route: "POST /v1/admin/webhooks", body: WebhookCreate{Name: "Home", URL: "http://192.168.1.20:8123/hook", Events: []string{"server.started"}},
				other: WebhookCreate{Name: "Other", URL: "http://192.168.1.20:8123/hook", Events: []string{"server.started"}},
				count: func() int { return th.named("/v1/admin/webhooks", "name", "Home") }},
		}
	})
	safeToRepeat(map[string]string{
		"POST /v1/admin/webhooks/{id}/test":   "sends a test message: a repeat sends another, which changes nothing",
		"POST /v1/admin/webhooks/{id}/secret": "replaces the secret; the answer is the only copy either way",
	})
}
