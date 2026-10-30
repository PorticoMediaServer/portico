package catalog

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

// workspaceFixture is built and settled once per test binary; each test gets
// its own copy.
func workspaceFixture(t *testing.T) (*Service, *sql.DB, catalogtest.Names) {
	t.Helper()
	db, err := persistence.Open(filepath.Join(t.TempDir(), "workspace.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	c := catalogtest.New(t, db)
	tv := c.Library("tv", "TV", "tv", "/tv")
	anime := c.Library("anime", "Anime", "anime", "/anime")
	names := catalogtest.Names{}
	names["show"] = c.Show(tv, "Harbor", 2020)
	names["other"] = c.Show(tv, "Other", 0)
	names["absolute"] = c.Show(anime, "Absolute", 0)
	names["empty"] = c.Show(tv, "Empty", 0)
	names["specials"] = c.Season(names["show"], 0)
	names["season1"] = c.Season(names["show"], 1)
	names["season2"] = c.Season(names["show"], 2)
	names["other-season"] = c.Season(names["other"], 1)
	for n := 1; n <= 200; n++ {
		alias := fmt.Sprintf("ep-%03d", n)
		names[alias] = workspaceEpisode(t, c, tv, names["show"], names["season1"], "harbor:2020", alias, fmt.Sprintf("Title %d", n), "seasonal", 1, n)
	}
	names["special"] = workspaceEpisode(t, c, tv, names["show"], names["specials"], "harbor:2020", "special", "Special", "seasonal", 0, 1)
	names["next-season"] = workspaceEpisode(t, c, tv, names["show"], names["season2"], "harbor:2020", "next-season", "Next Season", "seasonal", 2, 1)
	names["absolute-1"] = workspaceEpisode(t, c, anime, names["absolute"], catalogtest.Item{}, "absolute:0", "absolute-1", "Absolute 1", "absolute", 0, 1)
	names["absolute-2"] = workspaceEpisode(t, c, anime, names["absolute"], catalogtest.Item{}, "absolute:0", "absolute-2", "Absolute 2", "absolute", 0, 2)
	c.Drain()
	return New(db), db, names
}

func workspaceEpisode(t *testing.T, c *catalogtest.Catalog, library int64, show, season catalogtest.Item, showKey, alias, title, numbering string, seasonNumber, number int) catalogtest.Item {
	t.Helper()
	parent := show.ID
	keySeason := 0
	var seasonID any
	if season.ID != 0 {
		parent = season.ID
		seasonID = season.ID
		keySeason = seasonNumber
	}
	key := compactcatalog.EpisodeKey(showKey, numbering, keySeason, number)
	item := c.Entity(compactcatalog.Entity{Library: library, Kind: compactcatalog.Episode, Parent: parent, Key: key, Title: title, Year: 2020, Added: "2026-01-01T00:00:00.000Z"}, map[string]any{"show_id": show.ID, "season_id": seasonID, "numbering": numbering, "number": number})
	item.Asset, item.Token = c.File(item.ID, filepath.Join("/fixture", alias+".mp4"), 90)
	return item
}

func workspaceRequest(names catalogtest.Names) ShowWorkspaceRequest {
	return ShowWorkspaceRequest{Viewer: Viewer{Profile: "viewer", Fence: "fence", Libraries: []string{"tv", "anime"}}, ServerID: "server", Library: "tv", Profile: "viewer", ViewerFence: "fence", ShowID: names["show"].Public, Limit: 7}
}

func workspaceCeiling(age int) *int { return &age }

func workspaceRestrictionOf(max *int, blockUnrated bool, labels ...string) identity.ContentRestrictions {
	return identity.ContentRestrictions{MaximumAge: max, BlockUnrated: blockUnrated, BlockedLabels: labels, Revision: 2}
}

func workspaceScreenField(t *testing.T, db *sql.DB, library, kind string, entity int64, field, value string) {
	t.Helper()
	if _, err := db.Exec(`INSERT OR IGNORE INTO screen_metadata_work(target_kind,target_id,library_id,title_seed) VALUES(?,?,?,?)`, kind, entity, library, "fixture"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO screen_metadata_fields(target_kind,target_id,field,value,provider,source_kind,source_url,confidence,observed_at,language,region,selection_revision) VALUES(?,?,?,?,?,'provider','',1,'now','en','US',1)`, kind, entity, field, value, "fixture"); err != nil {
		t.Fatal(err)
	}
}

func workspaceCredits(c *catalogtest.Catalog, entity int64, provider string, credits ...compactcatalog.Credit) {
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		return compactcatalog.SetCreditsTx(ctx, tx, entity, provider, credits)
	})
}

func workspaceShowCredit(t *testing.T, c *catalogtest.Catalog, db *sql.DB, show catalogtest.Item, provider, providerID, name, role, department string, ordinal int) string {
	t.Helper()
	key := provider + ":" + providerID
	workspaceCredits(c, show.ID, provider, compactcatalog.Credit{PersonKey: key, PersonName: name, PersonSortName: name, ProviderPersonID: providerID, CreditID: providerID, CreditedName: name, Role: role, Department: department, Ordinal: ordinal})
	var personID int64
	var token string
	if err := db.QueryRow(`SELECT id,token FROM catalog_people WHERE identity_key=?`, key).Scan(&personID, &token); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO show_people_credits(show_id,identity_key,provider,credit_id,name,role,department,ordinal) VALUES(?,?,?,?,?,?,?,?)`, show.ID, key, provider, providerID, name, role, department, ordinal); err != nil {
		t.Fatal(err)
	}
	return token
}

func workspaceRename(db *sql.DB, names catalogtest.Names, title string) error {
	ctx := context.Background()
	return dbwork.WithWriteTx(ctx, db, dbwork.ClassMaintenance, func(tx *sql.Tx) error {
		tv, err := compactcatalog.LibraryTx(ctx, tx, "tv")
		if err != nil {
			return err
		}
		showKey := "harbor:2020"
		show, _, err := compactcatalog.UpsertEntityTx(ctx, tx, compactcatalog.Entity{Library: tv, Kind: compactcatalog.Show, Key: compactcatalog.ShowKey(showKey), Title: title, Year: 2020})
		if err != nil {
			return err
		}
		if err = compactcatalog.SetFactsTx(ctx, tx, show, map[string]any{"local_key": showKey}); err != nil {
			return err
		}
		season := names["season1"].ID
		epKey := compactcatalog.EpisodeKey(showKey, "seasonal", 1, 1)
		episode, _, err := compactcatalog.UpsertEntityTx(ctx, tx, compactcatalog.Entity{Library: tv, Kind: compactcatalog.Episode, Parent: season, Key: epKey, Title: title, Year: 2020, Added: "2026-01-01T00:00:00.000Z"})
		if err != nil {
			return err
		}
		return compactcatalog.SetFactsTx(ctx, tx, episode, map[string]any{"show_id": show, "season_id": season, "numbering": "seasonal", "number": 1})
	})
}

func TestShowWorkspaceProjectsSelectedLogoSeparatelyFromPoster(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	s, db, names := workspaceFixture(t)
	logo, poster := strings.Repeat("a", 64), strings.Repeat("b", 64)
	if _, err := db.Exec(`INSERT INTO artwork_blobs(digest,mime,width,height,bytes,created_at,status) VALUES(?, 'image/png',800,300,10,'now','ready'),(?, 'image/png',800,1200,10,'now','ready'); INSERT OR IGNORE INTO artwork_sources(digest,accepted_mime,accepted_width,accepted_height,small_blob,large_blob,ambient_blob,large_edge,small_outlier,large_outlier,encoder_revision,created_at) SELECT digest,mime,width,height,digest,digest,digest,1920,0,0,1,created_at FROM artwork_blobs`, logo, poster); err != nil {
		t.Fatal(err)
	}
	for _, candidate := range []struct{ id, role, digest string }{{"logo", "logo", logo}, {"poster", "poster", poster}} {
		if _, err := db.Exec(`INSERT INTO artwork_candidates(id,kind,entity_id,role,subject,provider,image_id,origin,attribution,source_fence,observed_at) VALUES(?,'show',?,?,'','fixture',?,'local','fixture','fence','now')`, candidate.id, names["show"].ID, candidate.role, candidate.id); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO artwork_selections(kind,entity_id,role,subject,candidate_id,digest,locked,revision,actor,observed_at) SELECT column1,column2,column3,column4,column5,column6,column8,column9,column10,column11 FROM (VALUES('show',?,?,'',?,?,?,0,1,'fixture','now'))`, names["show"].ID, candidate.role, candidate.id, candidate.digest, candidate.digest); err != nil {
			t.Fatal(err)
		}
	}
	page, err := s.ShowWorkspace(workspaceRequest(names))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(page.Show.LogoURL, "/art/logo?"+fixtureArtQuery(logo, 800, 300, 10)) || !strings.Contains(page.Show.PosterURL, "/art/poster?"+fixtureArtQuery(poster, 800, 1200, 10)) {
		t.Fatalf("show hero artwork roles were mixed: %+v", page.Show)
	}
}

func TestShowWorkspaceProjectsShowFacts(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	s, db, names := workspaceFixture(t)
	c := catalogtest.New(t, db)
	c.Fields(names["show"].ID, map[string]any{"original_title": "Original Harbor", "tagline": "Across the water", "content_rating": "TV-14", "studio": "Harbor Studio", "network": "North Network", "country": "CA"})
	workspaceScreenField(t, db, "tv", "show", names["show"].ID, "genres", `[{"id":"18","name":"Drama"},{"id":"18b","name":"drama"},{"id":"99","name":"Mystery"}]`)
	workspacePublishedLanguage(t, db, "tv", "show", names["show"].ID, "en")
	workspaceScreenField(t, db, "tv", "show", names["show"].ID, "credits", `[{"id":"p1","name":"Actor","role":"Captain","department":"Acting","ordinal":0}]`)
	workspaceShowCredit(t, c, db, names["show"], "fixture", "p1", "Actor", "Captain", "Acting", 0)
	page, err := s.ShowWorkspace(workspaceRequest(names))
	if err != nil {
		t.Fatal(err)
	}
	show := page.Show
	if show.OriginalTitle != "Original Harbor" || show.Tagline != "Across the water" || show.ContentRating != "TV-14" || show.Studio != "Harbor Studio" || show.Network != "North Network" || show.Country != "CA" || show.OriginalLanguage != "en" || !reflect.DeepEqual(show.Genres, []string{"Drama", "Mystery"}) {
		t.Fatalf("show facts missing: %+v", show)
	}
	if len(page.ShowCredits) != 1 || page.ShowCredits[0].Name != "Actor" || page.ShowCredits[0].Role != "Captain" {
		t.Fatalf("show credits missing: %+v", page.ShowCredits)
	}
}

func TestShowWorkspaceEpisodeCreditsUseSelectedVisibleItem(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	s, db, names := workspaceFixture(t)
	c := catalogtest.New(t, db)
	digest := strings.Repeat("c", 64)
	thumbnail := strings.Repeat("d", 64)
	workspaceCredits(c, names["ep-001"].ID, "fixture", compactcatalog.Credit{PersonKey: "fixture:visible", PersonName: "Guest", PersonSortName: "Guest", CreditedName: "Guest", Role: "Detective", Department: "Acting"})
	workspaceCredits(c, names["ep-002"].ID, "fixture", compactcatalog.Credit{PersonKey: "fixture:hidden", PersonName: "Hidden Guest", PersonSortName: "Hidden Guest", CreditedName: "Hidden Guest", Role: "Secret", Department: "Acting"})
	var visibleID int64
	var visibleToken string
	if err := db.QueryRow(`SELECT id,token FROM catalog_people WHERE identity_key='fixture:visible'`).Scan(&visibleID, &visibleToken); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO artwork_blobs(digest,mime,width,height,bytes,created_at,status) VALUES(?, 'image/png',800,800,10,'now','ready'),(?, 'image/png',400,400,10,'now','ready'); INSERT OR IGNORE INTO artwork_sources(digest,accepted_mime,accepted_width,accepted_height,small_blob,large_blob,ambient_blob,large_edge,small_outlier,large_outlier,encoder_revision,created_at) SELECT digest,mime,width,height,digest,digest,digest,1920,0,0,1,created_at FROM artwork_blobs`, digest, thumbnail); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO artwork_candidates(id,kind,entity_id,role,subject,provider,image_id,origin,attribution,source_fence,observed_at) VALUES('portrait','item',?,'portrait','fixture:visible','fixture','portrait','local','fixture','fence','now')`, names["ep-001"].ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO artwork_selections(kind,entity_id,role,subject,candidate_id,digest,locked,revision,actor,observed_at) SELECT column1,column2,column3,column4,column5,column6,column8,column9,column10,column11 FROM (VALUES('item',?,'portrait','fixture:visible','portrait',?,?,0,1,'fixture','now'))`, names["ep-001"].ID, digest, thumbnail); err != nil {
		t.Fatal(err)
	}
	c.Attributes(names["ep-001"].ID, "contentRating", "PG")
	c.Attributes(names["ep-002"].ID, "contentRating", "R")
	if _, err := db.Exec(`INSERT INTO content_rating_ages(value_key,minimum_age) VALUES('pg',10),('r',17) ON CONFLICT(value_key) DO UPDATE SET minimum_age=excluded.minimum_age`); err != nil {
		t.Fatal(err)
	}
	c.Drain()
	r := workspaceRequest(names)
	r.EpisodeID = names["ep-001"].Public
	r.Viewer.Restrictions = workspaceRestrictionOf(workspaceCeiling(13), false)
	page, err := s.ShowWorkspace(r)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.EpisodeCredits) != 1 || page.EpisodeCredits[0].ID != visibleToken || page.EpisodeCredits[0].PortraitURL != fixtureArtURL("item", names["ep-001"].Public, "portrait", "fixture:visible", digest, 800, 800, 10) {
		t.Fatalf("selected episode credits: %+v", page.EpisodeCredits)
	}
	r.EpisodeID = names["ep-002"].Public
	if _, err = s.ShowWorkspace(r); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("hidden episode credits remained addressable: %v", err)
	}
}

