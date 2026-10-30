package compactcatalog

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

func visibleCount(t *testing.T, db *sql.DB, collection int64) int {
	t.Helper()
	var count int
	err := db.QueryRow(`SELECT COALESCE((SELECT total FROM catalog_collection_visible_counts v JOIN compact_visibility_classes cl ON cl.id=v.class_id WHERE cl.class_key='policy' AND v.generation=1 AND v.collection_id=?),0)`, collection).Scan(&count)
	if err != nil {
		t.Fatal(err)
	}
	return count
}

func setCollectionMember(t *testing.T, db *sql.DB, collection, item int64, present bool) {
	t.Helper()
	cc1Write(t, db, func(ctx context.Context, tx *sql.Tx) error {
		return SetCollectionMemberTx(ctx, tx, collection, item, "", present)
	})
}

func TestCollectionVisibleCountReplayRemovalAndRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "visible.sqlite")
	db, err := persistence.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { db.Close() }()
	lib := cc1Library(t, db, "movies", "Movies", "movie", "/movies")
	vis := cc1Entity(t, db, Entity{Library: lib, Kind: Movie, Key: "file:visible.mkv", Title: "Visible"}, nil)
	hidden := cc1Entity(t, db, Entity{Library: lib, Kind: Movie, Key: "file:hidden.mkv", Title: "Hidden"}, nil)
	collection := cc1Entity(t, db, Entity{Library: lib, Kind: Collection, Key: "collection:set", Title: "Set"},
		map[string]any{"library_id": lib, "name_key": "set", "created_at": "2026-01-01T00:00:00Z"})
	setCollectionMember(t, db, collection, vis, true)
	setCollectionMember(t, db, collection, hidden, true)
	cc1Drain(t, db)
	class := cc1Class(t, db, lib, "policy")
	cc1Visible(t, db, class, vis, 1)
	if count := cc1Scalar(t, db, `SELECT count(*) FROM catalog_collection_visible_item_jobs`); count != 1 {
		t.Fatalf("class change did not queue item work: %d", count)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = persistence.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	cc1Drain(t, db)
	if count := visibleCount(t, db, collection); count != 1 {
		t.Fatalf("visible count after restart = %d", count)
	}
	cc1Exec(t, db, `DELETE FROM compact_visibility_rows WHERE entity_id=?`, vis)
	cc1Drain(t, db)
	if count := visibleCount(t, db, collection); count != 0 {
		t.Fatalf("visible count after policy removal = %d", count)
	}
	cc1Visible(t, db, class, vis, 1)
	cc1Drain(t, db)
	if count := visibleCount(t, db, collection); count != 1 {
		t.Fatalf("visible count after policy replay = %d", count)
	}
	setCollectionMember(t, db, collection, vis, false)
	// No baseline trigger feeds the collection pair queue on member edits
	// (unlike movie category members), so queue the affected class/item
	// slice directly; the reconcile itself is what this exercises.
	cc1Exec(t, db, `INSERT INTO catalog_collection_visible_item_jobs(class_id,generation,item_id) VALUES(?,?,?) ON CONFLICT(class_id,generation,item_id) DO NOTHING`, class, 1, vis)
	cc1Drain(t, db)
	if count := visibleCount(t, db, collection); count != 0 {
		t.Fatalf("visible count after member removal = %d", count)
	}
	if edges := cc1Scalar(t, db, `SELECT count(*) FROM catalog_collection_visible_edges`); edges != 0 {
		t.Fatalf("removed visible contribution remains: %d", edges)
	}
}

func TestCollectionVisibleItemFanoutResumesFromCursor(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fanout.sqlite")
	db, err := persistence.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { db.Close() }()
	lib := cc1Library(t, db, "movies", "Movies", "movie", "/movies")
	movie := cc1Entity(t, db, Entity{Library: lib, Kind: Movie, Key: "file:movie.mkv", Title: "Movie"}, nil)
	collections := []int64{}
	for i := 0; i < 12; i++ {
		id := fmt.Sprintf("collection-%02d", i)
		collection := cc1Entity(t, db, Entity{Library: lib, Kind: Collection, Key: "collection:" + id, Title: id},
			map[string]any{"library_id": lib, "name_key": id, "created_at": "2026-01-01T00:00:00Z"})
		setCollectionMember(t, db, collection, movie, true)
		collections = append(collections, collection)
	}
	cc1Drain(t, db)
	class := cc1Class(t, db, lib, "policy")
	cc1Visible(t, db, class, movie, 1)
	var n int
	err = dbwork.WithWriteTx(context.Background(), db, dbwork.ClassMaintenance, func(tx *sql.Tx) error {
		var err error
		n, err = StepCollectionVisibleCounts(context.Background(), tx, 3)
		return err
	})
	if err != nil || n < 1 || n > 3 {
		t.Fatalf("unbounded derived step: n=%d err=%v", n, err)
	}
	if pending := cc1Scalar(t, db, `SELECT count(*) FROM catalog_collection_visible_item_jobs`); pending != 1 {
		t.Fatalf("fanout cursor finished early: %d", pending)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = persistence.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	cc1Drain(t, db)
	if edges := cc1Scalar(t, db, `SELECT count(*) FROM catalog_collection_visible_edges`); edges != 12 {
		t.Fatalf("cursor restart lost contributions: %d", edges)
	}
	for i, collection := range collections {
		if count := visibleCount(t, db, collection); count != 1 {
			t.Fatalf("collection %d visible count = %d", i, count)
		}
	}
}
