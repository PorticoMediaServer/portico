package catalog

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"reflect"
	"testing"
	"time"
)

func relativeNode(field string, days int) *BrowseNode {
	return &BrowseNode{Field: field, Operator: "within-last-days", Value: days}
}

func TestBrowseRelativeValidationAndCapabilities(t *testing.T) {
	for _, value := range []string{`null`, `0`, `-1`, `36501`, `1.5`, `"30"`, `true`, `[]`, `{}`, `1e999`} {
		_, err := ParseBrowseQuery(json.RawMessage(`{"variant":"relative","field":"dateAdded","operator":"within-last-days","value":`+value+`}`), "query")
		var issue *BrowseValidationError
		if !errors.As(err, &issue) || issue.Path != "query.value" {
			t.Fatalf("%s: %v", value, err)
		}
	}
	for _, raw := range []string{`{"variant":"relative","field":"dateAdded","operator":"within-last-days"}`, `{"variant":"relative","field":"year","operator":"within-last-days","value":30}`} {
		if _, err := ParseBrowseQuery(json.RawMessage(raw), "query"); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
	for _, value := range []string{`1`, `36500`, `30.0`, `3e1`} {
		if _, err := ParseBrowseQuery(json.RawMessage(`{"variant":"relative","field":"releaseDate","operator":"within-last-days","value":`+value+`}`), "query"); err != nil {
			t.Fatal(err)
		}
	}
	_, s, _ := browseFixture(t)
	caps, err := s.BrowseCapabilitiesFor("a", "movies")
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range caps.Fields {
		if field.Type == "date" && !containsString(field.Operators, "within-last-days") {
			t.Fatalf("missing operator: %s", field.ID)
		}
	}
}

func TestBrowseRelativeDateFieldsAndExactBoundaries(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	db, s, names := browseFixture(t)
	c := catalogtest.New(t, db)
	now := time.Date(2026, 10, 23, 12, 0, 0, 500000000, time.UTC)
	s.Clock = func() time.Time { return now }
	dates := map[string]time.Time{"m1": now.Add(-24 * time.Hour), "m2": now.Add(-time.Hour), "m3": now, "m4": now.Add(time.Nanosecond), "m5": now.Add(-24*time.Hour - time.Nanosecond)}
	for name, at := range dates {
		text := at.Format(time.RFC3339Nano)
		c.Fields(names[name].ID, map[string]any{"added_text": text})
		c.Attributes(names[name].ID, "releaseDate", text)
		c.Exec(`INSERT INTO personal_items(profile_id,item_id,last_played_at,revision) VALUES('p',?,?,1) ON CONFLICT(profile_id,item_id) DO UPDATE SET last_played_at=excluded.last_played_at`, names[name].ID, text)
	}
	c.Drain()
	for _, field := range []string{"dateAdded", "releaseDate", "lastPlayedAt"} {
		t.Run(field, func(t *testing.T) {
			got := browseIDs(t, s, names, fmt.Sprintf(`{"variant":"relative","field":%q,"operator":"within-last-days","value":1}`, field))
			if !reflect.DeepEqual(got, []string{"m1", "m2", "m3"}) {
				t.Fatalf("boundary/future: %v", got)
			}
		})
	}
	c.Attributes(names["m1"].ID, "releaseDate", "2026-10-22")
	c.Attributes(names["m2"].ID, "releaseDate", "2026-10-23")
	c.Attributes(names["m3"].ID, "releaseDate", "2026-10-24")
	c.Drain()
	if got := browseIDs(t, s, names, `{"variant":"relative","field":"releaseDate","operator":"within-last-days","value":1}`); !reflect.DeepEqual(got, []string{"m2"}) {
		t.Fatalf("calendar dates: %v", got)
	}
}

func TestBrowseRelativeFreshCountsFacetsAndSnapshotPages(t *testing.T) {
	db, s, names := browseFixture(t)
	c := catalogtest.New(t, db)
	now := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	s.Clock = func() time.Time { return now }
	for name, at := range map[string]time.Time{"m1": now.Add(-24*time.Hour + 30*time.Second), "m2": now.Add(-time.Hour), "m3": now, "m4": now.Add(48 * time.Hour), "m5": now.Add(-48 * time.Hour)} {
		c.Fields(names[name].ID, map[string]any{"added_text": at.Format(time.RFC3339Nano)})
		c.Exec(`INSERT INTO personal_items(profile_id,item_id,watchlisted,revision) VALUES('p',?,1,1) ON CONFLICT(profile_id,item_id) DO UPDATE SET watchlisted=1`, names[name].ID)
	}
	c.Drain()
	viewer := Viewer{Profile: "p", Fence: "f", Libraries: []string{"a"}}
	query := relativeNode("dateAdded", 1)
	req := BrowseRequest{Library: "a", Pivot: "movies", Query: query, Limit: 1}
	first, err := s.BrowseEntities(viewer, req)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Entries) != 1 || first.Entries[0].ID != names["m1"].Public || first.PageInfo.Total != 3 || first.PageInfo.NextCursor == "" {
		t.Fatalf("first: %+v", first)
	}
	counts := func() BrowseCount {
		out, e := s.BrowseCounts(viewer, "a", BrowseCountRequest{Pivot: "movies", Queries: []BrowseCountQuery{{ID: "relative", Query: query}}}, nil)
		if e != nil {
			t.Fatal(e)
		}
		return out.Counts[0]
	}
	facets := func() FacetPage {
		out, e := s.Facets(viewer, FacetRequest{Library: "a", Pivot: "movies", Query: query, Field: "year"})
		if e != nil {
			t.Fatal(e)
		}
		return out
	}
	beforeCount, beforeFacet := counts(), facets()
	if beforeCount.Total != 3 || beforeFacet.Total != 3 {
		t.Fatalf("before: %+v %+v", beforeCount, beforeFacet)
	}
	absolute := BrowseRequest{Library: "a", Pivot: "movies", Query: &BrowseNode{Field: "dateAdded", Operator: "at-least", Value: "2026-10-22"}, Limit: 1}
	absFirst, e := s.BrowseEntities(viewer, absolute)
	if e != nil {
		t.Fatal(e)
	}
	across := AcrossRequest{Pivot: "titles", Query: browseAnd(&BrowseNode{Field: "watchlisted", Operator: "equals", Value: true}, query), Limit: 1}
	acrossFirst, e := s.BrowseAcross(viewer, across)
	if e != nil {
		t.Fatal(e)
	}
	aggregate := BrowseRequest{Library: "a", Pivot: "categories", Query: query, Limit: 1}
	groupFirst, e := s.BrowseEntities(viewer, aggregate)
	if e != nil {
		t.Fatal(e)
	}
	now = now.Add(2 * time.Minute)
	afterCount, afterFacet := counts(), facets()
	if afterCount.Total != 2 || afterFacet.Total != 2 || beforeCount.Revision == afterCount.Revision || beforeFacet.Revision == afterFacet.Revision {
		t.Fatalf("stale: %+v %+v %+v %+v", beforeCount, afterCount, beforeFacet, afterFacet)
	}
	req.Cursor = first.PageInfo.NextCursor
	second, e := s.BrowseEntities(viewer, req)
	if e != nil {
		t.Fatal(e)
	}
	if len(second.Entries) != 1 || second.Entries[0].ID != names["m2"].Public || second.PageInfo.Total != 3 || second.PageInfo.Revision != first.PageInfo.Revision {
		t.Fatalf("snapshot: %+v", second)
	}
	req.Cursor = ""
	fresh, e := s.BrowseEntities(viewer, req)
	if e != nil {
		t.Fatal(e)
	}
	if fresh.PageInfo.Total != 2 || fresh.Entries[0].ID != names["m2"].Public || fresh.PageInfo.Revision == first.PageInfo.Revision {
		t.Fatalf("fresh: %+v", fresh)
	}
	across.Cursor = acrossFirst.PageInfo.NextCursor
	acrossNext, e := s.BrowseAcross(viewer, across)
	if e != nil {
		t.Fatal(e)
	}
	if across.Cursor == "" || len(acrossNext.Entries) != 1 || acrossNext.Entries[0].ID != names["m2"].Public || acrossNext.PageInfo.Total != 3 {
		t.Fatalf("across snapshot: %+v", acrossNext)
	}
	aggregate.Cursor = groupFirst.PageInfo.NextCursor
	groupNext, e := s.BrowseEntities(viewer, aggregate)
	if e != nil {
		t.Fatal(e)
	}
	if aggregate.Cursor == "" || groupNext.PageInfo.Revision != groupFirst.PageInfo.Revision || groupNext.PageInfo.Total != groupFirst.PageInfo.Total {
		t.Fatalf("aggregate snapshot: %+v", groupNext)
	}
	absFresh, e := s.BrowseEntities(viewer, absolute)
	if e != nil {
		t.Fatal(e)
	}
	if absFirst.PageInfo.Revision != absFresh.PageInfo.Revision || absFirst.PageInfo.Total != absFresh.PageInfo.Total {
		t.Fatal("absolute changed with time")
	}
	absolute.Cursor = absFirst.PageInfo.NextCursor
	if _, e = s.BrowseEntities(viewer, absolute); e != nil {
		t.Fatal(e)
	}
	req.Cursor = first.PageInfo.NextCursor[:len(first.PageInfo.NextCursor)-3] + "xxx"
	if _, e = s.BrowseEntities(viewer, req); !errors.Is(e, ErrCursor) {
		t.Fatalf("tampered cursor: %v", e)
	}
}

