package scan_test

import (
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

// tagged writes a small MP3-named file with an ID3 tag. Values are in pairs: frame id, text.
func (f *fixture) tagged(rel string, frames ...string) {
	f.t.Helper()
	var body []byte
	for i := 0; i+1 < len(frames); i += 2 {
		text := append([]byte{3}, frames[i+1]...) // 3: UTF-8
		head := append([]byte(frames[i]), make([]byte, 6)...)
		binary.BigEndian.PutUint32(head[4:], uint32(len(text)))
		body = append(body, append(head, text...)...)
	}
	f.sizes++
	raw := []byte{'I', 'D', '3', 3, 0, 0, 0, 0, 0, 0}
	for i, n := 9, len(body); i >= 6; i-- {
		raw[i] = byte(n & 127)
		n >>= 7
	}
	raw = append(append(raw, body...), make([]byte, 64+f.sizes)...)
	full := filepath.Join(f.root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(full, raw, 0o644); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) text(rel, content string) {
	f.t.Helper()
	full := filepath.Join(f.root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) byTitle(parent string) map[string]db.Item {
	f.t.Helper()
	page, err := f.d.Items(ctx, f.viewer, db.ItemQuery{Parent: parent, Limit: 200})
	if err != nil {
		f.t.Fatal(err)
	}
	out := map[string]db.Item{}
	for _, item := range page.Items {
		if _, twice := out[item.Title]; twice {
			f.t.Fatalf("under %s: %q is there twice", parent, item.Title)
		}
		out[item.Title] = item
	}
	return out
}

// Music comes from its tags first and from Artist/Album/01 - Track otherwise; disc folders are
// discs of one album; one file with a cue sheet is an album of tracks.
func TestMusicIsReadFromTagsThenFolders(t *testing.T) {
	f := newFixture(t, db.LibraryMusic)
	// Tags win over the folders the files happen to be in.
	f.tagged("downloads/a.mp3", "TIT2", "Airbag", "TPE1", "Radiohead", "TALB", "OK Computer", "TRCK", "1/12", "TYER", "1997")
	f.tagged("downloads/b.mp3", "TIT2", "Paranoid Android", "TPE1", "Radiohead", "TALB", "OK Computer", "TRCK", "2/12")
	f.tagged("downloads/a.lrc")
	// No tags: the folders say it.
	f.write("Portishead/Dummy/01 - Mysterons.flac")
	f.write("Portishead/Dummy/02 - Sour Times.flac")
	f.write("Portishead/Third/CD1/01 - Silence.flac")
	f.write("Portishead/Third/CD2/01 - Threads.flac")
	// A whole album in one file.
	f.write("Boards of Canada/Geogaddi/Geogaddi.flac")
	f.text("Boards of Canada/Geogaddi/Geogaddi.cue", "FILE \"Geogaddi.flac\" WAVE\n  TRACK 01 AUDIO\n    TITLE \"Ready Lets Go\"\n    INDEX 01 00:00:00\n"+
		"  TRACK 02 AUDIO\n    TITLE \"Music Is Math\"\n    INDEX 01 00:59:00\n  TRACK 03 AUDIO\n    TITLE \"Beware the Friendly Stranger\"\n    INDEX 01 06:20:37\n")
	f.scan()
	artists := f.titles(db.KindArtist)
	if len(artists) != 3 {
		t.Fatalf("artists: %v", keys(artists))
	}
	ok := f.byTitle(artists["Radiohead"].PublicID)["OK Computer"]
	tracks := f.children(ok.PublicID)
	if ok.Year != 1997 || len(tracks) != 2 || tracks[1].Title != "Airbag" || tracks[2].Title != "Paranoid Android" {
		t.Fatalf("OK Computer: year %d, tracks %v", ok.Year, tracks)
	}
	albums := f.byTitle(artists["Portishead"].PublicID)
	if len(albums) != 2 || len(f.children(albums["Dummy"].PublicID)) != 2 {
		t.Fatalf("Portishead's albums: %v", keys(albums))
	}
	// Two discs of one album: the same track number on each, told apart by the disc.
	page, err := f.d.Items(ctx, f.viewer, db.ItemQuery{Parent: albums["Third"].PublicID})
	if err != nil || len(page.Items) != 2 || *page.Items[0].ParentIndex != 1 || *page.Items[1].ParentIndex != 2 {
		t.Fatalf("Third should have one track on each of two discs: %+v, %v", page.Items, err)
	}
	geogaddi := f.children(f.byTitle(artists["Boards of Canada"].PublicID)["Geogaddi"].PublicID)
	if len(geogaddi) != 3 || geogaddi[2].Title != "Music Is Math" {
		t.Fatalf("the cue album's tracks: %v", geogaddi)
	}
	second := f.detail(geogaddi[2].PublicID).Versions[0].Parts[0]
	if second.StartMS == nil || *second.StartMS != 59000 || second.EndMS == nil || *second.EndMS != 380493 {
		t.Errorf("track 2 of the cue album plays the wrong part: %v to %v", second.StartMS, second.EndMS)
	}

	// A moved file is the same track: nothing about it is read again or changes.
	airbag := tracks[1]
	yes := true
	if _, _, err := f.d.SetState(ctx, f.viewer, airbag.PublicID, db.StateEdit{Favorite: &yes}); err != nil {
		t.Fatal(err)
	}
	f.move("downloads", "Radiohead/OK Computer (1997)")
	stats := f.scan()
	if stats.FilesMoved != 2 || stats.ItemsNew != 0 || !f.detail(f.children(ok.PublicID)[1].PublicID).State.Favorite {
		t.Fatalf("after moving the album: %+v", stats)
	}
	if again := f.scan(); again.RowsWritten != 0 {
		t.Fatalf("an unchanged rescan wrote %d rows", again.RowsWritten)
	}
}

// An audiobook is its folder: its files are its parts in order across disc folders, and
// Audiobookshelf's metadata.json names it.
func TestAudiobooksAreReadAsAuthorSeriesBook(t *testing.T) {
	f := newFixture(t, db.LibraryAudiobooks)
	f.write("Ursula K. Le Guin/Earthsea/Book 1 - A Wizard of Earthsea/Disc 1/01.mp3")
	f.write("Ursula K. Le Guin/Earthsea/Book 1 - A Wizard of Earthsea/Disc 1/02.mp3")
	f.write("Ursula K. Le Guin/Earthsea/Book 1 - A Wizard of Earthsea/Disc 2/01.mp3")
	f.write("Frank Herbert/Dune/Dune.m4b")
	f.text("Frank Herbert/Dune/metadata.json", `{"title": "Dune", "authors": ["Frank Herbert"], "narrators": ["Scott Brick"],
		"publishedYear": "1965", "description": "Arrakis.", "chapters": [{"start": 0, "end": 30, "title": "Book One"}]}`)
	f.write("Loose book.m4b")
	f.scan()
	books := f.titles(db.KindAudiobook)
	if len(books) != 3 {
		t.Fatalf("books: %v", keys(books))
	}
	wizard := f.detail(books["A Wizard of Earthsea"].PublicID)
	if len(wizard.Versions) != 1 || len(wizard.Versions[0].Parts) != 3 || len(wizard.Credits) != 1 || wizard.Credits[0].Name != "Ursula K. Le Guin" {
		t.Fatalf("A Wizard of Earthsea: %d versions, credits %v", len(wizard.Versions), wizard.Credits)
	}
	dune := f.detail(books["Dune"].PublicID)
	if dune.Year != 1965 || dune.Summary != "Arrakis." || len(dune.Credits) != 2 || len(dune.Versions[0].Markers) != 1 {
		t.Errorf("Dune was not read from metadata.json: %+v", dune)
	}
	if series := f.titles(db.KindSeries); len(series) != 1 || series["Earthsea"].PublicID == "" {
		t.Errorf("series: %v", keys(series))
	}
	// A new file in a disc folder becomes a part of the same book.
	f.write("Ursula K. Le Guin/Earthsea/Book 1 - A Wizard of Earthsea/Disc 2/02.mp3")
	if stats := f.scan(); stats.ItemsNew != 0 || len(f.detail(wizard.PublicID).Versions[0].Parts) != 4 {
		t.Fatalf("a new part: %+v", stats)
	}
	if again := f.scan(); again.RowsWritten != 0 {
		t.Fatalf("an unchanged rescan wrote %d rows", again.RowsWritten)
	}
}

func TestBooksAndComicsAreReadFromNamesAndOPF(t *testing.T) {
	f := newFixture(t, db.LibraryBooks)
	f.write("Jane Austen/Emma (1815)/Emma - Jane Austen.epub")
	f.write("Jane Austen/Emma (1815)/Emma - Jane Austen.pdf")
	f.text("Jane Austen/Emma (1815)/metadata.opf", `<package xmlns:dc="http://purl.org/dc/elements/1.1/"><metadata><dc:title>Emma</dc:title>
		<dc:creator>Jane Austen</dc:creator><dc:date>1815-12-23</dc:date><dc:identifier>urn:isbn:9780141439587</dc:identifier></metadata></package>`)
	f.write("The Economist 2026-11-07.pdf")
	f.scan()
	books := f.titles(db.KindBook)
	emma := f.detail(books["Emma"].PublicID)
	if len(books) != 2 || len(emma.Versions) != 2 || emma.Year != 1815 || len(emma.Credits) != 1 || len(emma.ExternalIDs) != 1 {
		t.Fatalf("books %v; Emma: %d versions, year %d, credits %v", keys(books), len(emma.Versions), emma.Year, emma.Credits)
	}

	c := newFixture(t, db.LibraryComics)
	c.write("Saga/Saga 001 (2012).cbz")
	c.write("Saga/Saga 002 (2012).cbz")
	c.write("Watchmen.cbz")
	c.scan()
	saga := c.children(c.titles(db.KindSeries)["Saga"].PublicID)
	if len(saga) != 2 || saga[2].PublicID == "" || c.titles(db.KindComic)["Watchmen"].PublicID == "" {
		t.Fatalf("comics: %v issues of Saga, %v", len(saga), keys(c.titles(db.KindComic)))
	}
}

// A cue album is not limited to sixteen tracks, and every track has its own place in the file.
func TestACueAlbumOfTwentyTracks(t *testing.T) {
	f := newFixture(t, db.LibraryMusic)
	f.write("Artist/Long Album/Long Album.flac")
	sheet := "FILE \"Long Album.flac\" WAVE\n"
	for n := 1; n <= 20; n++ {
		sheet += fmt.Sprintf("  TRACK %02d AUDIO\n    TITLE \"Song %d\"\n    INDEX 01 %02d:00:00\n", n, n, n-1)
	}
	f.text("Artist/Long Album/Long Album.cue", sheet)
	f.scan()
	tracks := f.children(f.byTitle(f.titles(db.KindArtist)["Artist"].PublicID)["Long Album"].PublicID)
	starts := map[int64]bool{}
	for _, track := range tracks {
		starts[*f.detail(track.PublicID).Versions[0].Parts[0].StartMS] = true
	}
	if len(tracks) != 20 || len(starts) != 20 || tracks[20].Title != "Song 20" {
		t.Fatalf("%d tracks at %d places; want 20 of each", len(tracks), len(starts))
	}
}
