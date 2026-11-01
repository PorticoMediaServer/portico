package catalog

import (
	"context"
	"database/sql"
	"errors"
	"sort"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

// The restriction predicate is the only thing standing between a child profile
// and an adult title, so it is tested through every surface that can put a title
// on a screen. A surface that forgets it would pass its own tests and still leak.

// restrictionFixture builds a movie library whose ratings span the whole scale,
// plus a show whose episodes are all adult, so container behaviour is testable.
func tl10RestrictionFixture(t *testing.T) (*sql.DB, *Service, catalogtest.Names) {
	t.Helper()
	c := catalogtest.Open(t)
	movieLibrary := c.Library("a", "Movies", "movie", "/a")
	tvLibrary := c.Library("tv", "Shows", "tv", "/tv")
	names := catalogtest.Names{}
	movies := []struct {
		id, title, rating, label string
	}{
		{"kids", "Kids Film", "G", ""},
		{"family", "Family Film", "PG", ""},
		{"teen", "Teen Film", "PG-13", ""},
		{"adult", "Adult Film", "R", ""},
		{"extreme", "Extreme Film", "NC-17", ""},
		{"unrated", "Unrated Film", "", ""},
		{"notrated", "Explicitly Unrated", "NR", ""},
		{"labelled", "Labelled Film", "G", "Halloween"},
		{"foreign", "Region Qualified Film", "US:PG-13", ""},
	}
	for _, movie := range movies {
		names[movie.id] = c.Movie(movieLibrary, "/a/"+movie.id+".mkv", movie.title, 2000)
		if movie.rating != "" {
			c.Attributes(names[movie.id].ID, "contentRating", movie.rating)
		}
		if movie.label != "" {
			c.Attributes(names[movie.id].ID, "label", movie.label)
		}
	}
	queuedRatings := map[string]string{}
	for _, movie := range movies {
		if movie.rating != "" {
			queuedRatings[strings.ToLower(movie.rating)] = movie.rating
		}
	}
	// A show whose every episode is adult, so the container must disappear too.
	names["show"] = c.Show(tvLibrary, "Adult Series", 2001)
	names["season"] = c.Season(names["show"], 1)
	for i, episode := range []string{"e1", "e2"} {
		names[episode] = c.Episode(names["show"], names["season"], i+1, "/tv/"+episode+".mkv")
		c.Attributes(names[episode].ID, "contentRating", "TV-MA")
	}
	queuedRatings["tv-ma"] = "TV-MA"
	for key, value := range queuedRatings {
		c.Exec(`INSERT INTO content_rating_pending(value_key,value) VALUES(?,?) ON CONFLICT(value_key) DO UPDATE SET value=excluded.value`, key, value)
	}
	// An empty collection: nothing in it can be restricted, so it stays visible.
	names["empty"] = c.Collection(movieLibrary, "Empty Shelf")
	c.Drain()
	s := New(c.DB)
	if _, err := s.ClassifyPendingRatings(context.Background()); err != nil {
		t.Fatal(err)
	}
	c.Drain()
	return c.DB, s, names
}

func TestPendingRatingIsUnratedUntilBackgroundClassification(t *testing.T) {
	db, s, names := tl10RestrictionFixture(t)
	c := catalogtest.New(t, db)
	names["new"] = c.Movie(c.Handle("a"), "/a/new.mkv", "New Film", 2026)
	c.Drain()
	c.Exec(`DELETE FROM content_rating_ages WHERE value_key='g'`)
	c.Attributes(names["new"].ID, "contentRating", "G")
	c.Exec(`INSERT INTO content_rating_pending(value_key,value) VALUES('g','G') ON CONFLICT(value_key) DO UPDATE SET value=excluded.value`)
	viewer := Viewer{Profile: "p", Libraries: []string{"a"}, Restrictions: restrictionOf(ceiling(13), true)}
	if err := s.VisibleItem(context.Background(), viewer, names["new"].Public); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("pending rating admitted: %v", err)
	}
	var pending int
	if err := db.QueryRow(`SELECT count(*) FROM content_rating_pending WHERE value_key='g'`).Scan(&pending); err != nil || pending != 1 {
		t.Fatalf("viewer read drained the classifier queue: %d %v", pending, err)
	}
	if _, err := s.ClassifyPendingRatings(context.Background()); err != nil {
		t.Fatal(err)
	}
	settleCompact(t, db)
	if err := s.VisibleItem(context.Background(), viewer, names["new"].Public); err != nil {
		t.Fatalf("classified G rating stayed hidden: %v", err)
	}
}

