package catalog

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

var homeNamesMu sync.Mutex
var homeNamesByDB = map[*sql.DB]catalogtest.Names{}

func homeNames(t *testing.T, db *sql.DB) catalogtest.Names {
	t.Helper()
	homeNamesMu.Lock()
	names := homeNamesByDB[db]
	homeNamesMu.Unlock()
	if names == nil {
		t.Fatal("home fixture names missing")
	}
	return names
}

func homeItem(t *testing.T, db *sql.DB, name string) catalogtest.Item {
	t.Helper()
	if item, ok := homeNames(t, db)[name]; ok {
		return item
	}
	t.Fatalf("unknown home fixture item %q", name)
	return catalogtest.Item{}
}

func homeRegister(db *sql.DB, name string, item catalogtest.Item) {
	homeNamesMu.Lock()
	defer homeNamesMu.Unlock()
	homeNamesByDB[db][name] = item
}

func homeAddMovie(t *testing.T, db *sql.DB, name, title string, year int) catalogtest.Item {
	t.Helper()
	c := catalogtest.New(t, db)
	item := c.Movie(c.Handle("movies"), "/movies/"+name+".mkv", title, year)
	homeRegister(db, name, item)
	return item
}

func homeAddSeason(t *testing.T, db *sql.DB, showName, seasonName string, number int) catalogtest.Item {
	t.Helper()
	c := catalogtest.New(t, db)
	item := c.Season(homeItem(t, db, showName), number)
	homeRegister(db, seasonName, item)
	return item
}

func homeAddSeasonalEpisode(t *testing.T, db *sql.DB, showName, seasonName, episodeName, title, path string, number int) catalogtest.Item {
	t.Helper()
	c := catalogtest.New(t, db)
	item := c.Episode(homeItem(t, db, showName), homeItem(t, db, seasonName), number, path)
	c.Fields(item.ID, map[string]any{"title": title})
	homeRegister(db, episodeName, item)
	return item
}

func homeAddAbsoluteEpisode(t *testing.T, db *sql.DB, showName, episodeName, title, path string, number int) catalogtest.Item {
	t.Helper()
	c := catalogtest.New(t, db)
	show := homeItem(t, db, showName)
	item := c.Entity(compactcatalog.Entity{Library: c.Handle("tv"), Kind: compactcatalog.Episode, Key: episodeName, Title: title, Added: "2026-01-01T00:00:00.000Z"}, map[string]any{
		"show_id":   show.ID,
		"numbering": "absolute",
		"number":    number,
	})
	c.File(item.ID, path, 600)
	homeRegister(db, episodeName, item)
	return item
}

func homeExec(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, e := db.Exec(query, args...); e != nil {
		t.Fatal(query, e)
	}
}

func homeFixture(t *testing.T) *sql.DB {
	t.Helper()
	c, names := fixtureTemplates.Open(t, "home", buildHomeFixture)
	homeNamesMu.Lock()
	homeNamesByDB[c.DB] = names
	homeNamesMu.Unlock()
	t.Cleanup(func() { homeNamesMu.Lock(); delete(homeNamesByDB, c.DB); homeNamesMu.Unlock() })
	return c.DB
}

func buildHomeFixture(t *testing.T) (*catalogtest.Catalog, catalogtest.Names) {
	t.Helper()
	c := catalogtest.Open(t)
	movies := c.Library("movies", "Movies", "movie", "/movies")
	tv := c.Library("tv", "Shows", "tv", "/tv")
	music := c.Library("music", "Music", "music", "/music")
	books := c.Library("books", "Books", "audiobook", "/books")
	names := catalogtest.Names{}
	for index, id := range []string{"m1", "m2", "m3", "m4", "m5"} {
		item := c.Movie(movies, "/movies/"+id+".mkv", "Movie "+id, 2000+index)
		names[id] = item
		c.Fields(item.ID, map[string]any{"backdrop_url": "/backdrop", "added_text": fmt.Sprintf("2026-01-%02dT00:00:00.000Z", index+1)})
		c.Genres(item.ID, "tmdb", "Animation")
		c.Write(func(ctx context.Context, tx *sql.Tx) error {
			return compactcatalog.SetCreditsTx(ctx, tx, item.ID, "tmdb", []compactcatalog.Credit{{PersonKey: "tmdb:99", PersonName: "Ada Director", ProviderPersonID: "99", CreditID: "dir-" + id, CreditedName: "Ada Director", Role: "Director", Department: "Directing", Ordinal: 0}})
		})
	}
	names["show"] = c.Show(tv, "Harbor", 2020)
	names["s1"] = c.Season(names["show"], 1)
	names["s2"] = c.Season(names["show"], 2)
	for _, row := range []struct {
		id, season string
		number     int
	}{{"e101", "s1", 1}, {"e102", "s1", 2}, {"e201", "s2", 1}, {"e202", "s2", 2}} {
		item := c.Episode(names["show"], names[row.season], row.number, "/tv/"+row.id+".mkv")
		names[row.id] = item
		c.Fields(item.ID, map[string]any{"title": "Episode " + row.id, "added_text": "2026-02-01T00:00:00.000Z"})
	}
	names["artist"] = c.Artist(music, "Artist")
	names["album"] = c.Album(names["artist"], "Release", 2020)
	for index, id := range []string{"t1", "t2", "t3"} {
		item := c.Song(names["album"], index+1, "/music/"+id+".flac", "Track "+id)
		names[id] = item
		c.Fields(item.ID, map[string]any{"added_text": "2026-03-01T00:00:00.000Z"})
	}
	names["book"] = c.Book(books, "Alpha", "Author")
	for index, id := range []string{"p1", "p2", "p3"} {
		item := c.BookFile(names["book"], index+1, "/books/"+id+".m4b")
		names[id] = item
		c.Fields(item.ID, map[string]any{"title": "Part " + id, "added_text": "2026-04-01T00:00:00.000Z"})
	}
	c.Drain()
	return c, names
}

