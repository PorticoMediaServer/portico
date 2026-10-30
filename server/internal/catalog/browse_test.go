package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

// browseFixture builds one movie library with enough descriptive, personal and
// availability facts to exercise every published operator.
func browseFixture(t *testing.T) (*sql.DB, *Service, catalogtest.Names) {
	t.Helper()
	c := catalogtest.Open(t)
	db := c.DB
	films := c.Library("a", "Movies", "movie", "/a")
	c.Library("tv", "Shows", "tv", "/tv")
	names := catalogtest.Names{}
	rows := []struct {
		id, title string
		year      int
	}{{"m1", "Alpha", 1995}, {"m2", "Beta", 2005}, {"m3", "Gamma", 2015}, {"m4", "Delta", 2015}, {"m5", "9 Lives", 1985}}
	for index, row := range rows {
		if row.id == "m4" {
			names[row.id] = c.Entity(compactcatalog.Entity{Library: films, Kind: compactcatalog.Movie, Key: compactcatalog.ItemKey("/a", "/a/m4.mkv", 0), Title: row.title, Year: row.year}, nil)
		} else {
			names[row.id] = c.Movie(films, "/a/"+row.id+".mkv", row.title, row.year)
		}
		c.Fields(names[row.id].ID, map[string]any{"added_text": fmt.Sprintf("2026-09-0%dT00:00:00.000Z", index+1)})
	}
	assets := []struct {
		item      string
		height    int
		duration  float64
		available bool
	}{{"m1", 1080, 100, true}, {"m2", 2160, 200, true}, {"m3", 480, 300, false}, {"m5", 720, 50, true}}
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		for _, asset := range assets {
			if err := compactcatalog.SetAssetTx(ctx, tx, names[asset.item].Asset, map[string]any{"height": asset.height, "duration": asset.duration, "available": asset.available}); err != nil {
				return err
			}
		}
		return nil
	})
	c.Genres(names["m1"].ID, "tmdb", "Action", "Drama")
	c.Genres(names["m2"].ID, "tmdb", "Action")
	c.Genres(names["m3"].ID, "tmdb", "Comedy")
	c.Genres(names["m5"].ID, "tmdb", "Drama")
	for _, attribute := range [][3]string{{"m1", "contentRating", "PG"}, {"m2", "contentRating", "R"}, {"m1", "studio", "Example Pictures"}, {"m2", "tag", "rewatch"}} {
		c.Attributes(names[attribute[0]].ID, attribute[1], attribute[2])
	}
	c.Exec(`INSERT INTO personal_items(profile_id,item_id,watchlisted,favorite,rating,revision,watched,last_played_at) VALUES('p',?,0,1,9,1,0,''),('p',?,0,0,NULL,1,1,'2026-09-10T00:00:00Z')`, names["m1"].ID, names["m2"].ID)
	c.Exec(`INSERT INTO personal_watched_intents(profile_id,item_id,watched,authored_at) VALUES('p',?,1,'2026-09-10T00:00:00.000000000Z')`, names["m2"].ID)
	c.Exec(`INSERT INTO progress(profile_id,item_id,position,unit,completed,playback_id) VALUES('p',?,?,0,0,'pb')`, names["m3"].ID, 10000)
	c.Drain()
	return db, New(db), names
}

func browseIDs(t *testing.T, s *Service, names catalogtest.Names, query string) []string {
	t.Helper()
	parsed, e := ParseBrowseQuery(json.RawMessage(query), "query")
	if e != nil {
		t.Fatalf("parse %s: %v", query, e)
	}
	out, e := s.BrowseEntities(Viewer{Profile: "p", Fence: "f", Libraries: []string{"a"}}, BrowseRequest{Library: "a", Profile: "p", ViewerFence: "f", Pivot: "movies", Query: parsed, Limit: 50})
	if e != nil {
		t.Fatalf("browse %s: %v", query, e)
	}
	ids := []string{}
	for _, entry := range out.Entries {
		ids = append(ids, entry.ID)
	}
	sort.Strings(ids)
	ids = names.All(ids)
	sort.Strings(ids)
	return ids
}

func TestBrowseCompilesEveryOperator(t *testing.T) {
	_, s, names := browseFixture(t)
	cases := []struct {
		name, query string
		want        []string
	}{
		{"equals", `{"field":"title","operator":"equals","value":"Alpha"}`, []string{"m1"}},
		{"not-equals", `{"field":"title","operator":"not-equals","value":"Alpha"}`, []string{"m2", "m3", "m4", "m5"}},
		{"contains", `{"field":"title","operator":"contains","value":"amm"}`, []string{"m3"}},
		{"starts-with", `{"field":"title","operator":"starts-with","value":"D"}`, []string{"m4"}},
		{"in", `{"field":"year","operator":"in","value":[1995,2005]}`, []string{"m1", "m2"}},
		{"not-in", `{"field":"year","operator":"not-in","value":[1995,2005]}`, []string{"m3", "m4", "m5"}},
		{"less-than", `{"field":"year","operator":"less-than","value":2000}`, []string{"m1", "m5"}},
		{"at-most", `{"field":"year","operator":"at-most","value":1995}`, []string{"m1", "m5"}},
		{"greater-than", `{"field":"year","operator":"greater-than","value":2005}`, []string{"m3", "m4"}},
		{"at-least", `{"field":"year","operator":"at-least","value":2005}`, []string{"m2", "m3", "m4"}},
		{"between", `{"field":"year","operator":"between","value":[1995,2005]}`, []string{"m1", "m2"}},
		{"is-present", `{"field":"contentRating","operator":"is-present","value":null}`, []string{"m1", "m2"}},
		{"is-missing", `{"field":"contentRating","operator":"is-missing","value":null}`, []string{"m3", "m4", "m5"}},
		{"contains-any", `{"field":"genre","operator":"contains-any","value":["Action","Comedy"]}`, []string{"m1", "m2", "m3"}},
		{"contains-all", `{"field":"genre","operator":"contains-all","value":["Action","Drama"]}`, []string{"m1"}},
		{"set contains", `{"field":"genre","operator":"contains","value":"Drama"}`, []string{"m1", "m5"}},
		{"decade", `{"field":"decade","operator":"equals","value":2010}`, []string{"m3", "m4"}},
		{"availability", `{"field":"availability","operator":"equals","value":"available"}`, []string{"m1", "m2", "m5"}},
		{"unavailable", `{"field":"availability","operator":"equals","value":"unavailable"}`, []string{"m3", "m4"}},
		{"resolution", `{"field":"resolution","operator":"equals","value":"4k"}`, []string{"m2"}},
		{"duration", `{"field":"durationMs","operator":"at-least","value":200000}`, []string{"m2", "m3"}},
		{"favorite", `{"field":"favorite","operator":"equals","value":true}`, []string{"m1"}},
		{"not favorite", `{"field":"favorite","operator":"equals","value":false}`, []string{"m2", "m3", "m4", "m5"}},
		{"played", `{"field":"playState","operator":"equals","value":"played"}`, []string{"m2"}},
		{"in-progress", `{"field":"playState","operator":"equals","value":"in-progress"}`, []string{"m3"}},
		{"unplayed", `{"field":"playState","operator":"equals","value":"unplayed"}`, []string{"m1", "m4", "m5"}},
		{"personalRating", `{"field":"personalRating","operator":"at-least","value":4}`, []string{"m1"}},
		{"lastPlayedAt present", `{"field":"lastPlayedAt","operator":"is-present","value":null}`, []string{"m2"}},
		{"studio", `{"field":"studio","operator":"contains","value":"Example"}`, []string{"m1"}},
		{"tag", `{"field":"tag","operator":"contains","value":"rewatch"}`, []string{"m2"}},
		{"all group", `{"all":[{"field":"genre","operator":"contains","value":"Action"},{"field":"year","operator":"at-least","value":2000}]}`, []string{"m2"}},
		{"any group", `{"any":[{"field":"title","operator":"equals","value":"Alpha"},{"field":"title","operator":"equals","value":"Delta"}]}`, []string{"m1", "m4"}},
		{"not group", `{"not":{"field":"genre","operator":"contains","value":"Action"}}`, []string{"m3", "m4", "m5"}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			got := browseIDs(t, s, names, test.query)
			if strings.Join(got, ",") != strings.Join(test.want, ",") {
				t.Fatalf("want %v got %v", test.want, got)
			}
		})
	}
}

