package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/readprep"
)

func localOrderedFixture(t *testing.T, s *Service, v Viewer, q QueueQuery) *QueueLocalOrderedIterator {
	t.Helper()
	var basis string
	if err := dbwork.WithReadSnapshot(context.Background(), s.db, func(ctx context.Context) error {
		var err error
		basis, err = s.WithContext(ctx).browseSelectionWitness(q.LibraryID, v.Profile, v.EffectiveRestrictions())
		return err
	}); err != nil {
		t.Fatal(err)
	}
	q.SelectionBasis = basis
	raw, err := json.Marshal(localQueueSpec{Query: q, Viewer: v, Basis: basis})
	if err != nil {
		t.Fatal(err)
	}
	return &QueueLocalOrderedIterator{service: s, spec: raw, caller: readprep.Caller(operationHash([]any{string(raw)}))}
}
func localOrderedDrain(t *testing.T, it *QueueLocalOrderedIterator) []string {
	t.Helper()
	defer it.Close()
	ids := []string{}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for {
		batch, err := it.Next(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if !batch.Ready {
			select {
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			case <-time.After(time.Millisecond):
			}
			continue
		}
		if len(batch.IDs) > 128 {
			t.Fatal("unbounded delivery")
		}
		ids = append(ids, batch.IDs...)
		if batch.EOF {
			return ids
		}
	}
}
func localOrderedStore(t *testing.T, s *Service) *readprep.Store {
	t.Helper()
	store, err := readprep.New(readprep.Config{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	s.UseReadPreparations(store)
	return store
}

func TestQueueLocalOrderedEveryMaintainedSortOracle(t *testing.T) {
	_, s, _ := browseFixture(t)
	localOrderedStore(t, s)
	v := Viewer{Profile: "p", Fence: "f", Libraries: []string{"a"}}
	query := &BrowseNode{Any: []BrowseNode{{Field: "search", Operator: "contains", Value: "a"}, {Field: "year", Operator: "at-least", Value: float64(2000)}}}
	for _, definition := range browseSorts {
		if !containsString(definition.ApplicableKinds, "movie") {
			continue
		}
		for _, direction := range definition.Directions {
			sort := []BrowseSortSelection{{Field: definition.ID, Direction: direction}}
			if !LocalSearchQueueSupported(query, sort) {
				continue
			}
			t.Run(definition.ID+"/"+direction, func(t *testing.T) {
				page, err := s.BrowseEntities(v, BrowseRequest{Library: "a", Pivot: "movies", Query: query, Sort: sort, Limit: 100})
				if err != nil {
					t.Fatal(err)
				}
				want := []string{}
				for _, entry := range page.Entries {
					want = append(want, entry.ID)
				}
				got := localOrderedDrain(t, localOrderedFixture(t, s, v, QueueQuery{LibraryID: "a", Pivot: "movies", Query: query, Sort: sort}))
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("got %v want Browse %v", got, want)
				}
			})
		}
	}
}

func TestQueueLocalOrderedRetainedOwnershipAndNoCase(t *testing.T) {
	db, s, names := browseFixture(t)
	localOrderedStore(t, s)
	// Retain the old serving generation while keys move and a new tombstone is
	// inserted. Current raw keys must be rejected by singleton ownership checks.
	if _, err := db.Exec(`UPDATE catalog_generations SET retaining=1 WHERE singleton=1`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE catalog_browse_rows SET sort_key='000-new',year=2999 WHERE entity_id=?`, names["m1"].ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE catalog_browse_rows SET sort_key='aLPHa' WHERE entity_id=?`, names["m2"].ID); err != nil {
		t.Fatal(err)
	}
	v := Viewer{Profile: "p", Fence: "f", Libraries: []string{"a"}}
	query := &BrowseNode{Not: &BrowseNode{Field: "search", Operator: "contains", Value: "no-match"}}
	for _, field := range []string{"title", "year", "added", "duration", "communityRating", "releaseDate", "criticRating", "fileSize", "resolution", "personalRating", "lastPlayed", "playCount"} {
		for _, direction := range []string{"asc", "desc"} {
			t.Run(field+direction, func(t *testing.T) {
				sort := []BrowseSortSelection{{Field: field, Direction: direction}}
				page, err := s.BrowseEntities(v, BrowseRequest{Library: "a", Pivot: "movies", Query: query, Sort: sort, Limit: 100})
				if err != nil {
					t.Fatal(err)
				}
				want := []string{}
				for _, entry := range page.Entries {
					want = append(want, entry.ID)
				}
				got := localOrderedDrain(t, localOrderedFixture(t, s, v, QueueQuery{LibraryID: "a", Pivot: "movies", Query: query, Sort: sort}))
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("got %v want %v", got, want)
				}
			})
		}
	}
}

func TestQueueLocalOrderedRejectedProgressAndStale(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	db, s, _ := browseFixture(t)
	store := localOrderedStore(t, s)
	v := Viewer{Profile: "p", Fence: "f", Libraries: []string{"a"}}
	q := QueueQuery{LibraryID: "a", Pivot: "movies", Query: &BrowseNode{Field: "search", Operator: "contains", Value: "none"}, Sort: []BrowseSortSelection{{Field: "title", Direction: "asc"}}}
	it := localOrderedFixture(t, s, v, q)
	defer it.Close()
	first, err := it.Next(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if first.Ready {
		t.Fatal("request walked source")
	}
	// Pausing an unpolled first window preserves exactly one handle for retry.
	handle := it.handle
	it.Pause()
	retry := localOrderedFixture(t, s, v, q)
	defer retry.Close()
	_, err = retry.Next(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if retry.handle != handle {
		t.Fatal("retry allocated a new handle")
	}
	got := localOrderedDrain(t, retry)
	if len(got) != 0 {
		t.Fatal(got)
	}
	if stats := store.Stats(); stats.Bytes > 32<<20 {
		t.Fatal(stats)
	}
	stale := localOrderedFixture(t, s, v, q)
	if _, err = db.Exec(`UPDATE library_revisions SET revision=revision+1 WHERE library_id='a'`); err != nil {
		t.Fatal(err)
	}
	defer stale.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	for {
		_, err = stale.Next(ctx)
		if err != nil {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if !errors.Is(err, readprep.ErrStale) {
		t.Fatalf("drift: %v", err)
	}
}

func TestQueueLocalOrderedSortGuardBeforeCompiler(t *testing.T) {
	db, s, _ := browseFixture(t)
	localOrderedStore(t, s)
	v := Viewer{Profile: "p", Fence: "f", Libraries: []string{"a"}}
	for _, sort := range [][]BrowseSortSelection{{{Field: "forYou", Direction: "desc"}}, {{Field: "title", Direction: "asc"}, {Field: "year", Direction: "desc"}}, {{Field: "groupOrder", Direction: "asc"}}, {{Field: "random", Direction: "asc", Seed: 1}}} {
		query := &BrowseNode{Field: "search", Operator: "contains", Value: "a"}
		if LocalSearchQueueSupported(query, sort) {
			t.Fatal(sort)
		}
		ctx, cost := dbwork.Measure(context.Background())
		err := dbwork.WithReadSnapshot(ctx, db, func(ctx context.Context) error {
			_, err := s.QueueKeysTx(ctx, dbwork.Snapshot(ctx), v, ResourceActor{}, &QueueQuery{LibraryID: "a", Pivot: "movies", Query: query, Sort: sort}, "", nil)
			return err
		})
		if err == nil {
			t.Fatalf("enabled %v", sort)
		}
		if cost().VMSteps > 20000 {
			t.Fatalf("guard did expensive work: %v", cost())
		}
	}
}

func TestQueueLocalOrderedRawIndexPlans(t *testing.T) {
	db, s, _ := browseFixture(t)
	for _, definition := range browseSorts {
		field := definition.ID
		index, ok := localQueueFactIndex(field)
		if !ok {
			continue
		}
		definition, _ := browseSortByID(field)
		for _, previous := range []bool{false, true} {
			table, ix, where := "catalog_browse_rows", index, "library_id=1 AND kind=1"
			if previous {
				table += "_previous"
				ix += "_previous"
				where = "generation=1 AND " + where
				if field == "title" || field == "added" || field == "year" || field == "duration" || field == "communityRating" {
					where += " AND present=1"
				}
			}
			query := `EXPLAIN QUERY PLAN SELECT entity_id FROM ` + table + ` e INDEXED BY ` + ix + ` WHERE ` + where + ` AND ` + definition.Expression + ` IS ? AND entity_id>? ORDER BY entity_id LIMIT 1`
			rows, err := db.Query(query, "", 0)
			if err != nil {
				t.Fatal(err)
			}
			for rows.Next() {
				var id, parent, unused int
				var detail string
				if err = rows.Scan(&id, &parent, &unused, &detail); err != nil {
					t.Fatal(err)
				}
				if strings.Contains(detail, "TEMP B-TREE") || !strings.Contains(detail, "SEARCH") {
					t.Fatalf("unbounded plan %s", detail)
				}
			}
			rows.Close()
		}
	}
	_ = s
}

func TestQueueLocalOrderedSongAndEpisodeSorts(t *testing.T) {
	c := catalogtest.Open(t)
	music := c.Library("music", "Music", "music", "/music")
	artist := c.Artist(music, "Artist")
	a := c.Album(artist, "Z Album", 2001)
	b := c.Album(artist, "A Album", 2000)
	for n := 0; n < 6; n++ {
		album := a
		if n%2 == 1 {
			album = b
		}
		c.Song(album, n%3+1, fmt.Sprintf("/music/%d.mp3", n), fmt.Sprintf("Alpha %d", n))
	}
	tv := c.Library("tv", "TV", "tv", "/tv")
	show := c.Show(tv, "Show", 2000)
	season := c.Season(show, 1)
	for n := 0; n < 4; n++ {
		c.Episode(show, season, n+1, fmt.Sprintf("/tv/%d.mkv", n))
	}
	c.Drain()
	s := New(c.DB)
	localOrderedStore(t, s)
	v := Viewer{Profile: "p", Fence: "f", Libraries: []string{"music", "tv"}}
	for _, scope := range []struct {
		lib, pivot string
		fields     []string
	}{{"music", "songs", []string{"album", "artist", "trackNumber", "title", "fileSize"}}, {"tv", "episodes", []string{"aired", "releaseDate", "criticRating", "resolution"}}} {
		for _, field := range scope.fields {
			for _, direction := range []string{"asc", "desc"} {
				t.Run(scope.pivot+field+direction, func(t *testing.T) {
					query := &BrowseNode{Not: &BrowseNode{Field: "search", Operator: "contains", Value: "no-match"}}
					sort := []BrowseSortSelection{{Field: field, Direction: direction}}
					page, err := s.BrowseEntities(v, BrowseRequest{Library: scope.lib, Pivot: scope.pivot, Query: query, Sort: sort, Limit: 100})
					if err != nil {
						t.Fatal(err)
					}
					want := []string{}
					for _, entry := range page.Entries {
						want = append(want, entry.ID)
					}
					got := localOrderedDrain(t, localOrderedFixture(t, s, v, QueueQuery{LibraryID: scope.lib, Pivot: scope.pivot, Query: query, Sort: sort}))
					if !reflect.DeepEqual(got, want) {
						t.Fatalf("got %v want %v", got, want)
					}
				})
			}
		}
	}
}

func TestQueueLocalOrderedSharedCapacityAndCancellation(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	_, s, _ := browseFixture(t)
	store, err := readprep.New(readprep.Config{MaxBytes: 256 << 10})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	s.UseReadPreparations(store)
	v := Viewer{Profile: "p", Fence: "f", Libraries: []string{"a"}}
	it := localOrderedFixture(t, s, v, QueueQuery{LibraryID: "a", Pivot: "movies", Query: &BrowseNode{Field: "search", Operator: "contains", Value: "a"}, Sort: []BrowseSortSelection{{Field: "title", Direction: "asc"}}})
	defer it.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	for {
		_, err = it.Next(ctx)
		if err != nil {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if !errors.Is(err, readprep.ErrCapacity) {
		t.Fatalf("unreserved construction: %v", err)
	}
	if stats := store.Stats(); stats.Bytes > 256<<10 {
		t.Fatalf("exceeded shared budget %+v", stats)
	}
	canceled, stop := context.WithCancel(context.Background())
	stop()
	if _, err = it.Next(canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
}

func TestQueueLocalOrderedCursorPreservesStoredBytes(t *testing.T) {
	db, s, names := browseFixture(t)
	localOrderedStore(t, s)
	for n, name := range []string{"m1", "m2", "m3", "m4", "m5"} {
		key := []byte{'a', byte(255 - n), 'z'}
		if _, err := db.Exec(`UPDATE catalog_browse_rows SET sort_key=? WHERE entity_id=?`, string(key), names[name].ID); err != nil {
			t.Fatal(err)
		}
	}
	v := Viewer{Profile: "p", Fence: "f", Libraries: []string{"a"}}
	query := &BrowseNode{Not: &BrowseNode{Field: "search", Operator: "contains", Value: "missing"}}
	for _, direction := range []string{"asc", "desc"} {
		sort := []BrowseSortSelection{{Field: "title", Direction: direction}}
		page, err := s.BrowseEntities(v, BrowseRequest{Library: "a", Pivot: "movies", Query: query, Sort: sort, Limit: 100})
		if err != nil {
			t.Fatal(err)
		}
		want := []string{}
		for _, entry := range page.Entries {
			want = append(want, entry.ID)
		}
		got := localOrderedDrain(t, localOrderedFixture(t, s, v, QueueQuery{LibraryID: "a", Pivot: "movies", Query: query, Sort: sort}))
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("stored byte order got %v want %v", got, want)
		}
	}
}
