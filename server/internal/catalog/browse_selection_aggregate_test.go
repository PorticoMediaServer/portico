package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

// pageAndAggregate reads a page and its whole-view aggregate inside one read
// snapshot, exactly as the browse route composes them.
func pageAndAggregate(t *testing.T, s *Service, viewer Viewer, request BrowseRequest, authority HomeActionAuthority) (BrowseResult, BrowseSelectionDescriptor) {
	t.Helper()
	var page BrowseResult
	var whole BrowseSelectionDescriptor
	if err := dbwork.WithReadSnapshot(context.Background(), s.db, func(ctx context.Context) error {
		read := s.WithContext(ctx)
		var err error
		if page, err = read.BrowseEntities(viewer, request); err != nil {
			return err
		}
		whole, err = read.BrowseSelectionAggregate(request, viewer, *page.Bound, authority)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return page, whole
}

// wholeSelection describes the whole authorized view, not the loaded window: the
// exact total and the per-action counts of the same entries the page returns.
func TestBrowseSelectionAggregateCountsTheBoundView(t *testing.T) {
	_, s, _ := browseFixture(t)
	viewer := Viewer{Profile: "p", Fence: "f", Libraries: []string{"a"}}
	authority := HomeActionAuthority{Personal: true, Organize: true, Bulk: true, Playback: true, Queue: true,
		Metadata: true, Refresh: true, Downloads: true, DeleteLibrary: func(string) (bool, error) { return true, nil }}
	page, whole := pageAndAggregate(t, s, viewer, BrowseRequest{Library: "a", Profile: "p", ViewerFence: "f", Pivot: "movies", Limit: 2}, authority)
	if whole.CatalogRevision != page.PageInfo.Revision || whole.CatalogRevision == "" {
		t.Fatalf("identity: revision %q page %q", whole.CatalogRevision, page.PageInfo.Revision)
	}
	if whole.Total != page.PageInfo.Total || whole.Total != 5 {
		t.Fatalf("total %d page %d", whole.Total, page.PageInfo.Total)
	}
	if whole.Selector.Query.LibraryID != "a" || whole.Selector.Query.Pivot != "movies" || len(whole.Selector.Query.Sort) == 0 {
		t.Fatalf("selector %+v", whole.Selector.Query)
	}
	if len(whole.Types) != 1 || whole.Types[0].Kind != "movie" || whole.Types[0].Count != 5 {
		t.Fatalf("types %+v", whole.Types)
	}
	counts := whole.Types[0].CapabilityCounts
	// The fixture has five films, of which three have an available asset.
	if counts.Play != 3 || counts.Queue != 3 || counts.Download != 3 {
		t.Fatalf("availability did not gate play/queue/download: %+v", counts)
	}
	if counts.MyList != 5 || counts.Playlist != 5 || counts.Edit != 5 || counts.Refresh != 5 || counts.Delete != 5 {
		t.Fatalf("granted actions: %+v", counts)
	}
	if len(whole.Types[0].Libraries) != 1 || whole.Types[0].Libraries[0].LibraryID != "a" || whole.Types[0].Libraries[0].Count != 5 {
		t.Fatalf("libraries %+v", whole.Types[0].Libraries)
	}
}

func TestBrowseSelectionAggregateNeverInventsGrantsOrSettings(t *testing.T) {
	_, s, _ := browseFixture(t)
	viewer := Viewer{Profile: "p", Fence: "f", Libraries: []string{"a"}}
	request := BrowseRequest{Library: "a", Profile: "p", ViewerFence: "f", Pivot: "movies", Limit: 2}
	_, none := pageAndAggregate(t, s, viewer, request, HomeActionAuthority{})
	if none.Total != 5 {
		t.Fatalf("none %+v", none.Total)
	}
	if c := none.Types[0].CapabilityCounts; c.Play != 0 || c.Queue != 0 || c.MyList != 0 || c.Edit != 0 || c.Delete != 0 {
		t.Fatalf("invented grants: %+v", c)
	}
	// A library whose deletion is turned off offers Delete nowhere, while Play stays.
	denied := HomeActionAuthority{Playback: true, Queue: true, Bulk: true, Personal: true, Organize: true,
		DeleteLibrary: func(string) (bool, error) { return false, nil }}
	_, off := pageAndAggregate(t, s, viewer, request, denied)
	if c := off.Types[0].CapabilityCounts; c.Delete != 0 || c.Play != 3 {
		t.Fatalf("delete setting ignored: %+v", c)
	}
}

func TestBrowseSelectionAggregateOmitsViewsTheJobCannotReplay(t *testing.T) {
	_, s, _ := browseFixture(t)
	viewer := Viewer{Profile: "p", Fence: "f", Libraries: []string{"a", "tv"}}
	authority := HomeActionAuthority{Personal: true, Organize: true, Bulk: true, Playback: true, Queue: true, Metadata: true, Refresh: true, Downloads: true}
	// A Home-row predicate is rejected structurally even when no row resolution
	// happened to be attached to the request.
	var homeRow BrowseSelectionDescriptor
	if err := dbwork.WithReadSnapshot(context.Background(), s.db, func(ctx context.Context) error {
		bound := BoundBrowseQuery{Where: "1=1", Library: "a", Pivot: "movies", Sorts: []BrowseSortSelection{{Field: "title", Direction: "asc"}},
			Query: &BrowseNode{Field: "homeRow", Operator: "equals", Value: "new_on_server"}, snapshot: dbwork.Snapshot(ctx)}
		var err error
		homeRow, err = s.WithContext(ctx).BrowseSelectionAggregate(BrowseRequest{Library: "a"}, viewer, bound, authority)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if homeRow.Selector.Query.LibraryID != "" {
		t.Fatalf("homeRow view was not omitted: %+v", homeRow)
	}
	// A seeded order matches different titles when a job replays it.
	var seeded BrowseSelectionDescriptor
	if err := dbwork.WithReadSnapshot(context.Background(), s.db, func(ctx context.Context) error {
		read := s.WithContext(ctx)
		var err error
		if _, err = read.BrowseEntities(viewer, BrowseRequest{Library: "a", Profile: "p", ViewerFence: "f", Pivot: "movies", Limit: 2}); err != nil {
			return err
		}
		bound := BoundBrowseQuery{Where: "1=1", Library: "a", Pivot: "movies", Sorts: []BrowseSortSelection{{Field: "random", Direction: "asc", Seed: 9}}, snapshot: dbwork.Snapshot(ctx)}
		seeded, err = read.BrowseSelectionAggregate(BrowseRequest{Library: "a"}, viewer, bound, authority)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if seeded.Selector.Query.LibraryID != "" || seeded.Total != 0 {
		t.Fatalf("seeded view was not omitted: %+v", seeded)
	}
	// A relative-time predicate would match a different set when the job runs.
	var relative BrowseSelectionDescriptor
	if err := dbwork.WithReadSnapshot(context.Background(), s.db, func(ctx context.Context) error {
		read := s.WithContext(ctx)
		bound := BoundBrowseQuery{Where: "1=1", Library: "a", Pivot: "movies", Sorts: []BrowseSortSelection{{Field: "title", Direction: "asc"}},
			Query: &BrowseNode{Field: "addedAt", Operator: "within-last-days", Value: float64(7)}, snapshot: dbwork.Snapshot(ctx)}
		var err error
		relative, err = read.BrowseSelectionAggregate(BrowseRequest{Library: "a"}, viewer, bound, authority)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if relative.Selector.Query.LibraryID != "" {
		t.Fatalf("relative view was not omitted: %+v", relative)
	}
}

func TestBrowseSelectionAggregateEchoesTheCompiledFilterAndEmptyView(t *testing.T) {
	_, s, _ := browseFixture(t)
	viewer := Viewer{Profile: "p", Fence: "f", Libraries: []string{"a"}}
	query, err := ParseBrowseQuery(json.RawMessage(`{"variant":"predicate","field":"year","operator":"at-least","value":2000}`), "query")
	if err != nil {
		t.Fatal(err)
	}
	_, whole := pageAndAggregate(t, s, viewer, BrowseRequest{Library: "a", Profile: "p", ViewerFence: "f", Pivot: "movies", Query: query, Limit: 2},
		HomeActionAuthority{Bulk: true, Playback: true, Personal: true, Organize: true})
	var echoed BrowseNode
	if err = json.Unmarshal(whole.Selector.Query.Filter, &echoed); err != nil {
		t.Fatal(err)
	}
	if echoed.Field != "year" || echoed.Operator != "at-least" {
		t.Fatalf("filter echo %+v from %s", echoed, whole.Selector.Query.Filter)
	}
	if whole.Total != 3 {
		t.Fatalf("filtered total %d", whole.Total)
	}
	// An empty leaf view still publishes the descriptor, so the client can see
	// that the whole view is empty rather than guess.
	none, err := ParseBrowseQuery(json.RawMessage(`{"variant":"predicate","field":"year","operator":"at-least","value":3000}`), "query")
	if err != nil {
		t.Fatal(err)
	}
	_, empty := pageAndAggregate(t, s, viewer, BrowseRequest{Library: "a", Profile: "p", ViewerFence: "f", Pivot: "movies", Query: none, Limit: 2}, HomeActionAuthority{Bulk: true})
	if empty.Total != 0 || len(empty.Types) != 0 || empty.Selector.Query.LibraryID != "a" || empty.CatalogRevision == "" {
		t.Fatalf("empty view %+v", empty)
	}
}

// A container pivot is omitted: the job selector expands it to members, so a
// whole-view action would not act on the titles the bar counts.
func TestBrowseSelectionAggregateOmitsContainerPivots(t *testing.T) {
	c := catalogtest.Open(t)
	shows := c.Library("tv", "Shows", "tv", "/tv")
	show := c.Show(shows, "A Show", 2020)
	season := c.Season(show, 1)
	c.Episode(show, season, 1, "/tv/e1.mkv")
	music := c.Library("mu", "Music", "music", "/mu")
	artist := c.Artist(music, "An Artist")
	album := c.Album(artist, "An Album", 2020)
	c.Song(album, 1, "/mu/s.mkv", "A Song")
	c.Drain()
	s := New(c.DB)
	viewer := Viewer{Profile: "p", Fence: "f", Libraries: []string{"tv", "mu"}}
	authority := HomeActionAuthority{Personal: true, Organize: true, Bulk: true, Playback: true, Queue: true, Downloads: true, Metadata: true, Refresh: true}
	for _, pivot := range []struct{ library, id string }{{"tv", "shows"}, {"mu", "artists"}, {"mu", "albums"}} {
		var page BrowseResult
		var whole BrowseSelectionDescriptor
		if err := dbwork.WithReadSnapshot(context.Background(), s.db, func(ctx context.Context) error {
			read := s.WithContext(ctx)
			var err error
			if page, err = read.BrowseEntities(viewer, BrowseRequest{Library: pivot.library, Profile: "p", ViewerFence: "f", Pivot: pivot.id, Limit: 5}); err != nil {
				return err
			}
			whole, err = read.BrowseSelectionAggregate(BrowseRequest{Library: pivot.library}, viewer, *page.Bound, authority)
			return err
		}); err != nil {
			t.Fatal(err)
		}
		if page.PageInfo.Total == 0 {
			t.Fatalf("%s/%s fixture empty", pivot.library, pivot.id)
		}
		if whole.Selector.Query.LibraryID != "" || whole.Total != 0 || len(whole.Types) != 0 {
			t.Fatalf("%s/%s container pivot was published: %+v", pivot.library, pivot.id, whole)
		}
	}
}

// The shared availability predicate is viewer-bound for containers: a show is
// available only through a member this viewer may see and play.
func TestHomeActionAvailabilityIsViewerBoundForContainers(t *testing.T) {
	c := catalogtest.Open(t)
	shows := c.Library("tv", "Shows", "tv", "/tv")
	show := c.Show(shows, "Half Hidden", 2020)
	season := c.Season(show, 1)
	hiddenAvailable := c.Episode(show, season, 1, "/tv/hidden.mkv")
	visibleUnavailable := c.Episode(show, season, 2, "/tv/unavailable.mkv")
	c.Attributes(hiddenAvailable.ID, "label", "private")
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		return compactcatalog.SetAssetTx(ctx, tx, visibleUnavailable.Asset, map[string]any{"available": false})
	})
	c.Drain()
	s := New(c.DB)
	read := func(viewer Viewer) int {
		t.Helper()
		predicate, args := homeActionAvailableSQLFor(viewer, "e.id")
		args = append(args, show.ID)
		var available int
		if err := dbwork.WithReadSnapshot(context.Background(), s.db, func(ctx context.Context) error {
			return s.WithContext(ctx).read().QueryRow(`SELECT CASE WHEN `+predicate+` THEN 1 ELSE 0 END FROM catalog_entities e JOIN catalog_kinds k ON k.id=e.kind LEFT JOIN catalog_item_availability a ON a.entity_id=e.id AND a.retired=0 WHERE e.id=?`, args...).Scan(&available)
		}); err != nil {
			t.Fatal(err)
		}
		return available
	}
	limited := Viewer{Profile: "p", Fence: "f", Libraries: []string{"tv"}, Restrictions: identity.ContentRestrictions{BlockedLabels: []string{"private"}}}
	if read(limited) != 0 {
		t.Fatal("a hidden available member made the container available")
	}
	open := Viewer{Profile: "p", Fence: "f", Libraries: []string{"tv"}}
	if read(open) != 1 {
		t.Fatal("an open viewer lost the available member")
	}
}

// A kind the projection skips offers nothing, whatever the grants.
func TestSelectionOffersUnsupportedKindsNothing(t *testing.T) {
	authority := HomeActionAuthority{Personal: true, Organize: true, Bulk: true, Playback: true, Queue: true, Metadata: true, Refresh: true, Downloads: true,
		DeleteLibrary: func(string) (bool, error) { return true, nil }}
	for _, kind := range []string{"author", "extra", "disc", "category", "playlist"} {
		if offers := selectionOffers(authority, kind, true, true); offers != (HomeSelectionActions{}) {
			t.Fatalf("%s offered %+v", kind, offers)
		}
	}
}

// The aggregate must be read in the page's own snapshot: Bound, authority and
// restriction predicate all belong to that state. A Bound carried out of its
// snapshot is refused rather than answered under a later state.
func TestBrowseSelectionAggregateRefusesAnotherSnapshot(t *testing.T) {
	_, s, _ := browseFixture(t)
	viewer := Viewer{Profile: "p", Fence: "f", Libraries: []string{"a"}}
	request := BrowseRequest{Library: "a", Profile: "p", ViewerFence: "f", Pivot: "movies", Limit: 2}
	var bound BoundBrowseQuery
	if err := dbwork.WithReadSnapshot(context.Background(), s.db, func(ctx context.Context) error {
		page, err := s.WithContext(ctx).BrowseEntities(viewer, request)
		bound = *page.Bound
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.BrowseSelectionAggregate(request, viewer, bound, HomeActionAuthority{}); !errors.Is(err, sql.ErrTxDone) {
		t.Fatalf("outside a snapshot: %v", err)
	}
	// A different snapshot is still the wrong state.
	if err := dbwork.WithReadSnapshot(context.Background(), s.db, func(ctx context.Context) error {
		_, err := s.WithContext(ctx).BrowseSelectionAggregate(request, viewer, bound, HomeActionAuthority{})
		if !errors.Is(err, sql.ErrTxDone) {
			t.Fatalf("carried Bound answered in a later snapshot: %v", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

// Every offered whole-view action must execute to exactly the displayed titles.
// The descriptor is a promise the job really acts on those ids, so this runs the
// compiled SELECT and compares it with the page's own entities.
func TestBrowseSelectionDescriptorReplaysToTheSameTitles(t *testing.T) {
	c := catalogtest.Open(t)
	movies := c.Library("m", "Movies", "movie", "/m")
	shows := c.Library("tv", "Shows", "tv", "/tv")
	music := c.Library("mu", "Music", "music", "/mu")
	books := c.Library("ab", "Audiobooks", "audiobook", "/ab")
	for index, title := range []string{"Alpha", "Beta", "Gamma"} {
		c.Movie(movies, fmt.Sprintf("/m/%d.mkv", index), title, 2000+index)
	}
	show := c.Show(shows, "A Show", 2020)
	season := c.Season(show, 1)
	c.Episode(show, season, 1, "/tv/e1.mkv")
	c.Episode(show, season, 2, "/tv/e2.mkv")
	artist := c.Artist(music, "An Artist")
	album := c.Album(artist, "An Album", 2020)
	c.Song(album, 1, "/mu/one.flac", "A Song")
	c.Song(album, 2, "/mu/two.flac", "Another Song")
	book := c.Book(books, "A Book", "An Author")
	c.BookFile(book, 1, "/ab/part1.m4b")
	c.Drain()
	s := New(c.DB)
	viewer := Viewer{Profile: "p", Fence: "f", Libraries: []string{"m", "tv", "mu", "ab"}}
	authority := HomeActionAuthority{Playback: true, Queue: true, Personal: true, Organize: true, Bulk: true, Metadata: true, Refresh: true}
	principal := identity.Principal{Viewer: identity.Viewer{Authority: "local", AccountID: "a", ProfileID: "p", Role: "owner"}}
	for _, pivot := range []struct{ library, id string }{{"m", "movies"}, {"tv", "episodes"}, {"mu", "songs"}, {"ab", "parts"}} {
		var page BrowseResult
		var whole BrowseSelectionDescriptor
		if err := dbwork.WithReadSnapshot(context.Background(), s.db, func(ctx context.Context) error {
			read := s.WithContext(ctx)
			var err error
			if page, err = read.BrowseEntities(viewer, BrowseRequest{Library: pivot.library, Profile: "p", ViewerFence: "f", Pivot: pivot.id, Limit: 100}); err != nil {
				return err
			}
			whole, err = read.BrowseSelectionAggregate(BrowseRequest{Library: pivot.library}, viewer, *page.Bound, authority)
			return err
		}); err != nil {
			t.Fatal(err)
		}
		if whole.Selector.Query.LibraryID != pivot.library || whole.Total != len(page.Entries) || whole.Total == 0 {
			t.Fatalf("%s/%s descriptor %+v page %d", pivot.library, pivot.id, whole, len(page.Entries))
		}
		displayed := map[int64]bool{}
		for _, entry := range page.Entries {
			var id int64
			if err := c.DB.QueryRow(`SELECT id FROM catalog_entities WHERE public_id=pid_blob(?)`, entry.ID).Scan(&id); err != nil {
				t.Fatal(err)
			}
			displayed[id] = true
		}
		query := whole.Selector.Query
		for _, command := range []string{"personal-state", "playlist-add", "collection-add", "metadata-edit", "refresh"} {
			where, args, err := s.bulkSelection(principal, viewer, JobRequest{Command: command, Selector: JobSelector{Query: &query}})
			if err != nil || where == "" {
				t.Fatalf("%s/%s %s compiled: %v", pivot.library, pivot.id, command, err)
			}
			rows, err := c.DB.Query(where, args...)
			if err != nil {
				t.Fatalf("%s/%s %s executed: %v", pivot.library, pivot.id, command, err)
			}
			got := map[int64]bool{}
			for rows.Next() {
				var id int64
				if err = rows.Scan(&id); err != nil {
					rows.Close()
					t.Fatal(err)
				}
				got[id] = true
			}
			rows.Close()
			if len(got) != len(displayed) {
				t.Fatalf("%s/%s %s targeted %d titles, page showed %d", pivot.library, pivot.id, command, len(got), len(displayed))
			}
			for id := range got {
				if !displayed[id] {
					t.Fatalf("%s/%s %s targeted a title the page did not show", pivot.library, pivot.id, command)
				}
			}
		}
	}
}

// Container pivots are omitted rather than compiled-and-advertised.
func TestBrowseSelectionDescriptorOmitsContainers(t *testing.T) {
	c := catalogtest.Open(t)
	shows := c.Library("tv", "Shows", "tv", "/tv")
	show := c.Show(shows, "A Show", 2020)
	season := c.Season(show, 1)
	c.Episode(show, season, 1, "/tv/e1.mkv")
	c.Drain()
	s := New(c.DB)
	viewer := Viewer{Profile: "p", Fence: "f", Libraries: []string{"tv"}}
	var page BrowseResult
	var whole BrowseSelectionDescriptor
	if err := dbwork.WithReadSnapshot(context.Background(), s.db, func(ctx context.Context) error {
		read := s.WithContext(ctx)
		var err error
		if page, err = read.BrowseEntities(viewer, BrowseRequest{Library: "tv", Pivot: "shows", Limit: 5}); err != nil {
			return err
		}
		whole, err = read.BrowseSelectionAggregate(BrowseRequest{Library: "tv"}, viewer, *page.Bound, HomeActionAuthority{Bulk: true, Personal: true, Playback: true})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if whole.Selector.Query.LibraryID != "" || whole.Total != 0 {
		t.Fatalf("shows pivot was published: %+v", whole)
	}
}