func TestBrowseRejectsQueriesOverTheLimits(t *testing.T) {
	deep := `{"field":"title","operator":"equals","value":"Alpha"}`
	for i := 0; i < 6; i++ {
		deep = `{"not":` + deep + `}`
	}
	if _, e := ParseBrowseQuery(json.RawMessage(deep), "query"); e == nil {
		t.Fatal("depth accepted")
	} else {
		var issue *BrowseValidationError
		if !errors.As(e, &issue) || !strings.HasPrefix(issue.Path, "query.not") {
			t.Fatalf("depth path %v", e)
		}
	}
	clauses := []string{}
	for i := 0; i < BrowseMaximumClauses+1; i++ {
		clauses = append(clauses, `{"field":"year","operator":"equals","value":`+fmt.Sprint(1900+i)+`}`)
	}
	if _, e := ParseBrowseQuery(json.RawMessage(`{"any":[`+strings.Join(clauses, ",")+`]}`), "query"); e == nil {
		t.Fatal("clause count accepted")
	}
	for _, bad := range []struct{ query, path string }{
		{`{"field":"nonsense","operator":"equals","value":"x"}`, "query.field"},
		{`{"field":"title","operator":"at-least","value":"x"}`, "query.operator"},
		{`{"field":"year","operator":"equals","value":"x"}`, "query.value"},
		{`{"field":"playState","operator":"equals","value":"halfway"}`, "query.value"},
		{`{"all":[{"field":"year","operator":"between","value":[1]}]}`, "query.all[0].value"},
		{`{"all":[],"any":[]}`, "query"},
	} {
		_, e := ParseBrowseQuery(json.RawMessage(bad.query), "query")
		var issue *BrowseValidationError
		if !errors.As(e, &issue) || issue.Path != bad.path {
			t.Fatalf("query %s wanted path %s got %v", bad.query, bad.path, e)
		}
	}
}

func TestBrowseQuickFiltersRunOnTheEngine(t *testing.T) {
	_, s, names := browseFixture(t)
	expected := map[string][]string{"in-progress": {"m3"}, "unwatched": {"m1", "m4", "m5"}, "watched": {"m2"}, "favorites": {"m1"}, "watchlist": {}, "available": {"m1", "m2", "m5"}, "missing": {"m3", "m4"}}
	for _, filter := range BrowseQuickFilters() {
		raw, e := json.Marshal(filter.Query)
		if e != nil {
			t.Fatal(e)
		}
		got := browseIDs(t, s, names, string(raw))
		want, ok := expected[filter.ID]
		if !ok {
			t.Fatalf("unexpected quick filter %s", filter.ID)
		}
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Fatalf("%s want %v got %v", filter.ID, want, got)
		}
	}
}

func TestBrowsePositionIndexAndSeek(t *testing.T) {
	_, s, names := browseFixture(t)
	page, e := s.BrowseEntities(Viewer{Profile: "p", Fence: "f", Libraries: []string{"a"}}, BrowseRequest{Library: "a", Profile: "p", ViewerFence: "f", Pivot: "movies", Limit: 2})
	if e != nil {
		t.Fatal(e)
	}
	want := []BrowsePositionAnchor{{"#", 0}, {"A", 1}, {"B", 2}, {"D", 3}, {"G", 4}}
	if fmt.Sprint(page.PositionIndex) != fmt.Sprint(want) {
		t.Fatalf("index %v", page.PositionIndex)
	}
	if page.PageInfo.Total != 5 || !page.PageInfo.HasMore || page.PageInfo.NextCursor == "" {
		t.Fatalf("page %+v", page.PageInfo)
	}
	sought, e := s.BrowseEntities(Viewer{Profile: "p", Fence: "f", Libraries: []string{"a"}}, BrowseRequest{Library: "a", Profile: "p", ViewerFence: "f", Pivot: "movies", Limit: 2, Seek: &BrowseSeek{Prefix: "d"}})
	if e != nil {
		t.Fatal(e)
	}
	if sought.PageInfo.Start != 3 || len(sought.Entries) != 2 || sought.Entries[0].ID != names["m4"].Public {
		t.Fatalf("seek %+v %v", sought.PageInfo, sought.Entries)
	}
	if _, e = s.BrowseEntities(Viewer{Profile: "p", Fence: "f", Libraries: []string{"a"}}, BrowseRequest{Library: "a", Profile: "p", ViewerFence: "f", Pivot: "movies", Sort: []BrowseSortSelection{{Field: "year", Direction: "desc"}}, Seek: &BrowseSeek{Prefix: "d"}}); e == nil {
		t.Fatal("seek accepted without a title sort")
	}
	descending, e := s.BrowseEntities(Viewer{Profile: "p", Fence: "f", Libraries: []string{"a"}}, BrowseRequest{Library: "a", Profile: "p", ViewerFence: "f", Pivot: "movies", Sort: []BrowseSortSelection{{Field: "title", Direction: "desc"}}, Limit: 5})
	if e != nil {
		t.Fatal(e)
	}
	// M10: every sort publishes anchors; a descending title sort runs its
	// letters backwards, each at the index where its run begins.
	if len(descending.PositionIndex) == 0 || descending.PositionIndex[0].Index != 0 {
		t.Fatalf("descending letter index %+v", descending.PositionIndex)
	}
	for i := 1; i < len(descending.PositionIndex); i++ {
		a, b := descending.PositionIndex[i-1], descending.PositionIndex[i]
		if b.Index <= a.Index || b.Key >= a.Key {
			t.Fatalf("descending letter index out of order %+v", descending.PositionIndex)
		}
	}
}

