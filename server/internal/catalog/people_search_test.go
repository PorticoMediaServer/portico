package catalog

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

func searchFixture(t *testing.T) (*sql.DB, *Service, *catalogtest.Catalog) {
	t.Helper()
	db, err := persistence.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db, New(db), catalogtest.New(t, db)
}

func compactCredits(c *catalogtest.Catalog, item int64, credits ...compactcatalog.Credit) {
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		return compactcatalog.SetCreditsTx(ctx, tx, item, "tmdb", credits)
	})
}

func personToken(t *testing.T, db *sql.DB, key string) string {
	t.Helper()
	var token string
	if err := db.QueryRow(`SELECT token FROM catalog_people WHERE identity_key=?`, key).Scan(&token); err != nil {
		t.Fatal(err)
	}
	return token
}

func personCredit(key, providerID, name, role, department string, ordinal int) compactcatalog.Credit {
	return compactcatalog.Credit{PersonKey: key, PersonName: name, PersonSortName: name, ProviderPersonID: providerID, CreditID: "credit-" + providerID, CreditedName: name, Role: role, Department: department, Ordinal: ordinal}
}

func movieWithAdded(c *catalogtest.Catalog, library int64, path, title string, year int, added string) catalogtest.Item {
	c.T.Helper()
	var root string
	if err := c.DB.QueryRow(`SELECT root FROM catalog_libraries WHERE id=?`, library).Scan(&root); err != nil {
		c.T.Fatal(err)
	}
	item := c.Entity(compactcatalog.Entity{Library: library, Kind: compactcatalog.Movie, Key: compactcatalog.ItemKey(root, path, 0), Title: title, Year: year, Added: added}, nil)
	item.Asset, item.Token = c.File(item.ID, path, 5400)
	return item
}

func TestPersonCreditsCountsAndSearchAreRestrictedInsideQueries(t *testing.T) {
	db, s, c := searchFixture(t)
	movies := c.Library("m", "Movies", "movie", "/m")
	a := c.Movie(movies, "/m/Alpha.mkv", "Alpha", 2020)
	b := c.Movie(movies, "/m/Beta.mkv", "Beta", 2021)
	compactCredits(c, a.ID, personCredit("tmdb:42", "42", "Ada Lovelace", "Lead", "Acting", 0))
	compactCredits(c, b.ID, personCredit("tmdb:42", "42", "Ada Lovelace", "Lead", "Acting", 0))
	c.Attributes(b.ID, "label", "Adult")
	c.Drain()
	person := personToken(t, db, "tmdb:42")
	viewer := Viewer{Profile: "p", Fence: "restricted", Libraries: []string{"m"}, Restrictions: identity.ContentRestrictions{BlockedLabels: []string{"Adult"}}}
	page, err := s.Person(viewer, "server", person, "", "", 1)
	if err != nil || page.PageInfo.Total != 1 || len(page.Credits) != 1 || page.Credits[0].Media.ID != a.Public || page.PageInfo.NextCursor != "" {
		t.Fatal("restricted person page", page, err)
	}
	people, err := s.People(viewer, "server", "Ada", 10)
	if err != nil || len(people.People) != 1 || people.People[0].CreditCount != 1 {
		t.Fatal("restricted directory count", people, err)
	}
	viewer.Restrictions.BlockedLabels = []string{"Adult", "Other"}
	c.Attributes(a.ID, "label", "Other")
	if _, err = s.Person(viewer, "server", person, "", "", 1); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("restricted person remained reachable", err)
	}
}

func TestSearchGroupBudgetIsolatesOneSlowGroup(t *testing.T) {
	_, s, c := searchFixture(t)
	movies := c.Library("m", "Movies", "movie", "/m")
	tv := c.Library("tv", "TV", "tv", "/tv")
	one := c.Movie(movies, "/m/one.mkv", "Harbor Lights", 2020)
	c.Show(tv, "Harbor Story", 2020)
	c.Drain()
	searchGroupProbe = func(ctx context.Context, group string) error {
		if group != "shows" {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(30 * time.Second):
			return nil
		}
	}
	defer func() { searchGroupProbe = nil }()
	start := time.Now()
	out, err := s.Search(context.Background(), SearchRequest{Viewer: Viewer{Profile: "p", Fence: "f", Libraries: []string{"m", "tv"}}, ServerID: "server", Profile: "p", ViewerFence: "f", Q: "Harbor", Limit: 40, Libraries: []string{"m", "tv"}})
	if err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatal("one group blocked the response", elapsed)
	}
	byID := map[string]SearchGroupResult{}
	for _, group := range out.Groups {
		byID[group.ID] = group
	}
	slow := byID["shows"]
	if slow.Status != "error" || slow.ErrorCode != "search_group_timeout" || len(slow.Items) != 0 || slow.TotalCount != 0 {
		t.Fatal("slow group", slow)
	}
	moviesResult := byID["movies"]
	if moviesResult.Status != "success" || moviesResult.ErrorCode != "" || len(moviesResult.Items) != 1 || moviesResult.Items[0].ID != one.Public {
		t.Fatal("healthy group lost", moviesResult)
	}
	if out.Empty != nil {
		t.Fatal("partial success reported as empty")
	}
}

