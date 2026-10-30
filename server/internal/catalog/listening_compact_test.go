package catalog

import (
	"context"
	"database/sql"
	"fmt"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

func phase34ListeningFixture(t *testing.T) (*Service, *catalogtest.Catalog, catalogtest.Names) {
	t.Helper()
	c := catalogtest.Open(t)
	music := c.Library("music", "Music", "music", "/music")
	other := c.Library("other", "Other music", "music", "/other")
	books := c.Library("books", "Books", "audiobook", "/books")
	names := catalogtest.Names{}
	names["artist"] = c.Artist(music, "Artist")
	names["other-artist"] = c.Artist(other, "Artist")
	names["album"] = c.Album(names["artist"], "Release", 2020)
	names["other-album"] = c.Album(names["other-artist"], "Release", 2020)
	names["book-a"] = c.Book(books, "Alpha", "Author")
	names["book-b"] = c.Book(books, "Zed", "Author")
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		for _, entry := range []struct{ name, localKey, position string }{{"book-a", "alpha|author", "2"}, {"book-b", "zed|author", "1"}} {
			book := names[entry.name]
			if err := compactcatalog.SetFactsTx(ctx, tx, book.ID, map[string]any{"library_id": books, "local_key": entry.localKey, "author": "Author"}); err != nil {
				return err
			}
		}
		return nil
	})
	names["part-a1"] = c.BookFile(names["book-a"], 1, "/books/part-a1.m4b")
	names["part-a2"] = c.BookFile(names["book-a"], 2, "/books/part-a2.m4b")
	names["part-b1"] = c.BookFile(names["book-b"], 1, "/books/part-b1.m4b")
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		for _, name := range []string{"part-a1", "part-a2", "part-b1"} {
			part := names[name]
			if err := compactcatalog.SetBookChaptersTx(ctx, tx, part.ID, []compactcatalog.Chapter{{Title: "Chapter", Start: 0, End: 60}}); err != nil {
				return err
			}
			seriesIndex := "2"
			if name == "part-b1" {
				seriesIndex = "1"
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO audio_tag_evidence(library_id,asset_id,field,source,value) VALUES('books',?,'series','portico_json','Saga'),('books',?,'series_index','portico_json',?)`, part.Token, part.Token, seriesIndex); err != nil {
				return err
			}
		}
		for i := 1; i <= 205; i++ {
			name := fmt.Sprintf("song-%03d", i)
			path := "/music/" + name + ".mp3"
			itemID, _, err := compactcatalog.UpsertEntityTx(ctx, tx, compactcatalog.Entity{
				Library: music, Kind: compactcatalog.Track, Parent: names["album"].ID,
				Key: compactcatalog.ItemKey("/music", path, 0), Title: fmt.Sprintf("Song %03d", 206-i), Added: "2026-01-01T00:00:00.000Z",
			})
			if err != nil {
				return err
			}
			if err = compactcatalog.SetFactsTx(ctx, tx, itemID, map[string]any{
				"album_id": names["album"].ID, "disc_number": 1 + (i-1)/100, "track_number": 1 + (i-1)%100,
			}); err != nil {
				return err
			}
			assetID, token, err := compactcatalog.UpsertAssetTx(ctx, tx, compactcatalog.Asset{Path: path, Size: 1000, ModifiedNS: 1, Container: "mp3", AudioCodec: "mp3", Duration: 300})
			if err != nil {
				return err
			}
			if err = compactcatalog.LinkAssetTx(ctx, tx, itemID, assetID, compactcatalog.Link{}); err != nil {
				return err
			}
			if i == 205 {
				if err = compactcatalog.SetAssetAvailableTx(ctx, tx, assetID, false); err != nil {
					return err
				}
			}
			if err = compactcatalog.SetSongArtistsTx(ctx, tx, itemID, []int64{names["artist"].ID}); err != nil {
				return err
			}
			names[name] = catalogtest.Item{ID: itemID, Asset: assetID, Token: token}
		}
		outsideID, _, err := compactcatalog.UpsertEntityTx(ctx, tx, compactcatalog.Entity{
			Library: other, Kind: compactcatalog.Track, Parent: names["other-album"].ID,
			Key: compactcatalog.ItemKey("/other", "/other/outside.mp3", 0), Title: "Outside", Added: "2026-01-01T00:00:00.000Z",
		})
		if err != nil {
			return err
		}
		if err = compactcatalog.SetFactsTx(ctx, tx, outsideID, map[string]any{"album_id": names["other-album"].ID, "track_number": 1}); err != nil {
			return err
		}
		assetID, token, err := compactcatalog.UpsertAssetTx(ctx, tx, compactcatalog.Asset{Path: "/other/outside.mp3", Size: 1000, ModifiedNS: 1, Container: "mp3", AudioCodec: "mp3", Duration: 300})
		if err != nil {
			return err
		}
		if err = compactcatalog.LinkAssetTx(ctx, tx, outsideID, assetID, compactcatalog.Link{}); err != nil {
			return err
		}
		if err = compactcatalog.SetSongArtistsTx(ctx, tx, outsideID, []int64{names["other-artist"].ID}); err != nil {
			return err
		}
		names["outside"] = catalogtest.Item{ID: outsideID, Asset: assetID, Token: token}
		return nil
	})
	for name, item := range names {
		if item.Public == "" {
			item.Public = c.Public(item.ID)
			names[name] = item
		}
	}
	c.Drain()
	s := New(c.DB)
	for {
		more, err := s.RefreshListeningGroups(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if !more {
			break
		}
	}
	c.Drain()
	return s, c, names
}

func phase34ListeningRequest(library, view, entity string) ContentRequest {
	return ContentRequest{Viewer: Viewer{Profile: "local:account:profile", Fence: "viewer", Libraries: []string{library}}, ServerID: "server", Library: library, Profile: "local:account:profile", ViewerFence: "viewer", View: view, EntityID: entity, Limit: 100}
}

func phase34Ceiling(age int) *int { return &age }

func phase34RestrictionOf(max *int, blockUnrated bool, labels ...string) identity.ContentRestrictions {
	return identity.ContentRestrictions{MaximumAge: max, BlockUnrated: blockUnrated, BlockedLabels: labels, Revision: 2}
}

func TestCompactListeningReadsProjectedFacts(t *testing.T) {
	c := catalogtest.Open(t)
	music := c.Library("music", "Music", "music", "/music")
	books := c.Library("books", "Books", "audiobook", "/books")
	names := catalogtest.Names{}
	names["artist"] = c.Artist(music, "Artist")
	names["guest"] = c.Artist(music, "Guest")
	names["album"] = c.Album(names["artist"], "Album", 2021)
	albumKey := fmt.Sprintf("album|%d", names["artist"].ID)
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		return compactcatalog.SetFactsTx(ctx, tx, names["album"].ID, map[string]any{"artist_id": names["artist"].ID, "local_key": albumKey, "provider_match_status": "matched"})
	})
	names["song"] = c.Song(names["album"], 2, "/music/song.mp3", "Song")
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		return compactcatalog.SetAssetTx(ctx, tx, names["song"].Asset, map[string]any{"duration": 180.25})
	})
	c.Fields(names["song"].ID, map[string]any{"poster_url": "local:song-art"})
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		return compactcatalog.SetSongArtistsTx(ctx, tx, names["song"].ID, []int64{names["artist"].ID, names["guest"].ID})
	})
	names["book"] = c.Book(books, "Book", "Author")
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		return compactcatalog.SetFactsTx(ctx, tx, names["book"].ID, map[string]any{"library_id": books, "local_key": "book|author", "author": "Author", "narrator": "Narrator", "provider_match_status": "matched"})
	})
	names["part"] = c.BookFile(names["book"], 3, "/books/part.m4b")
	c.Fields(names["part"].ID, map[string]any{"poster_url": "local:book-art"})
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		if err := compactcatalog.SetBookChaptersTx(ctx, tx, names["part"].ID, []compactcatalog.Chapter{{Title: "Opening", Start: 0, End: 2.5}}); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO audio_source_metadata(library_id,asset_id,issue) VALUES('music',?,'needs_review')`, names["song"].Token)
		return err
	})
	c.Drain()
	s := New(c.DB)
	viewer := Viewer{Profile: "profile", Fence: "f", Libraries: []string{"music", "books"}}
	artists, next, err := s.Artists(viewer, "music", "", 10)
	if err != nil || next != "" || len(artists) != 2 || artists[0].ID != names["artist"].Public || artists[1].ID != names["guest"].Public {
		t.Fatalf("artists: %+v %q %v", artists, next, err)
	}
	albums, next, err := s.Albums(viewer, names["artist"].Public, "", 10)
	if err != nil || next != "" || len(albums) != 1 || albums[0].ID != names["album"].Public || albums[0].AlbumArtist != "Artist" || albums[0].PosterURL != "/v1/items/"+names["song"].Public+"/art/poster" {
		t.Fatalf("albums: %+v %q %v", albums, next, err)
	}
	booksPage, next, err := s.Books(viewer, "books", "", 10)
	if err != nil || next != "" || len(booksPage) != 1 || booksPage[0].ID != names["book"].Public || booksPage[0].Author != "Author" || booksPage[0].PosterURL != "/v1/items/"+names["part"].Public+"/art/poster" {
		t.Fatalf("books: %+v %q %v", booksPage, next, err)
	}
	for _, entry := range []struct{ kind, id, library string }{{"artist", names["artist"].Public, "music"}, {"album", names["album"].Public, "music"}, {"book", names["book"].Public, "books"}} {
		got, err := s.LibraryForAudioEntity(entry.kind, entry.id)
		if err != nil || got != entry.library {
			t.Fatalf("library for %s: %q %v", entry.kind, got, err)
		}
	}
	songs, next, err := s.AudioItems(viewer, names["album"].Public, "album", "", 1)
	if err != nil || next != "" || len(songs) != 1 || songs[0].ID != names["song"].Public || songs[0].Song == nil || songs[0].Song.LocalMetadataIssue != "needs_review" || songs[0].Song.Artist != "Artist; Guest" || songs[0].Duration != 180.25 {
		t.Fatalf("songs: %+v %q %v", songs, next, err)
	}
	parts, next, err := s.AudioItems(viewer, names["book"].Public, "book", "", 1)
	if err != nil || next != "" || len(parts) != 1 || parts[0].ID != names["part"].Public || parts[0].BookFile == nil || parts[0].BookFile.BookTitle != "Book" {
		t.Fatalf("parts: %+v %q %v", parts, next, err)
	}
	chapters, next, err := s.BookChapters(viewer, names["book"].Public, "", 1)
	if err != nil || next != "" || len(chapters) != 1 || chapters[0].ID != names["part"].Public+":0" || chapters[0].ItemID != names["part"].Public || chapters[0].EndSeconds != 2.5 {
		t.Fatalf("chapters: %+v %q %v", chapters, next, err)
	}
}
