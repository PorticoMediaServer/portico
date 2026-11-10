package scan_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

// An album's album.nfo speaks over its files' tags, its cover is the picture beside it before
// the one inside its files, and what the files and album.nfo gave is never replaced by a
// provider's answer. A changed album.nfo or cover is read again.
func TestAnAlbumsNFOFileAndCoversAreReadAndKept(t *testing.T) {
	f := newFixture(t, db.LibraryMusic)
	store := pictures{}
	f.s.Pictures = store
	inside := picture(t, 40)
	// APIC: UTF-8, the picture's type, "front cover", no description, the picture.
	f.tagged("Air/Moon Safari/01.mp3", "TIT2", "La Femme d'Argent", "TPE1", "Air", "TALB", "Moon Safari", "TRCK", "1", "TYER", "1998",
		"APIC", "image/png\x00\x03\x00"+string(inside))
	f.tagged("Air/Moon Safari/02.mp3", "TIT2", "Sexy Boy", "TPE1", "Air", "TALB", "Moon Safari", "TRCK", "2")
	f.text("Air/Moon Safari/album.nfo", `<album><title>Moon Safari (Remastered)</title><genre>Electronic</genre><year>1998</year>
		<review>Debut album.</review></album>`)
	f.put("Air/Moon Safari/cover.jpg", picture(t, 50))
	f.scan()

	album := f.titles(db.KindAlbum)["Moon Safari (Remastered)"]
	detail := f.detail(album.PublicID)
	if detail.Summary != "Debut album." || detail.Year != 1998 || len(detail.Tags) != 1 || detail.Tags[0].Name != "Electronic" {
		t.Fatalf("album.nfo was not read: %+v", detail)
	}
	if cover := shown(album, db.ImageCover); cover == nil || cover.Width != 50 {
		t.Errorf("the cover beside the files is not the one shown: %+v", album.Images)
	}
	sum := sha256.Sum256(inside)
	if !bytes.Equal(store[hex.EncodeToString(sum[:])], inside) {
		t.Error("the cover inside the file was not kept")
	}

	// A provider's answer fills only what the files did not give.
	if err := f.d.ApplyDetails(ctx, db.Background, album.ID, db.ItemDetails{Match: db.MatchFound, Title: "Moon Safari",
		Year: 2000, Summary: "A provider's words.", ReleaseDate: "1998-01-16", Tags: []db.TagName{{Kind: db.TagGenre, Name: "Pop"}}}); err != nil {
		t.Fatal(err)
	}
	if got := f.detail(album.PublicID); got.Title != "Moon Safari (Remastered)" || got.Year != 1998 || got.Summary != "Debut album." ||
		got.ReleaseDate != "1998-01-16" || len(got.Tags) != 1 || got.Tags[0].Name != "Electronic" {
		t.Errorf("a provider replaced what the files gave: %+v", got)
	}
	track := f.titles(db.KindTrack)["Sexy Boy"]
	if err := f.d.ApplyDetails(ctx, db.Background, track.ID, db.ItemDetails{Match: db.MatchFound, Title: "Sexy boy"}); err != nil {
		t.Fatal(err)
	}
	if got := f.detail(track.PublicID); got.Title != "Sexy Boy" {
		t.Errorf("a provider replaced a track's tagged title: %q", got.Title)
	}
	if stats := f.scan(); stats.RowsWritten != 0 {
		t.Fatalf("an unchanged rescan wrote %d rows", stats.RowsWritten)
	}

	// album.nfo and the cover change: the same album, read again.
	f.text("Air/Moon Safari/album.nfo", `<album><title>Moon Safari</title><genre>Downtempo</genre></album>`)
	f.put("Air/Moon Safari/cover.jpg", picture(t, 51))
	f.scan()
	again, ok := f.titles(db.KindAlbum)["Moon Safari"]
	if !ok || again.PublicID != album.PublicID {
		t.Fatalf("reading album.nfo again: %v", f.titles(db.KindAlbum))
	}
	if cover := shown(again, db.ImageCover); cover == nil || cover.Width != 51 {
		t.Errorf("the cover after the change: %+v", again.Images)
	}
	if got := f.detail(again.PublicID); len(got.Tags) != 1 || got.Tags[0].Name != "Downtempo" {
		t.Errorf("genres after the change: %+v", got.Tags)
	}
}

// Lyrics in a song's tags are kept with the song, timed ones following it, and only pasted
// lyrics replace them.
func TestLyricsInASongsTagsAreKept(t *testing.T) {
	f := newFixture(t, db.LibraryMusic)
	// USLT: UTF-8, the language, no description, the words.
	f.tagged("Air/Moon Safari/01.mp3", "TIT2", "Plain", "TPE1", "Air", "TALB", "Moon Safari", "TRCK", "1", "USLT", "eng\x00First line\nSecond line")
	f.tagged("Air/Moon Safari/02.mp3", "TIT2", "Timed", "TPE1", "Air", "TALB", "Moon Safari", "TRCK", "2", "USLT", "eng\x00[00:01.00]First line")
	f.scan()
	tracks := f.titles(db.KindTrack)
	plain, err := f.d.SongLyrics(ctx, tracks["Plain"].ID)
	if err != nil || plain.Body != "First line\nSecond line" || plain.Timed || plain.Source != db.LyricsFromFile || plain.Language != "eng" {
		t.Fatalf("plain lyrics: %+v, %v", plain, err)
	}
	if timed, err := f.d.SongLyrics(ctx, tracks["Timed"].ID); err != nil || !timed.Timed {
		t.Errorf("timed lyrics: %+v, %v", timed, err)
	}
	if stored, err := f.d.SetLyrics(ctx, db.Background, tracks["Plain"].ID, db.Lyrics{Source: db.LyricsFromProvider, Body: "Other words"}); err != nil || stored {
		t.Errorf("a provider replaced the file's lyrics: %v, %v", stored, err)
	}
	if stored, err := f.d.SetLyrics(ctx, db.User, tracks["Plain"].ID, db.Lyrics{Source: db.LyricsPasted, Body: "Pasted"}); err != nil || !stored {
		t.Errorf("pasted lyrics were refused: %v, %v", stored, err)
	}
}
