package catalog

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

// A published class kept current from its journal must hold exactly what a
// fresh build of the same facts holds: the same rows, counts and unavailable
// marks, and anchors whose ordinals are their rows' true positions. The class
// spans more than two anchor spans so joins and departures shift anchors.
func TestVisibilityJournalMatchesARebuild(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	c := catalogtest.Open(t)
	var field1, field2 string
	if err := c.DB.QueryRow(`SELECT (SELECT field FROM catalog_attribute_fields WHERE id=1),(SELECT field FROM catalog_attribute_fields WHERE id=2)`).Scan(&field1, &field2); err != nil || field1 != "contentRating" || field2 != "label" {
		t.Fatalf("restriction SQL assumes attribute fields 1=contentRating, 2=label: %q %q %v", field1, field2, err)
	}
	films := c.Library("films", "Films", "movie", "/films")
	movies := make([]catalogtest.Item, 0, 600)
	for i := 0; i < 600; i++ {
		m := c.Movie(films, fmt.Sprintf("/films/%03d.mkv", i), fmt.Sprintf("Title %03d", i), 1950+i%70)
		if i%7 == 0 {
			c.Attributes(m.ID, "label", "violence")
		}
		movies = append(movies, m)
	}
	c.Drain()
	s := New(c.DB)
	ctx := context.Background()
	r := identity.ContentRestrictions{BlockedLabels: []string{"violence"}}
	if err := s.RebuildVisibilityClass(ctx, "films", r); err != nil {
		t.Fatal(err)
	}
	class := func() (id, library, generation int64) {
		t.Helper()
		key, _ := visibilityClassKey("films", r)
		if err := c.DB.QueryRow(`SELECT id,library_id,active_generation FROM compact_visibility_classes WHERE class_key=?`, key).Scan(&id, &library, &generation); err != nil {
			t.Fatal(err)
		}
		return
	}
	classID, libraryID, generation := class()
	if n := count(t, c.DB, `SELECT count(*) FROM compact_visibility_rows WHERE class_id=? AND generation=?`, classID, generation); n != 600-86 {
		t.Fatalf("built class: %d rows, want %d", n, 600-86)
	}
	if n := count(t, c.DB, `SELECT count(*) FROM compact_visibility_dirty WHERE class_id=?`, classID); n != 0 {
		t.Fatalf("journal after the build: %d", n)
	}

	// Leave, join, arrive, depart, move and lose a file.
	for i := 1; i <= 40; i += 2 {
		c.Attributes(movies[i].ID, "label", "violence")
	}
	for i := 0; i < 70; i += 7 {
		c.Attributes(movies[i].ID, "label")
	}
	for i := 0; i < 30; i++ {
		c.Movie(films, fmt.Sprintf("/films/new%02d.mkv", i), fmt.Sprintf("Title %03da", i*19), 2001)
	}
	for i := 300; i < 315; i++ {
		c.Delete(movies[i].ID)
	}
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		for i := 500; i < 510; i++ {
			if _, _, err := compactcatalog.UpsertEntityTx(ctx, tx, compactcatalog.Entity{Library: films, Kind: compactcatalog.Movie,
				Key: compactcatalog.ItemKey("/films", fmt.Sprintf("/films/%03d.mkv", i), 0), Title: fmt.Sprintf("Aardvark %03d", i), Year: 1990}); err != nil {
				return err
			}
		}
		for i := 540; i < 545; i++ {
			if err := compactcatalog.SetAssetAvailableTx(ctx, tx, movies[i].Asset, false); err != nil {
				return err
			}
		}
		return nil
	})
	c.Drain()
	if n := count(t, c.DB, `SELECT count(*) FROM compact_visibility_dirty WHERE class_id=?`, classID); n == 0 || n >= visibilityBulkBacklog {
		t.Fatalf("journal after the changes: %d entries, want an incremental backlog", n)
	}
	if err := s.refreshVisibilityClass(ctx, "films", r, classID, libraryID, generation); err != nil {
		t.Fatal(err)
	}
	if n := count(t, c.DB, `SELECT count(*) FROM compact_visibility_dirty WHERE class_id=?`, classID); n != 0 {
		t.Fatalf("journal after the refresh: %d", n)
	}
	if _, _, g := class(); g != generation {
		t.Fatalf("a small backlog rebuilt the class (generation %d → %d)", generation, g)
	}
	checkAnchors(t, c.DB, classID, generation)
	incremental := snapshotClass(t, c.DB, classID, generation)

	if err := s.RebuildVisibilityClass(ctx, "films", r); err != nil {
		t.Fatal(err)
	}
	_, _, rebuilt := class()
	if rebuilt == generation {
		t.Fatal("the rebuild did not publish a new generation")
	}
	checkAnchors(t, c.DB, classID, rebuilt)
	if fresh := snapshotClass(t, c.DB, classID, rebuilt); !reflect.DeepEqual(incremental, fresh) {
		for name := range fresh {
			if !reflect.DeepEqual(incremental[name], fresh[name]) {
				t.Errorf("%s differs:\n incremental %v\n rebuilt     %v", name, incremental[name], fresh[name])
			}
		}
	}
}