func TestBrowseCursorPagesTheWholeLibrary(t *testing.T) {
	_, s, _ := browseFixture(t)
	request := BrowseRequest{Library: "a", Profile: "p", ViewerFence: "f", Pivot: "movies", Limit: 2}
	seen := map[string]bool{}
	for {
		page, e := s.BrowseEntities(Viewer{Profile: request.Profile, Fence: request.ViewerFence, Libraries: []string{request.Library}, Restrictions: request.Restrictions}, request)
		if e != nil {
			t.Fatal(e)
		}
		for _, entry := range page.Entries {
			if seen[entry.ID] {
				t.Fatal("duplicate", entry.ID)
			}
			seen[entry.ID] = true
		}
		if page.PageInfo.NextCursor == "" {
			break
		}
		request.Cursor = page.PageInfo.NextCursor
	}
	if len(seen) != 5 {
		t.Fatal("missing rows", len(seen))
	}
}

func TestBrowseRangeReanchorsAfterAPublication(t *testing.T) {
	db, s, names := browseFixture(t)
	page, e := s.BrowseEntities(Viewer{Profile: "p", Fence: "f", Libraries: []string{"a"}}, BrowseRequest{Library: "a", Profile: "p", ViewerFence: "f", Pivot: "movies", Limit: 2, Range: &BrowseRange{Start: 3}})
	if e != nil {
		t.Fatal(e)
	}
	if page.PageInfo.Start != 3 || page.Entries[0].ID != names["m4"].Public {
		t.Fatalf("range %+v", page.PageInfo)
	}
	revision := page.PageInfo.Revision
	catalog := catalogtest.New(t, db)
	names["m0"] = catalog.Movie(catalog.Handle("a"), "/a/m0.mkv", "AAA", 2001)
	settleCompact(t, db)
	recovered, e := s.BrowseEntities(Viewer{Profile: "p", Fence: "f", Libraries: []string{"a"}}, BrowseRequest{Library: "a", Profile: "p", ViewerFence: "f", Pivot: "movies", Limit: 2, Range: &BrowseRange{Start: 3, Revision: revision}})
	if e != nil || recovered.PageInfo.Start != 3 || len(recovered.Entries) != 2 || recovered.Entries[0].ID != names["m2"].Public || recovered.PageInfo.Revision == revision {
		t.Fatalf("quiet range recovery %+v %v", recovered, e)
	}
	moved, e := s.BrowseEntities(Viewer{Profile: "p", Fence: "f", Libraries: []string{"a"}}, BrowseRequest{Library: "a", Profile: "p", ViewerFence: "f", Pivot: "movies", Limit: 2, Range: &BrowseRange{Start: 3, Revision: revision, AnchorID: names["m4"].Public}})
	if e != nil {
		t.Fatal(e)
	}
	if moved.PageInfo.Start != 4 || moved.Entries[0].ID != names["m4"].Public {
		t.Fatalf("anchor %+v %v", moved.PageInfo, moved.Entries)
	}
	missing, e := s.BrowseEntities(Viewer{Profile: "p", Fence: "f", Libraries: []string{"a"}}, BrowseRequest{Library: "a", Profile: "p", ViewerFence: "f", Pivot: "movies", Limit: 2, Range: &BrowseRange{Start: 3, Revision: revision, AnchorID: "missing"}})
	if e != nil || missing.PageInfo.Start != 3 || len(missing.Entries) != 2 || missing.Entries[0].ID != names["m2"].Public {
		t.Fatalf("missing anchor recovery %+v %v", missing, e)
	}
}

func TestBrowseCapabilitiesPublishOnlyExecutableVocabulary(t *testing.T) {
	_, s, _ := browseFixture(t)
	capabilities, e := s.BrowseCapabilitiesFor("a", "")
	if e != nil {
		t.Fatal(e)
	}
	ids := []string{}
	for _, pivot := range capabilities.Pivots {
		ids = append(ids, pivot.ID)
	}
	if strings.Join(ids, ",") != "discover,movies,collections,categories" {
		t.Fatalf("pivots %v", ids)
	}
	if capabilities.QueryLimits.MaximumDepth != 5 || capabilities.QueryLimits.MaximumClauses != 40 || capabilities.QueryLimits.CursorTTLMs != 1800000 {
		t.Fatalf("limits %+v", capabilities.QueryLimits)
	}
	for _, field := range capabilities.Fields {
		if field.ControlHint == "" || field.Complexity == "" || field.Cost == "" || len(field.Operators) == 0 {
			t.Fatalf("incomplete field %+v", field)
		}
		if _, ok := browseFieldByID(field.ID); !ok {
			t.Fatalf("unknown field %s", field.ID)
		}
	}
	scoped, e := s.BrowseCapabilitiesFor("a", "collections")
	if e != nil {
		t.Fatal(e)
	}
	if scoped.ResolvedPivot == nil || scoped.ResolvedPivot.ID != "collections" {
		t.Fatalf("resolved %+v", scoped.ResolvedPivot)
	}
	for _, field := range scoped.Fields {
		if field.ID == "network" {
			t.Fatal("network published for a movie collection pivot")
		}
	}
	if _, e = s.BrowseCapabilitiesFor("a", "songs"); e == nil {
		t.Fatal("foreign pivot accepted")
	}
	television, e := s.BrowseCapabilitiesFor("tv", "")
	if e != nil {
		t.Fatal(e)
	}
	ids = ids[:0]
	for _, pivot := range television.Pivots {
		ids = append(ids, pivot.ID)
	}
	if strings.Join(ids, ",") != "discover,shows,episodes,collections,categories" {
		t.Fatalf("television pivots %v", ids)
	}
}

