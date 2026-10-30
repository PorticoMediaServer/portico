package catalog

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
	"testing"
	"time"
)

func TestNestedShowFoldersRemainSeparate(t *testing.T) {
	c := catalogtest.Open(t)
	c.Library("tv", "TV", "tv", "/tv")
	s := New(c.DB)
	for _, rel := range []string{"UK/The Office/Season 1/The Office S01E01.mkv", "US/The Office/Season 1/The Office S01E01.mkv"} {
		episodeFile(t, c, s, "tv", "/tv/"+rel, rel, "tv")
	}
	if n := rows(t, c, `SELECT count(*) FROM catalog_entities WHERE kind=2`); n != 2 {
		t.Fatalf("two distinct folders became %d shows", n)
	}
}

func TestAudioRetainedStateRestoresAfterTags(t *testing.T) {
	c := catalogtest.Open(t)
	h := c.Library("m", "Music", "music", "/m")
	key := compactcatalog.TrackKey("Artist", "Album", 1, 1, "Song")
	a, _ := identityFile(t, c, "m", "/m/old.flac", 1)
	var old int64
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		var e error
		old, e = AttachTrackTx(ctx, tx, TrackFile{Library: h, Kind: compactcatalog.Track, Asset: a, Path: "/m/old.flac", Title: "Song", TagKey: key})
		return e
	})
	c.Exec(`INSERT INTO personal_watched_intents VALUES('p',?,1,'2026-01-01T00:00:00.000000000Z')`, old)
	c.Write(func(ctx context.Context, tx *sql.Tx) error { return RemoveTitleTx(ctx, tx, old) })
	b, _ := identityFile(t, c, "m", "/m/new.flac", 1)
	var back int64
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		var e error
		back, e = AttachTrackTx(ctx, tx, TrackFile{Library: h, Kind: compactcatalog.Track, Asset: b, Path: "/m/new.flac", Title: "Song"})
		return e
	})
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		var e error
		back, e = IdentifyTrackTx(ctx, tx, back, key)
		return e
	})
	if n := rows(t, c, `SELECT count(*) FROM personal_watched_intents WHERE profile_id='p' AND item_id=? AND watched=1`, back); n != 1 {
		t.Fatalf("restored watched rows=%d; retained rows=%d", n, rows(t, c, `SELECT count(*) FROM retained_title_state`))
	}
}

func TestProviderClaimsFindPriorUnmatchedFile(t *testing.T) {
	c := catalogtest.Open(t)
	c.Library("m", "Movies", "movie", "/m")
	var first, second int64
	for i, title := range []string{"Alien", "Alien translated"} {
		path := fmt.Sprintf("/m/%d.mkv", i)
		a, _ := identityFile(t, c, "m", path, 1080)
		c.Write(func(ctx context.Context, tx *sql.Tx) error {
			id, e := AttachMovieTx(ctx, tx, MovieFile{Library: "m", Asset: a, Path: path, Title: title, Year: 1979, Provider: []compactcatalog.ProviderIdentity{{Provider: "tmdb", Kind: "movie", ID: "348"}}})
			if i == 0 {
				first = id
			} else {
				second = id
			}
			return e
		})
	}
	if first != second {
		t.Fatalf("same provider claim became two titles: %d %d", first, second)
	}
}