func TestShowWorkspaceNextUpChoosesSeasonAndHonoursVisibility(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	s, db, names := workspaceFixture(t)
	c := catalogtest.New(t, db)
	r := workspaceRequest(names)
	page, err := s.ShowWorkspace(r)
	if err != nil || page.NextUp == nil || page.NextUp.ID != names["ep-001"].Public || page.Selected.SeasonID == nil || *page.Selected.SeasonID != names["season1"].Public {
		t.Fatalf("unstarted show did not offer its first episode: %+v %v", page.NextUp, err)
	}
	if _, err = db.Exec(`INSERT INTO personal_items(profile_id,item_id,watched,revision,last_played_at) VALUES('viewer',?,1,1,'2026-09-22T00:00:00.000Z'); INSERT INTO personal_watched_intents(profile_id,item_id,watched,authored_at) VALUES('viewer',?,1,'2026-09-22T00:00:00.000000000Z')`, names["ep-200"].ID, names["ep-200"].ID); err != nil {
		t.Fatal(err)
	}
	page, err = s.ShowWorkspace(r)
	if err != nil || page.NextUp == nil || page.NextUp.ID != names["next-season"].Public || page.Selected.SeasonID == nil || *page.Selected.SeasonID != names["season2"].Public {
		t.Fatalf("show hero did not advance to next season: %+v %v", page.NextUp, err)
	}
	c.Attributes(names["next-season"].ID, "label", "Adult")
	r.Viewer.Restrictions = identity.ContentRestrictions{BlockedLabels: []string{"Adult"}}
	page, err = s.ShowWorkspace(r)
	if err != nil || page.NextUp != nil {
		t.Fatalf("hidden successor leaked or restarted the show: %+v %v", page.NextUp, err)
	}
}

