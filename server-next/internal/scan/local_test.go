package scan_test

import (
	"bytes"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

// pictures is a picture store in memory.
type pictures map[string][]byte

func (p pictures) Put(id string, data []byte) error { p[id] = data; return nil }

func (f *fixture) put(rel string, data []byte) {
	f.t.Helper()
	full := filepath.Join(f.root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(full, data, 0o644); err != nil {
		f.t.Fatal(err)
	}
}

// picture is a real PNG, w pixels wide. Each width is a different file.
func picture(t *testing.T, w int) []byte {
	var out bytes.Buffer
	if err := png.Encode(&out, image.NewGray(image.Rect(0, 0, w, 3))); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

// shown is the picture an item shows for a role, or nil.
func shown(item db.Item, role db.ImageRole) *db.Image {
	for i := range item.Images {
		if item.Images[i].Role == role {
			return &item.Images[i]
		}
	}
	return nil
}

func TestAFilmsNFOFileAndPicturesAreReadAndReadAgain(t *testing.T) {
	f := newFixture(t, db.LibraryMovies)
	store := pictures{}
	f.s.Pictures = store
	f.write("Heat (1995)/Heat (1995).mkv")
	f.write("Other (2001).mkv")
	f.put("Heat (1995)/movie.nfo", []byte(`<movie><title>Heat</title><plot>A thief and a detective.</plot><mpaa>R</mpaa><genre>Crime</genre>
		<actor><name>Al Pacino</name><role>Vincent Hanna</role></actor><director>Michael Mann</director></movie>`))
	f.put("Heat (1995)/poster.jpg", picture(t, 10))
	f.put("Heat (1995)/fanart.jpg", picture(t, 20))
	f.put("poster.jpg", picture(t, 30)) // at the top of the library it is nobody's
	f.scan()

	heat := f.detail(f.film("Heat").PublicID)
	if heat.Summary != "A thief and a detective." || heat.ContentRating != "R" || len(heat.Tags) != 1 || heat.CreditTotal != 2 {
		t.Fatalf("the NFO file was not read: %+v", heat)
	}
	poster, backdrop := shown(heat.Item, db.ImagePoster), shown(heat.Item, db.ImageBackdrop)
	if poster == nil || backdrop == nil || poster.Width != 10 || backdrop.Width != 20 || !bytes.Equal(store[poster.FileHash], picture(t, 10)) {
		t.Fatalf("pictures: %+v", heat.Images)
	}
	if other := f.film("Other"); len(other.Images) != 0 {
		t.Errorf("a film took a picture that is not its own: %+v", other.Images)
	}
	f.watch(heat.PublicID)
	if stats := f.scan(); stats.RowsWritten != 0 {
		t.Fatalf("an unchanged rescan wrote %d rows", stats.RowsWritten)
	}

	// The NFO file changes and the poster is replaced: the same title, with the new details.
	f.put("Heat (1995)/movie.nfo", []byte(`<movie><title>Heat: Director's Definitive Edition</title><plot>Rewritten.</plot><mpaa>PG-13</mpaa></movie>`))
	f.put("Heat (1995)/poster.jpg", picture(t, 11))
	f.scan()
	again := f.detail(f.film("Heat: Director's Definitive Edition").PublicID)
	if again.PublicID != heat.PublicID || !again.State.Watched {
		t.Fatal("reading the NFO file again made a new title or lost its state")
	}
	if again.Summary != "Rewritten." || again.ContentRating != "PG-13" {
		t.Errorf("details after the change: %+v", again)
	}
	if poster := shown(again.Item, db.ImagePoster); poster == nil || poster.Width != 11 {
		t.Errorf("poster after the change: %+v", again.Images)
	}
	if backdrop := shown(again.Item, db.ImageBackdrop); backdrop == nil || backdrop.Width != 20 {
		t.Errorf("the backdrop nobody touched: %+v", again.Images)
	}

	// The poster goes: a second picture for the role takes over, and then none is shown.
	f.put("Heat (1995)/folder.jpg", picture(t, 12))
	f.scan()
	f.remove("Heat (1995)/poster.jpg")
	f.scan()
	if poster := shown(f.film("Heat: Director's Definitive Edition"), db.ImagePoster); poster == nil || poster.Width != 12 {
		t.Errorf("the next best poster is not shown: %+v", poster)
	}
	f.remove("Heat (1995)/folder.jpg")
	f.scan()
	if poster := shown(f.film("Heat: Director's Definitive Edition"), db.ImagePoster); poster != nil {
		t.Errorf("a picture whose file is gone is still shown: %+v", poster)
	}
	if stats := f.scan(); stats.RowsWritten != 0 {
		t.Fatalf("an unchanged rescan wrote %d rows", stats.RowsWritten)
	}
}

func TestAShowsFolderDescribesTheShowItsSeasonsAndEpisodes(t *testing.T) {
	f := newFixture(t, db.LibraryShows)
	f.s.Pictures = pictures{}
	f.write("The Wire/Season 01/The Wire - S01E01.mkv")
	f.write("The Wire/Season 02/The Wire - S02E01.mkv")
	f.put("The Wire/tvshow.nfo", []byte(`<tvshow><title>The Wire</title><plot>Baltimore.</plot></tvshow>`))
	f.put("The Wire/poster.jpg", picture(t, 10))
	f.put("The Wire/season02-poster.jpg", picture(t, 12))
	f.put("The Wire/Season 01/poster.jpg", picture(t, 11))
	f.put("The Wire/Season 01/The Wire - S01E01.nfo", []byte(`<episodedetails><title>The Target</title><plot>A trial.</plot></episodedetails>`))
	f.put("The Wire/Season 01/The Wire - S01E01-thumb.jpg", picture(t, 13))
	f.scan()

	show := f.titles(db.KindShow)["The Wire"]
	if detail := f.detail(show.PublicID); detail.Summary != "Baltimore." {
		t.Errorf("show: %+v", detail)
	}
	if poster := shown(show, db.ImagePoster); poster == nil || poster.Width != 10 {
		t.Errorf("show poster: %+v", show.Images)
	}
	seasons := f.titles(db.KindSeason)
	for name, width := range map[string]int{"Season 1": 11, "Season 2": 12} {
		if poster := shown(seasons[name], db.ImagePoster); poster == nil || poster.Width != width {
			t.Errorf("%s poster: %+v", name, seasons[name].Images)
		}
	}
	episode := f.titles(db.KindEpisode)["The Target"]
	if thumb := shown(episode, db.ImageThumb); thumb == nil || thumb.Width != 13 || f.detail(episode.PublicID).Summary != "A trial." {
		t.Errorf("episode: %+v", episode)
	}
	if stats := f.scan(); stats.RowsWritten != 0 {
		t.Fatalf("an unchanged rescan wrote %d rows", stats.RowsWritten)
	}

	// Later changes in the show's folder, and beside an episode, reach the titles they are for.
	f.put("The Wire/tvshow.nfo", []byte(`<tvshow><title>The Wire</title><plot>Baltimore, again.</plot></tvshow>`))
	f.put("The Wire/season02-poster.jpg", picture(t, 22))
	f.put("The Wire/Season 01/The Wire - S01E01.nfo", []byte(`<episodedetails><title>The Target (Pilot)</title></episodedetails>`))
	f.scan()
	if detail := f.detail(show.PublicID); detail.Summary != "Baltimore, again." {
		t.Errorf("show after its NFO file changed: %+v", detail.Summary)
	}
	if poster := shown(f.titles(db.KindSeason)["Season 2"], db.ImagePoster); poster == nil || poster.Width != 22 {
		t.Errorf("season poster after it changed: %+v", poster)
	}
	if renamed, ok := f.titles(db.KindEpisode)["The Target (Pilot)"]; !ok || renamed.PublicID != episode.PublicID {
		t.Errorf("episode after its NFO file changed: %v", keys(f.titles(db.KindEpisode)))
	}
}
