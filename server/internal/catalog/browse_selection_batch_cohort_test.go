package catalog

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

func TestSelectionBatchReplayableLeafCohort(t *testing.T) {
	c := catalogtest.Open(t)
	film := c.Library("m", "Movies", "movie", "/m")
	c.Movie(film, "/m/a.mkv", "Movie", 2020)
	tv := c.Library("tv", "TV", "tv", "/tv")
	show := c.Show(tv, "Show", 2020)
	season := c.Season(show, 1)
	for i := 1; i <= 3; i++ {
		item := c.Episode(show, season, i, fmt.Sprintf("/tv/%d.mkv", i))
		if i == 2 {
			c.Attributes(item.ID, "label", "hidden")
		}
	}
	c.Genres(show.ID, "tmdb", "Drama")
	anime := c.Library("an", "Anime", "anime", "/an")
	ashow := c.Show(anime, "Anime", 2020)
	aseason := c.Season(ashow, 1)
	c.Episode(ashow, aseason, 1, "/an/1.mkv")
	music := c.Library("mu", "Music", "music", "/mu")
	artist := c.Artist(music, "Artist")
	album := c.Album(artist, "Album", 2020)
	c.Song(album, 1, "/mu/1.flac", "Song")
	books := c.Library("ab", "Audiobooks", "audiobook", "/ab")
	book := c.Book(books, "Book", "Author")
	c.BookFile(book, 1, "/ab/1.m4b")
	c.Drain()
	s := New(c.DB)
	authority := HomeActionAuthority{Personal: true, Organize: true, Bulk: true, Playback: true, Queue: true, Metadata: true, Refresh: true, Downloads: true, DeleteLibrary: func(string) (bool, error) { return true, nil }}
	for _, view := range []struct{ library, pivot, query string }{
		{"m", "movies", `null`}, {"tv", "episodes", `null`}, {"an", "episodes", `null`}, {"mu", "songs", `null`}, {"ab", "parts", `null`},
		{"tv", "episodes", `{"variant":"predicate","field":"genre","operator":"equals","value":"Drama"}`},
		{"ab", "parts", `{"variant":"predicate","field":"author","operator":"equals","value":"Author"}`},
		{"mu", "songs", `{"variant":"predicate","field":"album","operator":"equals","value":"Album"}`},
		{"tv", "episodes", fmt.Sprintf(`{"variant":"predicate","field":"episodeGroup","operator":"equals","value":%q}`, show.Public+"/range:0")},
		{"tv", "episodes", fmt.Sprintf(`{"variant":"predicate","field":"episodeGroup","operator":"equals","value":%q}`, show.Public+"/specials")},
	} {
		t.Run(view.library+view.query, func(t *testing.T) {
			query, err := ParseBrowseQuery(json.RawMessage(view.query), "query")
			if err != nil {
				t.Fatal(err)
			}
			v := Viewer{Profile: "p", Fence: "f", Libraries: []string{view.library}, Restrictions: identity.ContentRestrictions{BlockedLabels: []string{"hidden"}}}
			state, oracle, witness := selectionBatchFixture(t, s, v, BrowseRequest{Library: view.library, Profile: "p", ViewerFence: "f", Pivot: view.pivot, Query: query, Limit: 1}, authority)
			got := finishSelectionBatch(t, s, state, witness, authority, 7)
			if !reflect.DeepEqual(got, oracle) {
				t.Fatalf("got %+v want %+v", got, oracle)
			}
		})
	}
}