func TestFacetsRespectVisibilityAndCacheByRevision(t *testing.T) {
	db, s, names := browseFixture(t)
	page, e := s.Facets(Viewer{Profile: "p", Libraries: []string{"a"}}, FacetRequest{Library: "a", Profile: "p", Field: "genre"})
	if e != nil {
		t.Fatal(e)
	}
	counts := map[string]int{}
	for _, value := range page.Values {
		counts[value.Value] = value.Count
	}
	if counts["Action"] != 2 || counts["Drama"] != 2 || counts["Comedy"] != 1 {
		t.Fatalf("counts %+v", counts)
	}
	if _, e = db.Exec(`INSERT INTO dvr_catalog_retirements(item_id,requested_ms) VALUES(?,0)`, names["m1"].ID); e != nil {
		t.Fatal(e)
	}
	settleCompact(t, db)
	page, e = s.Facets(Viewer{Profile: "p", Libraries: []string{"a"}}, FacetRequest{Library: "a", Profile: "p", Field: "genre"})
	if e != nil {
		t.Fatal(e)
	}
	counts = map[string]int{}
	for _, value := range page.Values {
		counts[value.Value] = value.Count
	}
	if counts["Action"] != 1 || counts["Drama"] != 1 {
		t.Fatalf("retired row still counted %+v", counts)
	}
	decades, e := s.Facets(Viewer{Profile: "p", Libraries: []string{"a"}}, FacetRequest{Library: "a", Profile: "p", Field: "decade"})
	if e != nil {
		t.Fatal(e)
	}
	found := false
	for _, value := range decades.Values {
		if value.Value == "2010" && value.Label == "2010s" && value.Count == 2 {
			found = true
		}
	}
	if !found {
		t.Fatalf("decades %+v", decades.Values)
	}
	if _, e = s.Facets(Viewer{Profile: "p", Libraries: []string{"a"}}, FacetRequest{Library: "a", Profile: "p", Field: "nonsense"}); e == nil {
		t.Fatal("unknown facet accepted")
	}
	filtered, e := s.Facets(Viewer{Profile: "p", Libraries: []string{"a"}}, FacetRequest{Library: "a", Profile: "p", Field: "genre", Q: "dra"})
	if e != nil {
		t.Fatal(e)
	}
	if len(filtered.Values) != 1 || filtered.Values[0].Value != "Drama" {
		t.Fatalf("filtered %+v", filtered.Values)
	}
}

func TestSavedViewValidationNamesTheFailingPath(t *testing.T) {
	db, s, _ := browseFixture(t)
	_ = s
	good := SavedViewDefinition{LibraryID: "a", Pivot: "movies", Presentation: "grid", Sort: []BrowseSortSelection{{Field: "title", Direction: "asc"}}, Query: &BrowseNode{Field: "genre", Operator: "contains", Value: "Action"}}
	if bad := validateSavedView(db, good); len(bad) != 0 {
		t.Fatalf("valid view rejected %v", bad)
	}
	for _, test := range []struct {
		name string
		path string
		make func(SavedViewDefinition) SavedViewDefinition
	}{
		{"library", "libraryId", func(v SavedViewDefinition) SavedViewDefinition { v.LibraryID = "nope"; return v }},
		{"pivot", "pivot", func(v SavedViewDefinition) SavedViewDefinition { v.Pivot = "songs"; return v }},
		{"presentation", "presentation", func(v SavedViewDefinition) SavedViewDefinition { v.Presentation = "carousel"; return v }},
		{"sort field", "sort[0].field", func(v SavedViewDefinition) SavedViewDefinition {
			v.Sort = []BrowseSortSelection{{Field: "nonsense", Direction: "asc"}}
			return v
		}},
		{"query field", "query.field", func(v SavedViewDefinition) SavedViewDefinition {
			v.Query = &BrowseNode{Field: "nonsense", Operator: "equals", Value: "x"}
			return v
		}},
	} {
		bad := validateSavedView(db, test.make(good))
		if len(bad) == 0 || bad[0] != test.path {
			t.Fatalf("%s wanted %s got %v", test.name, test.path, bad)
		}
	}
	// A whole expression survives the store round trip unchanged.
	raw, e := json.Marshal(good)
	if e != nil {
		t.Fatal(e)
	}
	var restored SavedViewDefinition
	if e = json.Unmarshal(raw, &restored); e != nil {
		t.Fatal(e)
	}
	if restored.Query == nil || restored.Query.Field != "genre" || restored.Query.Value != "Action" {
		t.Fatalf("round trip %+v", restored.Query)
	}
	if bad := validateSavedView(db, restored); len(bad) != 0 {
		t.Fatalf("restored view rejected %v", bad)
	}
}

func TestPinOrderAndPinnedLibraries(t *testing.T) {
	db, s, _ := browseFixture(t)
	actor := ResourceActor{Authority: "local", AccountID: "account", ProfileID: "profile"}
	owner := actorKey(actor)
	for _, name := range []string{"Zulu", "Alpha", "Mike"} {
		if _, e := db.Exec(`INSERT INTO saved_resources(id,kind,owner_key,owner_authority,owner_account,owner_profile,name,created_at) VALUES(?,'view',?,?,?,?,?,'2026-09-01T00:00:00Z')`, name, owner, actor.Authority, actor.AccountID, actor.ProfileID, name); e != nil {
			t.Fatal(e)
		}
		if _, e := db.Exec(`INSERT INTO saved_pins VALUES(?,'view',?,1,1)`, owner, name); e != nil {
			t.Fatal(e)
		}
	}
	if _, e := s.SetPinOrder(actor, 1, []PinOrderEntry{{"view", "Zulu"}}, func(*sql.Tx) error { return nil }); !errors.Is(e, ErrPinOrderConflict) {
		t.Fatalf("stale revision accepted %v", e)
	}
	order, e := s.SetPinOrder(actor, 0, []PinOrderEntry{{"view", "Zulu"}, {"view", "Mike"}}, func(*sql.Tx) error { return nil })
	if e != nil {
		t.Fatal(e)
	}
	if order.Revision != 1 {
		t.Fatalf("revision %d", order.Revision)
	}
	if _, e = s.SetPinOrder(actor, 1, []PinOrderEntry{{"view", "unknown"}}, func(*sql.Tx) error { return nil }); e == nil {
		t.Fatal("unpinned resource accepted into the order")
	}
	page, e := s.SavedResources("server", "fence", actor, "view", "", true, 10)
	if e != nil {
		t.Fatal(e)
	}
	names := []string{}
	for _, resource := range page.Resources {
		names = append(names, resource.Name)
	}
	if strings.Join(names, ",") != "Zulu,Mike,Alpha" {
		t.Fatalf("pin order not applied: %v", names)
	}
	read, e := s.PinOrder(actor)
	if e != nil {
		t.Fatal(e)
	}
	// Arranged pins first in their order, then the pinned rest by name, each named.
	if len(read.Order) != 3 || read.Order[0].ID != "Zulu" || read.Order[1].ID != "Mike" || read.Order[2].ID != "Alpha" || read.Order[0].Name != "Zulu" {
		t.Fatalf("stored order %+v", read.Order)
	}
	if _, e = db.Exec(`UPDATE saved_resources SET deleted=1 WHERE id='Mike'`); e != nil {
		t.Fatal(e)
	}
	if read, e = s.PinOrder(actor); e != nil || len(read.Order) != 2 || read.Order[1].ID != "Alpha" {
		t.Fatalf("a deleted pin is still listed %+v %v", read.Order, e)
	}
}

