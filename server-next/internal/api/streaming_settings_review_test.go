package api

import (
	"encoding/json"
	"sync"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/playback"
)

// A viewer away from home can always lower their own stream's quality, even when the upload
// speed is over-full (the owner gave or lowered the upload speed while streams were playing).
func TestReviewLoweringQualityIsNeverRefusedForTheUploadSpeed(t *testing.T) {
	th := newTheatre(t)
	ladder := ladderEngine{th.engine}
	th.s.playback.SetEngine(ladder, ladder)
	save := func(body string) {
		if code := status(t, th.s, "PUT", "/v1/admin/settings/streaming", th.owner.AccessToken, json.RawMessage(body)); code != 200 {
			t.Fatalf("saving %s answered %d", body, code)
		}
	}
	phone := onMain(t, th.s, signIn(t, th.s, 1)).AccessToken
	tv := onMain(t, th.s, signIn(t, th.s, 2)).AccessToken
	save(`{"uploadSpeedKbps":12500}`)
	first := th.play(phone, th.film) // 8,000
	th.play(tv, th.eps[0])           // 1,500
	save(`{"uploadSpeedKbps":1250}`) // the owner corrects the speed: 1,000 to share, 9,500 in use
	w := serve(th.s, newRequest("PATCH", "/v1/playback/sessions/"+first.ID, phone,
		PlaybackChange{Quality: &playback.Quality{Mode: playback.QualityFixed, ID: "480p"}}))
	if w.Code != 200 || th.quality(first.ID) != "480p" {
		t.Fatalf("lowering a stream from 8,000 to 1,500 kbps answered %d and it plays %q: %s", w.Code, th.quality(first.ID), w.Body)
	}
}

// Eight devices away from home start at once with room for one 8,000 kbps stream and one
// 1,500: whatever the order, what is admitted fits in the share, and the Dashboard's counts
// add up to the eight starts.
func TestReviewStartsRacingForTheUploadSpeed(t *testing.T) {
	th := newTheatre(t)
	ladder := ladderEngine{th.engine}
	th.s.playback.SetEngine(ladder, ladder)
	if code := status(t, th.s, "PUT", "/v1/admin/settings/streaming", th.owner.AccessToken, json.RawMessage(`{"uploadSpeedKbps":12500}`)); code != 200 {
		t.Fatal(code)
	}
	takes := map[string]int{"1080p-low": 8000, "720p-high": 4000, "480p": 1500}
	const devices = 8
	tokens := make([]string, devices)
	for n := range tokens {
		tokens[n] = onMain(t, th.s, signIn(t, th.s, n+1)).AccessToken
	}
	var wg sync.WaitGroup
	codes := make([]int, devices)
	ids := make([]string, devices)
	for n := range tokens {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w := serve(th.s, newRequest("POST", "/v1/playback/sessions", tokens[n], PlaybackStart{ItemID: th.film}))
			codes[n] = w.Code
			var out PlaybackSession
			if w.Code == 201 && jsonInto(w, &out) == nil {
				ids[n] = out.ID
			}
		}()
	}
	wg.Wait()
	used, started, refused := 0, 0, 0
	for n, code := range codes {
		switch code {
		case 201:
			started++
			used += takes[th.quality(ids[n])]
		case 503:
			refused++
		default:
			t.Errorf("a start answered %d", code)
		}
	}
	held := th.s.playback.UploadHeld()
	if used > 10000 || started == 0 || started+refused != devices || held.Refused != refused || held.Narrowed != started {
		t.Fatalf("%d started taking %d kbps of 10,000, %d refused; the Dashboard says %+v", started, used, refused, held)
	}
}
