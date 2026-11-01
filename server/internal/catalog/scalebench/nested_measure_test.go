package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
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
}
