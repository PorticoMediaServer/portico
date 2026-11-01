package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
	"reflect"
	"testing"
	"time"
)

type recommendationFixture struct {
	db    *sql.DB
	c     *catalogtest.Catalog
	items map[string]catalogtest.Item
	libs  map[string]int64
}

func recommendationFixtureDB(t *testing.T) *recommendationFixture {
	t.Helper()
	db, err := persistence.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	c := catalogtest.New(t, db)
	f := &recommendationFixture{db: db, c: c, items: map[string]catalogtest.Item{}, libs: map[string]int64{}}
	for _, row := range []struct{ id, name, kind, root string }{
		{"movies", "Movies", "movie", "/movies"}, {"tv", "Shows", "tv", "/tv"}, {"music", "Music", "music", "/music"}, {"books", "Books", "audiobook", "/books"},
	} {
		f.libs[row.id] = c.Library(row.id, row.name, row.kind, row.root)
	}
	for index, key := range []string{"m1", "m2", "m3", "m4", "m5"} {
		item := c.Movie(f.libs["movies"], "/movies/"+key+".mkv", "Movie "+key, 2000+index)
		f.items[key] = item
	}
	show := c.Show(f.libs["tv"], "Harbor", 2020)
	f.items["show"] = show
	season1 := c.Season(show, 1)
	season2 := c.Season(show, 2)
	f.items["s1"], f.items["s2"] = season1, season2
	for _, row := range []struct {
		id     string
		season catalogtest.Item
		number int
	}{{"e101", season1, 1}, {"e102", season1, 2}, {"e201", season2, 1}, {"e202", season2, 2}} {
		f.items[row.id] = c.Episode(show, row.season, row.number, "/tv/"+row.id+".mkv")
	}
	artist := c.Artist(f.libs["music"], "Artist")
	album := c.Album(artist, "Release", 2020)
	f.items["artist"], f.items["album"] = artist, album
	for index, key := range []string{"t1", "t2", "t3"} {
		f.items[key] = c.Song(album, index+1, "/music/"+key+".flac", "Track "+key)
	}
	book := c.Book(f.libs["books"], "Alpha", "Author")
	f.items["book"] = book
	for index, key := range []string{"p1", "p2", "p3"} {
		f.items[key] = c.BookFile(book, index+1, "/books/"+key+".m4b")
	}
	c.Drain()
	return f
}

func engineSetTerms(t *testing.T, f *recommendationFixture, id int64, provider string, terms ...compactcatalog.Term) {
	t.Helper()
	f.c.Write(func(ctx context.Context, tx *sql.Tx) error {
		return compactcatalog.SetTermsTx(ctx, tx, id, compactcatalog.VocabGenre, provider, terms)
	})
}

func engineMeta(t *testing.T, f *recommendationFixture, key, provider, providerID string) {
	t.Helper()
	f.c.Exec(`INSERT INTO metadata_details(item_id,provider,provider_id,source_url,observed_at) VALUES(?,?,?,'','')`, f.items[key].ID, provider, providerID)
}

func engineExec(t *testing.T, f *recommendationFixture, query string, args ...any) {
	t.Helper()
	f.c.Exec(query, args...)
}

func engineWatched(t *testing.T, f *recommendationFixture, profile, at string, keys ...string) {
	t.Helper()
	for _, key := range keys {
		id := f.items[key].ID
		engineExec(t, f, `INSERT INTO personal_items(profile_id,item_id,watchlisted,favorite,revision,watched,last_played_at) VALUES(?,?,0,0,1,1,?) ON CONFLICT(profile_id,item_id) DO UPDATE SET watched=1,last_played_at=excluded.last_played_at`, profile, id, at)
		engineExec(t, f, `INSERT INTO personal_watched_intents VALUES(?,?,1,?) ON CONFLICT(profile_id,item_id) DO UPDATE SET watched=1,authored_at=excluded.authored_at`, profile, id, at)
	}
}