func TestCompactAttributePermissionsApplyOnWrite(t *testing.T) {
	db, s, names := tl10RestrictionFixture(t)
	viewer := Viewer{Profile: "p", Libraries: []string{"a"}, Restrictions: identity.ContentRestrictions{}.Narrow(8, true, nil, 0)}
	if err := s.VisibleItem(context.Background(), viewer, names["kids"].Public); err != nil {
		t.Fatalf("settled child-safe movie hidden: %v", err)
	}
	c := catalogtest.New(t, db)
	// Facts are written synchronously: a rating applies at once, before and
	// after the worker runs.
	c.Attributes(names["kids"].ID, "contentRating", "G")
	if err := s.VisibleItem(context.Background(), viewer, names["kids"].Public); err != nil {
		t.Fatalf("a G rating hidden before the worker ran: %v", err)
	}
	settleCompact(t, db)
	if err := s.VisibleItem(context.Background(), viewer, names["kids"].Public); err != nil {
		t.Fatalf("a G rating hidden after the worker ran: %v", err)
	}
	// A title carrying two ratings is judged by every one of them, for an
	// account limit exactly as for a profile's: the stricter rating decides.
	c.Attributes(names["kids"].ID, "contentRating", "G", "R")
	if _, err := s.ClassifyPendingRatings(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := s.VisibleItem(context.Background(), viewer, names["kids"].Public); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("a title rated both G and R passed a PG limit: %v", err)
	}
	// A tightened rating hides the item in the same write: no window in which
	// stale derived data admits it.
	c.Attributes(names["kids"].ID, "contentRating", "R")
	if err := s.VisibleItem(context.Background(), viewer, names["kids"].Public); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("adult rating admitted before the worker ran: %v", err)
	}
	if blocked, err := s.RestrictedItem(context.Background(), viewer.Restrictions, names["kids"].Public); err != nil || !blocked {
		t.Fatalf("adult rating not blocked by the direct permission check: %v %v", blocked, err)
	}
	settleCompact(t, db)
	if err := s.VisibleItem(context.Background(), viewer, names["kids"].Public); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("adult rating admitted after the worker ran: %v", err)
	}
}

func TestCompactViewerParentAndMembershipPendingFences(t *testing.T) {
	db, s, names := tl10RestrictionFixture(t)
	c := catalogtest.New(t, db)
	viewer := Viewer{Profile: "p", Libraries: []string{"tv"}}
	if err := s.VisibleEntity(context.Background(), viewer, "show", names["show"].Public); err != nil {
		t.Fatalf("settled show hidden: %v", err)
	}
	c.Fields(names["show"].ID, map[string]any{"title": "Changed"})
	if err := s.VisibleEntity(context.Background(), viewer, "show", names["show"].Public); err != nil {
		t.Fatalf("synchronous parent facts became unreadable: %v", err)
	}
	settleCompact(t, db)
	if err := s.VisibleEntity(context.Background(), viewer, "show", names["show"].Public); err != nil {
		t.Fatalf("settled parent hidden: %v", err)
	}
	c.Delete(names["e1"].ID)
	if err := s.VisibleEntity(context.Background(), viewer, "show", names["show"].Public); err != nil {
		t.Fatalf("queued episode deletion hid a container that still has a member: %v", err)
	}
	settleCompact(t, db)
	if err := s.VisibleEntity(context.Background(), viewer, "show", names["show"].Public); err != nil {
		t.Fatalf("settled parent membership hidden: %v", err)
	}
}

