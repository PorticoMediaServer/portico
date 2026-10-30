package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"reflect"
	"testing"
)

func TestBulkMetadataPreviewKeepsDisplayedShowAndRefusesWrongScope(t *testing.T) {
	service, _, _, viewer, _, fixture, names := bulkFixture(t, 3)
	ctx := context.Background()
	gated, err := dbwork.BeginSnapshot(ctx, fixture.DB)
	if err != nil {
		t.Fatal(err)
	}
	defer gated.Rollback()
	target := BulkMetadataTarget{Kind: "show", ContentKind: "show", ID: names["show"].Public, LibraryID: "lib"}
	selected, err := service.BulkMetadataSelectionTx(ctx, gated.Tx(), viewer, BulkMetadataPreviewRequest{Targets: []BulkMetadataTarget{target}})
	if err != nil {
		t.Fatal(err)
	}
	var count int
	var id int64
	if err = gated.Tx().QueryRowContext(ctx, `SELECT count(*),min(id) FROM (`+selected.SQL+`)`, selected.Args...).Scan(&count, &id); err != nil {
		t.Fatal(err)
	}
	if count != 1 || id != names["show"].ID {
		t.Fatalf("show preview expanded into descendants: count=%d id=%d", count, id)
	}
	viewer.Libraries = []string{}
	if _, err = service.BulkMetadataSelectionTx(ctx, gated.Tx(), viewer, BulkMetadataPreviewRequest{Targets: []BulkMetadataTarget{target}}); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("wrong scope disclosed metadata: %v", err)
	}
}
func TestBulkMetadataPreviewQueryBindsRevisionAndDoesNotExpandShows(t *testing.T) {
	service, _, _, viewer, _, fixture, names := bulkFixture(t, 3)
	ctx := context.Background()
	sorts := []BrowseSortSelection{{Field: "title", Direction: "asc"}}
	browse, err := service.BrowseEntities(viewer, BrowseRequest{Library: "lib", Pivot: "shows", Limit: 1, Sort: sorts})
	if err != nil {
		t.Fatal(err)
	}
	gated, err := dbwork.BeginSnapshot(ctx, fixture.DB)
	if err != nil {
		t.Fatal(err)
	}
	defer gated.Rollback()
	request := BulkMetadataPreviewRequest{Selector: &JobSelector{Query: &JobQuery{LibraryID: "lib", Pivot: "shows", Sort: sorts}}, Expected: &JobExpected{CatalogRevision: browse.PageInfo.Revision}}
	selected, err := service.BulkMetadataSelectionTx(ctx, gated.Tx(), viewer, request)
	if err != nil {
		t.Fatal(err)
	}
	var count int
	var id int64
	if err = gated.Tx().QueryRowContext(ctx, `SELECT count(*),min(id) FROM (`+selected.SQL+`)`, selected.Args...).Scan(&count, &id); err != nil {
		t.Fatal(err)
	}
	if count != 1 || id != names["show"].ID {
		t.Fatalf("query preview changed entity kind: %d %d", count, id)
	}
	request.Expected = &JobExpected{CatalogRevision: "stale"}
	if _, err = service.BulkMetadataSelectionTx(ctx, gated.Tx(), viewer, request); !errors.Is(err, ErrStaleContinuation) {
		t.Fatalf("stale query accepted: %v", err)
	}
}