func homeWatched(t *testing.T, db *sql.DB, profile string, at string, ids ...string) {
	t.Helper()
	for _, id := range ids {
		item := homeItem(t, db, id)
		homeExec(t, db, `INSERT INTO personal_items(profile_id,item_id,watchlisted,favorite,revision,watched,last_played_at) VALUES(?,?,0,0,1,1,?) ON CONFLICT(profile_id,item_id) DO UPDATE SET watched=1,last_played_at=excluded.last_played_at`, profile, item.ID, at)
		homeExec(t, db, `INSERT INTO personal_watched_intents(profile_id,item_id,watched,authored_at) VALUES(?,?,1,?) ON CONFLICT(profile_id,item_id) DO UPDATE SET watched=1,authored_at=excluded.authored_at`, profile, item.ID, at)
	}
}

func homeRequestFixture(libraries ...string) HomeRequest {
	if len(libraries) == 0 {
		libraries = []string{"movies", "tv", "music", "books"}
	}
	return HomeRequest{Viewer: Viewer{Profile: "p", Fence: "fence", Libraries: libraries}, ServerID: "server", Profile: "p", ViewerFence: "fence", Libraries: libraries, Now: time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)}
}

func homeViewerForItem(s *Service, profile, fence, item string) Viewer {
	library, _ := s.LibraryForItem(item)
	return Viewer{Profile: profile, Fence: fence, Libraries: []string{library}}
}

func homeRowByID(rows []HomeRow, id string) *HomeRow {
	for index := range rows {
		if rows[index].ID == id {
			return &rows[index]
		}
	}
	return nil
}

// Composition is server-owned: empty rows disappear unless critical, the
// viewer's order is applied before server priority, and hidden rows vanish.
func TestHomeRowsComposeOrderAndHide(t *testing.T) {
	db := homeFixture(t)
	service := New(db)
	homeExec(t, db, `INSERT INTO personal_items(profile_id,item_id,watchlisted,favorite,revision) VALUES('p',?,1,0,1),('p',?,0,1,1)`, homeItem(t, db, "m2").ID, homeItem(t, db, "m3").ID)
	settleCompact(t, db)
	out, e := service.HomeRows(homeRequestFixture())
	if e != nil {
		t.Fatal(e)
	}
	if homeRowByID(out.Rows, "continue") == nil {
		t.Fatal("critical continue row omitted when empty", out.Rows)
	}
	if homeRowByID(out.Rows, "ondeck") != nil {
		t.Fatal("empty non-critical row published", out.Rows)
	}
	// The approved Home merges video libraries while preserving poster geometry.
	for _, id := range []string{"watchlist", "favorites"} {
		if homeRowByID(out.Rows, id) != nil {
			t.Fatal("a Saved tab is not a Home row", id, out.Rows)
		}
	}
	for _, id := range []string{"new_on_server", "listen_next"} {
		if homeRowByID(out.Rows, id) == nil {
			t.Fatal("missing row", id, out.Rows)
		}
	}
	if homeRowByID(out.Rows, "community_watching") != nil {
		t.Fatal("community row published without the owner setting")
	}
	priorities := []int{}
	for _, row := range out.Rows {
		priorities = append(priorities, row.Priority)
	}
	for index := 1; index < len(priorities); index++ {
		if priorities[index] < priorities[index-1] {
			t.Fatal("default order is not server priority", priorities)
		}
	}
	request := homeRequestFixture()
	request.RowOrder = []string{"listen_next", "recommended"}
	request.HiddenRowIDs = []string{"new_on_server"}
	settleCompact(t, db)
	out, e = service.HomeRows(request)
	if e != nil {
		t.Fatal(e)
	}
	if out.Rows[0].ID != "listen_next" {
		t.Fatal("viewer order not applied first", out.Rows[0].ID)
	}
	if homeRowByID(out.Rows, "new_on_server") != nil {
		t.Fatal("hidden row published")
	}
	if homeRowByID(out.Rows, "continue") == nil {
		t.Fatal("required row lost with a viewer layout")
	}
	if out.Layout.Revision != 0 || len(out.Layout.HiddenRowIDs) != 1 {
		t.Fatal("layout not echoed", out.Layout)
	}
	if out.GeneratedAt == "" || out.Revision.Catalog == 0 {
		t.Fatal("home document is not fenced", out.Revision, out.GeneratedAt)
	}
}

