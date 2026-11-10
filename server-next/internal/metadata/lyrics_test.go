package metadata_test

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/metadata"
	"github.com/PorticoMediaServer/portico/server-next/internal/metadata/lrclib"
	"github.com/PorticoMediaServer/portico/server-next/internal/metadata/lrclib/lrclibtest"
)

// songs makes a music library with two songs of one album and returns their ids by title.
func songs(t *testing.T) (*db.DB, db.Library, map[string]int64) {
	t.Helper()
	d, err := db.Open(ctx, filepath.Join(t.TempDir(), "main.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	if _, err := d.CreateLibrary(ctx, "Music", db.LibraryMusic, "en", []string{t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	libraries, err := d.AllLibraries(ctx)
	if err != nil {
		t.Fatal(err)
	}
	lib := libraries[0]
	artist := db.ItemKey{Kind: db.KeyName, Key: "artist"}
	album := db.ItemKey{Kind: db.KeyName, Key: "album"}
	batch := db.ImportBatch{LibraryID: lib.ID, PathID: lib.Paths[0].ID, Scan: 1, Dirs: []db.ImportDir{{RelPath: ""}},
		Items: []db.ImportItem{{Kind: db.KindArtist, Title: "The Beatles", Keys: []db.ItemKey{artist}},
			{Kind: db.KindAlbum, Title: "Abbey Road", Parent: &artist, Keys: []db.ItemKey{album}}}}
	for n, title := range []string{"Come Together", "Something"} {
		ref := db.FileRef{Name: title + ".flac"}
		batch.Files = append(batch.Files, db.ImportFile{FileRef: ref, Role: db.FileMedia, Size: 1000, Container: "flac", DurationMS: 260_000})
		index, disc := n+1, 1
		batch.Items = append(batch.Items, db.ImportItem{Kind: db.KindTrack, Title: title, Parent: &album, IndexNumber: &index, ParentIndex: &disc,
			Keys: []db.ItemKey{{Kind: db.KeyFile, Key: ref.Name}}, Versions: []db.ImportVersion{{Parts: []db.ImportPart{{File: ref}}}}})
	}
	if _, err := d.Import(ctx, batch); err != nil {
		t.Fatal(err)
	}
	ids := map[string]int64{}
	page, err := d.SongsWithoutLyrics(ctx, lib.ID, time.Now(), nil, 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, song := range page {
		ids[song.Title] = song.ID
	}
	return d, lib, ids
}

func TestLyricsAreFetchedOnlyWhereTheOwnerChoseAndPastedLyricsWin(t *testing.T) {
	d, lib, ids := songs(t)
	server := lrclibtest.New(t)
	s := &metadata.Service{DB: d, Lyrics: lrclib.New(server.URL, 0)}

	// Files only, the default: nothing is fetched by itself.
	if n, err := s.FetchMissingLyrics(ctx, lib.ID, false); err != nil || n != 0 || server.Requests.Load() != 0 {
		t.Fatalf("a Files-only library fetched lyrics: %d, %v, %d requests", n, err, server.Requests.Load())
	}

	// Files, then LRCLIB: one song gets timed lyrics, the other is recorded as having none
	// and is not asked about again at the next run.
	if err := d.SetLibraryOptions(ctx, lib.ID, db.LibraryOptions{Lyrics: db.LyricsLRCLIB}); err != nil {
		t.Fatal(err)
	}
	if n, err := s.FetchMissingLyrics(ctx, lib.ID, false); err != nil || n != 1 {
		t.Fatalf("fetched %d, %v; want 1", n, err)
	}
	if got, err := d.SongLyrics(ctx, ids["Come Together"]); err != nil || !got.Timed || got.Source != db.LyricsFromProvider ||
		got.Body != "[00:01.00] First line\n[00:05.50] Second line" {
		t.Errorf("Come Together: %+v, %v", got, err)
	}
	asked := server.Requests.Load()
	if n, err := s.FetchMissingLyrics(ctx, lib.ID, false); err != nil || n != 0 || server.Requests.Load() != asked {
		t.Errorf("a second run asked again: %d, %v, %d requests", n, err, server.Requests.Load()-asked)
	}

	// Pasted lyrics are what everyone sees: a fetch by hand does not replace them.
	pasted := db.Lyrics{Source: db.LyricsPasted, Body: "Our own words"}
	if stored, err := d.SetLyrics(ctx, db.User, ids["Come Together"], pasted); err != nil || !stored {
		t.Fatalf("pasting: %v, %v", stored, err)
	}
	if stored, err := s.FetchLyrics(ctx, ids["Come Together"]); err != nil || stored {
		t.Errorf("a fetch replaced pasted lyrics: %v, %v", stored, err)
	}
	if got, _ := d.SongLyrics(ctx, ids["Come Together"]); got.Body != "Our own words" || got.Source != db.LyricsPasted {
		t.Errorf("after a fetch: %+v", got)
	}
	// Lyrics are only ever a song's.
	if _, err := d.SetLyrics(ctx, db.User, 1, pasted); !errors.Is(err, db.ErrNotFound) {
		t.Errorf("lyrics on an artist: %v", err)
	}
}