func TestBulkMetadataPreviewLockFencePreservesOrdinaryBrowseRevision(t *testing.T) {
	service, _, _, viewer, _, fixture, names := bulkFixture(t, 3)
	request := BulkMetadataPreviewRequest{Targets: []BulkMetadataTarget{{Kind: "show", ContentKind: "show", ID: names["show"].Public, LibraryID: "lib"}}}
	fence := func() string {
		t.Helper()
		var page BulkMetadataSelectionPage
		err := dbwork.WithReadSnapshot(context.Background(), fixture.DB, func(ctx context.Context) error {
			var err error
			page, err = service.BulkMetadataSelectionPageTx(ctx, dbwork.Snapshot(ctx), viewer, request, 0)
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		return page.Fence
	}
	browse := func() string {
		t.Helper()
		page, err := service.BrowseEntities(viewer, BrowseRequest{Library: "lib", Pivot: "shows", Limit: 1})
		if err != nil {
			t.Fatal(err)
		}
		return page.PageInfo.Revision
	}
	beforeFence, beforeBrowse := fence(), browse()
	fixture.Exec(`INSERT INTO metadata_owner_fields(kind,entity_id,field,value,automatic_value,locked,actor,observed_at) VALUES('show',?,'year','2026','2026',1,'owner','now')`, names["show"].ID)
	if fence() == beforeFence {
		t.Fatal("owner lock did not retire the private preview fence")
	}
	if browse() != beforeBrowse {
		t.Fatal("owner lock invalidated an unchanged ordinary browse receipt")
	}
}

func TestBulkMetadataPreviewPagesBoundCandidatesAndAdvanceEmptyPages(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	service, _, _, viewer, _, fixture, names := bulkFixture(t, BulkMetadataPageSize*2+1)
	sorts := []BrowseSortSelection{{Field: "title", Direction: "asc"}}
	browse, err := service.BrowseEntities(viewer, BrowseRequest{Library: "lib", Pivot: "shows", Limit: 1, Sort: sorts})
	if err != nil {
		t.Fatal(err)
	}
	request := BulkMetadataPreviewRequest{Selector: &JobSelector{Query: &JobQuery{LibraryID: "lib", Pivot: "shows", Sort: sorts}}, Expected: &JobExpected{CatalogRevision: browse.PageInfo.Revision}}
	var after int64
	var fence string
	ids := []int64{}
	emptyPages := 0
	pages := 0
	for {
		var page BulkMetadataSelectionPage
		err = dbwork.WithReadSnapshot(context.Background(), fixture.DB, func(ctx context.Context) error {
			var e error
			page, e = service.BulkMetadataSelectionPageTx(ctx, dbwork.Snapshot(ctx), viewer, request, after)
			if e != nil {
				return e
			}
			rows, e := dbwork.Snapshot(ctx).QueryContext(ctx, page.Selection.SQL, page.Selection.Args...)
			if e != nil {
				return e
			}
			defer rows.Close()
			matches := 0
			for rows.Next() {
				var id int64
				if e = rows.Scan(&id); e != nil {
					return e
				}
				ids = append(ids, id)
				matches++
			}
			if matches == 0 {
				emptyPages++
			}
			if matches > BulkMetadataPageSize {
				t.Fatal("unbounded page", matches)
			}
			return rows.Err()
		})
		if err != nil {
			t.Fatal(err)
		}
		pages++
		if fence == "" {
			fence = page.Fence
		} else if page.Fence != fence {
			t.Fatal("unchanged source changed its fence")
		}
		if page.Selection.Revision != request.Expected.CatalogRevision {
			t.Fatal("selection revision lost")
		}
		if page.Done {
			break
		}
		if page.After <= after {
			t.Fatal("empty candidate page did not advance")
		}
		after = page.After
		if pages > 5 {
			t.Fatal("source did not exhaust")
		}
	}
	if len(ids) != 1 || ids[0] != names["show"].ID || emptyPages < 2 || pages != 3 {
		t.Fatalf("selection expanded, truncated or stalled: pages=%d empty=%d ids=%v", pages, emptyPages, ids)
	}
}

func TestBulkMetadataPreviewBoundedPredicatesMatchBrowse(t *testing.T) {
	db, s, names := browseFixture(t)
	c := catalogtest.New(t, db)
	show := c.Show(c.Handle("tv"), "Bounded Show", 2020)
	season := c.Season(show, 1)
	episode := c.Episode(show, season, 1, "/tv/bounded.mkv")
	c.Genres(show.ID, "tvdb", "Drama")
	c.Genres(episode.ID, "tvdb", "Comedy")
	collection := c.Collection(c.Handle("a"), "Bounded Collection", names["m1"], names["m2"])
	owner := identity.PersonalKey(identity.Viewer{Authority: "local", AccountID: "account", ProfileID: "profile"})
	c.Exec(`INSERT INTO saved_resources(id,kind,owner_key,owner_authority,owner_account,owner_profile,name,created_at,visibility) VALUES('bounded-saved','collection',?,'local','account','profile','Saved','now','server')`, owner)
	c.Exec(`INSERT INTO saved_resource_entries(id,resource_id,item_id) VALUES('bounded-a','bounded-saved',?),('bounded-show','bounded-saved',?)`, names["m1"].ID, show.ID)
	c.Drain()
	queries := []string{
		`{"field":"genre","operator":"contains","value":"Drama"}`,
		`{"field":"genre","operator":"contains-any","value":["Drama","Comedy"]}`,
		`{"field":"genre","operator":"contains-all","value":["Action","Drama"]}`,
		`{"field":"favorite","operator":"equals","value":true}`,
		`{"field":"favorite","operator":"equals","value":false}`,
		`{"field":"playState","operator":"in","value":["played"]}`,
		`{"field":"playState","operator":"in","value":["in-progress"]}`,
		`{"field":"playState","operator":"in","value":["unplayed"]}`,
		`{"field":"playState","operator":"not-in","value":["unplayed","played"]}`,
		`{"field":"search","operator":"contains","value":"Alpha"}`,
		`{"field":"title","operator":"contains","value":"Alpha"}`,
		fmt.Sprintf(`{"field":"collection","operator":"equals","value":%q}`, collection.Public),
		fmt.Sprintf(`{"field":"memberOf","operator":"equals","value":%q}`, show.Public),
		`{"field":"savedCollection","operator":"equals","value":"bounded-saved"}`,
		`{"field":"savedCollection","operator":"not-in","value":["bounded-saved"]}`,
		fmt.Sprintf(`{"field":"episodeGroup","operator":"equals","value":%q}`, show.Public+"/all"),
		`{"field":"match","operator":"equals","value":"matched"}`,
		`{"field":"match","operator":"equals","value":"unmatched"}`,
	}
	for _, profile := range []string{"p", owner} {
		for _, query := range queries {
			t.Run(profile+"/"+query, func(t *testing.T) {
				node, err := ParseBrowseQuery(json.RawMessage(query), "query")
				if err != nil {
					t.Fatal(err)
				}
				var got, want []int64
				err = dbwork.WithReadSnapshot(context.Background(), db, func(ctx context.Context) error {
					scoped := s.WithContext(ctx)
					for _, bounded := range []bool{false, true} {
						compiler := &browseCompiler{profile: profile, bounded: bounded, groupSearch: bounded}
						if !bounded {
							compiler.count, compiler.ids = scoped.countRows, scoped.entityIDs
						}
						where, e := compiler.compile(node)
						if e != nil {
							return e
						}
						ids, e := scoped.entityIDs(`SELECT e.entity_id FROM catalog_browse_rows e WHERE `+where+` ORDER BY e.entity_id`, compiler.args...)
						if e != nil {
							return e
						}
						if bounded {
							got = ids
						} else {
							want = ids
						}
					}
					return nil
				})
				if err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("bounded=%v browse=%v", got, want)
				}
			})
		}
	}
}

func TestBulkMetadataPreviewExactCandidateBoundaryNeedsExhaustionPage(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	// A show + a season + 62 episodes fills exactly one source page.
	s, _, _, viewer, _, c, _ := bulkFixture(t, BulkMetadataPageSize-2)
	sorts := []BrowseSortSelection{{Field: "title", Direction: "asc"}}
	browse, err := s.BrowseEntities(viewer, BrowseRequest{Library: "lib", Pivot: "shows", Sort: sorts, Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	request := BulkMetadataPreviewRequest{Selector: &JobSelector{Query: &JobQuery{LibraryID: "lib", Pivot: "shows", Sort: sorts}}, Expected: &JobExpected{CatalogRevision: browse.PageInfo.Revision}}
	var after int64
	for index := 0; index < 2; index++ {
		var page BulkMetadataSelectionPage
		err = dbwork.WithReadSnapshot(context.Background(), c.DB, func(ctx context.Context) error {
			var e error
			page, e = s.BulkMetadataSelectionPageTx(ctx, dbwork.Snapshot(ctx), viewer, request, after)
			return e
		})
		if err != nil {
			t.Fatal(err)
		}
		if index == 0 {
			if page.Done || page.After <= after {
				t.Fatalf("full page incorrectly exhausted: %+v", page)
			}
			after = page.After
		} else if !page.Done || page.After != after {
			t.Fatalf("empty exhaustion page stalled: %+v", page)
		}
	}
}