func TestPinnedLibrariesLeadTheListing(t *testing.T) {
	_, s, _ := browseFixture(t)
	actor := ResourceActor{Authority: "local", AccountID: "account", ProfileID: "profile"}
	navigation, e := s.LibraryNavigation(actor, nil)
	if e != nil || navigation.Revision != 0 || len(navigation.PinnedLibraryIDs) != 0 {
		t.Fatalf("empty navigation %+v %v", navigation, e)
	}
	if _, e = s.SetLibraryNavigation(actor, 1, []string{"tv"}, func(*sql.Tx, string) error { return nil }); !errors.Is(e, ErrLibraryNavigationConflict) {
		t.Fatalf("stale revision accepted %v", e)
	}
	saved, e := s.SetLibraryNavigation(actor, 0, []string{"tv"}, func(*sql.Tx, string) error { return nil })
	if e != nil {
		t.Fatal(e)
	}
	if saved.Revision != 1 || saved.PinnedLibraryIDs[0] != "tv" {
		t.Fatalf("saved %+v", saved)
	}
	if _, e = s.SetLibraryNavigation(actor, 1, []string{"tv", "tv"}, func(*sql.Tx, string) error { return nil }); e == nil {
		t.Fatal("duplicate library accepted")
	}
	libraries, e := s.LibrariesForViewer(actor)
	if e != nil {
		t.Fatal(e)
	}
	if libraries[0].ID != "tv" || !libraries[0].Pinned || libraries[1].ID != "a" || libraries[1].Pinned {
		t.Fatalf("ordering %+v", libraries)
	}
}

func TestAggregatePivotProjectsFacets(t *testing.T) {
	_, s, _ := browseFixture(t)
	page, e := s.BrowseEntities(Viewer{Profile: "p", Fence: "f", Libraries: []string{"a"}}, BrowseRequest{Library: "a", Profile: "p", ViewerFence: "f", Pivot: "categories"})
	if e != nil {
		t.Fatal(e)
	}
	if page.Pivot != "categories" || len(page.Entries) == 0 {
		t.Fatalf("categories %+v", page)
	}
	for _, entry := range page.Entries {
		if entry.Kind != "category" || entry.Navigation == nil || !strings.HasPrefix(entry.Navigation.Category, "decade:") {
			t.Fatalf("entry %+v", entry)
		}
	}
	viewer := Viewer{Profile: "p", Fence: "f", Libraries: []string{"a"}}
	filtered, err := s.BrowseEntities(viewer, BrowseRequest{Library: "a", Pivot: "categories", Query: &BrowseNode{Field: "title", Operator: "equals", Value: "x"}})
	if err != nil || len(filtered.Entries) != 0 || filtered.PageInfo.Total != 0 {
		t.Fatalf("filtered grouping: %+v %v", filtered, err)
	}
	for _, field := range []string{"year", "added", "random"} {
		carried, err := s.BrowseEntities(viewer, BrowseRequest{Library: "a", Pivot: "categories", Sort: []BrowseSortSelection{{Field: field, Direction: "desc", Seed: 123}}, Seek: &BrowseSeek{Prefix: "Z"}})
		if err != nil || fmt.Sprint(entryIDs(carried.Entries)) != fmt.Sprint(entryIDs(page.Entries)) || canonicalSort(carried.Applied.Sort) != "title:asc" || carried.Applied.Seek != nil {
			t.Fatalf("carried %s: %+v %v", field, carried, err)
		}
	}
	if _, err := s.BrowseEntities(viewer, BrowseRequest{Library: "a", Pivot: "categories", Sort: []BrowseSortSelection{{Field: "title", Direction: "sideways"}}}); err == nil {
		t.Fatal("invalid grouping direction accepted")
	}
}

// CB-1/3/4: contextual counts share the canonical pivot, omit only the counted
// field, and never count an actor/genre from a restricted member.
func TestBrowseContextualFacetsAndPeopleAreBounded(t *testing.T) {
	db, s, names := browseFixture(t)
	c := catalogtest.New(t, db)
	credits := make([]compactcatalog.Credit, 250)
	for i := range credits {
		credits[i] = compactcatalog.Credit{PersonKey: fmt.Sprintf("person-%03d", i), PersonName: fmt.Sprintf("Person %03d", i), CreditedName: fmt.Sprintf("Person %03d", i), Department: "Acting", Role: "Actor", Ordinal: i}
	}
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		return compactcatalog.SetCreditsTx(ctx, tx, names["m2"].ID, "fixture", credits)
	})
	c.Drain()
	viewer := Viewer{Profile: "p", Fence: "f", Libraries: []string{"a"}}
	query := &BrowseNode{All: []BrowseNode{{Field: "year", Operator: "at-least", Value: float64(2000)}, {Field: "genre", Operator: "contains", Value: "Drama"}}}
	page, err := s.Facets(viewer, FacetRequest{Library: "a", Pivot: "movies", Field: "genre", Query: query})
	if err != nil || fmt.Sprint(page.Values) != fmt.Sprint([]FacetValue{{Value: "Action", Label: "Action", Count: 1}, {Value: "Comedy", Label: "Comedy", Count: 1}}) {
		t.Fatalf("contextual genre: %+v %v", page, err)
	}
	people, err := s.Facets(viewer, FacetRequest{Library: "a", Pivot: "movies", Field: "actor", Limit: 10})
	if err != nil || len(people.Values) != 10 || people.Total != 250 {
		t.Fatalf("bounded people: %+v %v", people, err)
	}
	found, err := s.Facets(viewer, FacetRequest{Library: "a", Pivot: "movies", Field: "actor", Q: "Person 249", Limit: 10})
	if err != nil || len(found.Values) != 1 || found.Values[0].Count != 1 || found.Values[0].Value != "Person 249" {
		t.Fatalf("people typeahead: %+v %v", found, err)
	}
	// Removing the actor clause leaves the year, so this result changes with the
	// other filter rather than reusing library-wide cached counts.
	noPeople, err := s.Facets(viewer, FacetRequest{Library: "a", Pivot: "movies", Field: "actor", Query: &BrowseNode{Field: "year", Operator: "equals", Value: float64(1995)}})
	if err != nil || len(noPeople.Values) != 0 {
		t.Fatalf("context leaked people: %+v %v", noPeople, err)
	}
	libraries, err := s.FacetsAcross(viewer, FacetRequest{Field: "library", Query: &BrowseNode{Field: "favorite", Operator: "equals", Value: true}})
	if err != nil || len(libraries.Values) != 1 || libraries.Values[0].Value != "a" || libraries.Values[0].Label != "Movies" || libraries.Values[0].Count != 1 {
		t.Fatalf("library facet: %+v %v", libraries, err)
	}
}