func TestBrowseRelativeSavedViewAndHomeStayCurrent(t *testing.T) {
	db, s, names := browseFixture(t)
	c := catalogtest.New(t, db)
	now := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	s.Clock = func() time.Time { return now }
	for name, at := range map[string]time.Time{"m1": now.Add(-24 * time.Hour), "m2": now.Add(-time.Hour), "m3": now, "m4": now.Add(48 * time.Hour), "m5": now.Add(-48 * time.Hour)} {
		c.Fields(names[name].ID, map[string]any{"added_text": at.Format(time.RFC3339Nano)})
	}
	c.Drain()
	actor := ResourceActor{Authority: "local", AccountID: "account", ProfileID: "profile"}
	name := "Recent"
	definition := SavedViewDefinition{LibraryID: "a", Pivot: "movies", Query: relativeNode("dateAdded", 1), Sort: titleAscending(), Presentation: "grid"}
	made, e := s.MutateSavedResource(actor, "", "create", SavedResourceMutation{OperationID: "relative-view-create", Kind: "view", Name: &name, Definition: &definition}, func(*sql.Tx) error { return nil })
	if e != nil {
		t.Fatal(e)
	}
	viewer := Viewer{Profile: actorKey(actor), Fence: "owner", Libraries: []string{"a"}}
	read := func() SavedResourceContent {
		out, e := s.SavedResourceContent(viewer, "server", "owner", made.ResourceID, actor, ResourceContentQuery{}, "", 50)
		if e != nil {
			t.Fatal(e)
		}
		return out
	}
	before := read()
	if before.Browse.PageInfo.Total != 3 {
		t.Fatalf("saved before: %+v", before)
	}
	rowID := homeViewPrefix + made.ResourceID
	home := HomeRequest{Viewer: viewer, RowOrder: []string{rowID}}
	first, e := s.HomeSingleRow(home, rowID, HomeRowPage{Limit: 1})
	if e != nil {
		t.Fatal(e)
	}
	if first.CacheTTLMs != 30000 || first.Total != 3 || len(first.Entries) != 1 || first.Entries[0].ID != names["m1"].Public || first.NextCursor == "" {
		t.Fatalf("Home before: %+v", first)
	}
	now = now.Add(61 * time.Second)
	after := read()
	if after.Browse.PageInfo.Total != 2 || after.Browse.Entries[0].ID != names["m2"].Public || after.Resource.Definition.Query.Operator != "within-last-days" {
		t.Fatalf("saved stale: %+v", after)
	}
	next, e := s.HomeSingleRow(home, rowID, HomeRowPage{Limit: 1, Cursor: first.NextCursor})
	if e != nil {
		t.Fatal(e)
	}
	if next.Total != 3 || len(next.Entries) != 1 || next.Entries[0].ID != names["m2"].Public {
		t.Fatalf("Home snapshot: %+v", next)
	}
	fresh, e := s.HomeSingleRow(home, rowID, HomeRowPage{Limit: 1})
	if e != nil {
		t.Fatal(e)
	}
	if fresh.Total != 2 || fresh.Entries[0].ID != names["m2"].Public {
		t.Fatalf("Home stale: %+v", fresh)
	}
}
