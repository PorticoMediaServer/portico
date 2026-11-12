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
	b.Quality = librarychannels.LibraryChannelQuality{Mode: "limited", MaxKbps: 2000}
	ch := makeChannel(t, th.s, token, "Films", *b)
	rows := programmes(t, th.s, ch.ID, th.clock.Now(), th.clock.Now().Add(time.Hour))
	claims, _ := th.s.tokens.CheckAccess(token, th.clock.Now())
	th.clock.Set(rows[0].StartsAt.Add(5 * time.Minute))
	token, _ = th.s.tokens.Access(claims, th.clock.Now(), th.clock.Now().Add(time.Hour))
	code, session := th.start(token, "join-cap", PlaybackStart{Channel: ch.ID})
	if q := session.Presentation.Quality; code != 201 || q.Mode == playback.QualityOriginal || q.Mode == playback.QualityLimit && q.MaxVideoBitrateKbps > 2000 {
		t.Fatalf("a start naming no quality on a 2000 kbps channel answered %d with %+v", code, q)
	}
	// Automatic on a limited channel is planned at the cap, never above it (definition area 11,
	// rule 110: no viewer is sent more than the number the owner gives).
	code, s2 := th.start(token, "join-cap2", PlaybackStart{Channel: ch.ID, Quality: &playback.Quality{Mode: playback.QualityAutomatic}})
	if q := s2.Presentation.Quality; code != 201 || q.Mode != playback.QualityLimit || q.MaxVideoBitrateKbps == 0 || q.MaxVideoBitrateKbps > 2000 {
		t.Fatalf("automatic on a 2000 kbps channel answered %d with %+v", code, q)
	}
	// Switching to Original in the player stays under the cap.
	var changed PlaybackSession
	if w := call(t, th.s, "PATCH", "/v1/playback/sessions/"+s2.ID, token, PlaybackChange{Quality: &playback.Quality{Mode: playback.QualityOriginal}},
		&changed); w.Code != 200 || changed.Presentation.Quality.Mode == playback.QualityOriginal || changed.Presentation.Quality.MaxVideoBitrateKbps > 2000 {
		t.Fatalf("switching to Original answered %d with %+v", w.Code, changed.Presentation.Quality)
	}
	// Automatic on a channel with no limit is unchanged. (A new start ends the session above.)
	free := makeChannel(t, th.s, th.owner.AccessToken, "Films, any quality", *th.filmChannel())
	if code, s3 := th.start(token, "join-free", PlaybackStart{Channel: free.ID, Quality: &playback.Quality{Mode: playback.QualityAutomatic}}); code != 201 ||
		s3.Presentation.Quality.Mode != playback.QualityAutomatic || s3.Presentation.Quality.MaxVideoBitrateKbps != 0 {
		t.Fatalf("automatic on a channel with no limit answered %d with %+v", code, s3.Presentation.Quality)
	}
}