func TestDifferentFilesystemsDoNotShareAsset(t *testing.T) {
	c := catalogtest.Open(t)
	c.Library("a", "A", "movie", "/a")
	c.Library("b", "B", "movie", "/b")
	s := New(c.DB)
	for i, lib := range []string{"a", "b"} {
		sources, e := s.LibrarySources(context.Background(), lib)
		if e != nil {
			t.Fatal(e)
		}
		source := sources[0]
		source.RootIdentity = fmt.Sprintf("dev:%d:ino:2", i+10)
		source.ResolvedPath = "/" + lib
		c.Write(func(ctx context.Context, tx *sql.Tx) error {
			_, e := s.observeInventoryTx(ctx, tx, "job-"+lib, source, "Movie.mkv", storage.Snapshot{Path: "/" + lib + "/Movie.mkv", Size: 100, ModifiedNS: 1, ObjectIdentity: fmt.Sprintf("dev:%d:ino:123", i+10)}, "", ScanPolicy{Tier: "file_list_only"}, nil)
			return e
		})
	}
	if n := rows(t, c, `SELECT count(*) FROM catalog_assets`); n != 2 {
		var p string
		c.DB.QueryRow(`SELECT path FROM catalog_assets LIMIT 1`).Scan(&p)
		t.Fatalf("distinct filesystem files became %d assets; surviving path=%s", n, p)
	}
}

func TestOfflineMissingTitleIsNotEmptied(t *testing.T) {
	c := catalogtest.Open(t)
	c.Library("m", "Movies", "movie", "/m")
	a, token := identityFile(t, c, "m", "/m/Film.mkv", 1080)
	film := attachMovie(t, c, "m", a, "/m/Film.mkv", "Film", 0)
	markMissing(t, c, "m", token, time.Now().Add(-time.Hour))
	c.Exec(`UPDATE library_sources SET health='offline' WHERE library_id='m'`)
	out, e := New(c.DB).EmptyMissing(context.Background(), "", time.Now(), nil)
	if e != nil {
		t.Fatal(e)
	}
	if n := rows(t, c, `SELECT count(*) FROM catalog_entities WHERE id=?`, film); n != 1 {
		t.Fatalf("offline title deleted: %+v; file objects=%d", out, rows(t, c, `SELECT count(*) FROM inventory_objects`))
	}
}

func TestLibraryRemovalBoundsRetainedState(t *testing.T) {
	c := catalogtest.Open(t)
	c.Library("m", "Movies", "movie", "/m")
	c.Exec(`WITH RECURSIVE n(x) AS(SELECT 1 UNION ALL SELECT x+1 FROM n WHERE x<100000) INSERT INTO retained_title_state(profile_id,kind,title,watched,retained_ms) SELECT 'p',1,CAST(x AS TEXT),1,x FROM n`)
	a, _ := identityFile(t, c, "m", "/m/Film.mkv", 1080)
	film := attachMovie(t, c, "m", a, "/m/Film.mkv", "Film", 0)
	c.Exec(`INSERT INTO personal_watched_intents VALUES('p',?,1,'2026-01-01T00:00:00.000000000Z')`, film)
	c.Exec(`UPDATE catalog_libraries SET retired=1 WHERE library_id='m'`)
	// The original ten title/removal turns are now followed by the explicit
	// Reading lifetime rail, even for a movie library (its final source/root
	// triggers enqueue Reading work). This fixture has no private scopes and
	// one source: source, exhausted-source proof, and post-delete settle each
	// need at most one turn per literal unit plus two verification transitions.
	const legacyTurns = 10
	turnLimit := legacyTurns + 3*(len(readingRemovalResidueUnits)+2)
	for i := 0; i < turnLimit; i++ {
		done := false
		c.Write(func(ctx context.Context, tx *sql.Tx) error {
			var e error
			done, e = removeLibraryBatchTx(ctx, tx, "m")
			return e
		})
		if done {
			break
		}
		if i == turnLimit-1 {
			t.Fatal("removal did not finish")
		}
	}
	if n := rows(t, c, `SELECT count(*) FROM retained_title_state WHERE profile_id='p'`); n > RetainedPerProfile {
		t.Fatalf("library removal completed with %d retained rows, limit %d", n, RetainedPerProfile)
	}
}