func TestBrowseQueryDurationAndBatchCountsUseWholeVisibleQuery(t *testing.T) {
	db, s, names := browseFixture(t)
	viewer := Viewer{Profile: "p", Fence: "f", Libraries: []string{"a"}}
	query := &BrowseNode{Field: "genre", Operator: "contains", Value: "Action"}
	first, err := s.BrowseEntities(viewer, BrowseRequest{Library: "a", Pivot: "movies", Query: query, Limit: 1})
	if err != nil || len(first.Entries) != 1 || first.PageInfo.DurationMs != 300000 || first.PageInfo.DurationIsLowerBound {
		t.Fatalf("duration is not full query: %+v %v", first, err)
	}
	next, err := s.BrowseEntities(viewer, BrowseRequest{Library: "a", Pivot: "movies", Query: query, Limit: 1, Cursor: first.PageInfo.NextCursor})
	if err != nil || next.PageInfo.DurationMs != first.PageInfo.DurationMs || next.PageInfo.DurationIsLowerBound || next.PageInfo.NextCursor != "" {
		t.Fatalf("duration continuation: %+v %v", next, err)
	}
	batch, err := s.BrowseCounts(viewer, "a", BrowseCountRequest{Pivot: "movies", Queries: []BrowseCountQuery{{ID: "genre", Query: query}, {ID: "clear", Query: nil}}}, nil)
	if err != nil || len(batch.Counts) != 2 || batch.Counts[0].Total != 2 || batch.Counts[1].Total != 5 || batch.Counts[0].TotalIsLowerBound {
		t.Fatalf("batch counts %+v %v", batch, err)
	}
	denied, err := s.BrowseCounts(Viewer{Profile: "p", Fence: "f"}, "a", BrowseCountRequest{Pivot: "movies", Queries: []BrowseCountQuery{{ID: "q", Query: query}}}, nil)
	if err == nil || len(denied.Counts) != 0 {
		t.Fatalf("unauthorized count %+v %v", denied, err)
	}
	if _, err = s.BrowseCounts(viewer, "a", BrowseCountRequest{Pivot: "movies", Queries: make([]BrowseCountQuery, 9)}, nil); err == nil {
		t.Fatal("uncapped batch")
	}
	defer func(n int) { browseTotalThreshold = n }(browseTotalThreshold)
	browseTotalThreshold = 1
	if _, err = db.Exec(`UPDATE personal_items SET favorite=1,revision=revision+1 WHERE profile_id='p' AND item_id=?`, names["m2"].ID); err != nil {
		t.Fatal(err)
	}
	personal := &BrowseNode{Field: "favorite", Operator: "equals", Value: true}
	group, err := s.BrowseAcross(viewer, AcrossRequest{Pivot: "titles", Query: personal, Limit: 1})
	if err != nil || group.PageInfo.DurationMs <= 0 || !group.PageInfo.DurationIsLowerBound {
		t.Fatalf("partial group duration %+v %v", group, err)
	}
	empty, err := s.BrowseAcross(viewer, AcrossRequest{Pivot: "titles", Query: personal, Range: &BrowseRange{Start: 50}, Limit: 1})
	if err != nil || len(empty.Entries) != 0 || empty.PageInfo.HasMore || empty.PageInfo.NextCursor != "" || empty.PageInfo.Total != 1 || !empty.PageInfo.TotalIsLowerBound || empty.PageInfo.DurationMs != group.PageInfo.DurationMs || !empty.PageInfo.DurationIsLowerBound {
		t.Fatalf("empty final page lost query duration or invented continuation: %+v %v", empty, err)
	}
}

