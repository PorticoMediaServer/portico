package api

import (
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/librarychannels"
	"github.com/PorticoMediaServer/portico/server-next/internal/playback"
)

func TestALimitedChannelCapsEveryQuality(t *testing.T) {
	th := newTheatre(t)
	token := th.owner.AccessToken
	b := th.filmChannel()
	b.Quality = librarychannels.ChannelQuality{Mode: "limited", MaxKbps: 2000}
	ch := makeChannel(t, th.s, token, "Films", *b)
	rows := programmes(t, th.s, ch.ID, th.clock, th.clock.Add(time.Hour))
	claims, _ := th.s.tokens.CheckAccess(token, th.clock)
	th.clock = rows[0].StartsAt.Add(5 * time.Minute)
	token, _ = th.s.tokens.Access(claims, th.clock, th.clock.Add(time.Hour))
	code, session := th.start(token, "join-cap", PlaybackStart{Channel: ch.ID})
	if q := session.Presentation.Quality; code != 201 || q.Mode == playback.QualityOriginal || q.Mode == playback.QualityLimit && q.MaxVideoBitrateKbps > 2000 {
		t.Fatalf("a start naming no quality on a 2000 kbps channel answered %d with %+v", code, q)
	}
	code, s2 := th.start(token, "join-cap2", PlaybackStart{Channel: ch.ID, Quality: &playback.Quality{Mode: playback.QualityAutomatic}})
	if code != 201 || s2.Presentation.Quality.Mode != playback.QualityAutomatic {
		t.Fatalf("automatic answered %d with %+v", code, s2.Presentation.Quality)
	}
	// Switching to Original in the player stays under the cap.
	var changed PlaybackSession
	if w := call(t, th.s, "PATCH", "/v1/playback/sessions/"+s2.ID, token, PlaybackChange{Quality: &playback.Quality{Mode: playback.QualityOriginal}},
		&changed); w.Code != 200 || changed.Presentation.Quality.Mode == playback.QualityOriginal || changed.Presentation.Quality.MaxVideoBitrateKbps > 2000 {
		t.Fatalf("switching to Original answered %d with %+v", w.Code, changed.Presentation.Quality)
	}
}
