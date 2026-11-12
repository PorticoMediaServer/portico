package scan_test

import (
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

// Scan all files treats every file as changed, in every kind of library. Each title is read
// again where it is: none is made twice (03-scanning rule 29), and none loses its id or a
// person's watched mark (music has play counts instead).
func TestScanAllFilesKeepsEveryTitleAndItsState(t *testing.T) {
	for _, c := range []struct {
		kind  string
		files func(f *fixture)
		top   db.ItemKind
	}{
		{db.LibraryMovies, func(f *fixture) {
			f.write("Heat (1995)/Heat (1995).mkv")
			f.write("Heat (1995)/Heat (1995) - 4K.mkv")
			f.text("Heat (1995)/Heat (1995).nfo", `<movie><title>Heat</title><year>1995</year></movie>`)
			f.write("Alien (1979)/Alien (1979).mkv")
		}, db.KindMovie},
		{db.LibraryShows, func(f *fixture) {
			f.write("Lost/Season 1/Lost - S01E01.mkv")
			f.write("Lost/Season 1/Lost - S01E02.mkv")
		}, db.KindShow},
		{db.LibraryMusic, func(f *fixture) {
			f.tagged("ok/a.mp3", "TIT2", "Airbag", "TPE1", "Radiohead", "TALB", "OK Computer", "TRCK", "1/12", "TYER", "1997")
			f.tagged("ok/b.mp3", "TIT2", "Paranoid Android", "TPE1", "Radiohead", "TALB", "OK Computer", "TRCK", "2/12")
		}, db.KindAlbum},
	} {
		t.Run(c.kind, func(t *testing.T) {
			f := newFixture(t, c.kind)
			c.files(f)
			f.scan()
			before := f.titles(c.top)
			// Music has play counts, not a watched mark.
			watchable := c.kind != db.LibraryMusic
			for _, it := range before {
				if watchable {
					f.watch(it.PublicID)
				}
			}
			f.s.AllFiles = true
			f.scan()
			after := f.titles(c.top)
			if len(after) != len(before) || len(before) == 0 {
				t.Fatalf("%d titles before, %d after", len(before), len(after))
			}
			for title, it := range before {
				if after[title].PublicID != it.PublicID || watchable && !f.watched(it.PublicID) {
					t.Errorf("%q: id %s → %s", title, it.PublicID, after[title].PublicID)
				}
			}
		})
	}
}
