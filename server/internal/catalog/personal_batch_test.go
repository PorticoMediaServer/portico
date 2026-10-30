package catalog

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

func phase34ListFixture(t *testing.T) (*sql.DB, *Service, catalogtest.Names) {
	t.Helper()
	c := catalogtest.Open(t)
	movies := c.Library("m", "Movies", "movie", "/m")
	other := c.Library("other", "Other", "movie", "/o")
	names := catalogtest.Names{
		"a":       c.Movie(movies, "/m/a.mp4", "Alpha", 2001),
		"b":       c.Movie(movies, "/m/b.mp4", "Beta", 2002),
		"c":       c.Movie(movies, "/m/c.mp4", "Gamma", 2003),
		"outside": c.Movie(other, "/o/outside.mp4", "Outside", 2004),
	}
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		for name, duration := range map[string]float64{"a": 90, "b": 120, "c": 100, "outside": 90} {
			if err := compactcatalog.SetAssetTx(ctx, tx, names[name].Asset, map[string]any{"duration": duration}); err != nil {
				return err
			}
		}
		return nil
	})
	c.Drain()
	return c.DB, New(c.DB), names
}

func TestSetPersonalBatchAppliesEachItemIndependently(t *testing.T) {
	db, s, names := phase34ListFixture(t)
	yes := true
	missingID := identity.Token()
	m := PersonalBatchMutation{OperationID: "batch-1", Items: []PersonalBatchItem{
		{ItemID: names["a"].Public, Watchlisted: &yes},
		{ItemID: names["b"].Public, Watched: &yes, Favorite: &yes},
		{ItemID: missingID, Watchlisted: &yes},
		{ItemID: names["outside"].Public, Watchlisted: &yes},
	}}
	out, e := s.SetPersonalBatch("local:account", "p", m, func(tx *sql.Tx, item string) error {
		var library string
		if err := tx.QueryRow(`SELECT l.library_id FROM catalog_entities e JOIN catalog_libraries l ON l.id=e.library_id WHERE e.public_id=pid_blob(?)`, item).Scan(&library); err != nil {
			return err
		}
		if library != "m" {
			return identity.ErrUnauthorized
		}
		return nil
	})
	if e != nil {
		t.Fatal(e)
	}
	if out.Updated != 2 || out.Failed != 2 || len(out.Results) != 4 {
		t.Fatal("partial application", out)
	}
	byID := map[string]PersonalBatchResult{}
	for _, result := range out.Results {
		byID[result.ItemID] = result
	}
	if !byID[names["a"].Public].OK || !byID[names["a"].Public].Personal.Watchlisted {
		t.Fatal("watchlist not applied", byID[names["a"].Public])
	}
	// Several fields on one row are several intents; both must land.
	if !byID[names["b"].Public].OK || !byID[names["b"].Public].Personal.Watched || !byID[names["b"].Public].Personal.Favorite {
		t.Fatal("multi-field row", byID[names["b"].Public])
	}
	if byID[missingID].OK || byID[missingID].Code != "not_found" {
		t.Fatal("missing item", byID[missingID])
	}
	if byID[names["outside"].Public].OK || byID[names["outside"].Public].Code != "invalid_request" {
		t.Fatal("unauthorized item", byID[names["outside"].Public])
	}
	// A replay returns the first outcome rather than re-judging revisions that
	// its own first attempt advanced.
	replay, e := s.SetPersonalBatch("local:account", "p", m, nil)
	if e != nil {
		t.Fatal(e)
	}
	if replay.Updated != out.Updated || replay.Failed != out.Failed {
		t.Fatal("replay diverged", replay)
	}
	var revision int64
	if e = db.QueryRow(`SELECT revision FROM personal_items WHERE profile_id='p' AND item_id=?`, names["a"].ID).Scan(&revision); e != nil || revision != 1 {
		t.Fatal("replay double applied", revision, e)
	}
	// A different body under the same operationId is a conflict, never a merge.
	changed := m
	changed.Items = m.Items[:1]
	if _, e = s.SetPersonalBatch("local:account", "p", changed, nil); !errors.Is(e, ErrOperationConflict) {
		t.Fatal("operation reuse", e)
	}
	// An explicit stale revision is rejected for that row alone.
	stale := int64(99)
	conflict, e := s.SetPersonalBatch("local:account", "p", PersonalBatchMutation{OperationID: "batch-2", Items: []PersonalBatchItem{{ItemID: names["a"].Public, ExpectedRevision: &stale, Favorite: &yes}, {ItemID: names["c"].Public, Favorite: &yes}}}, nil)
	if e != nil {
		t.Fatal(e)
	}
	if conflict.Updated != 1 || conflict.Failed != 1 || conflict.Results[0].Code != "personal_state_conflict" || !conflict.Results[1].OK {
		t.Fatal("conflict isolation", conflict)
	}
	if _, e = s.SetPersonalBatch("local:account", "p", PersonalBatchMutation{OperationID: "batch-3", Items: []PersonalBatchItem{{ItemID: names["a"].Public}}}, nil); e == nil {
		t.Fatal("empty intent accepted")
	}
}