func TestShowWorkspaceRestrictionAppliesBeforeCountsAndEpisodePage(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	s, db, names := workspaceFixture(t)
	c := catalogtest.New(t, db)
	c.Attributes(names["ep-001"].ID, "label", "Adult")
	if _, err := db.Exec(`INSERT INTO personal_items(profile_id,item_id,watched,revision) VALUES('viewer',?,1,1),('viewer',?,1,1);
 INSERT INTO personal_watched_intents(profile_id,item_id,watched,authored_at) VALUES('viewer',?,1,'2026-09-10T00:00:00.000000000Z'),('viewer',?,1,'2026-09-10T00:00:00.000000000Z')`, names["ep-001"].ID, names["ep-002"].ID, names["ep-001"].ID, names["ep-002"].ID); err != nil {
		t.Fatal(err)
	}
	workspaceShowCredit(t, c, db, names["show"], "fixture", "hidden-person", "Hidden Actor", "Guest", "Acting", 0)
	workspaceScreenField(t, db, "tv", "show", names["show"].ID, "credits", `[{"id":"hidden-person","name":"Hidden Actor","role":"Guest","department":"Acting","ordinal":0}]`)
	r := workspaceRequest(names)
	r.Viewer.Restrictions = identity.ContentRestrictions{BlockedLabels: []string{"Adult"}}
	w, err := s.ShowWorkspace(r)
	if err != nil || w.SeasonTotalCount != 3 || w.Episodes.Sections[0].TotalCount != 199 || w.Episodes.Sections[0].Entries[0].ID != names["ep-002"].Public {
		t.Fatal("restricted workspace count/page", w, err)
	}
	// A restriction filters titles, not people: the show's cast stays (lead,
	// 24 Sep; Spec — Page Content §2).
	if len(w.ShowCredits) != 1 || w.ShowCredits[0].Name != "Hidden Actor" {
		t.Fatal("show cast must stay under restrictions", w.ShowCredits)
	}
	for _, season := range w.Seasons {
		if season.ID == names["season1"].Public && (season.EpisodeCount == nil || *season.EpisodeCount != 199 || season.WatchedCount == nil || *season.WatchedCount != 1) {
			t.Fatalf("restricted season counts include hidden episode: %+v", season)
		}
	}
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		for n := 1; n <= 200; n++ {
			if err := compactcatalog.SetAttributesTx(ctx, tx, names[fmt.Sprintf("ep-%03d", n)].ID, "label", []string{"Adult"}); err != nil {
				return err
			}
		}
		for _, alias := range []string{"special", "next-season"} {
			if err := compactcatalog.SetAttributesTx(ctx, tx, names[alias].ID, "label", []string{"Adult"}); err != nil {
				return err
			}
		}
		return nil
	})
	if _, err = s.ShowWorkspace(r); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("fully restricted show remained reachable", err)
	}
}

