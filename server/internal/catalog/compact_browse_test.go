package catalog

import (
	"context"
	"database/sql"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
)

// A queued change (a scan) doesn't fail browse: the page serves the last
// published generation and the change appears once it publishes.
func TestCompactBrowseServesThePublishedGenerationWhileChangesQueue(t *testing.T) {
	db, service, names := tl10BrowseFixture(t)
	c := catalogtest.New(t, db)
	viewer := Viewer{Profile: "p", Fence: "f", Libraries: []string{"a"}}
	request := BrowseRequest{Library: "a", Pivot: "movies", Limit: 20}
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		_, _, err := compactcatalog.UpsertAssetTx(ctx, tx, compactcatalog.Asset{Path: "/a/m1.mkv", Size: 1000, ModifiedNS: 1, Container: "mkv", VideoCodec: "h264", AudioCodec: "aac", Width: 1920, Height: 1080, Duration: 333.25})
		return err
	})
	c.Fields(names["m1"].ID, map[string]any{"title": "Renamed"})
	title := func() string {
		t.Helper()
		page, err := service.BrowseEntities(viewer, request)
		if err != nil {
			t.Fatalf("browse while changes are queued: %v", err)
		}
		if page.PageInfo.Total != 5 {
			t.Fatalf("browse lost rows: %+v", page.PageInfo)
		}
		for _, entry := range page.Entries {
			if entry.ID == names["m1"].Public {
				return entry.Title
			}
		}
		t.Fatal("m1 missing")
		return ""
	}
	if got := title(); got != "Renamed" {
		t.Fatalf("synchronous catalogue facts were not visible immediately: %q", got)
	}
	settleCompact(t, db)
	if got := title(); got != "Renamed" {
		t.Fatalf("published title: %q", got)
	}
	if err := compactcatalog.NewWorker(db).Rebuild(context.Background(), compactcatalog.DomainBrowseRows); err != nil {
		t.Fatal(err)
	}
	if _, err := service.BrowseEntities(viewer, request); err != nil {
		t.Fatalf("browse during a rebuild: %v", err)
	}
}

func TestCompactBrowseStaleClassNeverListsANowHiddenTitle(t *testing.T) {
	db, service, names := tl10RestrictionFixture(t)
	c := catalogtest.New(t, db)
	restrictions := restrictionOf(ceiling(13), true)
	viewer := Viewer{Profile: "p", Fence: "f", Libraries: []string{"a"}, Restrictions: restrictions}
	request := BrowseRequest{Library: "a", Pivot: "movies", Limit: 20}
	c.Drain()
	if err := service.RebuildVisibilityClass(context.Background(), "a", viewer.EffectiveRestrictions()); err != nil {
		t.Fatal(err)
	}
	c.Drain()
	before, err := service.BrowseEntities(viewer, request)
	if err != nil {
		t.Fatal(err)
	}
	if before.PageInfo.Total == 0 {
		t.Fatal("expected a nonempty published restricted count")
	}
	var priorRevision int64
	if err := db.QueryRow(`SELECT revision FROM library_revisions WHERE library_id='a'`).Scan(&priorRevision); err != nil {
		t.Fatal(err)
	}
	c.Attributes(names["kids"].ID, "contentRating", "R")
	c.Drain()
	var updatedRevision int64
	if err := db.QueryRow(`SELECT revision FROM library_revisions WHERE library_id='a'`).Scan(&updatedRevision); err != nil {
		t.Fatal(err)
	}
	if updatedRevision <= priorRevision {
		t.Fatalf("attribute update did not invalidate library revision: %d to %d", priorRevision, updatedRevision)
	}
	// The class is one library revision behind: it serves its last
	// published generation (its refresh is queued), but every row is checked
	// against the current rating, so the now-adult title is never listed.
	stale, err := service.BrowseEntities(viewer, request)
	if err != nil {
		t.Fatalf("a class one revision behind failed browse: %v", err)
	}
	for _, entry := range stale.Entries {
		if entry.ID == names["kids"].Public {
			t.Fatal("a stale class listed a title now above the ceiling")
		}
	}
	if err := service.RebuildVisibilityClass(context.Background(), "a", restrictions); err != nil {
		t.Fatal(err)
	}
	c.Drain()
	after, err := service.BrowseEntities(viewer, request)
	if err != nil {
		t.Fatal(err)
	}
	if after.PageInfo.Total != before.PageInfo.Total-1 {
		t.Fatalf("refreshed restricted count = %d, want %d", after.PageInfo.Total, before.PageInfo.Total-1)
	}
	for _, entry := range after.Entries {
		if entry.ID == names["kids"].Public {
			t.Fatal("updated adult title remained visible after class refresh")
		}
	}
}

func TestPublishedRestrictedBrowseSurvivesAncillaryBackfill(t *testing.T) {
	db, service, _ := tl10RestrictionFixture(t)
	c := catalogtest.New(t, db)
	viewer := Viewer{Profile: "p", Fence: "f", Libraries: []string{"a"}, Restrictions: restrictionOf(ceiling(13), true)}
	c.Drain()
	if err := service.RebuildVisibilityClass(context.Background(), "a", viewer.EffectiveRestrictions()); err != nil {
		t.Fatal(err)
	}
	c.Drain()
	for _, table := range []string{"catalog_visibility_sort_state", "catalog_personal_sort_state", "catalog_movie_category_visible_state", "catalog_collection_visible_state"} {
		if _, err := db.Exec(`UPDATE ` + table + ` SET backfill_done=0 WHERE id=1`); err != nil {
			t.Fatal(err)
		}
	}
	for _, field := range []string{"added", "personalRating"} {
		page, err := service.BrowseEntities(viewer, BrowseRequest{Library: "a", Pivot: "movies", Limit: 20, Sort: []BrowseSortSelection{{Field: field, Direction: "desc"}}})
		if err != nil || len(page.Entries) == 0 {
			t.Fatalf("%s discarded published rows: %+v %v", field, page, err)
		}
	}
	for _, view := range []string{"browse", "categories"} {
		page, err := service.Content(ContentRequest{Viewer: viewer, Library: "a", View: view, Limit: 20})
		if err != nil || len(page.Sections) == 0 {
			t.Fatalf("%s discarded its published document: %+v %v", view, page, err)
		}
	}
}

func TestContentWithoutARestrictionClassAnswersQuietly(t *testing.T) {
	_, service, _ := tl10RestrictionFixture(t)
	viewer := Viewer{Profile: "p", Fence: "f", Libraries: []string{"a"}, Restrictions: restrictionOf(ceiling(1), true)}
	for _, view := range []string{"browse", "categories", "collections"} {
		if _, err := service.Content(ContentRequest{Viewer: viewer, Library: "a", View: view, Limit: 20}); err != nil {
			t.Fatalf("%s returned preparation failure: %v", view, err)
		}
	}
}
