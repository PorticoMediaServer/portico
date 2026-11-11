package api

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/playback"
)

// ladderEngine is the fake engine with a quality ladder that says what each rung takes.
type ladderEngine struct{ *fakeEngine }

func (ladderEngine) Qualities(db.PlaybackVersion, db.ItemKind, string) []playback.QualityOption {
	return []playback.QualityOption{{Mode: "original"}, {Mode: "automatic"},
		{Mode: "fixed", ID: "1080p-low", MaxHeight: 1080, MaxVideoBitrateKbps: 8000},
		{Mode: "fixed", ID: "720p-high", MaxHeight: 720, MaxVideoBitrateKbps: 4000},
		{Mode: "fixed", ID: "480p", MaxHeight: 480, MaxVideoBitrateKbps: 1500}}
}

func (th *theatre) quality(sessionID string) string {
	th.engine.mu.Lock()
	defer th.engine.mu.Unlock()
	return th.engine.running[sessionID].Plan.Quality.ID
}

// The owner's upload speed: away plays share 80% of it, each new one is offered only what is
// left, one that cannot fit even the lowest quality is refused with the definition's
// sentence, and a stream already playing is never lowered. (Test requests come from away.)
func TestUploadSpeedAndTheAwayLimit(t *testing.T) {
	th := newTheatre(t)
	ladder := ladderEngine{th.engine}
	th.s.playback.SetEngine(ladder, ladder)
	owner := th.owner.AccessToken
	streaming := func(body string) int {
		return status(t, th.s, "PUT", "/v1/admin/settings/streaming", owner, json.RawMessage(body))
	}
	if code := streaming(`{"convertWhenNeeded":"sometimes"}`); code != 422 {
		t.Fatalf("an unknown conversion choice answered %d", code)
	}
	if code := streaming(`{"uploadSpeedKbps":12500}`); code != 200 {
		t.Fatalf("setting the upload speed answered %d", code)
	}
	phone := onMain(t, th.s, signIn(t, th.s, 1)).AccessToken
	tv := onMain(t, th.s, signIn(t, th.s, 2)).AccessToken
	tablet := onMain(t, th.s, signIn(t, th.s, 3)).AccessToken

	first := th.play(phone, th.film) // 10,000 kbps to share: the best rung that fits
	if got := th.quality(first.ID); got != "1080p-low" {
		t.Fatalf("the first away play got %q", got)
	}
	second := th.play(tv, th.eps[0]) // 2,000 left
	if got := th.quality(second.ID); got != "480p" {
		t.Fatalf("the second away play got %q", got)
	}
	w := serve(th.s, newRequest("POST", "/v1/playback/sessions", tablet, PlaybackStart{ItemID: th.eps[1]}))
	if w.Code != 503 || !strings.Contains(w.Body.String(), "internet connection is full right now") {
		t.Fatalf("a play that cannot fit answered %d: %s", w.Code, w.Body)
	}
	if th.quality(first.ID) != "1080p-low" || th.quality(second.ID) != "480p" {
		t.Fatalf("the refused play changed what was playing")
	}

	// The owner's own limit away from home, with no upload speed given.
	if code := streaming(`{"uploadSpeedKbps":0,"awayVideoKbps":4000}`); code != 200 {
		t.Fatalf("setting the away limit answered %d", code)
	}
	third := th.play(tablet, th.eps[1])
	if got := th.quality(third.ID); got != "720p-high" {
		t.Fatalf("a play under the away limit got %q", got)
	}
	for _, q := range th.options(tablet, th.film, "").Qualities {
		if q.ID == "1080p-low" || q.Mode == "original" {
			t.Errorf("a quality above the away limit is offered: %+v", q)
		}
	}
	if th.quality(first.ID) != "1080p-low" {
		t.Errorf("lowering the away limit lowered a stream already playing")
	}
}
