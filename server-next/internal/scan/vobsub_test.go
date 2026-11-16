package scan_test

import (
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

// A DVD subtitle has one selectable track, backed by both files. Losing its
// companion hides it; putting the companion back restores that same track.
func TestVobSubPairIsOneTrackAndOrphansAreNotOffered(t *testing.T) {
	f := newFixture(t, db.LibraryMovies)
	for _, name := range []string{"Film (2020).mkv", "Film (2020).en.idx", "Film (2020).en.sub", "Film (2020).fr.idx", "Film (2020).de.sub"} {
		f.write(name)
	}
	f.scan()
	id := f.titles(db.KindMovie)["Film"].PublicID
	tracks := func() []db.Track {
		t.Helper()
		item, err := f.d.PlaybackItem(ctx, f.viewer, id)
		if err != nil {
			t.Fatal(err)
		}
		var out []db.Track
		for _, track := range item.Versions[0].Tracks {
			if track.Kind == db.StreamSubtitle {
				out = append(out, track)
			}
		}
		return out
	}
	first := tracks()
	if len(first) != 1 || first[0].Codec != "idx" || first[0].Language != "en" {
		t.Fatalf("the pair must be one English DVD subtitle, no orphans: %+v", first)
	}
	f.remove("Film (2020).en.sub")
	f.scan()
	if got := tracks(); len(got) != 0 {
		t.Fatalf("missing companion still offered: %+v", got)
	}
	f.write("Film (2020).en.sub")
	f.scan()
	if got := tracks(); len(got) != 1 || got[0].FileID != first[0].FileID {
		t.Fatalf("restored companion changed the track: %+v", got)
	}
}