func TestSearchRelevanceRanksExactThenPrefixThenMatch(t *testing.T) {
	_, s, c := searchFixture(t)
	movies := c.Library("m", "Movies", "movie", "/m")
	names := catalogtest.Names{
		"mid":   movieWithAdded(c, movies, "/m/mid.mkv", "Harbor Lights", 1999, "2026-01-01T00:00:00.000Z"),
		"exact": movieWithAdded(c, movies, "/m/exact.mkv", "Harbor", 2005, "2026-02-01T00:00:00.000Z"),
		"late":  movieWithAdded(c, movies, "/m/late.mkv", "A Quiet Harbor Town", 2011, "2026-03-01T00:00:00.000Z"),
	}
	c.Drain()
	r := SearchRequest{Viewer: Viewer{Profile: "p", Fence: "f", Libraries: []string{"m"}}, ServerID: "server", Profile: "p", ViewerFence: "f", Q: "Harbor", Group: "movies", Limit: 40, Libraries: []string{"m"}}
	out, err := s.Search(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	order := []string{}
	for _, entry := range out.Groups[0].Items {
		order = append(order, entry.ID)
	}
	if len(order) != 3 || order[0] != names["exact"].Public || order[1] != names["mid"].Public {
		t.Fatal("relevance order", order)
	}
	r.Sort, r.Direction = "releaseYear", "asc"
	out, err = s.Search(context.Background(), r)
	if err != nil || out.Groups[0].Items[0].ID != names["mid"].Public || out.Groups[0].Items[2].ID != names["late"].Public {
		t.Fatal("releaseYear order", out.Groups[0].Items, err)
	}
	r.Sort = "title"
	out, err = s.Search(context.Background(), r)
	if err != nil || out.Groups[0].Items[0].ID != names["exact"].Public {
		t.Fatal("title order", out.Groups[0].Items, err)
	}
	r.Sort, r.Direction = "dateAdded", "desc"
	out, err = s.Search(context.Background(), r)
	if err != nil || out.Groups[0].Items[0].ID != names["late"].Public {
		t.Fatal("dateAdded order", out.Groups[0].Items, err)
	}
	widen := r
	widen.LibraryIDs = []string{"other"}
	if _, err = s.Search(context.Background(), widen); !errors.Is(err, ErrSearchQuery) {
		t.Fatal("library restriction widened access", err)
	}
	only := SearchRequest{Viewer: Viewer{Profile: "p", Fence: "f", Libraries: []string{"m"}}, ServerID: "server", Profile: "p", ViewerFence: "f", Q: "Harbor", Limit: 40, Libraries: []string{"m"}, Groups: []string{"movies", "people"}}
	out, err = s.Search(context.Background(), only)
	if err != nil || len(out.Groups) != 2 || out.Groups[0].ID != "movies" || out.Groups[1].ID != "people" {
		t.Fatal("group restriction", out.Groups, err)
	}
}

func TestPersonPageBackfillPagingAndAuthorization(t *testing.T) {
	db, s, c := searchFixture(t)
	movies := c.Library("m", "Movies", "movie", "/m")
	hidden := c.Library("hidden", "Private", "movie", "/h")
	names := catalogtest.Names{
		"a":      c.Movie(movies, "/m/Alpha.mkv", "Alpha", 2020),
		"b":      c.Movie(movies, "/m/Beta.mkv", "Beta", 2021),
		"secret": c.Movie(hidden, "/h/Secret.mkv", "Secret", 2022),
	}
	compactCredits(c, names["a"].ID, personCredit("tmdb:42", "42", "Ada Lovelace", "Herself", "Acting", 0), personCredit("tmdb:43", "43", "Grace Hopper", "Director", "Directing", 5))
	compactCredits(c, names["b"].ID, personCredit("tmdb:42", "42", "Ada Lovelace", "Herself", "Acting", 1))
	compactCredits(c, names["secret"].ID, personCredit("tmdb:42", "42", "Ada Lovelace", "Herself", "Acting", 0))
	c.Drain()
	person := personToken(t, db, "tmdb:42")
	libraries := []string{"m"}
	page, err := s.Person(Viewer{Profile: "p", Fence: "fence", Libraries: libraries}, "server", person, "", "", 1)
	if err != nil {
		t.Fatal(err)
	}
	if page.PageInfo.Total != 2 || len(page.Credits) != 1 || page.Credits[0].Media.ID != names["a"].Public || page.Credits[0].CreditKind != "cast" {
		t.Fatal("first page", page)
	}
	for _, entry := range page.Person.KnownFor {
		if entry.LibraryID == "hidden" {
			t.Fatal("known-for leak", entry)
		}
	}
	if page.PageInfo.NextCursor == "" {
		t.Fatal("no continuation")
	}
	second, err := s.Person(Viewer{Profile: "p", Fence: "fence", Libraries: libraries}, "server", person, "", page.PageInfo.NextCursor, 1)
	if err != nil || len(second.Credits) != 1 || second.Credits[0].Media.ID != names["b"].Public || second.PageInfo.NextCursor != "" {
		t.Fatal("second page", second, err)
	}
	crew, err := s.Person(Viewer{Profile: "p", Fence: "fence", Libraries: libraries}, "server", person, "crew", "", 40)
	if err != nil || crew.PageInfo.Total != 0 {
		t.Fatal("role filter", crew, err)
	}
	directory, err := s.People(Viewer{Profile: "p", Fence: "fence", Libraries: libraries}, "server", "grace", 10)
	if err != nil || len(directory.People) != 1 || directory.People[0].Name != "Grace Hopper" || directory.People[0].CreditCount != 1 {
		t.Fatal("name search", directory, err)
	}
	if _, err = s.Person(Viewer{Profile: "p", Fence: "fence", Libraries: libraries}, "server", person, "", "", 101); !errors.Is(err, ErrPersonQuery) {
		t.Fatal("unbounded page accepted", err)
	}
	detail, err := s.Detail(Viewer{Profile: "p", Fence: "fence", Libraries: libraries}, "server", names["a"].Public, false)
	if err != nil {
		t.Fatal(err)
	}
	seen := false
	for _, credit := range detail.Metadata.Credits {
		if credit.Name == "Ada Lovelace" && credit.PersonID == person {
			seen = true
		}
	}
	if !seen {
		t.Fatal("detail credit has no canonical person", detail.Metadata.Credits)
	}
	out, err := s.Search(context.Background(), SearchRequest{Viewer: Viewer{Profile: "p", Fence: "f", Libraries: libraries}, ServerID: "s", Profile: "p", ViewerFence: "f", Q: "Lovelace", Group: "people", Limit: 40, Libraries: libraries})
	if err != nil || len(out.Groups) != 1 || out.Groups[0].TotalCount != 1 || out.Groups[0].Items[0].ID != person || out.Groups[0].Items[0].Navigation.View != "person" {
		t.Fatal("people group", out.Groups, err)
	}
}

// CD-28: cast and acting are cast even when providers vary the case; writing is crew.
func TestPersonCastCrewSplitIgnoresDepartmentCase(t *testing.T) {
	_, s, c := searchFixture(t)
	movies := c.Library("m", "Movies", "movie", "/m")
	names := catalogtest.Names{
		"a": c.Movie(movies, "/m/Alpha.mkv", "Alpha", 2020),
		"b": c.Movie(movies, "/m/Beta.mkv", "Beta", 2021),
		"c": c.Movie(movies, "/m/Gamma.mkv", "Gamma", 2022),
	}
	compactCredits(c, names["a"].ID, personCredit("tmdb:42", "42", "Ada Lovelace", "Herself", "cast", 0))
	compactCredits(c, names["b"].ID, personCredit("tmdb:42", "42", "Ada Lovelace", "Herself", "ACTING", 0))
	compactCredits(c, names["c"].ID, personCredit("tmdb:42", "42", "Ada Lovelace", "Writer", "Writing", 0))
	c.Drain()
	person := personToken(t, c.DB, "tmdb:42")
	v := Viewer{Profile: "p", Fence: "fence", Libraries: []string{"m"}}
	cast, err := s.Person(v, "server", person, "cast", "", 40)
	if err != nil || cast.PageInfo.Total != 2 {
		t.Fatal("cast", cast.PageInfo, err)
	}
	crew, err := s.Person(v, "server", person, "crew", "", 40)
	if err != nil || crew.PageInfo.Total != 1 || crew.Credits[0].CreditKind != "crew" {
		t.Fatal("crew", crew, err)
	}
}