// A layout may never hide a required row, and may never name a row this server
// does not publish. The refusal names the row so the client can report it.
func TestHomeLayoutRequiredRowProtection(t *testing.T) {
	db := homeFixture(t)
	service := New(db)
	request := homeRequestFixture()
	if e := service.ValidateHomeLayout(request, []string{"listen_next", "new_on_server"}, []string{"recommended"}); e != nil {
		t.Fatal(e)
	}
	for _, row := range []string{"continue"} {
		e := service.ValidateHomeLayout(request, nil, []string{row})
		if !errors.Is(e, ErrHomeLayoutRow) || !contains(e.Error(), row) {
			t.Fatal("required row hideable", row, e)
		}
	}
	if e := service.ValidateHomeLayout(request, []string{"invented"}, nil); !errors.Is(e, ErrHomeLayoutRow) {
		t.Fatal("unknown row accepted", e)
	}
	if e := service.ValidateHomeLayout(request, nil, []string{"new_on_server", "new_on_server"}); !errors.Is(e, ErrHomeLayoutRow) {
		t.Fatal("duplicate hidden row accepted", e)
	}
	// Hiding a hideable row is still allowed after the required-row refusal.
	request.HiddenRowIDs = []string{"recommended"}
	settleCompact(t, db)
	out, e := service.HomeRows(request)
	if e != nil || homeRowByID(out.Rows, "continue") == nil {
		t.Fatal(e)
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (haystack == needle || indexOf(haystack, needle) >= 0)
}

func indexOf(haystack, needle string) int {
	for index := 0; index+len(needle) <= len(haystack); index++ {
		if haystack[index:index+len(needle)] == needle {
			return index
		}
	}
	return -1
}

// Continue Watching advances within a season and across its boundary without
// publishing a separate Up Next row.
func TestHomeContinueAcrossSeasons(t *testing.T) {
	db := homeFixture(t)
	service := New(db)
	homeWatched(t, db, "p", "2026-09-01T00:00:00.000Z", "e101")
	settleCompact(t, db)
	row, e := service.HomeSingleRow(homeRequestFixture("tv"), "continue", HomeRowPage{})
	if e != nil {
		t.Fatal(e)
	}
	if len(row.Entries) != 1 || row.Entries[0].ID != homeItem(t, db, "e102").Public {
		t.Fatal("next episode in season not resolved", row.Entries)
	}
	homeWatched(t, db, "p", "2026-09-02T00:00:00.000Z", "e102")
	settleCompact(t, db)
	row, e = service.HomeSingleRow(homeRequestFixture("tv"), "continue", HomeRowPage{})
	if e != nil {
		t.Fatal(e)
	}
	if len(row.Entries) != 1 || row.Entries[0].ID != homeItem(t, db, "e201").Public {
		t.Fatal("season boundary not crossed", row.Entries)
	}
	settleCompact(t, db)
	if _, e = service.HomeSingleRow(homeRequestFixture("tv"), "ondeck", HomeRowPage{}); !errors.Is(e, ErrHomeRowUnknown) {
		t.Fatal("retired Up Next row remained addressable", e)
	}
	homeWatched(t, db, "p", "2026-09-04T00:00:00.000Z", "e201", "e202")
	settleCompact(t, db)
	row, e = service.HomeSingleRow(homeRequestFixture("tv"), "continue", HomeRowPage{})
	if e != nil || len(row.Entries) != 0 || rowTotal(row) != 0 {
		t.Fatal("finished show still on deck", row.Entries, e)
	}
}

func TestHomeContinueShowSlotRestrictionsAndReplay(t *testing.T) {
	db := homeFixture(t)
	service := New(db)
	homeWatched(t, db, "p", "2026-09-01T00:00:00.000Z", "e101")
	catalogtest.New(t, db).Attributes(homeItem(t, db, "e102").ID, "label", "adult")
	r := homeRequestFixture("tv")
	r.Viewer.Restrictions.BlockedLabels = []string{"adult"}
	r.Restrictions = r.Viewer.Restrictions
	settleCompact(t, db)
	row, err := service.HomeSingleRow(r, "continue", HomeRowPage{})
	want := homeItem(t, db, "e201").Public
	if err != nil || len(row.Entries) != 1 || row.Entries[0].ID != want {
		t.Fatalf("hidden next episode consumed the slot: got %+v want %s err %v", row.Entries, want, err)
	}
	homeExec(t, db, `INSERT INTO progress(profile_id,item_id,position,unit,playback_id) VALUES('p',?,120000,0,'play-1')`, homeItem(t, db, "e201").ID)
	homeExec(t, db, `INSERT INTO progress_activity VALUES('p','tv',?,'2026-09-02T00:00:00.000Z','paused')`, homeItem(t, db, "e201").ID)
	settleCompact(t, db)
	row, err = service.HomeSingleRow(r, "continue", HomeRowPage{})
	if err != nil || len(row.Entries) != 1 || row.Entries[0].ID != homeItem(t, db, "e201").Public {
		t.Fatalf("partial episode lost its show slot: %+v %v", row.Entries, err)
	}
	homeExec(t, db, `INSERT INTO continue_dismissals VALUES(?,?,?)`, "p", homeItem(t, db, "e201").ID, "play-1")
	settleCompact(t, db)
	row, err = service.HomeSingleRow(r, "continue", HomeRowPage{})
	if err != nil || len(row.Entries) != 0 {
		t.Fatalf("dismissed show slot returned: %+v %v", row.Entries, err)
	}
	homeExec(t, db, `UPDATE progress SET playback_id='play-2' WHERE profile_id='p' AND item_id=?`, homeItem(t, db, "e201").ID)
	settleCompact(t, db)
	row, err = service.HomeSingleRow(r, "continue", HomeRowPage{})
	if err != nil || len(row.Entries) != 1 || row.Entries[0].ID != homeItem(t, db, "e201").Public {
		t.Fatalf("replay did not restore the show slot: %+v %v", row.Entries, err)
	}
	homeWatched(t, db, "p", "2026-09-03T00:00:00.000Z", "e201")
	settleCompact(t, db)
	row, err = service.HomeSingleRow(r, "continue", HomeRowPage{})
	if err != nil || len(row.Entries) != 1 || row.Entries[0].ID != homeItem(t, db, "e202").Public {
		t.Fatalf("manual watched choice did not retire stale partial progress: %+v %v", row.Entries, err)
	}
}

func TestHomeContinueSkipsSpecialsAndFindsNewPremiere(t *testing.T) {
	db := homeFixture(t)
	service := New(db)
	homeAddSeason(t, db, "show", "s0", 0)
	homeAddSeasonalEpisode(t, db, "show", "s0", "special", "Special", "/tv/special.mkv", 1)
	homeWatched(t, db, "p", "2026-09-01T00:00:00.000Z", "e101")
	r := homeRequestFixture("tv")
	settleCompact(t, db)
	row, err := service.HomeSingleRow(r, "continue", HomeRowPage{})
	if err != nil || len(row.Entries) != 1 || row.Entries[0].ID != homeItem(t, db, "e102").Public {
		t.Fatalf("special interrupted normal season: %+v %v", row.Entries, err)
	}
	homeWatched(t, db, "p", "2026-09-02T00:00:00.000Z", "e102", "e201", "e202")
	settleCompact(t, db)
	row, err = service.HomeSingleRow(r, "continue", HomeRowPage{})
	if err != nil || len(row.Entries) != 0 {
		t.Fatalf("completed show stayed visible: %+v %v", row.Entries, err)
	}
	homeAddSeason(t, db, "show", "s3", 3)
	homeAddSeasonalEpisode(t, db, "show", "s3", "e301", "Premiere", "/tv/e301.mkv", 1)
	settleCompact(t, db)
	row, err = service.HomeSingleRow(r, "continue", HomeRowPage{})
	if err != nil || len(row.Entries) != 1 || row.Entries[0].ID != homeItem(t, db, "e301").Public {
		t.Fatalf("new premiere did not restore the show: %+v %v", row.Entries, err)
	}
	if _, err = service.MutateActivity("p", ActivityMutation{OperationID: "reset-show-history", Action: "reset-viewing-activity"}, func(*sql.Tx) error { return nil }); err != nil {
		t.Fatal(err)
	}
	settleCompact(t, db)
	row, err = service.HomeSingleRow(r, "continue", HomeRowPage{})
	if err != nil || len(row.Entries) != 0 {
		t.Fatalf("reset activity retained the show index: %+v %v", row.Entries, err)
	}
}

func TestHomeContinueOwnerWindowAndPremiereChoice(t *testing.T) {
	db := homeFixture(t)
	s := New(db)
	r := homeRequestFixture("tv")
	homeWatched(t, db, "p", "2026-01-01T00:00:00.000Z", "e101")
	settleCompact(t, db)
	row, err := s.HomeSingleRow(r, "continue", HomeRowPage{})
	if err != nil || len(row.Entries) != 0 {
		t.Fatalf("expired next episode stayed visible: %+v %v", row.Entries, err)
	}
	homeWatched(t, db, "p", "2026-01-02T00:00:00.000Z", "e102", "e201", "e202")
	homeAddSeason(t, db, "show", "s3", 3)
	premiere := homeAddSeasonalEpisode(t, db, "show", "s3", "e301", "Premiere", "/tv/e301.mkv", 1)
	catalogtest.New(t, db).Fields(premiere.ID, map[string]any{"added_text": "2026-01-03T00:00:00.000Z"})
	settleCompact(t, db)
	row, err = s.HomeSingleRow(r, "continue", HomeRowPage{})
	if err != nil || len(row.Entries) != 0 {
		t.Fatal("old premiere must expire", row, err)
	}
	catalogtest.New(t, db).Fields(premiere.ID, map[string]any{"added_text": r.Now.Add(-24 * time.Hour).Format(time.RFC3339)})
	settleCompact(t, db)
	row, err = s.HomeSingleRow(r, "continue", HomeRowPage{})
	if err != nil || len(row.Entries) != 1 || row.Entries[0].ID != homeItem(t, db, "e301").Public {
		t.Fatalf("new season premiere did not bypass the window: %+v %v", row.Entries, err)
	}
	homeExec(t, db, `INSERT INTO admin_documents(scope,revision,body,updated_ms) VALUES('library:tv',2,'{"continueWatching":{"weeks":16,"maximumItems":40,"includeSeasonPremieres":false}}',0)`)
	settleCompact(t, db)
	row, err = s.HomeSingleRow(r, "continue", HomeRowPage{})
	if err != nil || len(row.Entries) != 0 {
		t.Fatalf("disabled premiere exception ignored: %+v %v", row.Entries, err)
	}
}

func TestHomeContinueOwnerMaximumCapsBeforePaging(t *testing.T) {
	db := homeFixture(t)
	s := New(db)
	for n, id := range []string{"m1", "m2", "m3", "m4", "m5"} {
		item := homeItem(t, db, id)
		homeExec(t, db, `INSERT INTO progress(profile_id,item_id,position,unit,playback_id) VALUES('p',?,120000,0,?)`, item.ID, "play-"+id)
		homeExec(t, db, `INSERT INTO progress_activity VALUES('p','movies',?,?,'paused')`, item.ID, time.Date(2026, 9, 1+n, 0, 0, 0, 0, time.UTC).Format("2006-01-02T15:04:05.000Z"))
	}
	homeExec(t, db, `INSERT INTO admin_documents(scope,revision,body,updated_ms) VALUES('library:movies',2,'{"continueWatching":{"weeks":16,"maximumItems":2,"includeSeasonPremieres":true}}',0)`)
	settleCompact(t, db)
	row, err := s.HomeSingleRow(homeRequestFixture("movies"), "continue", HomeRowPage{Limit: 1})
	if err != nil || rowTotal(row) != 2 || len(row.Entries) != 1 || row.Entries[0].ID != homeItem(t, db, "m5").Public || row.NextCursor == "" {
		t.Fatalf("owner maximum did not cap the row before paging: %+v %v", row, err)
	}
}

func TestHomeContinueExpandsPastHiddenRecentShows(t *testing.T) {
	db := homeFixture(t)
	s := New(db)
	c := catalogtest.New(t, db)
	names := homeNames(t, db)
	for n := 0; n < 3; n++ {
		show, item := fmt.Sprintf("extra-show-%d", n), fmt.Sprintf("extra-episode-%d", n)
		names[show] = c.Show(c.Handle("tv"), show, 2020)
		homeRegister(db, show, names[show])
		names[item] = homeAddAbsoluteEpisode(t, db, show, item, item, "/tv/"+item+".mkv", 1)
		homeExec(t, db, `INSERT INTO progress(profile_id,item_id,position,unit,playback_id) VALUES('p',?,120000,0,?)`, names[item].ID, "play-"+item)
		homeExec(t, db, `INSERT INTO progress_activity VALUES('p','tv',?,?,'paused')`, names[item].ID, fmt.Sprintf("2026-09-%02dT00:00:00.000Z", 10+n))
	}
	c.Attributes(names["extra-episode-2"].ID, "contentRating", "R")
	c.Exec(`INSERT INTO content_rating_pending(value_key,value) VALUES('r','R') ON CONFLICT(value_key) DO UPDATE SET value=excluded.value`)
	if _, err := s.ClassifyPendingRatings(context.Background()); err != nil {
		t.Fatal(err)
	}
	homeExec(t, db, `INSERT INTO content_rating_ages(value_key,minimum_age) VALUES('r',17) ON CONFLICT(value_key) DO UPDATE SET minimum_age=excluded.minimum_age`)
	homeExec(t, db, `INSERT INTO admin_documents(scope,revision,body,updated_ms) VALUES('library:tv',2,'{"continueWatching":{"weeks":16,"maximumItems":2,"includeSeasonPremieres":true}}',0)`)
	r := homeRequestFixture("tv")
	r.Viewer.Restrictions = restrictionOf(ceiling(13), false)
	settleCompact(t, db)
	row, err := s.HomeSingleRow(r, "continue", HomeRowPage{Limit: 2})
	if err != nil || rowTotal(row) != 2 || len(row.Entries) != 2 || row.Entries[0].ID != homeItem(t, db, "extra-episode-1").Public || row.Entries[1].ID != homeItem(t, db, "extra-episode-0").Public {
		t.Fatalf("a hidden recent show consumed the owner cap: %+v %v", row, err)
	}
}

func TestHomeContinueAbsoluteAnimeAdvancesByNumber(t *testing.T) {
	c := catalogtest.Open(t)
	db := c.DB
	library := c.Library("anime", "Anime", "anime", "/anime")
	show := c.Show(library, "Absolute", 2020)
	first := c.Entity(compactcatalog.Entity{Library: library, Kind: compactcatalog.Episode, Key: "absolute-1", Title: "Absolute 1", Added: "2026-01-01T00:00:00.000Z"}, map[string]any{"show_id": show.ID, "numbering": "absolute", "number": 1})
	second := c.Entity(compactcatalog.Entity{Library: library, Kind: compactcatalog.Episode, Key: "absolute-2", Title: "Absolute 2", Added: "2026-01-02T00:00:00.000Z"}, map[string]any{"show_id": show.ID, "numbering": "absolute", "number": 2})
	c.File(first.ID, "/anime/absolute-1.mkv", 600)
	c.File(second.ID, "/anime/absolute-2.mkv", 600)
	names := catalogtest.Names{"absolute-1": first, "absolute-2": second}
	homeNamesMu.Lock()
	homeNamesByDB[db] = names
	homeNamesMu.Unlock()
	c.Drain()
	s := New(db)
	homeWatched(t, db, "viewer", "2026-09-22T00:00:00.000Z", "absolute-1")
	r := HomeRequest{Viewer: Viewer{Profile: "viewer", Fence: "fence", Libraries: []string{"anime"}}, ServerID: "server", Profile: "viewer", ViewerFence: "fence", Libraries: []string{"anime"}, Now: time.Date(2026, 9, 23, 0, 0, 0, 0, time.UTC)}
	settleCompact(t, db)
	row, err := s.HomeSingleRow(r, "continue", HomeRowPage{})
	if err != nil || len(row.Entries) != 1 || row.Entries[0].ID != second.Public {
		t.Fatalf("absolute anime did not advance: %+v %v", row.Entries, err)
	}
}

// The community row is owner policy, not a viewer preference: it is absent from
// the document and unreachable by id until an owner turns it on, and it is
// populated only by meaningful activity from at least three distinct eligible
// sharing profiles.
func TestHomeTrendingGating(t *testing.T) {
	db := homeFixture(t)
	service := New(db)
	p := persistence.PersonalOwnerKey("local", "a", "p")
	q := persistence.PersonalOwnerKey("local", "b", "q")
	homeExec(t, db, `INSERT INTO accounts(id,username,password_hash,profile_id,epoch) VALUES('a','user',x'00','p',1),('b','user2',x'00','q',1),('c','user3',x'00','r',1);UPDATE direct_memberships SET allowed_libraries='["movies"]' WHERE account_id IN('b','c')`)
	shareFixtureProfiles(t, db)
	item := homeItem(t, db, "m1")
	homeExec(t, db, `INSERT INTO progress(profile_id,item_id,position,unit,playback_id) VALUES(?,?,10000,0,'play')`, p, item.ID)
	homeExec(t, db, `INSERT INTO progress(profile_id,item_id,position,unit,playback_id) VALUES(?,?,20000,0,'play')`, q, item.ID)
	homeExec(t, db, `INSERT INTO personal_history(id,profile_id,item_id,started_at,updated_at,sequence,position,unit,completed) VALUES('h1',?,?,'2026-09-15T00:00:00.000Z','2026-09-15T00:00:00.000Z',1,10000,0,1),('h2',?,?,'2026-09-15T00:00:00.000Z','2026-09-15T00:00:00.000Z',1,20000,0,1)`, p, item.ID, q, item.ID)
	homeExec(t, db, `INSERT INTO personal_history(id,profile_id,item_id,started_at,updated_at,sequence,position,unit,completed) VALUES('h3',?,?,'2026-09-15T00:00:00.000Z','2026-09-15T00:00:00.000Z',1,30000,0,1)`, persistence.PersonalOwnerKey("local", "c", "r"), item.ID)
	settleCompact(t, db)
	if _, e := service.HomeSingleRow(homeRequestFixture(), "community_watching", HomeRowPage{}); !errors.Is(e, ErrHomeRowUnknown) {
		t.Fatal("gated row reachable", e)
	}
	request := homeRequestFixture()
	request.CommunityActivity = true
	// Popular on this server is hidden by default (collections frame 23):
	// the viewer places it from Customize Home.
	request.RowOrder = []string{"community_watching"}
	settleCompact(t, db)
	out, e := service.HomeRows(request)
	if e != nil {
		t.Fatal(e)
	}
	row := homeRowByID(out.Rows, "community_watching")
	if row == nil || len(row.Entries) != 1 || row.Entries[0].ID != item.Public {
		t.Fatal("enabled community row missing", out.Rows)
	}
	if row.PrivacySensitivity != "aggregated" || row.PolicyState != "available" {
		t.Fatal("community row policy not published", row)
	}
	// One distinct eligible viewer is below the threshold: the shelf disappears.
	homeExec(t, db, `DELETE FROM personal_history WHERE profile_id=?`, q)
	settleCompact(t, db)
	out, e = service.HomeRows(request)
	if e != nil {
		t.Fatal(e)
	}
	if homeRowByID(out.Rows, "community_watching") != nil {
		t.Fatal("community row published below the threshold", out.Rows)
	}
}

// Anchored ranges survive a publication: the client's anchor keeps its card on
// screen even though its absolute position moved.
func TestHomeRowPagingAnchorAfterPublication(t *testing.T) {
	db := homeFixture(t)
	service := New(db)
	request := homeRequestFixture("movies")
	settleCompact(t, db)
	row, e := service.HomeSingleRow(request, "new_on_server", HomeRowPage{Limit: 2})
	if e != nil {
		t.Fatal(e)
	}
	if len(row.Entries) != 2 || row.Entries[0].ID != homeItem(t, db, "m5").Public || row.Entries[1].ID != homeItem(t, db, "m4").Public || rowTotal(row) != 5 || row.NextCursor == "" {
		t.Fatal("first page", row.Entries, row.Total, row.NextCursor)
	}
	settleCompact(t, db)
	next, e := service.HomeSingleRow(request, "new_on_server", HomeRowPage{Limit: 2, Cursor: row.NextCursor})
	if e != nil {
		t.Fatal(e)
	}
	if len(next.Entries) != 2 || next.Entries[0].ID != homeItem(t, db, "m3").Public || next.Start != 2 {
		t.Fatal("cursor page", next.Entries, next.Start)
	}
	settleCompact(t, db)
	offset, e := service.HomeSingleRow(request, "new_on_server", HomeRowPage{Limit: 2, Start: 2})
	if e != nil || offset.Entries[0].ID != homeItem(t, db, "m3").Public {
		t.Fatal("offset range disagrees with cursor page", offset.Entries, e)
	}
	// A publication inserts a newer item, moving every anchor down by one.
	m6 := homeAddMovie(t, db, "m6", "Movie m6", 2006)
	catalogtest.New(t, db).Fields(m6.ID, map[string]any{"backdrop_url": "/backdrop", "added_text": "2026-01-09T00:00:00.000Z"})
	settleCompact(t, db)
	anchored, e := service.HomeSingleRow(request, "new_on_server", HomeRowPage{Limit: 2, Start: 2, AnchorID: homeItem(t, db, "m3").Public})
	if e != nil {
		t.Fatal(e)
	}
	if anchored.Start != 3 || anchored.Entries[0].ID != homeItem(t, db, "m3").Public || rowTotal(anchored) != 6 {
		t.Fatal("anchor did not re-resolve after publication", anchored.Start, anchored.Entries, anchored.Total)
	}
	settleCompact(t, db)
	if _, e = service.HomeSingleRow(request, "new_on_server", HomeRowPage{Limit: 2, Cursor: row.NextCursor}); !errors.Is(e, ErrStaleContinuation) {
		t.Fatal("changed membership accepted a stale cursor", e)
	}
	settleCompact(t, db)
	if _, e = service.HomeSingleRow(request, "new_on_server", HomeRowPage{Limit: 2, Revision: "1:1"}); !errors.Is(e, ErrStaleContinuation) {
		t.Fatal("a stale revision did not require an explicit restart", e)
	}
}

// Every kind gets rows from one engine; detail keeps its published shape.
func TestRecommendationsForEveryKind(t *testing.T) {
	db := homeFixture(t)
	service := New(db)
	c := catalogtest.New(t, db)
	names := homeNames(t, db)
	names["c"] = c.Collection(c.Handle("movies"), "Saga", names["m1"], names["m2"])
	settleCompact(t, db)
	// Music and audiobook relations are proven against a real listening
	// corpus in TestListeningRelatedRowsUseRealLibraryRelationships; the shared
	// kinds contract below is what every published row must hold.
	for _, row := range []struct{ item, relation string }{
		{"m1", "more_like"},
	} {
		public := homeItem(t, db, row.item).Public
		rows, _, e := service.ItemRecommendationRows(homeViewerForItem(service, "p", "", public), public, 0, false)
		if e != nil {
			t.Fatal(row.item, e)
		}
		found := false
		for _, candidate := range rows {
			if candidate.Relation == row.relation {
				found = true
				if len(candidate.Entries) == 0 || candidate.Title == "" || candidate.ArtworkShape == "" {
					t.Fatal("row published without entries or shape", candidate)
				}
			}
		}
		if !found {
			t.Fatal("missing relation", row.item, row.relation, rows)
		}
	}
	movie := homeItem(t, db, "m1").Public
	rows, _, e := service.ItemRecommendationRows(homeViewerForItem(service, "p", "", movie), movie, 0, false)
	if e != nil {
		t.Fatal(e)
	}
	collection := false
	for _, row := range rows {
		collection = collection || row.Relation == "collection"
	}
	if !collection {
		t.Fatal("collection membership not published", rows)
	}
	settleCompact(t, service.db)
	detail, e := service.Detail(homeViewerForItem(service, "p", "fence", movie), "server", movie, false)
	if e != nil || detail.Related == nil {
		t.Fatal(e)
	}
	for _, row := range detail.Related.Rows {
		if row.Relation != "genre" && row.Relation != "person" {
			t.Fatal("detail.related left its published shape", row.Relation)
		}
	}
}

func TestSuggestionsRankAndExplain(t *testing.T) {
	db := homeFixture(t)
	service := New(db)
	homeWatched(t, db, "p", "2026-09-01T00:00:00.000Z", "m1")
	homeExec(t, db, `INSERT INTO personal_items(profile_id,item_id,watchlisted,favorite,revision) VALUES('p',?,1,0,1) ON CONFLICT(profile_id,item_id) DO UPDATE SET watchlisted=1`, homeItem(t, db, "m4").ID)
	out, e := service.Suggestions(homeRequestFixture("movies"))
	if e != nil {
		t.Fatal(e)
	}
	if len(out.Items) == 0 || out.GeneratedAt == "" {
		t.Fatal("no suggestions", out)
	}
	for _, suggestion := range out.Items {
		if suggestion.Entry.ID == homeItem(t, db, "m1").Public {
			t.Fatal("a watched seed was suggested back", suggestion)
		}
		if suggestion.Reason == "" || suggestion.Source == "" || suggestion.Score <= 0 {
			t.Fatal("unexplained suggestion", suggestion)
		}
	}
}

// The shared recommendation rows list top-level works. Home's numbered Most
// watched row further restricts those works to video posters.
func TestHomeEngineRowsListOnlyTopLevelWorks(t *testing.T) {
	db := homeFixture(t)
	service := New(db)
	profile := persistence.PersonalOwnerKey("local", "a", "p")
	track := homeItem(t, db, "t1")
	homeExec(t, db, `INSERT INTO progress(profile_id,item_id,position,unit,playback_id) VALUES(?,?,30000,0,'play')`, profile, track.ID)
	homeExec(t, db, `INSERT INTO personal_history(id,profile_id,item_id,started_at,updated_at,sequence,position,unit,completed) VALUES('h1', ?, ?,'2026-09-15T00:00:00.000Z','2026-09-15T00:00:00.000Z',1,30000,0,1)`, profile, track.ID)
	legal := map[string]bool{"movie": true, "show": true, "album": true, "book": true}
	request := homeRequestFixture()
	request.CommunityActivity = true
	for _, id := range []string{"recommended", "trending_now", "community_watching"} {
		settleCompact(t, db)
		row, e := service.HomeSingleRow(request, id, HomeRowPage{})
		if e != nil {
			t.Fatal(id, e)
		}
		for _, entry := range row.Entries {
			if id == "community_watching" && entry.Kind != "movie" && entry.Kind != "show" {
				t.Fatalf("%s published audio work %s kind %q", id, entry.ID, entry.Kind)
			}
			if !legal[entry.Kind] {
				t.Fatalf("%s published %s kind %q", id, entry.ID, entry.Kind)
			}
		}
	}
}

// Listen next groups bounded real continuation files into unique album/book
// containers, then includes suggestions. The source keeps its activity index.
func TestHomeListenNextGroupsResumesAcrossLibraries(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	db := homeFixture(t)
	s := New(db)
	c := catalogtest.New(t, db)
	music2 := c.Library("music2", "More Music", "music", "/music2")
	artist2 := c.Artist(music2, "More Artist")
	album2 := c.Album(artist2, "More Release", 2020)
	album1 := homeItem(t, db, "album")
	stamp := func(n int) string {
		return time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC).Add(time.Duration(n) * time.Minute).Format("2006-01-02T15:04:05.000Z")
	}
	for n := 0; n < 60; n++ {
		library := "music"
		if n%3 == 0 {
			library = "music2"
		}
		id := fmt.Sprintf("song-%02d", n)
		album := album1
		if library == "music2" {
			album = album2
		}
		item := c.Song(album, n+1, "/"+library+"/"+id+".flac", "Song "+id)
		homeRegister(db, id, item)
		homeExec(t, db, `INSERT INTO progress(profile_id,item_id,position,unit,playback_id) VALUES('p',?,120000,0,?)`, item.ID, "play-"+id)
		homeExec(t, db, `INSERT INTO progress_activity VALUES('p',?,?,?,'paused')`, library, item.ID, stamp(n))

	}
	// Newer unfinished video activity shares the index and must not enter the row.
	movie := homeItem(t, db, "m1")
	homeExec(t, db, `INSERT INTO progress(profile_id,item_id,position,unit,playback_id) VALUES('p',?,120000,0,'play-m1')`, movie.ID)
	homeExec(t, db, `INSERT INTO progress_activity VALUES('p','movies',?,?, 'paused')`, movie.ID, stamp(100))
	r := homeRequestFixture("movies", "music", "music2", "books")
	settleCompact(t, db)
	row, err := s.HomeSingleRow(r, "listen_next", HomeRowPage{Limit: 12})
	if err != nil {
		t.Fatal(err)
	}
	if rowTotal(row) != 3 || len(row.Entries) != 3 || row.Entries[0].ID != album1.Public || row.Entries[1].ID != album2.Public || row.Entries[2].ID != homeItem(t, db, "book").Public {
		t.Fatalf("resume containers/order: %+v", row)
	}
	if _, err = s.HomeSingleRow(r, "continue_listening", HomeRowPage{}); !errors.Is(err, ErrHomeRowUnknown) {
		t.Fatal("retired row reachable", err)
	}
	raw := `["movies","music","music2","books"]`
	rows, err := db.Query(`EXPLAIN QUERY PLAN SELECT id,ord FROM (`+homeContinueBase("continue_listening")+`) ORDER BY ord DESC,id DESC LIMIT 40`, "p", "p", raw)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	plan := ""
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err = rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		plan += detail + "\n"
	}
	// The walk reads the activity index in row order, at most a window of it;
	// ordering that window is the only sort.
	if !contains(plan, "progress_home_profile_recent") {
		t.Fatalf("Continue Listening does not walk the activity index in row order:\n%s", plan)
	}
}