func TestSameShowFolderDoesNotSplitOnSpelling(t *testing.T) {
	c := catalogtest.Open(t)
	c.Library("tv", "TV", "tv", "/tv")
	s := New(c.DB)
	for _, rel := range []string{"Severance/Season 1/Severance S01E01.mkv", "Severance/Season 1/Severence S01E02.mkv"} {
		episodeFile(t, c, s, "tv", "/tv/"+rel, rel, "tv")
	}
	if n := rows(t, c, `SELECT count(*) FROM catalog_entities WHERE kind=2`); n != 1 {
		t.Fatalf("same folder became %d shows because one filename had a minor misspelling", n)
	}
}

func TestMergePreservesProviderIdentityOfRemovedTitle(t *testing.T) {
	c := catalogtest.Open(t)
	c.Library("m", "Movies", "movie", "/m")
	a, _ := identityFile(t, c, "m", "/m/Alien.mkv", 1080)
	b, _ := identityFile(t, c, "m", "/m/Alien 1979.mkv", 2160)
	from := attachMovie(t, c, "m", a, "/m/Alien.mkv", "Alien", 0)
	into := attachMovie(t, c, "m", b, "/m/Alien 1979.mkv", "Alien", 1979)
	c.Exec(`INSERT INTO catalog_external_ids VALUES(?,'imdb','movie','tt0078748')`, from)
	c.Exec(`INSERT INTO personal_watched_intents VALUES('p',?,1,'2026-01-01T00:00:00.000000000Z')`, from)
	c.Write(func(ctx context.Context, tx *sql.Tx) error { return MergeTitlesTx(ctx, tx, from, into) })
	c.Write(func(ctx context.Context, tx *sql.Tx) error { return RemoveTitleTx(ctx, tx, into) })
	if n := rows(t, c, `SELECT count(*) FROM retained_title_identities WHERE profile_id='p' AND identity='provider:imdb:movie:tt0078748'`); n != 1 {
		t.Fatalf("merged-away provider identity lost: retained imdb identities=%d", n)
	}
}

func TestNamedProviderHintsGroupFilmVersions(t *testing.T) {
	c := catalogtest.Open(t)
	c.Library("m", "Movies", "movie", "/m")
	var first, second int64
	for i, path := range []string{"/m/Alien (1979) {tmdb-348}.mkv", "/m/Translated (1979) {tmdb-348}.mkv"} {
		asset, _ := identityFile(t, c, "m", path, 1080)
		c.Write(func(ctx context.Context, tx *sql.Tx) error {
			id, err := commitMovieItem(ctx, tx, "m", asset, path)
			if i == 0 {
				first = id
			} else {
				second = id
			}
			return err
		})
	}
	if first != second {
		t.Fatalf("provider filename hints did not group versions: %d %d", first, second)
	}
}

func TestConflictingProviderClaimsStaySeparate(t *testing.T) {
	c := catalogtest.Open(t)
	c.Library("m", "Movies", "movie", "/m")
	var first, second int64
	for i, id := range []string{"348", "399"} {
		path := fmt.Sprintf("/m/%d.mkv", i)
		asset, _ := identityFile(t, c, "m", path, 1080)
		c.Write(func(ctx context.Context, tx *sql.Tx) error {
			entity, err := AttachMovieTx(ctx, tx, MovieFile{Library: "m", Asset: asset, Path: path, Title: "Same title", Provider: []compactcatalog.ProviderIdentity{{Provider: "tmdb", Kind: "movie", ID: id}}})
			if i == 0 {
				first = entity
			} else {
				second = entity
			}
			return err
		})
	}
	if first == second {
		t.Fatal("different proven provider identities were merged by parsed title")
	}
}

