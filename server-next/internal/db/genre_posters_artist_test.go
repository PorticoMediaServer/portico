package db_test

import (
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

// A music library's genre that only an artist carries is counted over that artist's albums,
// and so its posters are those albums' covers.
func TestAGenreOnlyAnArtistCarriesHasItsAlbumsCovers(t *testing.T) {
	d, err := db.Open(ctx, filepath.Join(t.TempDir(), "main.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	music, err := d.CreateLibrary(ctx, "Music", db.LibraryMusic, "en", []string{t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	artist := db.ItemKey{Kind: db.KeyName, Key: "artist|drone"}
	album := func(title, hash string) db.ImportItem {
		return db.ImportItem{Kind: db.KindAlbum, Keys: []db.ItemKey{{Kind: db.KeyName, Key: "album|" + title}}, Parent: &artist, Title: title,
			Images: []db.ImportImage{{Role: db.ImageCover, Source: db.ImageFromEmbedded, FileHash: hash, Width: 600, Height: 600}}}
	}
	if _, err := d.Import(ctx, db.ImportBatch{LibraryID: music.ID, PathID: music.Paths[0].ID, Scan: 1, Dirs: []db.ImportDir{{}}, Items: []db.ImportItem{
		{Kind: db.KindArtist, Keys: []db.ItemKey{artist}, Title: "Hum", Tags: []db.TagName{{Kind: db.TagGenre, Name: "Drone"}}},
		album("Low", "aaaa"), album("Lower", "bbbb"),
	}}); err != nil {
		t.Fatal(err)
	}
	v, err := d.CreateSeedProfile(ctx, db.SeedProfile{Name: "everyone"})
	if err != nil {
		t.Fatal(err)
	}
	genres, err := d.Genres(ctx, v, music.PublicID, false)
	if err != nil || len(genres) != 1 {
		t.Fatalf("genres: %+v, %v", genres, err)
	}
	if g := genres[0]; g.Count != 2 || len(g.Posters) != 2 {
		t.Errorf("the artist's genre counts %d albums and has posters %v; want 2 and both albums' covers", g.Count, g.Posters)
	}
}