// B83: the started-show walk continues slice by slice from the last key, each
// show evaluated once, until the maximum is filled or the history runs out; it
// never counts the profile's started shows. Here the 30 newest shows are
// finished (no slot) and only the oldest five can fill the row, so the walk
// crosses several slices (maximum 2 → 2, 4, 8, 16, 32) and then exhausts.
func TestHomeContinueWalksStartedShowsInSlices(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	db := homeFixture(t)
	s := New(db)
	c := catalogtest.New(t, db)
	for n := 0; n < 35; n++ {
		show, item := fmt.Sprintf("walk-show-%02d", n), fmt.Sprintf("walk-episode-%02d", n)
		showItem := c.Show(c.Handle("tv"), show, 2020)
		homeRegister(db, show, showItem)
		episode := homeAddAbsoluteEpisode(t, db, show, item, item, "/tv/"+item+".mkv", 1)
		homeExec(t, db, `INSERT INTO progress(profile_id,item_id,position,unit,playback_id) VALUES('p',?,120000,0,?)`, episode.ID, "play-"+item)
		homeExec(t, db, `INSERT INTO progress_activity VALUES('p','tv',?,?,'paused')`, episode.ID, time.Date(2026, 9, 1, 0, n, 0, 0, time.UTC).Format("2006-01-02T15:04:05.000Z"))
		if n >= 5 {
			homeWatched(t, db, "p", "2026-09-02T00:00:00.000Z", item)
		}
	}
	homeExec(t, db, `INSERT INTO admin_documents(scope,revision,body,updated_ms) VALUES('library:tv',2,'{"continueWatching":{"weeks":16,"maximumItems":2,"includeSeasonPremieres":true}}',0)`)
	settleCompact(t, db)
	row, err := s.HomeSingleRow(homeRequestFixture("tv"), "continue", HomeRowPage{Limit: 5})
	if err != nil || rowTotal(row) != 2 || len(row.Entries) != 2 || row.Entries[0].ID != homeItem(t, db, "walk-episode-04").Public || row.Entries[1].ID != homeItem(t, db, "walk-episode-03").Public {
		t.Fatalf("slice walk did not reach the older open shows: %+v %v", row, err)
	}
	homeExec(t, db, `UPDATE admin_documents SET body='{"continueWatching":{"weeks":16,"maximumItems":40,"includeSeasonPremieres":true}}' WHERE scope='library:tv'`)
	settleCompact(t, db)
	row, err = s.HomeSingleRow(homeRequestFixture("tv"), "continue", HomeRowPage{Limit: 40})
	if err != nil || rowTotal(row) != 5 || row.Entries[0].ID != homeItem(t, db, "walk-episode-04").Public || row.Entries[4].ID != homeItem(t, db, "walk-episode-00").Public {
		t.Fatalf("exhausted walk: %+v %v", row, err)
	}
}