func TestSelectionBatchRetainedRawRowsAndMemberships(t *testing.T) {
	c := catalogtest.Open(t)
	library := c.Library("m", "Movies", "movie", "/m")
	old := c.Movie(library, "/m/old.mkv", "Old", 2020)
	c.Attributes(old.ID, "label", "open")
	c.Drain()

	c.Fields(old.ID, map[string]any{"title": "New title"})
	for i := 0; i < 12; i++ {
		c.Movie(library, fmt.Sprintf("/m/new%d.mkv", i), "New", 2021)
	}
	c.Drain()
	c.Exec(`UPDATE catalog_generations SET retaining=1`)
	c.Exec(`UPDATE catalog_browse_rows SET title='Staging title' WHERE entity_id=?`, old.ID)
	c.Exec(`INSERT INTO catalog_browse_rows_previous(generation,present,entity_id) SELECT g.generation,0,r.entity_id FROM catalog_generations g CROSS JOIN catalog_browse_rows r WHERE r.entity_id<>? ON CONFLICT(generation,entity_id) DO UPDATE SET present=0`, old.ID)
	c.Exec(`INSERT INTO catalog_browse_memberships_previous(generation,present,entity_id,item_id,source) SELECT g.generation,0,m.entity_id,m.item_id,m.source FROM catalog_generations g CROSS JOIN catalog_browse_memberships m WHERE m.entity_id<>? ON CONFLICT(generation,entity_id,item_id,source) DO UPDATE SET present=0`, old.ID)
	s := New(c.DB)
	v := Viewer{Profile: "p", Fence: "f", Libraries: []string{"m"}, Restrictions: identity.ContentRestrictions{BlockedLabels: []string{"private"}}}
	state, oracle, witness := selectionBatchFixture(t, s, v, BrowseRequest{Library: "m", Profile: "p", ViewerFence: "f", Pivot: "movies", Limit: 1}, HomeActionAuthority{Playback: true})
	got := finishSelectionBatch(t, s, state, witness, HomeActionAuthority{Playback: true}, 1)
	if !reflect.DeepEqual(got, oracle) || got.Total != 1 {
		t.Fatalf("retained got %+v want %+v", got, oracle)
	}
}

func TestSelectionBatchSavedCollectionAndPlaylistSources(t *testing.T) {
	c := catalogtest.Open(t)
	library := c.Library("m", "Movies", "movie", "/m")
	item := c.Movie(library, "/m/a.mkv", "A", 2020)
	c.Movie(library, "/m/b.mkv", "B", 2020)
	c.Drain()
	parts := []string{"local", "account", "profile"}
	raw, _ := json.Marshal(parts)
	profile := "viewer:" + base64.RawURLEncoding.EncodeToString(raw)
	c.Exec(`INSERT INTO catalog_playlists(id,token,owner_authority,owner_account,owner_profile,name,creation_operation,creation_hash,created_at,updated_at) VALUES(1,'playlist','local','account','profile','Playlist','fixture','hash','','')`)
	c.Exec(`INSERT INTO catalog_playlist_entries(token,playlist_id,item_id,position) VALUES('entry',1,?,1)`, item.ID)
	c.Exec(`INSERT INTO saved_resources(id,owner_key,kind,name,visibility,owner_authority,owner_account,owner_profile,created_at) VALUES('saved',?,'collection','Saved','private','local','account','profile','')`, profile)
	c.Exec(`INSERT INTO saved_resource_entries(id,resource_id,item_id) VALUES('saved-entry','saved',?)`, item.ID)
	c.Drain()
	s := New(c.DB)
	v := Viewer{Profile: profile, Fence: "f", Libraries: []string{"m"}}
	for _, query := range []string{`{"variant":"predicate","field":"list","operator":"equals","value":"playlist"}`, `{"variant":"predicate","field":"savedCollection","operator":"equals","value":"saved"}`, `{"variant":"predicate","field":"savedCollection","operator":"not-in","value":["saved"]}`} {
		node, err := ParseBrowseQuery(json.RawMessage(query), "query")
		if err != nil {
			t.Fatal(err)
		}
		state, oracle, witness := selectionBatchFixture(t, s, v, BrowseRequest{Library: "m", Profile: profile, ViewerFence: "f", Pivot: "movies", Query: node, Limit: 1}, HomeActionAuthority{})
		got := finishSelectionBatch(t, s, state, witness, HomeActionAuthority{}, 2)
		if !reflect.DeepEqual(got, oracle) {
			t.Fatalf("%s got %+v want %+v", query, got, oracle)
		}
	}
}