func TestCompactHomeAttributeEnrichmentKeepsSourceOrderAndSynchronousFacts(t *testing.T) {
	db, s, names := tl10RestrictionFixture(t)
	c := catalogtest.New(t, db)
	c.Genres(names["kids"].ID, "tmdb", "Comedy", "Family")
	c.Drain()
	entries := []ContentEntry{{ID: names["kids"].Public, Kind: "movie"}}
	if err := s.homeEnrich("p", entries); err != nil {
		t.Fatal(err)
	}
	if entries[0].ContentRating != "G" || len(entries[0].Genres) != 2 || entries[0].Genres[0] != "Comedy" || entries[0].Genres[1] != "Family" {
		t.Fatalf("source values/order changed: %+v", entries[0])
	}
	c.Attributes(names["kids"].ID, "contentRating")
	entries = []ContentEntry{{ID: names["kids"].Public, Kind: "movie"}}
	if err := s.homeEnrich("p", entries); err != nil {
		t.Fatal(err)
	}
	if entries[0].ContentRating != "" || len(entries[0].Genres) != 2 || entries[0].Genres[0] != "Comedy" || entries[0].Genres[1] != "Family" {
		t.Fatalf("synchronous attribute update lost unrelated Home metadata: %+v", entries[0])
	}
}

func TestFullyRestrictedShowAndUnknownShowBothReturnAbsent(t *testing.T) {
	_, s, names := tl10RestrictionFixture(t)
	viewer := Viewer{Profile: "p", Libraries: []string{"tv"}, Restrictions: restrictionOf(ceiling(13), false)}
	for _, show := range []string{names["show"].Public, "unknown"} {
		if _, _, err := s.Episodes(viewer, show, "", "", 10); !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("show %s: %v", show, err)
		}
	}
	for _, target := range []struct{ kind, id string }{{"show", names["show"].Public}, {"season", names["season"].Public}, {"show", "unknown"}} {
		if err := s.VisibleEntity(context.Background(), viewer, target.kind, target.id); !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("%s %s artwork scope: %v", target.kind, target.id, err)
		}
	}
	open := Viewer{Profile: "p", Libraries: []string{"tv"}}
	if err := s.VisibleEntity(context.Background(), open, "show", names["show"].Public); err != nil {
		t.Fatalf("visible show artwork: %v", err)
	}
	if err := s.VisibleEntity(context.Background(), open, "season", names["season"].Public); err != nil {
		t.Fatalf("visible season artwork: %v", err)
	}
}

func ceiling(age int) *int { return &age }

func restrictionOf(max *int, blockUnrated bool, labels ...string) identity.ContentRestrictions {
	return identity.ContentRestrictions{MaximumAge: max, BlockUnrated: blockUnrated, BlockedLabels: labels, Revision: 2}
}

func browsedIDs(t *testing.T, s *Service, names catalogtest.Names, r identity.ContentRestrictions) []string {
	t.Helper()
	catalogtest.New(t, s.db).Drain()
	if r.Active() {
		if err := s.RebuildVisibilityClass(context.Background(), "a", r); err != nil {
			t.Fatal(err)
		}
		catalogtest.New(t, s.db).Drain()
	}
	out, e := s.BrowseEntities(Viewer{Profile: "p", Fence: "f" + RestrictionFence(r), Libraries: []string{"a"}, Restrictions: r}, BrowseRequest{Library: "a", Profile: "p", ViewerFence: "f" + RestrictionFence(r), Pivot: "movies", Limit: 50, Restrictions: r})
	if e != nil {
		t.Fatal(e)
	}
	ids := []string{}
	for _, entry := range out.Entries {
		ids = append(ids, names.Of(entry.ID))
	}
	sort.Strings(ids)
	if out.PageInfo.Total != len(ids) {
		t.Fatalf("count %d disagreed with the page of %d; a filtered count advertises hidden titles", out.PageInfo.Total, len(ids))
	}
	return ids
}