func engineRequest(libraries ...string) HomeRequest {
	if len(libraries) == 0 {
		libraries = []string{"movies", "tv", "music", "books"}
	}
	return HomeRequest{Viewer: Viewer{Profile: "p", Fence: "fence", Libraries: libraries}, ServerID: "server", Profile: "p", ViewerFence: "fence", Libraries: libraries, Now: time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)}
}

func engineWholeLibrary(t *testing.T, s *Service, request HomeRequest) []string {
	t.Helper()
	ids, whole, err := s.smallLibraryItems(request, 1<<20)
	if err != nil || !whole {
		t.Fatal("whole library", whole, err)
	}
	return ids
}

func engineBulkMovies(t *testing.T, f *recommendationFixture, n int) {
	t.Helper()
	var root string
	if err := f.db.QueryRow(`SELECT root FROM catalog_libraries WHERE id=?`, f.libs["movies"]).Scan(&root); err != nil {
		t.Fatal(err)
	}
	f.c.Write(func(ctx context.Context, tx *sql.Tx) error {
		for i := 0; i < n; i++ {
			path := fmt.Sprintf("/movies/bulk%06d.mkv", i)
			id, _, err := compactcatalog.UpsertEntityTx(ctx, tx, compactcatalog.Entity{Library: f.libs["movies"], Kind: compactcatalog.Movie, Key: compactcatalog.ItemKey(root, path, 0), Title: fmt.Sprintf("Other Fixture %06d", i), Year: 2025, Added: "2026-09-15T00:00:00.000Z"})
			if err != nil {
				return err
			}
			asset, _, err := compactcatalog.UpsertAssetTx(ctx, tx, compactcatalog.Asset{Path: path, Size: 1000, ModifiedNS: 1, Container: "mkv", VideoCodec: "h264", AudioCodec: "aac", Width: 1920, Height: 1080, Duration: 600})
			if err != nil {
				return err
			}
			if err = compactcatalog.LinkAssetTx(ctx, tx, id, asset, compactcatalog.Link{}); err != nil {
				return err
			}
			if err = compactcatalog.SetTermsTx(ctx, tx, id, compactcatalog.VocabGenre, "nfo", []compactcatalog.Term{{SourceID: fmt.Sprint(i), Name: "Noise"}}); err != nil {
				return err
			}
		}
		return nil
	})
}

func recIDs(candidates []recCandidate) []string {
	out := []string{}
	for _, candidate := range candidates {
		out = append(out, candidate.ID)
	}
	return out
}

func recHas(candidates []recCandidate, id string) bool {
	return containsString(recIDs(candidates), id)
}

func TestRecommendationLocalTasteNegativesAndEditionIdentity(t *testing.T) {
	f := recommendationFixtureDB(t)
	s := New(f.db)
	r := engineRequest("movies")
	engineSetTerms(t, f, f.items["m1"].ID, "nfo", compactcatalog.Term{SourceID: "random1", Name: "Sci-Fi"})
	engineSetTerms(t, f, f.items["m2"].ID, "manual", compactcatalog.Term{SourceID: "random2", Name: "Science Fiction"})
	engineSetTerms(t, f, f.items["m3"].ID, "manual", compactcatalog.Term{SourceID: "random3", Name: "Drama"})
	engineSetTerms(t, f, f.items["m4"].ID, "nfo", compactcatalog.Term{SourceID: "random4", Name: "Drama"})
	engineSetTerms(t, f, f.items["m5"].ID, "manual", compactcatalog.Term{SourceID: "random5", Name: "Sci Fi"})
	engineMeta(t, f, "m1", "tmdb", "42")
	engineMeta(t, f, "m5", "tmdb", "42")
	engineMeta(t, f, "m2", "tvdb", "42")
	engineWatched(t, f, "p", "2026-09-15T00:00:00Z", "m1")
	engineWatched(t, f, "q", "2026-09-15T00:00:00Z", "m3")
	f.c.Drain()
	got, err := s.recommendationCandidates(r, "recommended", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) == 0 || got[0].ID != f.items["m2"].Public || recHas(got, f.items["m5"].Public) {
		t.Fatalf("local taste or provider namespace failure: %v", recIDs(got))
	}
	r.Profile = "q"
	other, err := s.recommendationCandidates(r, "recommended", "")
	if err != nil || len(other) == 0 || other[0].ID != f.items["m4"].Public {
		t.Fatalf("contrasting taste: %v %v", recIDs(other), err)
	}
	engineExec(t, f, `UPDATE personal_items SET rating=1 WHERE profile_id='p'`)
	engineExec(t, f, `INSERT INTO personal_items(profile_id,item_id,favorite,rating,revision) VALUES('p',?,1,1,1)`, f.items["m2"].ID)
	r.Profile = "p"
	got, err = s.recommendationCandidates(r, "recommended", "")
	if err != nil {
		t.Fatal(err)
	}
	if recHas(got, f.items["m1"].Public) || recHas(got, f.items["m5"].Public) || recHas(got, f.items["m2"].Public) {
		t.Fatal("negative work/edition admitted", recIDs(got))
	}
}

