package catalog

import (
	"context"
	"database/sql"
	"fmt"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
)

func TestAudioTitlesDoNotMergeDifferentReleases(t *testing.T) {
	for _, field := range []string{"musicbrainz_albumid", "date", "edition"} {
		t.Run(field, func(t *testing.T) {
			s, db := openEmptyAlbumDB(t)
			values := map[string][]string{
				"musicbrainz_albumid": {"11111111-1111-1111-1111-111111111111", "22222222-2222-2222-2222-222222222222"},
				"date":                {"2000", "2020"}, "edition": {"Studio", "Live"},
			}
			for i := 0; i < 2; i++ {
				tags := map[string]string{"title": "Song", "album": "Release", "album_artist": "Artist", "artist": "Artist", "disc": "1", "track": "1", "date": "2000"}
				tags[field] = values[field][i]
				commitAlbumSong(t, s, db, fmt.Sprintf("copy%d", i), fmt.Sprintf("Release%d", i), tags, false)
			}
			var songs, albums int
			if err := db.QueryRow(`SELECT count(*),count(DISTINCT album_id) FROM catalog_songs`).Scan(&songs, &albums); err != nil {
				t.Fatal(err)
			}
			if songs != 2 || albums != 2 {
				t.Fatalf("different %s merged into songs=%d albums=%d", field, songs, albums)
			}
		})
	}
}

func TestExtraOwnershipDoesNotTrustTruncatedCandidates(t *testing.T) {
	c := catalogtest.Open(t)
	c.Library("m", "Movies", "movie", "/m")
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		for n := 0; n < 513; n++ {
			path := fmt.Sprintf("/m/film%d.mkv", n)
			asset, _, err := compactcatalog.UpsertAssetTx(ctx, tx, compactcatalog.Asset{Path: path, Size: 1, ModifiedNS: 1})
			if err != nil {
				return err
			}
			title := "Alpha"
			if n == 512 {
				title = "Beta"
			}
			if _, err = AttachMovieTx(ctx, tx, MovieFile{Library: "m", Asset: asset, Path: path, Title: title, Year: 2000}); err != nil {
				return err
			}
		}
		owner, err := ResolveExtraOwnerTx(ctx, tx, "m", "/m/Beta-trailer.mkv", "/m")
		if err != nil {
			return err
		}
		if owner != 0 {
			var title string
			if err = tx.QueryRow(`SELECT title FROM catalog_entities WHERE id=?`, owner).Scan(&title); err != nil {
				return err
			}
			if title != "Beta" {
				t.Errorf("Beta trailer attached to %q after candidate truncation", title)
			}
		}
		return nil
	})
}

func TestUnicodeEquivalentAudioTagsShareArtistAndAlbum(t *testing.T) {
	s, db := openEmptyAlbumDB(t)
	for i, artist := range []string{"Beyoncé", "Beyonce\u0301"} {
		tags := map[string]string{"title": fmt.Sprintf("Song %d", i), "album": "Album", "album_artist": artist, "artist": artist, "track": fmt.Sprint(i + 1), "date": "2000"}
		commitAlbumSong(t, s, db, fmt.Sprintf("track%d", i), "Album", tags, false)
	}
	var artists int
	if err := db.QueryRow(`SELECT count(*) FROM catalog_artists`).Scan(&artists); err != nil {
		t.Fatal(err)
	}
	if artists != 1 || albumCount(t, db) != 1 {
		t.Fatalf("Unicode-equivalent tags split artists=%d albums=%d", artists, albumCount(t, db))
	}
}