func TestViewerScopeKeepsPositionsCountsAndDetailPrivate(t *testing.T) {
	_, s, names := tl10RestrictionFixture(t)
	age := 0
	viewer := Viewer{Profile: "p", Fence: "restricted", Libraries: []string{"a"}, Restrictions: restrictionOf(ceiling(13), true, "Halloween").Narrow(age, false, nil, 0)}
	catalogtest.New(t, s.db).Drain()
	if err := s.RebuildVisibilityClass(context.Background(), "a", viewer.EffectiveRestrictions()); err != nil {
		t.Fatal(err)
	}
	catalogtest.New(t, s.db).Drain()
	page, err := s.BrowseEntities(viewer, BrowseRequest{Library: "a", Pivot: "movies", Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if page.PageInfo.Total != 1 || page.PageInfo.HasMore || len(page.Entries) != 1 || page.Entries[0].ID != names["kids"].Public || len(page.PositionIndex) != 1 || page.PositionIndex[0].Key != "K" || page.PositionIndex[0].Index != 0 {
		t.Fatalf("restricted browse leaked count or position: %+v", page)
	}
	settleCompact(t, s.db)
	if _, err := s.Detail(viewer, "server", names["adult"].Public, false); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("restricted detail: %v", err)
	}
	if _, _, err := s.ItemRecommendationRows(viewer, names["adult"].Public, 8, false); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("restricted recommendations: %v", err)
	}
	settleCompact(t, s.db)
	if _, err := s.Detail(viewer, "server", names["kids"].Public, false); err != nil {
		t.Fatalf("visible detail: %v", err)
	}
}

