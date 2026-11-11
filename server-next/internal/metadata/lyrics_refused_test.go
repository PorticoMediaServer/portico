package metadata_test

import (
	"context"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/metadata"
	"github.com/PorticoMediaServer/portico/server-next/internal/metadata/fetch"
)

// refusesOneSong answers as LRCLIB does for a request it will not take (a song with no
// artist in its tags is one: 400), for "Come Together" only.
type refusesOneSong struct{ asked []string }

func (*refusesOneSong) Name() string { return "lrclib" }
func (p *refusesOneSong) Lyrics(_ context.Context, q metadata.LyricsQuery) (metadata.FoundLyrics, error) {
	p.asked = append(p.asked, q.Title)
	if q.Title == "Come Together" {
		return metadata.FoundLyrics{}, &fetch.Status{Provider: "lrclib", Code: 400}
	}
	return metadata.FoundLyrics{Body: "words"}, nil
}

// One song the lyrics provider refuses does not keep the songs after it from being asked
// about, on this run or the next.
func TestOneSongTheLyricsProviderRefusesDoesNotStopTheRest(t *testing.T) {
	d, lib, ids := songs(t)
	p := &refusesOneSong{}
	s := &metadata.Service{DB: d, Lyrics: p}
	for run := 1; run <= 2; run++ {
		found, err := s.FetchMissingLyrics(ctx, lib.ID, true)
		t.Logf("run %d: found %d, err %v, asked %q", run, found, err, p.asked)
	}
	if l, err := d.SongLyrics(ctx, ids["Something"]); err != nil || l.Body != "words" {
		t.Errorf("after two runs the song after the refused one still has no lyrics: %+v, %v", l, err)
	}
}

// pausedLyrics answers every request as the pacing client does while a provider's pause is in
// force: the provider cannot be asked at all, so the run stops instead of recording every song.
type pausedLyrics struct{ asked int }

func (*pausedLyrics) Name() string { return "lrclib" }
func (p *pausedLyrics) Lyrics(context.Context, metadata.LyricsQuery) (metadata.FoundLyrics, error) {
	p.asked++
	return metadata.FoundLyrics{}, &fetch.SlowDown{Provider: "lrclib"}
}

func TestALyricsProviderThatAsksForAPauseStopsTheRun(t *testing.T) {
	d, lib, _ := songs(t)
	p := &pausedLyrics{}
	s := &metadata.Service{DB: d, Lyrics: p}
	if _, err := s.FetchMissingLyrics(ctx, lib.ID, true); err == nil || p.asked != 1 {
		t.Fatalf("a paused provider: asked %d times, %v; want the run stopped after the first", p.asked, err)
	}
}