func TestRetaggedAudioVersionDoesNotMoveOtherVersions(t *testing.T) {
	s, db := openEmptyAlbumDB(t)
	tags := map[string]string{"title": "Song", "album": "Release", "album_artist": "Artist", "artist": "Artist", "disc": "1", "track": "1", "date": "2000"}
	for _, copy := range []string{"one", "two"} {
		commitAlbumSong(t, s, db, copy, "Release", tags, false)
	}
	var first int64
	if err := db.QueryRow(`SELECT entity_id FROM catalog_songs`).Scan(&first); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO personal_watched_intents VALUES('p',?,1,'2026-01-01T00:00:00Z')`, first); err != nil {
		t.Fatal(err)
	}
	tags["date"] = "2020"
	commitAlbumSong(t, s, db, "two", "Release", tags, false)
	var songs, albums int
	if err := db.QueryRow(`SELECT count(*),count(DISTINCT album_id) FROM catalog_songs`).Scan(&songs, &albums); err != nil {
		t.Fatal(err)
	}
	if songs != 2 || albums != 2 {
		t.Fatalf("retagging one copy moved shared title: songs=%d albums=%d", songs, albums)
	}
	var year, links, watched int
	if err := db.QueryRow(`SELECT e.year,(SELECT count(*) FROM catalog_asset_links WHERE entity_id=?),(SELECT count(*) FROM personal_watched_intents WHERE item_id=? AND watched=1) FROM catalog_songs s JOIN catalog_entities e ON e.id=s.album_id WHERE s.entity_id=?`, first, first, first).Scan(&year, &links, &watched); err != nil {
		t.Fatal(err)
	}
	if year != 2000 || links != 1 || watched != 1 {
		t.Fatalf("prior release/state changed: year=%d files=%d watched=%d", year, links, watched)
	}
}

func TestAudiobookPartsKeepDifferentNarrationsSeparate(t *testing.T) {
	c := catalogtest.Open(t)
	c.Library("books", "Books", "audiobook", "/books")
	s := New(c.DB)
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		for n, narrator := range []string{"Reader One", "Reader Two"} {
			path := fmt.Sprintf("/books/copy%d.m4b", n)
			_, token, err := compactcatalog.UpsertAssetTx(ctx, tx, compactcatalog.Asset{Path: path, Size: 1, ModifiedNS: 1, Container: "mp4", AudioCodec: "aac", Duration: 60})
			if err != nil {
				return err
			}
			f := assets.Facts{AudioCodec: "aac", Container: "mp4", Duration: 60}
			assets.CopyEmbeddedAudioTags(&f, map[string]string{"title": "Part One", "album": "Book", "artist": "Author", "author": "Author", "narrator": narrator, "track": "1"})
			if err := s.commitAudio(ctx, tx, "books", "audiobook", "/books", token, path, f); err != nil {
				return err
			}
		}
		return nil
	})
	var parts, books int
	if err := c.DB.QueryRow(`SELECT count(*),count(DISTINCT book_id) FROM catalog_book_files`).Scan(&parts, &books); err != nil {
		t.Fatal(err)
	}
	if parts != 2 || books != 2 {
		t.Fatalf("different narrations merged: parts=%d books=%d", parts, books)
	}
}

func TestAudioReleaseEnrichmentKeepsVersionsAndRetiresWeakAlbumKey(t *testing.T) {
	s, db := openEmptyAlbumDB(t)
	tags := map[string]string{"title": "Song", "album": "Release", "album_artist": "Artist", "artist": "Artist", "disc": "1", "track": "1", "date": "2000"}
	weak := albumIdentityKey(tags, "Artist", "Release", 2000)
	for _, copy := range []string{"one", "two"} {
		commitAlbumSong(t, s, db, copy, "Release", tags, false)
	}
	tags["musicbrainz_albumid"] = "11111111-1111-1111-1111-111111111111"
	commitAlbumSong(t, s, db, "two", "Release", tags, false)
	var songs, files, weakAliases int
	if err := db.QueryRow(`SELECT count(*) FROM catalog_songs`).Scan(&songs); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT count(*) FROM catalog_asset_links l JOIN catalog_songs s ON s.entity_id=l.entity_id`).Scan(&files); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT count(*) FROM catalog_identities WHERE source_key=?`, compactcatalog.AlbumKey(weak)).Scan(&weakAliases); err != nil {
		t.Fatal(err)
	}
	if songs != 1 || files != 2 || albumCount(t, db) != 1 || weakAliases != 0 {
		t.Fatalf("release enrichment: songs=%d files=%d albums=%d stale album keys=%d", songs, files, albumCount(t, db), weakAliases)
	}
}

func TestAlbumEditionWithSeparatorEnrichesUnknownEvidence(t *testing.T) {
	s, db := openEmptyAlbumDB(t)
	tags := map[string]string{"title": "One", "album": "Release", "album_artist": "Artist", "artist": "Artist", "track": "1", "date": "2000"}
	commitAlbumSong(t, s, db, "one", "Release", tags, false)
	tags["title"], tags["track"], tags["edition"] = "Two", "2", "Live | Extended"
	commitAlbumSong(t, s, db, "two", "Release", tags, false)
	var edition string
	if err := db.QueryRow(`SELECT edition_key FROM catalog_albums LIMIT 1`).Scan(&edition); err != nil {
		t.Fatal(err)
	}
	if albumCount(t, db) != 1 || edition != audioEdition(tags) {
		t.Fatalf("literal edition separator lost enrichment: albums=%d edition=%q", albumCount(t, db), edition)
	}
}