func TestViewerMemberRatingLadderUnratedAndDeniedLabels(t *testing.T) {
	_, s, names := tl10RestrictionFixture(t)
	// The account's limit arrives as an age ceiling (PG is 8), an unrated rule
	// and a denied list, already folded into the viewer's restrictions.
	viewer := Viewer{Profile: "p", Fence: "member", Libraries: []string{"a"}, Restrictions: identity.ContentRestrictions{}.Narrow(8, true, []string{"Halloween"}, 0)}
	catalogtest.New(t, s.db).Drain()
	if err := s.RebuildVisibilityClass(context.Background(), "a", viewer.EffectiveRestrictions()); err != nil {
		t.Fatal(err)
	}
	catalogtest.New(t, s.db).Drain()
	page, err := s.BrowseEntities(viewer, BrowseRequest{Library: "a", Pivot: "movies", Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	ids := []string{}
	for _, entry := range page.Entries {
		ids = append(ids, entry.ID)
	}
	got := names.All(ids)
	sort.Strings(got)
	if !equal(got, []string{"family", "kids"}) || page.PageInfo.Total != 2 {
		t.Fatalf("member ladder, unrated and label scope: %v total=%d", got, page.PageInfo.Total)
	}
	viewer.Restrictions = identity.ContentRestrictions{}.Narrow(8, false, []string{"Halloween"}, 0)
	catalogtest.New(t, s.db).Drain()
	if err := s.RebuildVisibilityClass(context.Background(), "a", viewer.EffectiveRestrictions()); err != nil {
		t.Fatal(err)
	}
	catalogtest.New(t, s.db).Drain()
	page, err = s.BrowseEntities(viewer, BrowseRequest{Library: "a", Pivot: "movies", Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	ids = ids[:0]
	for _, entry := range page.Entries {
		ids = append(ids, entry.ID)
	}
	got = names.All(ids)
	sort.Strings(got)
	// "foreign" carries a rating qualified by its region ("US:PG-13"). It is
	// judged by the age that rating stands for, not passed off as unrated.
	if !equal(got, []string{"family", "kids", "notrated", "unrated"}) || page.PageInfo.Total != 4 {
		t.Fatalf("member allowUnrated: %v total=%d", got, page.PageInfo.Total)
	}
}

func TestRestrictionPredicateFiltersTheBrowseEngineAndItsCount(t *testing.T) {
	_, s, names := tl10RestrictionFixture(t)
	everything := []string{"adult", "extreme", "family", "foreign", "kids", "labelled", "notrated", "teen", "unrated"}
	if got := browsedIDs(t, s, names, identity.ContentRestrictions{}); !equal(got, everything) {
		t.Fatalf("an inactive restriction filtered the library: %v", got)
	}
	// A 13 ceiling admits G, PG and PG-13, including the region-qualified
	// spelling, and refuses R and NC-17. Unrated is still allowed here.
	if got := browsedIDs(t, s, names, restrictionOf(ceiling(13), false)); !equal(got, []string{"family", "foreign", "kids", "labelled", "notrated", "teen", "unrated"}) {
		t.Fatalf("ceiling 13: %v", got)
	}
	// Age 0 is a real ceiling, not "no ceiling".
	if got := browsedIDs(t, s, names, restrictionOf(ceiling(0), false)); !equal(got, []string{"kids", "labelled", "notrated", "unrated"}) {
		t.Fatalf("ceiling 0: %v", got)
	}
	// Blocking unrated removes both the missing rating and the explicit "NR".
	if got := browsedIDs(t, s, names, restrictionOf(ceiling(13), true)); !equal(got, []string{"family", "foreign", "kids", "labelled", "teen"}) {
		t.Fatalf("ceiling 13 without unrated: %v", got)
	}
	// A blocked label removes a title its rating would otherwise admit.
	if got := browsedIDs(t, s, names, restrictionOf(nil, false, "halloween")); !equal(got, []string{"adult", "extreme", "family", "foreign", "kids", "notrated", "teen", "unrated"}) {
		t.Fatalf("blocked label: %v", got)
	}
	// Label matching is case-insensitive, because a person types a label the way
	// they read it, not the way a provider cased it.
	if got := browsedIDs(t, s, names, restrictionOf(nil, false, "HALLOWEEN")); !equal(got, []string{"adult", "extreme", "family", "foreign", "kids", "notrated", "teen", "unrated"}) {
		t.Fatalf("blocked label upper case: %v", got)
	}
	// The clauses compose: all three at once.
	if got := browsedIDs(t, s, names, restrictionOf(ceiling(8), true, "halloween")); !equal(got, []string{"family", "kids"}) {
		t.Fatalf("all three clauses: %v", got)
	}
}

func TestRestrictionPredicateFiltersFacetCountsToMatchThePage(t *testing.T) {
	_, s, _ := tl10RestrictionFixture(t)
	page, e := s.Facets(Viewer{Profile: "p", Libraries: []string{"a"}}, FacetRequest{Library: "a", Profile: "p", Field: "contentRating", Limit: 20})
	if e != nil {
		t.Fatal(e)
	}
	if len(page.Values) < 5 {
		t.Fatalf("unrestricted facets %+v", page.Values)
	}
	restricted, e := s.Facets(Viewer{Profile: "p", Libraries: []string{"a"}, Restrictions: restrictionOf(ceiling(13), false)}, FacetRequest{Library: "a", Profile: "p", Field: "contentRating", Limit: 20, Restrictions: restrictionOf(ceiling(13), false)})
	if e != nil {
		t.Fatal(e)
	}
	// A facet list that still offered "R" would advertise exactly the titles the
	// page refuses to show, and would hand the viewer a filter that returns nothing.
	for _, value := range restricted.Values {
		switch strings.ToUpper(value.Value) {
		case "R", "NC-17":
			t.Fatalf("a restricted rating is still offered as a facet: %+v", restricted.Values)
		}
	}
	if len(restricted.Values) >= len(page.Values) {
		t.Fatalf("facets were not filtered: %d vs %d", len(restricted.Values), len(page.Values))
	}
}

func TestRestrictionPredicateFiltersSearchAndItsTotal(t *testing.T) {
	_, s, names := tl10RestrictionFixture(t)
	search := func(r identity.ContentRestrictions) SearchEnvelope {
		t.Helper()
		out, e := s.Search(context.Background(), SearchRequest{Viewer: Viewer{Profile: "p", Fence: "f", Libraries: []string{"a", "tv"}, Restrictions: r}, ServerID: "server", Profile: "p", ViewerFence: "f", Q: "Film", AllLibraries: true, Limit: 40, Group: "movies", Restrictions: r})
		if e != nil {
			t.Fatal(e)
		}
		return out
	}
	open := search(identity.ContentRestrictions{})
	limited := search(restrictionOf(ceiling(13), true))
	find := func(out SearchEnvelope) ([]string, int) {
		for _, group := range out.Groups {
			if group.ID == "movies" {
				ids := []string{}
				for _, entry := range group.Items {
					ids = append(ids, entry.ID)
				}
				sort.Strings(ids)
				return ids, group.TotalCount
			}
		}
		return nil, 0
	}
	// Group is set so this is the single-group page rather than the overview,
	// which caps every group at five and would hide the count comparison.
	openIDs, openTotal := find(open)
	limitedIDs, limitedTotal := find(limited)
	if len(openIDs) == 0 || openTotal != len(openIDs) {
		t.Fatalf("unrestricted search %v total %d", openIDs, openTotal)
	}
	for _, id := range limitedIDs {
		name := names.Of(id)
		if name == "adult" || name == "extreme" || name == "unrated" || name == "notrated" {
			t.Fatalf("search returned a restricted title: %v", limitedIDs)
		}
	}
	if limitedTotal != len(limitedIDs) {
		t.Fatalf("search total %d disagreed with %d results", limitedTotal, len(limitedIDs))
	}
	if limitedTotal >= openTotal {
		t.Fatalf("search was not filtered: %d vs %d", limitedTotal, openTotal)
	}
}

func TestRestrictionPredicateFiltersHomeRowsAndTheirCounts(t *testing.T) {
	_, s, names := tl10RestrictionFixture(t)
	row := func(r identity.ContentRestrictions) HomeRow {
		t.Helper()
		out, e := s.HomeSingleRow(HomeRequest{Viewer: Viewer{Profile: "p", Fence: "f" + RestrictionFence(r), Libraries: []string{"a"}, Restrictions: r}, ServerID: "server", Profile: "p", ViewerFence: "f" + RestrictionFence(r), Libraries: []string{"a"}, Restrictions: r}, "new_on_server", HomeRowPage{Limit: 20})
		if e != nil {
			t.Fatal(e)
		}
		return out
	}
	open := row(identity.ContentRestrictions{})
	if rowTotal(open) != 9 {
		t.Fatalf("unrestricted recent total %d", open.Total)
	}
	limited := row(restrictionOf(ceiling(13), false))
	if rowTotal(limited) >= rowTotal(open) || len(limited.Entries) != rowTotal(limited) {
		t.Fatalf("restricted recent total=%d entries=%+v", limited.Total, limited.Entries)
	}
	for _, entry := range limited.Entries {
		if entry.ID == names["adult"].Public || entry.ID == names["extreme"].Public {
			t.Fatalf("restricted row lists %s", entry.Title)
		}
	}
}

func TestRestrictedItemAnswersContainersAndEmptyShelves(t *testing.T) {
	ctx := context.Background()
	_, s, names := tl10RestrictionFixture(t)
	teenager := restrictionOf(ceiling(13), false)
	for name, wantBlocked := range map[string]bool{
		"kids":    false,
		"teen":    false,
		"foreign": false,
		"adult":   true,
		"extreme": true,
		"unrated": false,
		// A show whose every episode is adult disappears as a unit, rather than
		// leaving a poster that opens onto an empty season.
		"show":   true,
		"season": true,
		"e1":     true,
		// An empty shelf holds nothing to restrict, so hiding it would be a bug,
		// not caution.
		"empty": false,
	} {
		blocked, e := s.RestrictedItem(ctx, teenager, names[name].Public)
		if e != nil {
			t.Fatal(name, e)
		}
		if blocked != wantBlocked {
			t.Errorf("%s: blocked=%v want %v", name, blocked, wantBlocked)
		}
	}
	// An inactive restriction must never do work or block anything.
	for _, name := range []string{"adult", "show", "e1"} {
		if blocked, e := s.RestrictedItem(ctx, identity.ContentRestrictions{}, names[name].Public); e != nil || blocked {
			t.Errorf("%s blocked under an inactive restriction: %v %v", name, blocked, e)
		}
	}
	// Blocking unrated reaches an item with no rating at all.
	if blocked, e := s.RestrictedItem(ctx, restrictionOf(nil, true), names["unrated"].Public); e != nil || !blocked {
		t.Errorf("unrated item under BlockUnrated: %v %v", blocked, e)
	}
}

func TestRestrictionFenceChangesWithEveryRestrictionFact(t *testing.T) {
	// The fence is what makes an open cursor, a cached count and a rendered page
	// expire when the restriction changes. Two different restrictions sharing a
	// fence would let a page survive a tightening.
	seen := map[string]string{}
	for name, r := range map[string]identity.ContentRestrictions{
		"inactive":     {},
		"ceiling 13":   restrictionOf(ceiling(13), false),
		"ceiling 0":    restrictionOf(ceiling(0), false),
		"no unrated":   restrictionOf(ceiling(13), true),
		"label":        restrictionOf(ceiling(13), false, "halloween"),
		"other label":  restrictionOf(ceiling(13), false, "christmas"),
		"two labels":   restrictionOf(ceiling(13), false, "halloween", "christmas"),
		"new revision": {MaximumAge: ceiling(13), Revision: 9},
	} {
		fence := RestrictionFence(r)
		if previous, clash := seen[fence]; clash {
			t.Errorf("%s and %s share the fence %q", name, previous, fence)
		}
		seen[fence] = name
	}
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// P8: the Recordings switch turns over cursors and caches without making the
// viewer a restricted (per-row predicate) viewer.
func TestRecordingsSwitchFoldsIntoTheFenceOnly(t *testing.T) {
	open := identity.DefaultRestrictions("p")
	open.AllowDVR = true // the allowed fixture has an explicit recording grant
	off := open
	off.AllowDVR = false
	if open.Content().BlockRecordings || !off.Content().BlockRecordings {
		t.Fatal("Content() must carry the Recordings switch")
	}
	if off.Content().Active() {
		t.Fatal("the Recordings switch must not make the viewer a per-row restricted viewer")
	}
	if RestrictionFence(open.Content()) == RestrictionFence(off.Content()) {
		t.Fatal("the fence must change with the Recordings switch")
	}
	limited := off
	limited.MaximumAge = 13
	with, without := limited.Content(), limited.Content()
	without.BlockRecordings = false
	if RestrictionFence(with) == RestrictionFence(without) {
		t.Fatal("an active fence must change with the Recordings switch")
	}
}

// A library whose rating spellings were classified by an older rating table is
// classified again, once, by the first build that knows a newer one: a
// spelling the old table read as unrated gets the age the new table gives it.
func TestRatingSpellingsAreReclassifiedOnceWhenTheTableChanges(t *testing.T) {
	db, s, _ := tl10RestrictionFixture(t)
	ctx := context.Background()
	if _, err := s.ClassifyPendingRatings(ctx); err != nil {
		t.Fatal(err)
	}
	// As an older table would have left them: one spelling it did not know,
	// one it read differently, and the marker of the table that did it.
	for key, age := range map[string]int{"tv-y7-fv": -1, "pg": 3} {
		if _, err := db.Exec(`INSERT INTO content_rating_ages(value_key,minimum_age) VALUES(?,?) ON CONFLICT(value_key) DO UPDATE SET minimum_age=excluded.minimum_age`, key, age); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`INSERT INTO configuration VALUES('rating_table_version','an-older-table') ON CONFLICT(key) DO UPDATE SET value=excluded.value`); err != nil {
		t.Fatal(err)
	}
	age := func(key string) int {
		t.Helper()
		var v int
		if err := db.QueryRow(`SELECT minimum_age FROM content_rating_ages WHERE value_key=?`, key).Scan(&v); err != nil {
			t.Fatal(err)
		}
		return v
	}
	// The running process already classified once; a restart is a new service.
	restarted := New(db)
	if _, err := restarted.ClassifyPendingRatings(ctx); err != nil {
		t.Fatal(err)
	}
	if age("tv-y7-fv") != 7 || age("pg") != 8 {
		t.Fatalf("after a build with a newer table started: tv-y7-fv=%d pg=%d", age("tv-y7-fv"), age("pg"))
	}
	var version string
	if err := db.QueryRow(`SELECT value FROM configuration WHERE key='rating_table_version'`).Scan(&version); err != nil || version != identity.RatingTableVersion {
		t.Fatalf("the table that classified the library is recorded as %q: %v", version, err)
	}
	// With the marker current, a later start leaves the ages as they are.
	if _, err := db.Exec(`UPDATE content_rating_ages SET minimum_age=3 WHERE value_key='pg'`); err != nil {
		t.Fatal(err)
	}
	again := New(db)
	if _, err := again.ClassifyPendingRatings(ctx); err != nil {
		t.Fatal(err)
	}
	if age("pg") != 3 {
		t.Fatalf("a start with the same table reclassified the library: pg=%d", age("pg"))
	}
}