func TestEpisodeAndSeasonQueriesUseViewerRestrictions(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	s, db, names := workspaceFixture(t)
	c := catalogtest.New(t, db)
	c.Attributes(names["ep-001"].ID, "label", "Adult")
	viewer := Viewer{Profile: "viewer", Libraries: []string{"tv"}, Restrictions: identity.ContentRestrictions{BlockedLabels: []string{"Adult"}}}
	shows, _, err := s.Shows(viewer, "tv", "", 10)
	if err != nil || len(shows) != 1 || shows[0].ID != names["show"].Public {
		t.Fatal(shows, err)
	}
	seasons, err := s.Seasons(viewer, names["show"].Public)
	if err != nil || len(seasons) != 3 {
		t.Fatal(seasons, err)
	}
	episodes, _, err := s.Episodes(viewer, names["show"].Public, names["season1"].Public, "", 1)
	if err != nil || len(episodes) != 1 || episodes[0].ID != names["ep-002"].Public {
		t.Fatal(episodes, err)
	}
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		for n := 1; n <= 200; n++ {
			if err := compactcatalog.SetAttributesTx(ctx, tx, names[fmt.Sprintf("ep-%03d", n)].ID, "label", []string{"Adult"}); err != nil {
				return err
			}
		}
		for _, alias := range []string{"special", "next-season"} {
			if err := compactcatalog.SetAttributesTx(ctx, tx, names[alias].ID, "label", []string{"Adult"}); err != nil {
				return err
			}
		}
		return nil
	})
	shows, _, err = s.Shows(viewer, "tv", "", 10)
	if err != nil || len(shows) != 0 {
		t.Fatal("restricted show listed", shows, err)
	}
	if _, err = s.Seasons(viewer, names["show"].Public); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("restricted seasons reachable", err)
	}
	if _, _, err = s.Episodes(viewer, names["show"].Public, names["season1"].Public, "", 10); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("restricted season episodes reachable", err)
	}
}
func TestShowWorkspaceTargetDefaultAndBoundedPaging(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	s, _, names := workspaceFixture(t)
	r := workspaceRequest(names)
	started := time.Now()
	w, e := s.ShowWorkspace(r)
	if e != nil || w.Selected.SeasonID == nil || *w.Selected.SeasonID != names["season1"].Public || len(w.Seasons) != 3 || w.Seasons[0].Title != "Season 1" || w.Seasons[2].Title != "Specials" || len(w.Groups) != 0 || len(w.Episodes.Sections[0].Entries) != 7 {
		t.Fatal(w, e)
	}
	same, e := s.ShowWorkspace(r)
	finished := time.Now()
	if e != nil {
		t.Fatal(e)
	}
	// Compare all cursor claims independently of their issue-time-dependent
	// expiry, then compare the complete content. Both signatures and TTLs still
	// have to be valid; reads need not land inside the same wall-clock second.
	compareCursor := func(first, second string) {
		t.Helper()
		if first == "" || second == "" {
			if first != second {
				t.Fatal("equal request changed cursor presence")
			}
			return
		}
		a, err := s.readSignedBrowseCursor(first)
		if err != nil {
			t.Fatal("first cursor signature", err)
		}
		b, err := s.readSignedBrowseCursor(second)
		if err != nil {
			t.Fatal("second cursor signature", err)
		}
		for _, value := range []cursorValue{a, b} {
			if value.Expires < started.Add(30*time.Minute).Unix() || value.Expires > finished.Add(30*time.Minute).Unix() {
				t.Fatal("cursor expiry outside its issue window", value.Expires)
			}
		}
		a.Expires, b.Expires = 0, 0
		if !reflect.DeepEqual(a, b) {
			t.Fatal("equal request changed cursor claims", a, b)
		}
	}
	left, right := w, same
	compareCursor(left.NextSeasonCursor, right.NextSeasonCursor)
	left.NextSeasonCursor, right.NextSeasonCursor = "", ""
	left.Episodes.Sections = append([]ContentSection(nil), w.Episodes.Sections...)
	right.Episodes.Sections = append([]ContentSection(nil), same.Episodes.Sections...)
	if len(left.Episodes.Sections) != len(right.Episodes.Sections) {
		t.Fatal("equal request changed section count")
	}
	for i := range left.Episodes.Sections {
		compareCursor(left.Episodes.Sections[i].NextCursor, right.Episodes.Sections[i].NextCursor)
		left.Episodes.Sections[i].NextCursor, right.Episodes.Sections[i].NextCursor = "", ""
	}
	if !reflect.DeepEqual(left, right) {
		t.Fatal("equal request changed content")
	}
	r.ShowID = ""
	r.EpisodeID = names["ep-185"].Public
	w, e = s.ShowWorkspace(r)
	if e != nil || w.Show.ID != names["show"].Public || *w.Selected.SeasonID != names["season1"].Public || w.Episodes.Sections[0].Entries[0].ID != names["ep-185"].Public || len(w.Episodes.Sections[0].Entries) != 7 {
		t.Fatal(w, e)
	}
	if w.Revision != w.Episodes.Revision || w.Scope.ViewerFence != w.Episodes.Scope.ViewerFence {
		t.Fatal("torn envelope")
	}
	r.Cursor = w.Episodes.Sections[0].NextCursor
	w, e = s.ShowWorkspace(r)
	if e != nil || w.Episodes.Sections[0].Entries[0].ID != names["ep-192"].Public {
		t.Fatal(w, e)
	}
	r = workspaceRequest(names)
	r.ShowID = ""
	r.SeasonID = names["season2"].Public
	w, e = s.ShowWorkspace(r)
	if e != nil || w.Show.ID != names["show"].Public || w.Episodes.Sections[0].Entries[0].ID != names["next-season"].Public {
		t.Fatal(w, e)
	}
	r = workspaceRequest(names)
	r.SeasonLimit = 1
	w, e = s.ShowWorkspace(r)
	if e != nil || len(w.Seasons) != 1 || w.SeasonTotalCount != 3 || w.NextSeasonCursor == "" {
		t.Fatal(w, e)
	}
	if w.Seasons[0].ID != names["season1"].Public {
		t.Fatal("seasons start with season 1, Specials last", w.Seasons)
	}
	r.SeasonCursor = w.NextSeasonCursor
	w, e = s.ShowWorkspace(r)
	if e != nil || len(w.Seasons) != 1 || w.Seasons[0].ID != names["season2"].Public {
		t.Fatal(w, e)
	}
	r.SeasonCursor = w.NextSeasonCursor
	w, e = s.ShowWorkspace(r)
	if e != nil || len(w.Seasons) != 1 || w.Seasons[0].ID != names["specials"].Public || w.NextSeasonCursor != "" {
		t.Fatal("Specials page last", w.Seasons, e)
	}
}
func TestShowWorkspaceRejectsCrossedParentsAndCursorFences(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	s, db, names := workspaceFixture(t)
	for _, r := range []ShowWorkspaceRequest{
		{Viewer: Viewer{Profile: "viewer", Libraries: []string{"tv", "anime"}}, Library: "tv", Profile: "viewer", ShowID: names["other"].Public, EpisodeID: names["ep-001"].Public},
		{Viewer: Viewer{Profile: "viewer", Libraries: []string{"tv", "anime"}}, Library: "anime", Profile: "viewer", EpisodeID: names["ep-001"].Public},
		{Viewer: Viewer{Profile: "viewer", Libraries: []string{"tv", "anime"}}, Library: "tv", Profile: "viewer", ShowID: names["show"].Public, SeasonID: names["other-season"].Public},
		{Viewer: Viewer{Profile: "viewer", Libraries: []string{"tv", "anime"}}, Library: "tv", Profile: "viewer", EpisodeID: names["ep-001"].Public, SelectedSeasonID: names["season2"].Public},
		{Viewer: Viewer{Profile: "viewer", Libraries: []string{"tv", "anime"}}, Library: "tv", Profile: "viewer", ShowID: names["show"].Public, Group: "unassigned_absolute"},
	} {
		if _, e := s.ShowWorkspace(r); !errors.Is(e, ErrShowContext) {
			t.Fatal(r, e)
		}
	}
	r := workspaceRequest(names)
	w, e := s.ShowWorkspace(r)
	if e != nil {
		t.Fatal(e)
	}
	r.Cursor = w.Episodes.Sections[0].NextCursor
	bad := r
	bad.ViewerFence = "another"
	if _, e = s.ShowWorkspace(bad); !errors.Is(e, ErrCursor) {
		t.Fatal(e)
	}
	bad = r
	bad.SelectedSeasonID = names["season2"].Public
	if _, e = s.ShowWorkspace(bad); !errors.Is(e, ErrCursor) {
		t.Fatal(e)
	}
	c := catalogtest.New(t, db)
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		return compactcatalog.SetFactsTx(ctx, tx, names["ep-008"].ID, map[string]any{"show_id": names["show"].ID, "season_id": names["season1"].ID, "numbering": "seasonal", "number": 250})
	})
	c.Drain()
	if _, e = s.ShowWorkspace(r); !errors.Is(e, ErrStaleContinuation) {
		t.Fatal("reordered membership must explicitly stale the loaded revision", e)
	}
	if _, e = s.ShowWorkspace(workspaceRequest(names)); e != nil {
		t.Fatal("fresh revision after reorder", e)
	}
	r = workspaceRequest(names)
	r.EpisodeID = names["ep-185"].Public
	w, e = s.ShowWorkspace(r)
	if e != nil {
		t.Fatal(e)
	}
	r.Cursor = w.Episodes.Sections[0].NextCursor
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		return compactcatalog.UnlinkAssetTx(ctx, tx, names["ep-185"].ID, names["ep-185"].Asset)
	})
	c.Drain()
	if _, e = s.ShowWorkspace(r); !errors.Is(e, ErrStaleContinuation) {
		t.Fatal("removed target not fenced", e)
	}
}
func TestShowWorkspaceAbsoluteAndEmptyAreHonest(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	s, db, names := workspaceFixture(t)
	r := workspaceRequest(names)
	r.Library = "anime"
	r.ShowID = ""
	r.EpisodeID = names["absolute-2"].Public
	w, e := s.ShowWorkspace(r)
	if e != nil || w.Selected.SeasonID != nil || w.Selected.Group != "unassigned_absolute" || len(w.Seasons) != 0 || w.Groups[0].Count != 2 || w.Episodes.Sections[0].Entries[0].ID != names["absolute-2"].Public {
		t.Fatal(w, e)
	}
	c := catalogtest.New(t, db)
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		return compactcatalog.SetAssetAvailableTx(ctx, tx, names["absolute-2"].Asset, false)
	})
	c.Drain()
	w, e = s.ShowWorkspace(r)
	if e != nil || *w.Episodes.Sections[0].Entries[0].Available {
		t.Fatal("unavailable source lost or shown available", e)
	}
	r = workspaceRequest(names)
	r.ShowID = names["empty"].Public
	w, e = s.ShowWorkspace(r)
	if e != nil || len(w.Seasons) != 0 || len(w.Groups) != 0 || len(w.Episodes.Sections) != 0 || w.Episodes.Empty == nil {
		t.Fatal(w, e)
	}
}
func TestShowWorkspaceEpisodeSeekUsesIndexes(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	_, db, names := workspaceFixture(t)
	queries := []struct {
		sql  string
		args []any
	}{
		{`SELECT e.entity_id,e.number FROM catalog_episodes e WHERE e.season_id=? AND e.number>184 AND EXISTS(SELECT 1 FROM catalog_asset_links a WHERE a.entity_id=e.entity_id) ORDER BY e.number LIMIT 8`, []any{names["season1"].ID}},
		{`SELECT e.entity_id,e.number FROM catalog_episodes e WHERE e.show_id=? AND e.season_id IS NULL AND e.numbering='absolute' AND e.number>1 AND EXISTS(SELECT 1 FROM catalog_asset_links a WHERE a.entity_id=e.entity_id) ORDER BY e.number LIMIT 8`, []any{names["absolute"].ID}},
	}
	for _, q := range queries {
		rows, e := db.Query(`EXPLAIN QUERY PLAN `+q.sql, q.args...)
		if e != nil {
			t.Fatal(e)
		}
		plan := ""
		for rows.Next() {
			var a, b, c int
			var d string
			if e = rows.Scan(&a, &b, &c, &d); e != nil {
				t.Fatal(e)
			}
			plan += d + "\n"
		}
		rows.Close()
		t.Log(plan)
		if strings.Contains(plan, "SCAN e") || !strings.Contains(plan, "INDEX catalog_episodes_") {
			t.Fatal("unindexed episode seek", plan)
		}
	}
}

