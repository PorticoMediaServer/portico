package api

import "github.com/PorticoMediaServer/portico/server-next/internal/playqueue"

// Playback and play queues.
func init() {
	retryAreas = append(retryAreas, func(th *theatre) []retryCase {
		var queueID string
		return []retryCase{
			{route: "POST /v1/queues", body: QueueCreate{Source: playqueue.QueueSource{Items: th.eps[:]}},
				other: QueueCreate{Source: playqueue.QueueSource{Items: []string{th.eps[1]}}}, created: func(id string) { queueID = id }},
			{route: "POST /v1/queues/{id}/playlist", path: func() string { return "/v1/queues/" + queueID + "/playlist" },
				body: QueueSave{Name: "Evening"}, other: QueueSave{Name: "Morning"}},
			{route: "POST /v1/playback/sessions", body: PlaybackStart{ItemID: th.film}, other: PlaybackStart{ItemID: th.song},
				inMemoryOnly: true},
		}
	})
	safeToRepeat(map[string]string{
		"POST /v1/queues/{id}/entries":                "needs If-Match: a repeat has an old revision and is refused",
		"POST /v1/queues/{id}/entries/{entryId}/move": "needs If-Match: a repeat has an old revision and is refused",
		"POST /v1/queues/{id}/advance":                "sets the current entry",
		"POST /v1/queues/{id}/prepare-next":           "changes nothing that lasts",
		"POST /v1/queues/{id}/commit-next":            "sets the current entry",
	})
}
