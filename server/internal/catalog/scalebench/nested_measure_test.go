package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

// Read-only follow-up over completed growth seeds. Never reseed for a query
// probe, and fail closed when the compatible seed cache is absent.
func TestNestedGrowthMeasurement(t *testing.T) {
	n, _ := strconv.Atoi(os.Getenv("PORTICO_NESTED_ITEMS"))
	if n == 0 {
		t.Skip("opt-in cached-seed page diagnostic")
	}
	dir := os.Getenv("PORTICO_SCALE_REPORT")
	if dir == "" {
		t.Fatal("evidence directory required")
	}
	if e := os.MkdirAll(dir, 0700); e != nil {
		t.Fatal(e)
	}
	seed := filepath.Join(os.Getenv("TMPDIR"), fmt.Sprintf("F-growth-%d.sqlite", n))
	in, e := os.Open(seed)
	if e != nil {
		t.Fatal("completed compatible seed required:", e)
	}
	path := filepath.Join(t.TempDir(), "db")
	out, e := os.Create(path)
	if e != nil {
		t.Fatal(e)
	}
	_, e = io.Copy(out, in)
	in.Close()
	out.Close()
	if e != nil {
		t.Fatal(e)
	}
	db, e := persistence.Open(path)
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	s := catalog.New(db)
	results := []routeResult{}
	measure := func(name string, request catalog.ContentRequest) {
		r, e := exerciseRead(db, s, name, 10, func(ctx context.Context, s *catalog.Service) error {
			p, e := s.Content(request)
			if e == nil && (len(p.Sections) == 0 || len(p.Sections[0].Entries) == 0) {
				return fmt.Errorf("empty nested page")
			}
			return e
		})
		if e != nil {
			t.Fatal(e)
		}
		results = append(results, r)
		raw, _ := json.MarshalIndent(map[string]any{"base_items": n, "routes": results, "seed_source": "a13ca507 bulk seed; application baseline 444a7087", "peak_rss_kb": growthPeakRSS()}, "", "  ")
		if e = os.WriteFile(filepath.Join(dir, "nested.json"), raw, 0600); e != nil {
			t.Fatal(e)
		}
	}
	for _, scope := range []struct {
		lib, view string
		kind      int
	}{{"tv", "show", 2}, {"tv", "season", 3}, {"music", "artist", 5}, {"music", "album", 6}, {"audio", "book", 8}, {"anime", "show", 2}} {
		var pid string
		if e = db.QueryRow(`SELECT pid(public_id) FROM catalog_entities WHERE library_id=(SELECT id FROM catalog_libraries WHERE library_id=?) AND kind=? AND retired=0 ORDER BY id LIMIT 1`, scope.lib, scope.kind).Scan(&pid); e != nil {
			t.Fatal(e)
		}
		v := catalog.Viewer{Profile: "viewer", Fence: "growth", Libraries: []string{scope.lib}}
		request := catalog.ContentRequest{Viewer: v, Library: scope.lib, Profile: v.Profile, ViewerFence: v.Fence, View: scope.view, EntityID: pid, Limit: 40}
		measure(scope.lib+"/page/"+scope.view, request)
	}
	for _, view := range []string{"authors", "series"} {
		v := catalog.Viewer{Profile: "viewer", Fence: "growth", Libraries: []string{"audio"}}
		root := catalog.ContentRequest{Viewer: v, Library: "audio", Profile: v.Profile, ViewerFence: v.Fence, View: view, Limit: 40}
		p, e := s.Content(root)
		if e != nil {
			t.Fatal(e)
		}
		if len(p.Sections) == 0 || len(p.Sections[0].Entries) == 0 {
			t.Fatal("empty maintained audio group", view)
		}
		entry := p.Sections[0].Entries[0]
		if entry.Navigation == nil {
			t.Fatal("group has no navigation")
		}
		root.View = entry.Navigation.View
		root.EntityID = entry.ID
		measure("audio/page/"+root.View, root)
	}
	// Attribute the growing root paths using the completed seed, before the
	// provider-identity control changes this copy. These profiles are diagnostic
	// only; the ten-sample growth matrix remains the source of curve values.
	profileRoutes = true
	for _, library := range []string{"movies", "tv", "music", "audio", "reading"} {
		v := catalog.Viewer{Profile: "viewer", Fence: "growth", Libraries: []string{library}}
		homeRead := func(s *catalog.Service) error {
			_, e := s.HomeRows(catalog.HomeRequest{Viewer: v, ServerID: "s", Profile: v.Profile, ViewerFence: v.Fence, Libraries: v.Libraries, Now: time.Now()})
			return e
		}
		explainRoute(db, "diagnostic/"+library+"/home", homeRead, s)
		if _, e := exerciseRead(db, s, "diagnostic/"+library+"/home", 3, func(ctx context.Context, s *catalog.Service) error { return homeRead(s) }); e != nil {
			t.Fatal(e)
		}
	}
	vReading := catalog.Viewer{Profile: "viewer", Fence: "growth", Libraries: []string{"reading"}}
	for _, route := range []string{"browse", "author"} {
		read := func(s *catalog.Service) error {
			if route == "browse" {
				_, e := s.BrowseEntities(vReading, catalog.BrowseRequest{Library: "reading", Profile: "viewer", ViewerFence: "growth", Pivot: "books", Limit: 40})
				return e
			}
			_, e := s.Facets(vReading, catalog.FacetRequest{Library: "reading", Pivot: "authors", Field: "author", Limit: 40})
			return e
		}
		explainRoute(db, "diagnostic/reading/"+route, read, s)
		if _, e := exerciseRead(db, s, "diagnostic/reading/"+route, 3, func(ctx context.Context, s *catalog.Service) error { return read(s) }); e != nil {
			t.Fatal(e)
		}
	}
	profileRoutes = false
	// The reference catalogue contains no provider identities. Populate only
	// this writable copy to expose edition lookup work hidden by empty tables.
	if e := dbwork.WithWriteTx(context.Background(), db, dbwork.ClassMaintenance, func(tx *sql.Tx) error {
		_, e := tx.Exec(`INSERT INTO metadata_details(item_id,provider,provider_id,source_url,observed_at) SELECT id,CASE kind WHEN 1 THEN 'tmdb' ELSE 'tvdb' END,CAST(id AS TEXT),'','2026-01-01' FROM catalog_entities WHERE kind IN(1,2,4) ON CONFLICT(item_id,provider) DO NOTHING`)
		return e
	}); e != nil {
		t.Fatal(e)
	}
	if e := persistence.RefreshPlannerStatistics(context.Background(), db); e != nil {
		t.Fatal(e)
	}
	v := catalog.Viewer{Profile: "viewer", Fence: "growth", Libraries: []string{"movies"}}
	home := func(s *catalog.Service) error {
		_, e := s.HomeRows(catalog.HomeRequest{Viewer: v, ServerID: "s", Profile: v.Profile, ViewerFence: v.Fence, Libraries: v.Libraries, Now: time.Now()})
		return e
	}
	explainRoute(db, "provider_ids/home", home, s)
	profileRoutes = true
	defer func() { profileRoutes = false }()
	r, e := exerciseRead(db, s, "provider_ids/home", 10, func(ctx context.Context, s *catalog.Service) error { return home(s) })
	if e != nil {
		t.Fatal(e)
	}
	results = append(results, r)
	// Isolate the exact edition-expansion statement used by homeRecentWalker.
	// A fixed batch must not become more expensive with unrelated identities.
	rows, e := db.Query(`SELECT id FROM catalog_entities WHERE kind=1 ORDER BY id LIMIT 64`)
	if e != nil {
		t.Fatal(e)
	}
	var batch []int64
	for rows.Next() {
		var id int64
		if e := rows.Scan(&id); e != nil {
			t.Fatal(e)
		}
		batch = append(batch, id)
	}
	rows.Close()
	batchJSON, _ := json.Marshal(batch)
	const editionSQL = `SELECT DISTINCT d2.item_id FROM metadata_details d1 CROSS JOIN metadata_details d2 INDEXED BY metadata_details_provider ON d2.provider=d1.provider AND d2.provider_id=d1.provider_id WHERE d1.item_id IN(SELECT value FROM json_each(?)) AND d1.provider IN('tmdb','tvdb','anilist') AND d1.provider_id<>''`
	r, e = exerciseRead(db, s, "provider_ids/edition_batch", 10, func(ctx context.Context, _ *catalog.Service) error {
		rows, e := db.QueryContext(ctx, editionSQL, string(batchJSON))
		if e != nil {
			return e
		}
		defer rows.Close()
		found := 0
		for rows.Next() {
			found++
		}
		if e = rows.Err(); e == nil && found != len(batch) {
			return fmt.Errorf("edition expansion returned %d, expected %d", found, len(batch))
		}
		return e
	})
	if e != nil {
		t.Fatal(e)
	}
	results = append(results, r)
	var providerRows int
	if e := db.QueryRow(`SELECT count(*) FROM metadata_details`).Scan(&providerRows); e != nil {
		t.Fatal(e)
	}
	raw, _ := json.MarshalIndent(map[string]any{"base_items": n, "routes": results, "provider_rows": providerRows, "seed_source": "a13ca507 bulk seed; application baseline 444a7087", "peak_rss_kb": growthPeakRSS()}, "", "  ")
	if e := os.WriteFile(filepath.Join(dir, "nested.json"), raw, 0600); e != nil {
		t.Fatal(e)
	}
}