func TestMergeRejectsConflictingProviderIdentities(t *testing.T) {
	c := catalogtest.Open(t)
	c.Library("m", "Movies", "movie", "/m")
	a, _ := identityFile(t, c, "m", "/m/a.mkv", 1080)
	b, _ := identityFile(t, c, "m", "/m/b.mkv", 1080)
	from := attachMovie(t, c, "m", a, "/m/a.mkv", "A", 0)
	into := attachMovie(t, c, "m", b, "/m/b.mkv", "B", 0)
	c.Exec(`INSERT INTO catalog_external_ids VALUES(?,'tmdb','movie','348')`, from)
	c.Exec(`INSERT INTO catalog_external_ids VALUES(?,'tmdb','movie','399')`, into)
	err := dbwork.WithWriteTx(context.Background(), c.DB, dbwork.ClassMaintenance, func(tx *sql.Tx) error { return MergeTitlesTx(context.Background(), tx, from, into) })
	if !errors.Is(err, ErrNotMergeable) {
		t.Fatalf("conflicting identities accepted: %v", err)
	}
	if n := rows(t, c, `SELECT count(*) FROM catalog_entities`); n != 2 {
		t.Fatalf("rejected merge lost entities: %d", n)
	}
	if n := rows(t, c, `SELECT count(*) FROM catalog_asset_links`); n != 2 {
		t.Fatalf("rejected merge changed files: %d", n)
	}
}

func TestAnalyzedAudioTagsRestoreRetainedState(t *testing.T) {
	s, db := openEmptyAlbumDB(t)
	c := catalogtest.New(t, db)
	tags := map[string]string{"title": "Song", "artist": "Artist", "album_artist": "Artist", "album": "Album", "track": "1", "disc": "1"}
	commitAlbumSong(t, s, db, "old", "Album", tags, false)
	var old int64
	if err := db.QueryRow(`SELECT entity_id FROM catalog_songs`).Scan(&old); err != nil {
		t.Fatal(err)
	}
	c.Exec(`INSERT INTO personal_watched_intents VALUES('p',?,1,'2026-01-01T00:00:00.000000000Z')`, old)
	if err := s.RemoveTitle(context.Background(), c.Public(old)); err != nil {
		t.Fatal(err)
	}
	commitAlbumSong(t, s, db, "new", "Album", nil, true)
	commitAlbumSong(t, s, db, "new", "Album", tags, false)
	if n := rows(t, c, `SELECT count(*) FROM personal_watched_intents w JOIN catalog_songs s ON s.entity_id=w.item_id WHERE w.profile_id='p' AND w.watched=1`); n != 1 {
		t.Fatalf("tag analysis did not restore state: %d", n)
	}
}

func TestMissingTitlesWaitForSourceRecovery(t *testing.T) {
	for _, health := range []string{"offline", "root_changed", "inventory_running"} {
		t.Run(health, func(t *testing.T) {
			c := catalogtest.Open(t)
			c.Library("m", "Movies", "movie", "/m")
			a, token := identityFile(t, c, "m", "/m/Film.mkv", 1080)
			film := attachMovie(t, c, "m", a, "/m/Film.mkv", "Film", 0)
			markMissing(t, c, "m", token, time.Now().Add(-time.Hour))
			c.Exec(`UPDATE library_sources SET health=? WHERE library_id='m'`, health)
			s := New(c.DB)
			out, err := s.EmptyMissing(context.Background(), "", time.Now(), nil)
			if err != nil {
				t.Fatal(err)
			}
			if out.Titles != 0 || out.Files != 0 {
				t.Fatalf("unsafe source emptied: %+v", out)
			}
			if n := rows(t, c, `SELECT count(*) FROM catalog_entities WHERE id=?`, film); n != 1 {
				t.Fatal("missing title lost")
			}
			c.Exec(`UPDATE library_sources SET health='ready' WHERE library_id='m'`)
			out, err = s.EmptyMissing(context.Background(), "", time.Now(), nil)
			if err != nil || out.Titles != 1 {
				t.Fatalf("recovered source not emptied: %+v %v", out, err)
			}
		})
	}
}

