package api

// Downloads.
func init() {
	retryAreas = append(retryAreas, func(th *theatre) []retryCase {
		return []retryCase{{route: "POST /v1/downloads", body: DownloadAsk{ItemID: th.film, Quality: "original"},
			other: DownloadAsk{ItemID: th.eps[0], Quality: "original"},
			count: func() int { return len(get[Page[Download]](th.T, th.s, th.owner.AccessToken, "/v1/downloads").Items) }}}
	})
}