// ARCH-SRV-04: a Recently Added shelf walks the library newest first and stops
// at homeRecentMaximum works; it never materialises the library.
func TestHomeRecentShelfPagesTheFullSourceNewestFirst(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	db := homeFixture(t)
	s := New(db)
	c := catalogtest.New(t, db)
	for n := 1; n <= 150; n++ {
		name := fmt.Sprintf("new%03d", n)
		item := c.Movie(c.Handle("movies"), "/movies/"+name+".mkv", fmt.Sprintf("New %d", n), 2025)
		homeRegister(db, name, item)
		c.Fields(item.ID, map[string]any{"added_text": fmt.Sprintf("2026-09-%02dT%02d:00:00.000Z", 1+n/24, n%24)})
		if n == 150 {
			c.Attributes(item.ID, "label", "adult")
		}
	}
	r := homeRequestFixture("movies")
	settleCompact(t, db)
	row, err := s.HomeSingleRow(r, "new_on_server", HomeRowPage{Limit: 12})
	if err != nil || rowTotal(row) != 155 || row.Entries[0].ID != homeItem(t, db, "new150").Public || row.Entries[1].ID != homeItem(t, db, "new149").Public {
		t.Fatalf("open shelf: total %d first %v: %v", row.Total, row.Entries[:2], err)
	}
	r.Restrictions = identity.ContentRestrictions{BlockedLabels: []string{"adult"}}
	r.Viewer.Restrictions = r.Restrictions
	settleCompact(t, db)
	if row, err = s.HomeSingleRow(r, "new_on_server", HomeRowPage{Limit: 12}); err != nil || row.Entries[0].ID != homeItem(t, db, "new149").Public {
		t.Fatalf("restricted shelf starts with %v: %v", row.Entries[0].ID, err)
	}
	restriction, args := ItemRestrictionSQL("i.id", r.Restrictions)
	rows, err := db.Query(`EXPLAIN QUERY PLAN SELECT i.id FROM catalog_browse_rows br INDEXED BY catalog_browse_recent_dated JOIN catalog_entities i ON i.id=br.entity_id JOIN catalog_kinds k ON k.id=i.kind WHERE br.library_id=(SELECT id FROM catalog_libraries WHERE library_id=?) AND br.item_id IS NOT NULL AND br.added_text IS NOT NULL AND (COALESCE(br.added_text,''),br.entity_id)<(?,?) AND k.playable=1 AND i.kind<>11 AND i.retired=0 AND `+restriction+` ORDER BY COALESCE(br.added_text,'') DESC,br.entity_id DESC LIMIT 256`, append([]any{"movies", "￿", int64(1 << 62)}, args...)...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err = rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		if contains(detail, "TEMP B-TREE") {
			t.Fatalf("recent walk sorts instead of seeking: %s", detail)
		}
	}
}