func TestExplicitDeletionBoundsRetainedState(t *testing.T) {
	c := catalogtest.Open(t)
	c.Library("m", "Movies", "movie", "/m")
	c.Exec(`WITH RECURSIVE n(x) AS(SELECT 1 UNION ALL SELECT x+1 FROM n WHERE x<100000) INSERT INTO retained_title_state(profile_id,kind,title,watched,retained_ms) SELECT 'p',1,CAST(x AS TEXT),1,x FROM n`)
	a, _ := identityFile(t, c, "m", "/m/Film.mkv", 1080)
	film := attachMovie(t, c, "m", a, "/m/Film.mkv", "Film", 0)
	c.Exec(`INSERT INTO personal_watched_intents VALUES('p',?,1,'2026-01-01T00:00:00.000000000Z')`, film)
	if err := New(c.DB).RemoveTitle(context.Background(), c.Public(film)); err != nil {
		t.Fatal(err)
	}
	if n := rows(t, c, `SELECT count(*) FROM retained_title_state WHERE profile_id='p'`); n != RetainedPerProfile {
		t.Fatalf("explicit deletion retained %d titles, bound %d", n, RetainedPerProfile)
	}
	if n := rows(t, c, `SELECT count(*) FROM retained_title_state WHERE profile_id='p' AND retained_ms=1`); n != 0 {
		t.Fatal("oldest retained title was not evicted")
	}
}

func TestRetainedCountsFollowRestorationAndCascade(t *testing.T) {
	c := catalogtest.Open(t)
	c.Library("m", "Movies", "movie", "/m")
	c.Exec(`INSERT INTO retained_title_state(id,profile_id,kind,title,watched,retained_ms) VALUES(1,'p',1,'Film',1,1),(2,'q',1,'Other',1,2)`)
	c.Exec(`INSERT INTO retained_title_identities VALUES('local:/m|movie:film:0','p',1)`)
	a, _ := identityFile(t, c, "m", "/m/Film.mkv", 1080)
	film := attachMovie(t, c, "m", a, "/m/Film.mkv", "Film", 0)
	if n := rows(t, c, `SELECT count(*) FROM personal_watched_intents WHERE profile_id='p' AND item_id=? AND watched=1`, film); n != 1 {
		t.Fatal("state not restored")
	}
	if n := rows(t, c, `SELECT count(*) FROM retained_title_state_counts WHERE profile_id='p'`); n != 0 {
		t.Fatal("restored profile's count row remains")
	}
	if n := rows(t, c, `SELECT title_count FROM retained_title_state_counts WHERE profile_id='q'`); n != 1 {
		t.Fatal("another profile's count changed")
	}
	c.Exec(`DELETE FROM retained_title_state WHERE id=2`)
	if n := rows(t, c, `SELECT count(*) FROM retained_title_state_counts`); n != 0 {
		t.Fatalf("count rows remain after removal: %d", n)
	}
}

func TestNewProviderClaimRestoresStateOnExistingTitle(t *testing.T) {
	c := catalogtest.Open(t)
	c.Library("m", "Movies", "movie", "/m")
	c.Exec(`INSERT INTO retained_title_state(id,profile_id,kind,title,watched,retained_ms) VALUES(1,'p',1,'Old name',1,1)`)
	c.Exec(`INSERT INTO retained_title_identities VALUES('provider:tmdb:movie:348','p',1)`)
	a, _ := identityFile(t, c, "m", "/m/Alien.mkv", 1080)
	film := attachMovie(t, c, "m", a, "/m/Alien.mkv", "Alien", 0)
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		_, err := AttachMovieTx(ctx, tx, MovieFile{Library: "m", Asset: a, Path: "/m/Alien.mkv", Title: "Alien", Provider: []compactcatalog.ProviderIdentity{{Provider: "tmdb", Kind: "movie", ID: "348"}}})
		return err
	})
	if n := rows(t, c, `SELECT count(*) FROM personal_watched_intents WHERE profile_id='p' AND item_id=? AND watched=1`, film); n != 1 {
		t.Fatal("newly established provider identity did not restore state")
	}
}
