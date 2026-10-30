package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

func TestBulkReferenceFactsDeriveIntoPlayableMixedLibraries(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	db, err := persistence.Open(filepath.Join(t.TempDir(), "bulk.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	const n = 1000
	if _, _, err = seedBulkFacts(db, n); err != nil {
		t.Fatal(err)
	}
	shape := shapeOf(n)
	for _, tc := range []struct {
		q    string
		want int
	}{{`SELECT count(*) FROM catalog_entities e JOIN catalog_kinds k ON k.id=e.kind WHERE k.playable=1`, n}, {`SELECT count(*) FROM catalog_credits`, n * 6}, {`SELECT count(*) FROM catalog_term_sources`, n * 3}, {`SELECT count(*) FROM progress`, 30}, {`SELECT count(*) FROM catalog_episodes`, shape.episodes}, {`SELECT count(*) FROM catalog_songs`, shape.songs}} {
		var got int
		if err = db.QueryRow(tc.q).Scan(&got); err != nil || got != tc.want {
			t.Fatalf("%s = %d want %d: %v", tc.q, got, tc.want, err)
		}
	}
	rows, err := db.Query(`PRAGMA foreign_key_check`)
	if err != nil {
		t.Fatal(err)
	}
	if rows.Next() {
		t.Fatal("fixture has broken foreign keys")
	}
	rows.Close()
	if err = compactcatalog.Drain(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	service := catalog.New(db)
	v := catalog.Viewer{Profile: "viewer", Fence: "bulk", Libraries: []string{"movies", "tv", "music"}}
	for _, tc := range []struct {
		library, pivot string
		want           int
	}{{"movies", "movies", shape.movies}, {"tv", "episodes", shape.episodes}, {"music", "songs", shape.songs}} {
		page, err := service.BrowseEntities(v, catalog.BrowseRequest{Library: tc.library, Pivot: tc.pivot, Limit: 40})
		if err != nil || page.PageInfo.Total != tc.want || len(page.Entries) == 0 {
			t.Fatalf("%s page=%+v error=%v", tc.library, page.PageInfo, err)
		}
	}
	if _, err := service.HomeRows(catalog.HomeRequest{Viewer: v, Limit: 12}); err != nil {
		t.Fatal(err)
	}
}

func TestCopyReferenceFactsUsesFreshBaselineWithoutAuthentication(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	dir := filepath.Join(t.TempDir(), "_program/scratch/scale-check-2/.scratch/source")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	source, err := persistence.Open(filepath.Join(dir, "catalog.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = seedBulkFacts(source, 100); err != nil {
		t.Fatal(err)
	}
	source.Close()
	dest, err := persistence.Open(filepath.Join(t.TempDir(), "copied.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer dest.Close()
	if _, _, err = copyReferenceFacts(dest, filepath.Join(dir, "catalog.sqlite")); err != nil {
		t.Fatal(err)
	}
	if err = compactcatalog.Drain(context.Background(), dest); err != nil {
		t.Fatal(err)
	}
	var items int
	if err = dest.QueryRow(`SELECT count(*) FROM catalog_entities e JOIN catalog_kinds k ON k.id=e.kind WHERE k.playable=1`).Scan(&items); err != nil || items != 100 {
		t.Fatalf("copied %d items: %v", items, err)
	}
}

func TestNarrowReferenceKeepsMixedShapeAndProductionTriggers(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	db, err := persistence.Open(filepath.Join(t.TempDir(), "narrow.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	const n = 1000
	if _, _, err = seedBulkFactsDensity(db, n, true); err != nil {
		t.Fatal(err)
	}
	shape := shapeOf(n)
	for _, tc := range []struct {
		q    string
		want int
	}{
		{`SELECT count(*) FROM catalog_entities e JOIN catalog_kinds k ON k.id=e.kind WHERE k.playable=1`, n},
		{`SELECT count(*) FROM catalog_term_sources`, n},
		{`SELECT count(*) FROM progress`, 30},
		{`SELECT count(*) FROM catalog_episodes`, shape.episodes},
		{`SELECT count(*) FROM catalog_songs`, shape.songs},
	} {
		var got int
		if err = db.QueryRow(tc.q).Scan(&got); err != nil || got != tc.want {
			t.Fatalf("%s=%d want %d: %v", tc.q, got, tc.want, err)
		}
	}
	var credits, dirty int
	db.QueryRow(`SELECT count(*) FROM catalog_credits`).Scan(&credits)
	db.QueryRow(`SELECT count(*) FROM catalog_dirty`).Scan(&dirty)
	if credits == 0 || credits >= n*6 || dirty == 0 {
		t.Fatalf("credits=%d dirty=%d", credits, dirty)
	}
	if err = compactcatalog.Drain(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	page, err := catalog.New(db).BrowseEntities(catalog.Viewer{Profile: "viewer", Fence: "narrow", Libraries: []string{"music"}}, catalog.BrowseRequest{Library: "music", Pivot: "songs", Limit: 40})
	if err != nil || page.PageInfo.Total != shape.songs {
		t.Fatalf("page=%+v err=%v", page.PageInfo, err)
	}
}
