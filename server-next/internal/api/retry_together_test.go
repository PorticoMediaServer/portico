package api

import "github.com/PorticoMediaServer/portico/server-next/internal/receivers"

// Playing on another device and together.
func init() {
	retryAreas = append(retryAreas, func(th *theatre) []retryCase {
		return []retryCase{
			{route: "POST /v1/receivers/{id}/requests", path: func() string {
				var tv ReceiverOpen
				call(th.T, th.s, "PUT", "/v1/receivers/current", onMain(th.T, th.s, signIn(th.T, th.s, 1)).AccessToken, nil, &tv)
				return "/v1/receivers/" + tv.ID + "/requests"
			}, body: ReceiverAsk{Kind: "control"}, other: ReceiverAsk{Kind: "play", Play: &receivers.Play{ItemID: th.film}}, inMemoryOnly: true},
		}
	})
}