func TestRecommendationAllFamiliesLocalFacetsAndWorkCaps(t *testing.T) {
	f := recommendationFixtureDB(t)
	s := New(f.db)
	r := engineRequest()
	show2 := f.c.Show(f.libs["tv"], "Other show", 2021)
	season := f.c.Season(show2, 1)
	episode := f.c.Episode(show2, season, 1, "/tv/Other/episode.mkv")
	f.items["show2"], f.items["e201x"] = show2, episode
	artist := f.items["artist"]
	album2 := f.c.Album(artist, "Second release", 2020)
	f.items["album2"] = album2
	track := f.c.Song(album2, 1, "/music/Second/track.flac", "Track 3")
	f.items["t3x"] = track
	book2 := f.c.Book(f.libs["books"], "Second book", "Author")
	f.items["book2"] = book2
	part := f.c.BookFile(book2, 1, "/books/Second/part.m4b")
	f.items["p3x"] = part
	engineSetTerms(t, f, f.items["e101"].ID, "manual", compactcatalog.Term{SourceID: "mystery", Name: "Mystery"})
	engineSetTerms(t, f, episode.ID, "manual", compactcatalog.Term{SourceID: "mystery", Name: "Mystery"})
	f.c.Drain()
	engineWatched(t, f, "p", "2026-09-15T00:00:00Z", "e101", "t1", "p1")
	got, err := s.recommendationCandidates(r, "recommended", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"show2", "album2", "book2"} {
		if !recHas(got, f.items[key].Public) {
			t.Fatal("missing personalized family", key, recIDs(got))
		}
	}
	for _, candidate := range got {
		if candidate.Kind != "movie" && candidate.Kind != "show" && candidate.Kind != "album" && candidate.Kind != "book" {
			t.Fatal(candidate)
		}
	}
	before := map[string]float64{}
	for _, candidate := range got {
		before[candidate.ID] = candidate.Score
	}
	engineWatched(t, f, "p", "2026-09-15T00:00:00Z", "e102", "t2", "p2")
	after, err := s.recommendationCandidates(r, "recommended", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, candidate := range after {
		if before[candidate.ID] != candidate.Score {
			t.Fatal("more children amplified one work", candidate)
		}
	}
	f.c.Drain()
	rows, _, err := s.ItemRecommendationRows(Viewer{Profile: "p", Fence: "fence", Libraries: []string{"tv"}}, f.items["e101"].Public, 12, false)
	if err != nil || len(rows) == 0 {
		t.Fatal("no related show", rows, err)
	}
	for _, row := range rows {
		for _, entry := range row.Entries {
			if entry.Kind != "show" || entry.ID != show2.Public {
				t.Fatal(entry)
			}
		}
	}
}

func TestRecommendationProviderTrendsPolicyExpiryAndLocalMatch(t *testing.T) {
	f := recommendationFixtureDB(t)
	s := New(f.db)
	r := engineRequest()
	engineExec(t, f, `UPDATE screen_metadata_consent SET confirmed=1`)
	engineExec(t, f, `INSERT INTO screen_metadata_policies(library_id,providers) VALUES('movies','["tmdb"]') ON CONFLICT(library_id) DO UPDATE SET providers=excluded.providers`)
	for _, row := range []struct{ key, provider, id string }{{"m1", "tmdb", "101"}, {"m2", "tmdb", "102"}, {"m3", "tmdb", "103"}, {"m4", "tvdb", "101"}, {"m5", "tmdb", "105"}} {
		engineMeta(t, f, row.key, row.provider, row.id)
	}
	for rank := 1; rank <= 4; rank++ {
		engineExec(t, f, `INSERT INTO metadata_discovery_items VALUES('tmdb','movie',?,?,?,?)`, fmt.Sprint(100+rank), rank, r.Now.Add(-time.Hour).Format(time.RFC3339), r.Now.Add(time.Hour).Format(time.RFC3339))
	}
	got, err := s.recommendationCandidates(r, "trending_now", "")
	if err != nil || !reflect.DeepEqual(recIDs(got), []string{f.items["m1"].Public, f.items["m2"].Public, f.items["m3"].Public}) {
		t.Fatal(recIDs(got), err)
	}
	// Provider trends retain their engine source and keyset pagination. Home's
	// drawn Trending row is separately fed by genuine weekly local activity.
	spec := homeRowSpec{ID: "trending_now", Direction: "asc"}
	source, err := s.homeEngineSource(r, spec)
	if err != nil {
		t.Fatal(err)
	}
	revision, err := s.homeRevision(r.Libraries, r.Profile)
	if err != nil {
		t.Fatal(err)
	}
	first, _, total, start, cursor, _, err := s.homeRowIDs(spec, source, HomeRowPage{Limit: 2}, revision, r.ViewerFence, r.Profile, true)
	if err != nil || total == nil || *total != 3 || start != 0 || cursor == "" || !reflect.DeepEqual(first, []string{f.items["m1"].Public, f.items["m2"].Public}) {
		t.Fatal(first, total, start, cursor, err)
	}
	next, _, nextTotal, nextStart, exhausted, _, err := s.homeRowIDs(spec, source, HomeRowPage{Limit: 2, Cursor: cursor}, revision, r.ViewerFence, r.Profile, true)
	if err != nil || nextTotal == nil || *nextTotal != 3 || nextStart != 2 || exhausted != "" || !reflect.DeepEqual(next, []string{f.items["m3"].Public}) {
		t.Fatal(next, nextTotal, nextStart, exhausted, err)
	}
	weekly, err := s.HomeSingleRow(r, "trending_now", HomeRowPage{Limit: 2})
	if err != nil || rowTotal(weekly) != 0 || len(weekly.Entries) != 0 || weekly.NextCursor != "" {
		t.Fatal("provider feed invented genuine weekly Home activity", weekly, err)
	}
	for _, mutation := range []string{`UPDATE screen_metadata_consent SET confirmed=0`, `UPDATE screen_metadata_policies SET enabled=0`, `UPDATE screen_metadata_policies SET providers='["tvdb"]'`, `UPDATE metadata_discovery_items SET expires_at='2020-01-01T00:00:00Z'`, `UPDATE metadata_discovery_items SET fetched_at='2020-01-01T00:00:00Z',expires_at='2099-01-01T00:00:00Z'`} {
		engineExec(t, f, mutation)
		got, err = s.recommendationCandidates(r, "trending_now", "")
		if err != nil || len(got) != 0 {
			t.Fatal(mutation, recIDs(got), err)
		}
		engineExec(t, f, `UPDATE screen_metadata_consent SET confirmed=1;UPDATE screen_metadata_policies SET enabled=1,providers='["tmdb"]'`)
	}
}

func TestUnconsentedTrendsDoNotReadCatalogFacets(t *testing.T) {
	f := recommendationFixtureDB(t)
	engineExec(t, f, `UPDATE screen_metadata_consent SET confirmed=0;DROP TABLE catalog_related_facets`)
	got, err := New(f.db).recommendationCandidates(engineRequest(), "trending_now", "")
	if err != nil || len(got) != 0 {
		t.Fatalf("unconsented trends evaluated the catalogue: %v, %v", got, err)
	}
}

func TestRecommendationRestrictionSameAvailableChildAndCommunity(t *testing.T) {
	f := recommendationFixtureDB(t)
	s := New(f.db)
	r := engineRequest("tv")
	r.Restrictions = identity.ContentRestrictions{BlockedLabels: []string{"adult"}}
	r.CommunityActivity = true
	f.c.Attributes(f.items["e101"].ID, "label", "Adult")
	f.c.Attributes(f.items["e102"].ID, "label", "Adult")
	f.c.Write(func(ctx context.Context, tx *sql.Tx) error {
		if err := compactcatalog.SetAssetAvailableTx(ctx, tx, f.items["e201"].Asset, false); err != nil {
			return err
		}
		return compactcatalog.SetAssetAvailableTx(ctx, tx, f.items["e202"].Asset, false)
	})
	f.c.Drain()
	got, err := s.recommendationCandidates(r, "recommended", "")
	if err != nil || len(got) != 0 {
		t.Fatal("separate allowed and available siblings combined", recIDs(got), err)
	}
	recent, err := s.homeRecentSource(r, "tv")
	if err != nil || recent.total == nil || *recent.total != 0 {
		t.Fatal("separate allowed and available siblings combined in Recently Added", recent.total, err)
	}
	f.c.Write(func(ctx context.Context, tx *sql.Tx) error {
		return compactcatalog.SetAssetAvailableTx(ctx, tx, f.items["e201"].Asset, true)
	})
	// Popular on this server needs three viewers who share their activity.
	engineExec(t, f, `INSERT INTO accounts(id,username,password_hash,profile_id,epoch) VALUES('a','a',x'00','p',1),('b','b',x'00','q',1),('c','c',x'00','r',1);UPDATE direct_memberships SET allowed_libraries='["tv"]' WHERE account_id IN('b','c')`)
	shareFixtureProfiles(t, f.db)
	for index, key := range []string{persistence.PersonalOwnerKey("local", "a", "p"), persistence.PersonalOwnerKey("local", "b", "q"), persistence.PersonalOwnerKey("local", "c", "r")} {
		engineExec(t, f, `INSERT INTO personal_history(id,profile_id,item_id,started_at,updated_at,sequence,position,unit,completed) VALUES(?,?,?,?,?,1,300,0,1)`, fmt.Sprint(index), key, f.items["e101"].ID, r.Now.Format(time.RFC3339), r.Now.Format(time.RFC3339))
	}
	f.c.Drain()
	got, err = s.recommendationCandidates(r, "community_watching", "")
	if err != nil || len(got) != 0 {
		t.Fatal("restricted history counted through allowed sibling", got, err)
	}
	engineExec(t, f, `UPDATE personal_history SET item_id=?`, f.items["e201"].ID)
	got, err = s.recommendationCandidates(r, "community_watching", "")
	if err != nil || len(got) != 1 || got[0].ID != f.items["show"].Public {
		t.Fatal(got, err)
	}
	engineExec(t, f, `UPDATE direct_profiles SET deleted=1 WHERE id='q'`)
	got, err = s.recommendationCandidates(r, "community_watching", "")
	if err != nil || len(got) != 0 {
		t.Fatal("removed profile counted", got, err)
	}
}

func TestRecommendationLocalAudioPolicyAndOwnerLock(t *testing.T) {
	f := recommendationFixtureDB(t)
	s := New(f.db)
	r := engineRequest("music")
	engineSetTerms(t, f, f.items["t1"].ID, "local", compactcatalog.Term{SourceID: "jazz", Name: "Jazz"})
	engineExec(t, f, `INSERT INTO audio_metadata_policies(library_id,local_mode) VALUES('music','prefer') ON CONFLICT(library_id) DO UPDATE SET local_mode='prefer'`)
	f.c.Drain()
	base, args := recBase(r, engineWholeLibrary(t, s, r))
	read := func() int {
		t.Helper()
		var n int
		if err := s.read().QueryRow(base+` SELECT count(*) FROM facets WHERE f='g:jazz'`, args...).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if read() != 1 {
		t.Fatal("local genre missing")
	}
	engineExec(t, f, `UPDATE audio_metadata_policies SET local_mode='off' WHERE library_id='music'`)
	f.c.Drain()
	if read() != 0 {
		t.Fatal("disabled automatic genre leaked")
	}
	engineExec(t, f, `INSERT INTO metadata_relationship_decisions(kind,entity_id,relationship,value_json,source,locked,actor,observed_at) VALUES('item',?,'genre','[]','owner_lock',1,'owner','now')`, f.items["t1"].ID)
	engineSetTerms(t, f, f.items["t1"].ID, "local", compactcatalog.Term{SourceID: "jazz", Name: "Jazz"})
	f.c.Drain()
	if read() != 1 {
		t.Fatal("owner lock lost")
	}
}

func TestRecommendationLargeCatalogRetrievalAndStablePaging(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	f := recommendationFixtureDB(t)
	s := New(f.db)
	r := engineRequest("movies")
	for _, key := range []string{"m1", "m2", "m3", "m4", "m5"} {
		engineSetTerms(t, f, f.items[key].ID, "nfo")
	}
	engineSetTerms(t, f, f.items["m1"].ID, "local", compactcatalog.Term{SourceID: "seed", Name: "Rare genre"})
	engineSetTerms(t, f, f.items["m2"].ID, "manual", compactcatalog.Term{SourceID: "different-id", Name: "Rare genre"})
	engineBulkMovies(t, f, 1200)
	f.c.Drain()
	engineWatched(t, f, "p", r.Now.Format(time.RFC3339), "m1")
	got, err := s.HomeSingleRow(r, "recommended", HomeRowPage{Limit: 12})
	if err != nil {
		t.Fatal(err)
	}
	if rowTotal(got) != 1 || len(got.Entries) != 1 || got.Entries[0].ID != f.items["m2"].Public {
		t.Fatal("old relevant candidate excluded or irrelevant works used as filler", got.Total, got.Entries)
	}
	r.Profile, r.Viewer.Profile = "cold", "cold"
	got, err = s.HomeSingleRow(r, "recommended", HomeRowPage{Limit: 12})
	// A new profile gets the best-rated titles (a cold start, ranked by
	// quality), paged stably.
	if err != nil || rowTotal(got) < 24 || len(got.Entries) != 12 {
		t.Fatal(got.Total, err)
	}
	again, err := s.HomeSingleRow(r, "recommended", HomeRowPage{Limit: 12})
	if err != nil {
		t.Fatal(err)
	}
	x, _ := json.Marshal(got.Entries)
	y, _ := json.Marshal(again.Entries)
	if string(x) != string(y) {
		t.Fatal("unstable first page")
	}
	next, err := s.HomeSingleRow(r, "recommended", HomeRowPage{Limit: 12, Cursor: got.NextCursor})
	if err != nil || rowTotal(next) != rowTotal(got) || next.Start != 12 {
		t.Fatal(next, err)
	}
	seen := map[string]bool{}
	for _, entry := range got.Entries {
		seen[entry.ID] = true
	}
	for _, entry := range next.Entries {
		if seen[entry.ID] {
			t.Fatal("repeated page entry", entry.ID)
		}
	}
	f.c.Attributes(f.items["m2"].ID, "label", "Adult")
	r.Profile, r.Viewer.Profile = "p", "p"
	r.Viewer.Restrictions = identity.ContentRestrictions{BlockedLabels: []string{"adult"}}
	got, err = s.HomeSingleRow(r, "recommended", HomeRowPage{Limit: 12})
	if err != nil || rowTotal(got) != 0 {
		t.Fatal("restricted candidate escaped large-catalog scorer", got, err)
	}
}

func TestRecommendationDiscoverUsesWholeAvailableWorksForEveryLibrary(t *testing.T) {
	f := recommendationFixtureDB(t)
	s := New(f.db)
	now := engineRequest().Now
	for _, library := range []string{"movies", "tv", "music", "books"} {
		// Whole-work recommendations retain this contract for every kind of
		// library. Music Content also has the drawn artist/genre/playlist and
		// most-played-song rows, which are not recommendation work rails.
		discovery, err := s.DiscoverWithRestrictions(library, "p", identity.ContentRestrictions{}, now)
		if err != nil || len(discovery.Sections) == 0 {
			t.Fatal("empty whole-work discovery", library, discovery, err)
		}
		for _, row := range discovery.Sections {
			if row.Total < len(row.Items) {
				t.Fatal("wrong recommendation total", row)
			}
			for _, entry := range row.Items {
				if entry.Kind != "movie" && entry.Kind != "show" && entry.Kind != "album" && entry.Kind != "book" {
					t.Fatal("recommendation is not a whole work", library, entry)
				}
			}
		}
		out, err := s.Content(ContentRequest{Viewer: Viewer{Profile: "p", Fence: "v", Libraries: []string{library}}, Library: library, Profile: "p", View: "discover", ViewerFence: "v", Now: now})
		if err != nil {
			t.Fatal(library, err)
		}
		if len(out.Sections) == 0 {
			t.Fatal("empty discovery", library)
		}
		for _, row := range out.Sections {
			if row.TotalCount < len(row.Entries) {
				t.Fatal("wrong total", row)
			}
			for _, entry := range row.Entries {
				if library == "music" {
					kind := map[string]string{"artists": "artist", "genres": "genre", "playlists": "playlist", "most_played_songs": "song"}[row.ID]
					if kind != "" {
						if entry.Kind != kind {
							t.Fatal("wrong music discovery row kind", row.ID, entry)
						}
						continue
					}
				}
				if entry.Kind != "movie" && entry.Kind != "show" && entry.Kind != "album" && entry.Kind != "book" {
					t.Fatal(library, row.ID, entry)
				}
			}
		}
	}
}

func TestRecommendationCommunityPrivacyAndCurrentAccess(t *testing.T) {
	f := recommendationFixtureDB(t)
	s := New(f.db)
	r := engineRequest("movies")
	r.CommunityActivity = true
	engineExec(t, f, `INSERT INTO accounts(id,username,password_hash,profile_id,epoch) VALUES('a','a',x'00','p',1),('b','b',x'00','q',1),('c','c',x'00','r',1);UPDATE direct_memberships SET allowed_libraries='["movies"]' WHERE account_id IN('b','c')`)
	shareFixtureProfiles(t, f.db)
	for i, key := range []string{persistence.PersonalOwnerKey("local", "a", "p"), persistence.PersonalOwnerKey("local", "b", "q"), persistence.PersonalOwnerKey("local", "c", "r")} {
		engineExec(t, f, `INSERT INTO personal_history(id,profile_id,item_id,started_at,updated_at,sequence,position,unit,completed) VALUES(?,?,?,?,?,1,300,0,1)`, fmt.Sprint(i), key, f.items["m1"].ID, r.Now.Format(time.RFC3339), r.Now.Format(time.RFC3339))
	}
	check := func(n int) {
		t.Helper()
		f.c.Drain()
		got, err := s.recommendationCandidates(r, "community_watching", "")
		if err != nil || len(got) != n {
			t.Fatal(got, err)
		}
	}
	check(1)
	for _, field := range []string{"privacy.showActivityToMembers", "privacy.pauseWatchHistory"} {
		value := field == "privacy.pauseWatchHistory"
		storePreference(t, f.db, identity.Viewer{Authority: "local", AccountID: "b", ProfileID: "q"}, map[string]any{field: value})
		check(0)
		storePreference(t, f.db, identity.Viewer{Authority: "local", AccountID: "b", ProfileID: "q"}, map[string]any{"privacy.showActivityToMembers": true, "privacy.pauseWatchHistory": false})
		check(1)
	}
	engineExec(t, f, `UPDATE direct_memberships SET allowed_libraries='[]' WHERE account_id='b'`)
	check(0)
	f.c.Attributes(f.items["m1"].ID, "label", "Spoiler")
	engineExec(t, f, `UPDATE direct_memberships SET allowed_libraries='["movies"]' WHERE account_id='b';INSERT INTO profile_restrictions(profile_id,blocked_labels) VALUES('q','["spoiler"]')`)
	check(0)
	engineExec(t, f, `DELETE FROM profile_restrictions`)
	engineExec(t, f, `UPDATE personal_history SET completed=0,position=1 WHERE profile_id=?`, persistence.PersonalOwnerKey("local", "b", "q"))
	check(0)
}

// A daily ranking change cannot silently reshuffle an open row's loaded prefix.
func TestHomeRecommendationCursorRestartsAcrossDailyRotation(t *testing.T) {
	f := recommendationFixtureDB(t)
	s := New(f.db)
	r := engineRequest("movies")
	first, err := s.HomeSingleRow(r, "recommended", HomeRowPage{Limit: 1})
	if err != nil || first.NextCursor == "" {
		t.Fatal(first, err)
	}
	original := r
	r.Now = r.Now.Add(24 * time.Hour)
	if _, err = s.HomeSingleRow(r, "recommended", HomeRowPage{Limit: 1, Cursor: first.NextCursor}); !errors.Is(err, ErrStaleContinuation) {
		t.Fatal("ranking changed without typed restart", err)
	}
	replay, err := s.HomeSingleRow(original, "recommended", HomeRowPage{Limit: 1})
	if err != nil || !reflect.DeepEqual(replay.Entries, first.Entries) {
		t.Fatal("stable ranking replay changed", replay, err)
	}
}

func TestRecommendationShowTrendsUseAcceptedPublication(t *testing.T) {
	f := recommendationFixtureDB(t)
	s := New(f.db)
	r := engineRequest("tv")
	show := f.items["show"]
	engineExec(t, f, `UPDATE screen_metadata_consent SET confirmed=1;UPDATE screen_metadata_policies SET providers='["tmdb"]' WHERE library_id='tv'`)
	engineExec(t, f, `INSERT OR IGNORE INTO screen_metadata_work(target_kind,target_id,library_id,title_seed) VALUES('show',?,'tv','Harbor')`, show.ID)
	engineExec(t, f, `INSERT INTO screen_metadata_publications(id,target_kind,target_id,provider,provider_type,provider_id,payload,confidence,reasons,source_kind,input_digest,query_digest,observed_at,language,region,selection_revision,generation,decision)
 VALUES('pub','show',?,'tmdb','tv','99','{}',1,'[]','provider','','','2026-09-15','en-US','US',1,1,'accepted')`, show.ID)
	engineExec(t, f, `UPDATE screen_metadata_work SET accepted_publication='pub' WHERE target_id=?`, show.ID)
	engineExec(t, f, `INSERT INTO metadata_discovery_items VALUES('tmdb','tv','99',1,?,?)`, r.Now.Add(-time.Hour).Format(time.RFC3339), r.Now.Add(time.Hour).Format(time.RFC3339))
	got, err := s.recommendationCandidates(r, "trending_now", "")
	if err != nil || len(got) != 1 || got[0].ID != show.Public {
		t.Fatal(got, err)
	}
	engineExec(t, f, `UPDATE screen_metadata_work SET accepted_publication='' WHERE target_id=?`, show.ID)
	got, err = s.recommendationCandidates(r, "trending_now", "")
	if err != nil || len(got) != 0 {
		t.Fatal("retired publication still matched", got, err)
	}
}