// A backlog as large as the class rebuilds it instead of applying entries one
// by one (each would shift every later anchor).
func TestVisibilityJournalBulkBacklogRebuilds(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	c := catalogtest.Open(t)
	films := c.Library("films", "Films", "movie", "/films")
	movies := []catalogtest.Item{}
	for i := 0; i < 100; i++ {
		movies = append(movies, c.Movie(films, fmt.Sprintf("/films/%03d.mkv", i), fmt.Sprintf("Title %03d", i), 2000))
	}
	c.Drain()
	s := New(c.DB)
	ctx := context.Background()
	r := identity.ContentRestrictions{BlockedLabels: []string{"violence"}}
	if err := s.RebuildVisibilityClass(ctx, "films", r); err != nil {
		t.Fatal(err)
	}
	key, _ := visibilityClassKey("films", r)
	var classID, libraryID, generation int64
	if err := c.DB.QueryRow(`SELECT id,library_id,active_generation FROM compact_visibility_classes WHERE class_key=?`, key).Scan(&classID, &libraryID, &generation); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 60; i++ {
		c.Attributes(movies[i].ID, "label", "violence")
	}
	c.Drain()
	if err := s.refreshVisibilityClass(ctx, "films", r, classID, libraryID, generation); err != nil {
		t.Fatal(err)
	}
	var next int64
	if err := c.DB.QueryRow(`SELECT active_generation FROM compact_visibility_classes WHERE id=?`, classID).Scan(&next); err != nil || next == generation {
		t.Fatalf("bulk backlog: generation %d → %d (%v), want a rebuild", generation, next, err)
	}
	if n := count(t, c.DB, `SELECT count(*) FROM compact_visibility_rows WHERE class_id=? AND generation=?`, classID, next); n != 40 {
		t.Fatalf("rebuilt class: %d rows, want 40", n)
	}
}

// A library with no class journals nothing.
func TestVisibilityJournalOnlyForLibrariesWithAClass(t *testing.T) {
	c := catalogtest.Open(t)
	films := c.Library("films", "Films", "movie", "/films")
	for i := 0; i < 20; i++ {
		c.Movie(films, fmt.Sprintf("/films/%03d.mkv", i), fmt.Sprintf("Title %03d", i), 2000)
	}
	c.Drain()
	if n := count(t, c.DB, `SELECT count(*) FROM compact_visibility_dirty`); n != 0 {
		t.Fatalf("journal without a class: %d", n)
	}
}

func count(t *testing.T, db *sql.DB, query string, args ...any) int {
	t.Helper()
	var n int
	if err := db.QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func snapshotClass(t *testing.T, db *sql.DB, classID, generation int64) map[string][]string {
	t.Helper()
	out := map[string][]string{}
	for name, query := range map[string]string{
		"rows":               `SELECT entity_id||'|'||kind||'|'||sort_key||'|'||head||'|'||decade FROM compact_visibility_rows WHERE class_id=? AND generation=? ORDER BY entity_id`,
		"counts":             `SELECT kind||'|'||head||'|'||decade||'|'||total FROM compact_visibility_counts WHERE class_id=? AND generation=? ORDER BY kind,head,decade`,
		"unavailable":        `SELECT entity_id||'|'||kind FROM compact_visibility_unavailable WHERE class_id=? AND generation=? ORDER BY entity_id`,
		"unavailable counts": `SELECT kind||'|'||total FROM compact_visibility_unavailable_counts WHERE class_id=? AND generation=? AND total>0 ORDER BY kind`,
	} {
		rows, err := db.Query(query, classID, generation)
		if err != nil {
			t.Fatal(err)
		}
		values := []string{}
		for rows.Next() {
			var v string
			if err = rows.Scan(&v); err != nil {
				t.Fatal(err)
			}
			values = append(values, v)
		}
		rows.Close()
		out[name] = values
	}
	return out
}

// checkAnchors: the class's counted blocks count its rows exactly, in every
// ordering (the same check covers the browse rows' blocks).
func checkAnchors(t *testing.T, db *sql.DB, classID, generation int64) {
	t.Helper()
	if err := compactcatalog.CheckBrowseBlocks(context.Background(), db); err != nil {
		t.Fatalf("class %d generation %d: %v", classID, generation, err)
	}
}