func TestBrowseMatchUsesCurrentOnlineAttentionIncludingKept(t *testing.T) {
	c := catalogtest.Open(t)
	movies := c.Library("m", "Movies", "movie", "/m")
	tv := c.Library("tv", "TV", "tv", "/tv")
	music := c.Library("mu", "Music", "music", "/mu")
	local := c.Library("local", "Local", "movie", "/local")
	movie := c.Movie(movies, "/m/a.mkv", "Ordinary playable title", 2026)
	pending := c.Movie(movies, "/m/b.mkv", "Pending title", 2026)
	localMovie := c.Movie(local, "/local/a.mkv", "Local title", 2026)
	show := c.Show(tv, "Show", 2026)
	season := c.Season(show, 1)
	episode := c.Episode(show, season, 1, "/tv/a.mkv")
	artist := c.Artist(music, "Tagged artist")
	album := c.Album(artist, "Tagged album", 2026)
	song := c.Song(album, 1, "/mu/a.flac", "Tagged song")
	for _, item := range []catalogtest.Item{movie, pending, episode, localMovie} {
		library := "m"
		if item.ID == episode.ID {
			library = "tv"
		}
		if item.ID == localMovie.ID {
			library = "local"
		}
		c.Exec(`INSERT INTO screen_metadata_work(target_kind,target_id,library_id,title_seed,status)VALUES('item',?,?,'Fixture','needs_selection') ON CONFLICT(target_kind,target_id) DO UPDATE SET status='needs_selection'`, item.ID, library)
	}
	c.Exec(`UPDATE screen_metadata_work SET status='pending_search' WHERE target_id=?`, pending.ID)
	c.Exec(`UPDATE screen_metadata_attention SET dismissed_generation=generation WHERE entity_id=?`, movie.ID)
	c.Exec(`INSERT INTO listening_metadata_attention(entity_id,kind,library_id,reason,generation,dismissed_generation)VALUES(?,'song','mu','needs_selection',1,1)`, song.ID)
	c.Exec(`UPDATE library_metadata_agents SET agent='local',revision=revision+1 WHERE library_id='local'`)
	c.Drain()
	s := New(c.DB)
	v := Viewer{Profile: "p", Fence: "f", Libraries: []string{"m", "tv", "mu", "local"}}
	q := &BrowseNode{Field: "match", Operator: "equals", Value: "unmatched"}
	for _, test := range []struct {
		lib, pivot string
		id         string
	}{{"m", "movies", movie.Public}, {"tv", "shows", show.Public}, {"tv", "episodes", episode.Public}, {"mu", "artists", artist.Public}, {"mu", "albums", album.Public}, {"mu", "songs", song.Public}} {
		page, err := s.BrowseEntities(v, BrowseRequest{Library: test.lib, Pivot: test.pivot, Query: q})
		if err != nil || len(page.Entries) != 1 || page.Entries[0].ID != test.id {
			t.Fatalf("%s/%s match %+v %v", test.lib, test.pivot, page, err)
		}
		caps, err := s.BrowseCapabilitiesFor(test.lib, test.pivot)
		if err != nil {
			t.Fatal(err)
		}
		seen := false
		for _, field := range caps.Fields {
			seen = seen || field.ID == "match"
		}
		if !seen {
			t.Fatalf("member field missing on %s/%s", test.lib, test.pivot)
		}
	}
	// Album repair attention can exist without any song attention. Artists'
	// playable memberships contain songs, so retain the direct album-artist
	// edge too, after checking the album's current visibility.
	c.Exec(`UPDATE listening_metadata_attention SET retired=1 WHERE entity_id=?`, song.ID)
	c.Exec(`INSERT INTO listening_metadata_attention(entity_id,kind,library_id,reason,generation,dismissed_generation)VALUES(?,'album','mu','needs_selection',1,1) ON CONFLICT(entity_id) DO UPDATE SET reason='needs_selection',retired=0,dismissed_generation=generation`, album.ID)
	for _, pivot := range []string{"artists", "albums", "songs"} {
		page, err := s.BrowseEntities(v, BrowseRequest{Library: "mu", Pivot: pivot, Query: q})
		want := 1
		if pivot == "songs" {
			want = 0
		}
		if err != nil || len(page.Entries) != want {
			t.Fatalf("album-only attention on %s: %+v %v", pivot, page, err)
		}
	}
	otherAlbum := c.Album(artist, "Visible sibling album", 2026)
	c.Song(otherAlbum, 1, "/mu/visible.flac", "Visible sibling song")
	c.Attributes(song.ID, "label", "private")
	c.Drain()
	limited := v
	limited.Restrictions = identity.ContentRestrictions{BlockedLabels: []string{"private"}}
	visibleArtist, err := s.BrowseEntities(limited, BrowseRequest{Library: "mu", Pivot: "artists"})
	if err != nil || len(visibleArtist.Entries) != 1 {
		t.Fatalf("visible sibling fixture %+v %v", visibleArtist, err)
	}
	hiddenFact, err := s.BrowseEntities(limited, BrowseRequest{Library: "mu", Pivot: "artists", Query: q})
	if err != nil || len(hiddenFact.Entries) != 0 {
		t.Fatalf("hidden album attention leaked through visible artist: %+v %v", hiddenFact, err)
	}
	caps, err := s.BrowseCapabilitiesFor("local", "movies")
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range caps.Fields {
		if field.ID == "match" {
			t.Fatal("local-only capability advertises online match")
		}
	}
	page, err := s.BrowseEntities(v, BrowseRequest{Library: "local", Pivot: "movies", Query: q})
	if err != nil || len(page.Entries) != 0 {
		t.Fatalf("local matched online %+v %v", page, err)
	}
	// A source switch invalidates an existing cursor and the facet cache even
	// when the owner retained the same ordinary playable title.
	first, err := s.BrowseEntities(v, BrowseRequest{Library: "m", Pivot: "movies", Limit: 1})
	if err != nil || first.PageInfo.NextCursor == "" {
		t.Fatal("cursor fixture", err)
	}
	c.Exec(`UPDATE library_metadata_agents SET agent='local',revision=revision+1 WHERE library_id='m'`)
	_, err = s.BrowseEntities(v, BrowseRequest{Library: "m", Pivot: "movies", Limit: 1, Cursor: first.PageInfo.NextCursor})
	if !errors.Is(err, ErrStaleContinuation) {
		t.Fatalf("source-choice cursor survived: %v", err)
	}
}