// The Recommended list is a maintained model that may be served stale after a
// catalogue change; the viewer's restriction is applied again on every read.
func TestRecommendedModelReappliesRestrictionToAStaleList(t *testing.T) {
	db := homeFixture(t)
	s := New(db)
	r := homeRequestFixture("movies")
	r.Profile, r.Viewer.Profile = "cold", "cold"
	r.Restrictions = identity.ContentRestrictions{BlockedLabels: []string{"adult"}}
	r.Viewer.Restrictions = r.Restrictions
	first, err := s.HomeSingleRow(r, "recommended", HomeRowPage{Limit: 12})
	if err != nil || len(first.Entries) == 0 {
		t.Fatal(first.Total, err)
	}
	hidden := first.Entries[0].ID
	catalogtest.New(t, db).Attributes(catalogtest.New(t, db).ID(hidden), "label", "adult")
	// While the label is still publishing the row serves its last published
	// generation, but the per-row restriction fence hides the item whose
	// labels are pending.
	pending, err := s.HomeSingleRow(r, "recommended", HomeRowPage{Limit: 12})
	if err != nil {
		t.Fatalf("a pending label failed the row: %v", err)
	}
	for _, entry := range pending.Entries {
		if entry.ID == hidden {
			t.Fatal("an item with a pending blocked label was listed")
		}
	}
	settleCompact(t, db)
	second, err := s.HomeSingleRow(r, "recommended", HomeRowPage{Limit: 12})
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range second.Entries {
		if entry.ID == hidden {
			t.Fatal("a stale Recommended list showed a title restricted since it was computed")
		}
	}
}

// Home's default rows are Customize Home as drawn (collections frame 23):
// Recommended for you, More recommendations and Trending now are shown;
// Popular on this server is hidden until the viewer places it.
func TestHomeDefaultsFollowCustomizeHomeAsDrawn(t *testing.T) {
	db := homeFixture(t)
	request := homeRequestFixture()
	request.CommunityActivity = true
	view, err := New(db).HomeLayoutRows(request)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"recommended": false, recFamily: false, "trending_now": false, "community_watching": true}
	seen := 0
	for _, row := range view.Rows {
		if hidden, ok := want[row.ID]; ok {
			seen++
			if row.Hidden != hidden {
				t.Fatalf("%s hidden=%v by default, want %v", row.ID, row.Hidden, hidden)
			}
		}
	}
	if seen != len(want) {
		t.Fatalf("saw %d of the %d rows", seen, len(want))
	}
}
