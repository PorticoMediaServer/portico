package catalog

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
)

func TestLibraryOrderUsesPersistedPositionsAcrossOneHundredTwentyOneRows(t *testing.T) {
	c := catalogtest.Open(t)
	ids := []string{}
	for i := 0; i < 121; i++ {
		id := fmt.Sprintf("lib-%03d", i)
		c.Library(id, fmt.Sprintf("Name %03d", 121-i), "movie", "/"+id)
		ids = append([]string{id}, ids...)
	}
	s := New(c.DB)
	ctx := context.Background()
	allow := func(*sql.Tx) error { return nil }
	// Before the first owner reorder, the same name tie-break is used by Browse/Home.
	initial, err := s.AdminLibraries(ctx, AdminRequest{Limit: 40})
	if err != nil || len(initial.Items) != 40 || initial.Items[0].ID != ids[0] {
		t.Fatal(initial, err)
	}
	nextInitial, err := s.AdminLibraries(ctx, AdminRequest{Limit: 40, Cursor: initial.NextCursor})
	if err != nil || len(nextInitial.Items) != 40 || nextInitial.Items[0].ID != ids[40] {
		t.Fatal(nextInitial, err)
	}
	if err := s.OrderLibraries(ctx, ids, allow); err != nil {
		t.Fatal(err)
	}
	var got []string
	cursor := ""
	seen := map[string]bool{}
	for {
		page, err := s.AdminLibraries(ctx, AdminRequest{ServerID: "server", Profile: "profile", ViewerFence: "fence", Cursor: cursor, Limit: 40})
		if err != nil {
			t.Fatal(err)
		}
		for _, row := range page.Items {
			if seen[row.ID] {
				t.Fatal("duplicate page", row.ID)
			}
			seen[row.ID] = true
			got = append(got, row.ID)
		}
		cursor = page.NextCursor
		if cursor == "" {
			break
		}
	}
	if !reflect.DeepEqual(got, ids) {
		t.Fatal("admin order differs", got, ids)
	}
	if err := s.OrderLibraries(ctx, []string{ids[50], "unknown"}, allow); err == nil {
		t.Fatal("unknown library admitted")
	}
	page, err := s.AdminLibraries(ctx, AdminRequest{Limit: 40})
	if err != nil || page.Items[0].ID != ids[0] {
		t.Fatal("rejected reorder mutated order", page, err)
	}
	scoped, err := s.AdminLibraries(ctx, AdminRequest{Limit: 40, LibraryScope: func(context.Context, *sql.Tx) ([]string, error) { return []string{ids[30], ids[90]}, nil }})
	if err != nil || len(scoped.Items) != 2 || scoped.Items[0].ID != ids[30] || scoped.Items[1].ID != ids[90] || scoped.NextCursor != "" {
		t.Fatal("hidden libraries leaked into scoped page", scoped, err)
	}
}

func TestLibrarySortAndEpisodeDefaultAffectActualReaders(t *testing.T) {
	c := catalogtest.Open(t)
	movies := c.Library("movies", "Movies", "movie", "/movies")
	tv := c.Library("tv", "TV", "tv", "/tv")
	item := c.Movie(movies, "/movies/film.mkv", "The Matrix", 1999)
	show := c.Show(tv, "Series", 2020)
	c.Drain()
	prefs := DefaultLibraryPreferences()
	prefs.SortIgnoreArticles = false
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		return ApplyLibraryPreferencesTx(ctx, tx, "movies", prefs)
	})
	var key string
	if err := c.DB.QueryRow(`SELECT sort_key FROM catalog_entities WHERE id=?`, item.ID).Scan(&key); err != nil || key != "the matrix" {
		t.Fatal(key, err)
	}
	c.Fields(item.ID, map[string]any{"title": "The Matrix Returns"})
	if err := c.DB.QueryRow(`SELECT sort_key FROM catalog_entities WHERE id=?`, item.ID).Scan(&key); err != nil || key != "the matrix returns" {
		t.Fatal(key, err)
	}
	prefs = DefaultLibraryPreferences()
	prefs.EpisodeNumbering = "absolute"
	c.Write(func(ctx context.Context, tx *sql.Tx) error { return ApplyLibraryPreferencesTx(ctx, tx, "tv", prefs) })
	settings, err := readShowSettings(c.DB, show.Public)
	if err != nil || !settings.AbsoluteNumbering {
		t.Fatal(settings, err)
	}
	c.Exec(`INSERT INTO catalog_show_settings(entity_id,absolute_numbering,revision) VALUES(?,0,1)`, show.ID)
	settings, err = readShowSettings(c.DB, show.Public)
	if err != nil || settings.AbsoluteNumbering || settings.Revision != 1 {
		t.Fatal("explicit show choice overwritten", settings, err)
	}
}