func TestSavedListFiltersAndSorts(t *testing.T) {
	db, s, names := phase34ListFixture(t)
	if _, e := db.Exec(`INSERT INTO personal_items(profile_id,item_id,watchlisted,favorite,rating,revision,watched,last_played_at) VALUES
	('p',?,1,0,NULL,1,1,'2024-01-01'),('p',?,1,0,NULL,1,0,'2024-03-01'),('p',?,1,0,NULL,1,0,'2024-02-01')`, names["a"].ID, names["b"].ID, names["c"].ID); e != nil {
		t.Fatal(e)
	}
	if _, e := db.Exec(`INSERT INTO personal_watched_intents(profile_id,item_id,watched,authored_at) VALUES('p',?,1,'2024-01-01T00:00:00.000000000Z')`, names["a"].ID); e != nil {
		t.Fatal(e)
	}
	if _, e := db.Exec(`INSERT INTO progress(profile_id,item_id,position,unit,completed,playback_id) VALUES('p',?,45000,0,0,'playback')`, names["b"].ID); e != nil {
		t.Fatal(e)
	}
	libraries := []string{"m"}
	base := ContentRequest{Viewer: Viewer{Profile: "p", Fence: "f", Libraries: libraries}, ServerID: "s", Profile: "p", ViewerFence: "f", View: "watchlist", Limit: 40}
	all, e := s.Saved(base, libraries)
	if e != nil {
		t.Fatal(e)
	}
	if len(all.Sections[0].Entries) != 3 || all.Sections[0].TotalCount != 3 {
		t.Fatal("unfiltered", all.Sections)
	}
	counts := map[string]int{}
	for _, option := range all.Filters[0].Options {
		counts[option.ID] = option.Count
	}
	if counts["all"] != 3 || counts["unwatched"] != 2 || counts["inProgress"] != 1 {
		t.Fatal("published filter counts", counts)
	}
	unwatched := base
	unwatched.Filter = "unwatched"
	page, e := s.Saved(unwatched, libraries)
	if e != nil {
		t.Fatal(e)
	}
	if page.Sections[0].TotalCount != 2 || len(page.Sections[0].Entries) != 2 {
		t.Fatal("unwatched filter", page.Sections)
	}
	inProgress := base
	inProgress.Filter = "inProgress"
	page, e = s.Saved(inProgress, libraries)
	if e != nil {
		t.Fatal(e)
	}
	if len(page.Sections[0].Entries) != 1 || page.Sections[0].Entries[0].ID != names["b"].Public {
		t.Fatal("inProgress filter", page.Sections)
	}
	for _, check := range []struct{ sort, direction, first string }{
		{"updated", "desc", names["b"].Public},
		{"year", "desc", names["c"].Public},
		{"duration", "desc", names["b"].Public},
		{"progress", "desc", names["b"].Public},
		{"title", "asc", names["a"].Public},
	} {
		r := base
		r.Sort = check.sort
		r.Direction = check.direction
		sorted, err := s.Saved(r, libraries)
		if err != nil {
			t.Fatal(check.sort, err)
		}
		if sorted.Sections[0].Entries[0].ID != check.first {
			t.Fatal("sort", check.sort, sorted.Sections[0].Entries)
		}
		// The same sort must page without repeating or skipping a row.
		paged := r
		paged.Limit = 1
		first, err := s.Saved(paged, libraries)
		if err != nil {
			t.Fatal(check.sort, err)
		}
		paged.Cursor = first.Sections[0].NextCursor
		second, err := s.Saved(paged, libraries)
		if err != nil {
			t.Fatal(check.sort, err)
		}
		if second.Sections[0].Entries[0].ID == first.Sections[0].Entries[0].ID {
			t.Fatal("continuation repeated a row", check.sort, second.Sections[0].Entries)
		}
	}
	invalid := base
	invalid.Filter = "everything"
	if _, e = s.Saved(invalid, libraries); e == nil {
		t.Fatal("unknown filter accepted")
	}
}

func TestPersonalHistoryPeriodAndLibraryFilters(t *testing.T) {
	db, s, names := phase34ListFixture(t)
	if _, e := db.Exec(`INSERT INTO personal_history(id,profile_id,item_id,started_at,updated_at,sequence,position,unit,completed) VALUES
	('recent','p',?,strftime('%Y-%m-%dT%H:%M:%f','now','-1 hour')||'Z',strftime('%Y-%m-%dT%H:%M:%f','now')||'Z',1,10000,0,0),
	('old','p',?,'2020-01-01T00:00:00.000Z','2020-01-01T00:00:00.000Z',1,10000,0,1),
	('foreign','p',?,strftime('%Y-%m-%dT%H:%M:%f','now','-1 hour')||'Z',strftime('%Y-%m-%dT%H:%M:%f','now')||'Z',1,10000,0,0)`, names["a"].ID, names["b"].ID, names["outside"].ID); e != nil {
		t.Fatal(e)
	}
	libraries := []string{"m", "other"}
	all, e := s.History("s", "f", "p", "", "all", "", libraries, 40, Viewer{Profile: "p", Libraries: libraries})
	if e != nil || len(all.Entries) != 3 {
		t.Fatal("all", all, e)
	}
	recent, e := s.History("s", "f", "p", "", "24h", "", libraries, 40, Viewer{Profile: "p", Libraries: libraries})
	if e != nil || len(recent.Entries) != 2 || recent.Period != "24h" {
		t.Fatal("period window", recent, e)
	}
	scoped, e := s.History("s", "f", "p", "", "all", "m", libraries, 40, Viewer{Profile: "p", Libraries: libraries})
	if e != nil || len(scoped.Entries) != 2 || scoped.LibraryID != "m" {
		t.Fatal("library scope", scoped, e)
	}
	if _, e = s.History("s", "f", "p", "", "all", "forbidden", libraries, 40, Viewer{Profile: "p", Libraries: libraries}); !errors.Is(e, identity.ErrUnauthorized) {
		t.Fatal("unauthorized library accepted", e)
	}
	if _, e = s.History("s", "f", "p", "", "eternity", "", libraries, 40, Viewer{Profile: "p", Libraries: libraries}); !errors.Is(e, ErrCursor) {
		t.Fatal("unknown period accepted", e)
	}
}
