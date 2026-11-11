package playback

import (
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

// Rewind on resume moves a film's resume point back by the profile's setting, and an
// audiobook's by its own setting; never before the start, and never for a title not started.
func TestResumeAtRewinds(t *testing.T) {
	film := db.PlaybackItem{Kind: db.KindMovie, PositionMS: 60_000}
	book := db.PlaybackItem{Kind: db.KindAudiobook, PositionMS: 60_000}
	prefs := map[string]string{"playback.resumeRewindMs": "10000", "audiobooks.resumeRewindMs": "30000"}
	for name, c := range map[string]struct {
		item  db.PlaybackItem
		prefs map[string]string
		want  int64
	}{
		"film, nothing saved":     {film, nil, 60_000},
		"film, rewind 10 s":       {film, prefs, 50_000},
		"audiobook, its own 30 s": {book, prefs, 30_000},
		"near the start":          {db.PlaybackItem{Kind: db.KindEpisode, PositionMS: 4_000}, prefs, 0},
		"never started":           {db.PlaybackItem{Kind: db.KindMovie}, prefs, 0},
	} {
		if got := resumeAt(c.item, c.prefs); got != c.want {
			t.Errorf("%s: resumes at %d, want %d", name, got, c.want)
		}
	}
}