func TestLibraryCollectionChoicePreservesOwnerCollectionsAndReusesIdentity(t *testing.T) {
	c := catalogtest.Open(t)
	lib := c.Library("auto", "Movies", "movie", "/auto")
	a := c.Movie(lib, "/auto/a.mkv", "First", 2001)
	b := c.Movie(lib, "/auto/b.mkv", "Second", 2002)
	s := New(c.DB)
	for _, item := range []catalogtest.Item{a, b} {
		c.Exec(`INSERT INTO screen_metadata_work(target_kind,target_id,library_id,title_seed) VALUES('item',?,'auto','Film') ON CONFLICT DO NOTHING`, item.ID)
		c.Exec(`INSERT INTO screen_metadata_fields(target_kind,target_id,field,value,provider,source_kind,source_url,confidence,observed_at,language,region,selection_revision) VALUES('item',?,'relations','[{"kind":"collection","target":{"provider":"tmdb","type":"collection","id":"42"},"name":"Series Collection"}]','tmdb','provider','',1,'now','en','US',1)`, item.ID)
	}
	sync := func() {
		t.Helper()
		for i := 0; i < 10; i++ {
			more, err := s.SyncAutomaticCollections(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if !more {
				c.Drain()
				return
			}
		}
		t.Fatal("sync did not settle")
	}
	sync()
	var automatic int64
	if err := c.DB.QueryRow(`SELECT entity_id FROM catalog_automatic_collections WHERE provider_id='42'`).Scan(&automatic); err != nil {
		t.Fatal(err)
	}
	manual, err := s.CreateCollection("auto", "Owner collection")
	if err != nil {
		t.Fatal(err)
	}
	prefs := DefaultLibraryPreferences()
	prefs.GroupCollections = false
	c.Write(func(ctx context.Context, tx *sql.Tx) error { return ApplyLibraryPreferencesTx(ctx, tx, "auto", prefs) })
	sync()
	var retired bool
	if err = c.DB.QueryRow(`SELECT retired FROM catalog_entities WHERE id=?`, automatic).Scan(&retired); err != nil || !retired {
		t.Fatal("automatic series still visible", retired, err)
	}
	if _, err = s.Collection(manual.ID); err != nil {
		t.Fatal("owner collection removed", err)
	}
	prefs.GroupCollections = true
	c.Write(func(ctx context.Context, tx *sql.Tx) error { return ApplyLibraryPreferencesTx(ctx, tx, "auto", prefs) })
	sync()
	var restored int64
	if err = c.DB.QueryRow(`SELECT entity_id FROM catalog_automatic_collections WHERE provider_id='42'`).Scan(&restored); err != nil || restored != automatic {
		t.Fatal("collection identity changed", restored, automatic, err)
	}
	if err = c.DB.QueryRow(`SELECT retired FROM catalog_entities WHERE id=?`, automatic).Scan(&retired); err != nil || retired {
		t.Fatal("automatic series not restored", retired, err)
	}
}

func TestLibraryManagedActionsMatchExistingDVRRestrictions(t *testing.T) {
	c := catalogtest.Open(t)
	c.Library("private", "Recordings", "tv", "/recordings")
	c.Exec(`INSERT INTO dvr_private_libraries(library_id,authority,account_id,profile_id) VALUES('private','local','account','profile')`)
	page, err := New(c.DB).AdminLibraries(context.Background(), AdminRequest{Limit: 40})
	if err != nil || len(page.Items) != 1 || !reflect.DeepEqual(page.Items[0].Actions, []string{"rename"}) {
		t.Fatal(page, err)
	}
}
