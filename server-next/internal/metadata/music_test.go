package metadata_test

import (
	"path/filepath"
	"slices"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/metadata"
	"github.com/PorticoMediaServer/portico/server-next/internal/metadata/musicbrainz"
	"github.com/PorticoMediaServer/portico/server-next/internal/metadata/musicbrainz/musicbrainztest"
	"github.com/PorticoMediaServer/portico/server-next/internal/seed"
)

// musicLibrary makes a music library holding The Beatles' Abbey Road (two tracks named only by
// number, filed under 1969), Help! (one track, with its MusicBrainz id in its tags) and Losing It
// (one track, filed with the wrong artist, whose tags carry a MusicBrainz id that was later
// merged into another).
func musicLibrary(t *testing.T) (*db.DB, db.Library, db.Viewer) {
	t.Helper()
	d, err := db.Open(ctx, filepath.Join(t.TempDir(), "main.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	if _, err := d.CreateLibrary(ctx, "Music", db.LibraryMusic, "en", []string{t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	viewers, err := seed.Profiles(ctx, d, 1)
	if err != nil {
		t.Fatal(err)
	}
	libraries, err := d.AllLibraries(ctx)
	if err != nil {
		t.Fatal(err)
	}
	lib := libraries[0]
	one := 1
	artist := db.ItemKey{Kind: db.KeyName, Key: "artist|the beatles"}
	batch := db.ImportBatch{LibraryID: lib.ID, PathID: lib.Paths[0].ID, Scan: 1,
		Dirs:  []db.ImportDir{{RelPath: ""}, {RelPath: "The Beatles"}, {RelPath: "The Beatles/Abbey Road"}, {RelPath: "The Beatles/Help!"}, {RelPath: "The Beatles/Losing It"}},
		Items: []db.ImportItem{{Kind: db.KindArtist, Title: "The Beatles", Keys: []db.ItemKey{artist}}}}
	albums := []struct {
		title  string
		year   int
		tracks int
		ids    []db.ExternalID
	}{{"Abbey Road", 1969, 2, nil}, {"Help!", 0, 1, []db.ExternalID{{Provider: "musicbrainz", ID: "2c053984-4645-4699-9474-d2c35c227028"}}},
		{"Losing It", 0, 1, []db.ExternalID{{Provider: "musicbrainz", ID: "a82fd9b7-4129-4e44-8867-a1a21493ee8e"}}}}
	for _, a := range albums {
		dir := "The Beatles/" + a.title
		album := db.ItemKey{Kind: db.KeyFolder, Key: dir}
		batch.Items = append(batch.Items, db.ImportItem{Kind: db.KindAlbum, Title: a.title, Year: a.year, Parent: &artist,
			Keys: []db.ItemKey{album}, ExternalIDs: a.ids})
		for n := 1; n <= a.tracks; n++ {
			ref := db.FileRef{Dir: dir, Name: "0" + string(rune('0'+n)) + ".flac"}
			batch.Files = append(batch.Files, db.ImportFile{FileRef: ref, Role: db.FileMedia, Size: 1000, Container: "flac", DurationMS: 1000})
			index := n
			batch.Items = append(batch.Items, db.ImportItem{Kind: db.KindTrack, Title: "Track " + string(rune('0'+n)), Parent: &album,
				IndexNumber: &index, ParentIndex: &one, Keys: []db.ItemKey{{Kind: db.KeyFile, Key: dir + "/" + ref.Name}},
				Versions: []db.ImportVersion{{Parts: []db.ImportPart{{File: ref}}}}})
		}
	}
	if _, err := d.Import(ctx, batch); err != nil {
		t.Fatal(err)
	}
	return d, lib, viewers[0]
}

func TestAnAlbumIsMatchedWithItsTracksAndArtist(t *testing.T) {
	d, lib, viewer := musicLibrary(t)
	server := musicbrainztest.New(t)
	f := &fixture{t: t, d: d, lib: lib, viewer: viewer,
		s: &metadata.Service{DB: d, Providers: map[string]metadata.Provider{"musicbrainz": musicbrainz.New(server.URL, 0)}}}

	// Two editions of Abbey Road answer the search: they are one album, and the 1969 one is
	// taken. Help! is looked up by the id in its tags, without a search.
	if stats := f.match(); stats.Matched != 3 || stats.Unmatched != 0 {
		t.Fatalf("matched %d, unmatched %d; want 3 and 0", stats.Matched, stats.Unmatched)
	}
	album := f.title(db.KindAlbum, "Abbey Road")
	if album.Year != 1969 || album.ReleaseDate != "1969-09-26" || !slices.Contains(album.ExternalIDs,
		db.ExternalID{Provider: "musicbrainz", ID: "d6010be3-98f8-422c-a6c9-787e2e491e58"}) {
		t.Errorf("album: %+v", album)
	}
	// The genres with at least a tenth of the votes of the most-voted, rock; not ambient or dance.
	if got := tagNames(album, db.TagGenre); !slices.Equal(got, []string{"classic rock", "pop", "pop rock", "psychedelic", "psychedelic rock", "rock"}) {
		t.Errorf("genres: %q", got)
	}
	if got := tagNames(album, db.TagStudio); !slices.Equal(got, []string{"Apple Records"}) {
		t.Errorf("label: %q", got)
	}
	// The tracks named only by number get their names; the album's id gave Help! its track.
	f.title(db.KindTrack, "Come Together")
	f.title(db.KindTrack, "Something")
	f.title(db.KindTrack, "Help!")
	// The artist the albums are credited to is the library's artist, asked about once.
	artist := f.title(db.KindArtist, "The Beatles")
	if got := tagNames(artist, db.TagCountry); !slices.Equal(got, []string{"United Kingdom"}) ||
		!slices.Contains(artist.ExternalIDs, db.ExternalID{Provider: "musicbrainz", ID: "b10bbbfc-cf9e-42e0-be17-e2c3e1d2600d"}) {
		t.Errorf("artist: %+v", artist)
	}
	// MusicBrainz answers the merged id with 301 and the release it was merged into: that is
	// the album now, under its current id, and the run carries on.
	merged := f.title(db.KindAlbum, "Losing It")
	if !slices.Contains(merged.ExternalIDs, db.ExternalID{Provider: "musicbrainz", ID: "114bdc29-02ca-45e8-83cb-9b2dae354e20"}) {
		t.Errorf("merged album: %+v", merged.ExternalIDs)
	}
	f.title(db.KindTrack, "Losing It")
	// Search, Abbey Road (its tracks are the same answer), the artist, Help!, Losing It.
	if n := server.Requests.Load(); n != 5 {
		t.Errorf("%d requests, want 5", n)
	}
}
