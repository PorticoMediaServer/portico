package api

func init() {
	retryAreas = append(retryAreas, func(th *theatre) []retryCase {
		// A profile with two mixes: songs analysed near two sounds, and listened to.
		_, quiet := th.album("Retry quiet", 6)
		_, loud := th.album("Retry loud", 6)
		near := map[string]int{}
		for _, s := range quiet {
			near[s] = 0
		}
		for _, s := range loud {
			near[s] = 1
		}
		sounds(th.T, th, near)
		for _, s := range append(quiet, loud...) {
			listenTo(th.T, th, s, 2)
		}
		mix := func() string {
			mixes := get[Page[MusicMix]](th.T, th.s, th.owner.AccessToken, "/v1/music/mixes").Items
			if len(mixes) == 0 {
				th.Fatal("the retry profile has no mixes")
			}
			return "/v1/music/mixes/" + mixes[0].ID + "/playlist"
		}
		return []retryCase{
			{route: "POST /v1/music/mixes/{id}/playlist", path: mix, body: MusicMixSave{Name: "Retry mix"}, other: MusicMixSave{Name: "Other mix"}},
		}
	})
}