// 1,203 current authorized members are a source, never a page-length cap. The
// sorted/filtered See all cursor and facets keep that entire canonical source.
func TestBrowseHomeMembershipFacetsAndSortedExhaustion(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	c := catalogtest.Open(t)
	lib := c.Library("m", "Movies", "movie", "/m")
	hidden := c.Library("hidden", "Private", "movie", "/hidden")
	want := make([]string, 1203)
	filtered := []string{}
	for i := range want {
		item := c.Movie(lib, fmt.Sprintf("/m/%04d.mkv", i), fmt.Sprintf("Film %04d", i), 2025+i%2)
		want[i] = item.Public
		if i%2 == 1 {
			filtered = append([]string{item.Public}, filtered...)
		}
		c.Fields(item.ID, map[string]any{"added_text": "2026-10-20T00:00:00.000Z"})
		c.Genres(item.ID, "fixture", "Drama")
	}
	forbidden := c.Movie(lib, "/m/forbidden.mkv", "Restricted title", 2026)
	c.Attributes(forbidden.ID, "label", "private")
	c.Movie(hidden, "/hidden/film.mkv", "Outside scope", 2026)
	c.Drain()
	s := New(c.DB)
	viewer := Viewer{Profile: "p", Fence: "f", Libraries: []string{"m"}, Restrictions: identity.ContentRestrictions{BlockedLabels: []string{"private"}}}
	home := HomeRequest{Viewer: viewer}
	source, err := s.ResolveHomeRowMembership(home, "new_on_server")
	if err != nil {
		t.Fatal(err)
	}
	group := &BrowseNode{Field: "homeRow", Operator: "equals", Value: "new_on_server"}
	sources := map[string]HomeRowMembership{"new_on_server": source}
	for _, tc := range []struct {
		name      string
		query     *BrowseNode
		direction string
		want      []string
	}{{"all", group, "asc", want}, {"filtered", browseAnd(group, &BrowseNode{Field: "year", Operator: "equals", Value: float64(2026)}), "desc", filtered}} {
		request := AcrossRequest{Pivot: "titles", Query: tc.query, Sort: []BrowseSortSelection{{Field: "title", Direction: tc.direction}}, Limit: 37, RowSources: sources}
		got := []string{}
		pages := 0
		for {
			page, e := s.BrowseAcross(viewer, request)
			if e != nil {
				t.Fatal(e)
			}
			replay, e := s.BrowseAcross(viewer, request)
			if e != nil || !reflect.DeepEqual(page.Entries, replay.Entries) {
				t.Fatalf("%s replay %v", tc.name, e)
			}
			for _, entry := range page.Entries {
				got = append(got, entry.ID)
			}
			if fmt.Sprint(got) != fmt.Sprint(tc.want[:len(got)]) || page.PageInfo.Total != len(tc.want) || page.PageInfo.TotalIsLowerBound {
				t.Fatalf("%s prefix/count changed at page %d: %+v", tc.name, pages, page.PageInfo)
			}
			pages++
			if page.PageInfo.NextCursor == "" {
				if page.PageInfo.HasMore {
					t.Fatal("false end")
				}
				break
			}
			request.Cursor = page.PageInfo.NextCursor
		}
		if fmt.Sprint(got) != fmt.Sprint(tc.want) || tc.name == "all" && pages != 33 {
			t.Fatalf("%s incomplete %d/%d in %d pages", tc.name, len(got), len(tc.want), pages)
		}
	}
	facet, err := s.FacetsAcross(viewer, FacetRequest{Pivot: "titles", Field: "library", Query: group, RowSources: sources})
	if err != nil || len(facet.Values) != 1 || facet.Values[0].Count != 1203 {
		t.Fatalf("large facet %+v %v", facet, err)
	}
	q := browseAnd(group, &BrowseNode{Field: "year", Operator: "equals", Value: float64(2026)})
	genres, err := s.FacetsAcross(viewer, FacetRequest{Pivot: "titles", Field: "genre", Query: q, RowSources: sources})
	if err != nil || len(genres.Values) != 1 || genres.Values[0].Count != 601 {
		t.Fatalf("large filtered genre %+v %v", genres, err)
	}
	first, err := s.BrowseAcross(viewer, AcrossRequest{Pivot: "titles", Query: group, Limit: 37, RowSources: sources})
	if err != nil {
		t.Fatal(err)
	}
	c.Fields(forbidden.ID, map[string]any{"title": "Changed restricted revision"})
	c.Drain()
	fresh, err := s.ResolveHomeRowMembership(home, "new_on_server")
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.BrowseAcross(viewer, AcrossRequest{Pivot: "titles", Query: group, Limit: 37, Cursor: first.PageInfo.NextCursor, RowSources: map[string]HomeRowMembership{"new_on_server": fresh}})
	if !errors.Is(err, ErrStaleContinuation) {
		t.Fatalf("mixed revision continuation: %v", err)
	}
}

func TestBrowseContextFacetsAndMatchNeverExposeRestrictedMemberFacts(t *testing.T) {
	c := catalogtest.Open(t)
	tv := c.Library("tv", "TV", "tv", "/tv")
	show := c.Show(tv, "Shared show", 2026)
	season := c.Season(show, 1)
	visible := c.Episode(show, season, 1, "/tv/visible.mkv")
	hidden := c.Episode(show, season, 2, "/tv/hidden.mkv")
	c.Attributes(hidden.ID, "label", "private")
	c.Genres(visible.ID, "fixture", "Family")
	c.Genres(hidden.ID, "fixture", "Secret genre")
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		return compactcatalog.SetCreditsTx(ctx, tx, hidden.ID, "fixture", []compactcatalog.Credit{{PersonKey: "secret-person", PersonName: "Secret actor", CreditedName: "Secret actor", Role: "Actor", Department: "Acting"}})
	})
	c.Exec(`INSERT INTO screen_metadata_work(target_kind,target_id,library_id,title_seed,status)VALUES('item',?,'tv','Fixture','needs_selection') ON CONFLICT(target_kind,target_id) DO UPDATE SET status='needs_selection'`, hidden.ID)
	c.Drain()
	s := New(c.DB)
	q := &BrowseNode{Field: "year", Operator: "equals", Value: float64(2026)}
	open := Viewer{Profile: "p", Fence: "f", Libraries: []string{"tv"}}
	restricted := open
	restricted.Restrictions = identity.ContentRestrictions{BlockedLabels: []string{"private"}}
	for _, field := range []string{"actor", "genre"} {
		all, err := s.Facets(open, FacetRequest{Library: "tv", Pivot: "shows", Query: q, Field: field})
		if err != nil || len(all.Values) == 0 {
			t.Fatal("nonvacuous private fixture", field, all, err)
		}
		page, err := s.Facets(restricted, FacetRequest{Library: "tv", Pivot: "shows", Query: q, Field: field})
		if err != nil {
			t.Fatal(err)
		}
		for _, value := range page.Values {
			if strings.Contains(value.Value, "Secret") {
				t.Fatalf("%s disclosed hidden member: %+v", field, page)
			}
		}
		if field == "genre" && (len(page.Values) != 1 || page.Values[0].Value != "Family" || page.Values[0].Count != 1) {
			t.Fatalf("visible member lost: %+v", page)
		}
	}
	unmatched := &BrowseNode{Field: "match", Operator: "equals", Value: "unmatched"}
	all, err := s.BrowseEntities(open, BrowseRequest{Library: "tv", Pivot: "shows", Query: unmatched})
	if err != nil || len(all.Entries) != 1 {
		t.Fatalf("attention fixture %+v %v", all, err)
	}
	page, err := s.BrowseEntities(restricted, BrowseRequest{Library: "tv", Pivot: "shows", Query: unmatched})
	if err != nil || len(page.Entries) != 0 {
		t.Fatalf("hidden attention disclosed through sibling %+v %v", page, err)
	}
	counts, err := s.BrowseCounts(restricted, "tv", BrowseCountRequest{Pivot: "shows", Queries: []BrowseCountQuery{{ID: "unmatched", Query: unmatched}}}, nil)
	if err != nil || counts.Counts[0].Total != 0 {
		t.Fatalf("count leaked hidden attention: %+v %v", counts, err)
	}
}