func TestSelectionBatchLargeScalarIsTransient(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	c := catalogtest.Open(t)
	library := c.Library("m", "Movies", "movie", "/m")
	item := c.Movie(library, "/m/a.mkv", "A", 2020)
	value := strings.Repeat("x", (4<<20)+1)
	c.Attributes(item.ID, "tag", value)
	c.Drain()
	s := New(c.DB)
	v := Viewer{Profile: "p", Fence: "f", Libraries: []string{"m"}}
	node, err := ParseBrowseQuery(json.RawMessage(`{"variant":"predicate","field":"tag","operator":"starts-with","value":"x"}`), "query")
	if err != nil {
		t.Fatal(err)
	}
	state, oracle, witness := selectionBatchFixture(t, s, v, BrowseRequest{Library: "m", Profile: "p", ViewerFence: "f", Pivot: "movies", Query: node, Limit: 1}, HomeActionAuthority{})
	peak := 0
	for !state.Complete() {
		next, work, err := s.StepBrowseSelectionBatch(context.Background(), state, witness, SelectionBatchLimits{Rows: 20})
		if err != nil {
			t.Fatal(err)
		}
		peak = max(peak, work.TransientScalarBytes)
		if next.Bytes() > 100000 {
			t.Fatal("scalar retained in reducer")
		}
		state = next
	}
	if peak != len(value) {
		t.Fatalf("transient byte accounting=%d want=%d", peak, len(value))
	}
	got := finishSelectionBatch(t, s, state, witness, HomeActionAuthority{}, 1)
	if !reflect.DeepEqual(got, oracle) {
		t.Fatalf("got %+v want %+v", got, oracle)
	}
}

func TestBulkSelectionCompleteWitnessDrift(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	s, scheduler, p, v, access, c, names := bulkFixture(t, 205)
	yes := true
	in := JobRequest{OperationID: "predicate-drift", Command: "personal-state", Selector: JobSelector{Container: &JobContainer{Kind: "show", ID: names["show"].Public}}, Args: JobPersonalArgs{Favorite: &yes}}
	job, err := s.CreateBulkJob(context.Background(), p, v, in, scheduler, access)
	if err != nil {
		t.Fatal(err)
	}
	job, err = s.AdvanceBulkJob(context.Background(), job.JobID, access)
	if err != nil {
		t.Fatal(err)
	}
	var before, after string
	readFence := func() (string, error) {
		var value string
		err := dbwork.WithReadSnapshot(context.Background(), s.db, func(ctx context.Context) error {
			var err error
			value, err = bulkSelectionRevisionFence(dbwork.Snapshot(ctx), v, in.Selector)
			return err
		})
		return value, err
	}
	before, err = readFence()
	if err != nil {
		t.Fatal(err)
	}
	c.Exec(`INSERT INTO content_rating_ages(value_key,minimum_age) VALUES('new-classification-input',18)`)
	after, err = readFence()
	if err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Fatal("fixture changed old revision fence")
	}
	job, err = s.AdvanceBulkJob(context.Background(), job.JobID, access)
	if err != nil || job.State != "failed" || job.ErrorCode != "selection_changed" {
		t.Fatalf("capture admitted stale predicate %+v %v", job, err)
	}
	var count int
	if err = s.db.QueryRow(`SELECT count(*) FROM personal_items`).Scan(&count); err != nil || count != 0 {
		t.Fatal("stale capture applied personal state", count, err)
	}
}

// Constructor drift and command-authority errors are refusals, not successful
// empty descriptors. No method returns a usable partial count.
func TestSelectionBatchFinishAuthorityError(t *testing.T) {
	_, s, _ := browseFixture(t)
	v := Viewer{Profile: "p", Fence: "f", Libraries: []string{"a"}}
	state, _, witness := selectionBatchFixture(t, s, v, BrowseRequest{Library: "a", Profile: "p", ViewerFence: "f", Pivot: "movies", Limit: 1}, HomeActionAuthority{})
	for !state.Complete() {
		var err error
		state, _, err = s.StepBrowseSelectionBatch(context.Background(), state, witness, SelectionBatchLimits{})
		if err != nil {
			t.Fatal(err)
		}
	}
	want := errors.New("live authority unavailable")
	err := dbwork.WithReadSnapshot(context.Background(), s.db, func(ctx context.Context) error {
		_, err := s.WithContext(ctx).FinishBrowseSelectionBatch(state, witness, HomeActionAuthority{DeleteLibrary: func(string) (bool, error) { return false, want }})
		return err
	})
	if !errors.Is(err, want) {
		t.Fatal(err)
	}
}