func TestShowWorkspaceConcurrentPublicationDoesNotMixRevisions(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	s, db, names := workspaceFixture(t)
	var seq int
	var name, path string
	if e := db.QueryRow(`PRAGMA database_list`).Scan(&seq, &name, &path); e != nil {
		t.Fatal(e)
	}
	writer, e := persistence.Open(path)
	if e != nil {
		t.Fatal(e)
	}
	defer writer.Close()
	if e = workspaceRename(db, names, "Version A"); e != nil {
		t.Fatal(e)
	}
	c := catalogtest.New(t, db)
	c.Drain()
	var wg sync.WaitGroup
	fail := make(chan error, 1)
	wg.Add(1)
	go func() {
		defer wg.Done()
		for n := 0; n < 30; n++ {
			title := fmt.Sprintf("Version %d", n)
			if e := workspaceRename(writer, names, title); e != nil {
				fail <- e
				return
			}
		}
	}()
	valid, stale := 0, 0
	for n := 0; n < 50; n++ {
		w, e := s.ShowWorkspace(workspaceRequest(names))
		if errors.Is(e, ErrStaleContinuation) || errors.Is(e, ErrVisibilityBuilding) {
			stale++
			continue
		}
		if e != nil {
			t.Fatal(e)
		}
		valid++
		if w.Show.Title != w.Episodes.Sections[0].Entries[0].Title {
			t.Fatal("mixed publication", w.Show.Title, w.Episodes.Sections[0].Entries[0].Title)
		}
	}
	wg.Wait()
	select {
	case e := <-fail:
		t.Fatal(e)
	default:
	}
	c.Drain()
	w, e := s.ShowWorkspace(workspaceRequest(names))
	if e != nil || w.Show.Title != w.Episodes.Sections[0].Entries[0].Title {
		t.Fatal("projected publication mixed title revisions", w.Show.Title, e)
	}
	valid++
	if valid == 0 {
		t.Fatal("no stable read")
	}
	t.Logf("coherent reads=%d stale-rejected=%d", valid, stale)
}
