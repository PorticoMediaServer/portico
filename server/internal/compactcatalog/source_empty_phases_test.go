package compactcatalog

import (
	"context"
	"database/sql"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

func TestAssetSourceEmptyPhasesReachOneConsumerInTheSameTurn(t *testing.T) {
	db := cc1OpenDB(t, "source-empty-phases.sqlite")
	lib := cc1Library(t, db, "empty-phases", "Empty phases", "movie", "/empty-phases")
	item := facetTestEntity(t, db, lib, "empty-phases:item")
	asset := sourceTestAsset(t, db, []int64{item}, "/empty-phases/file.mkv")
	cc1Drain(t, db)
	before := cc1Scalar(t, db, `SELECT source_revision FROM metadata_publication_heads WHERE item_id=?`, item)
	cc1Write(t, db, func(ctx context.Context, tx *sql.Tx) error {
		return queueAssetSource(ctx, tx, asset, sourceMetadata)
	})

	// No inventory frontier means there is neither a raw page nor an
	// availability result to publish. One turn reaches exactly one owner.
	ctx, measure := dbwork.Measure(context.Background())
	dbwork.ResetPlans()
	if err := dbwork.WithWriteTx(ctx, db, dbwork.ClassMaintenance, func(tx *sql.Tx) error {
		w, err := readUpstreamSource(ctx, tx, DomainAssetSources, asset)
		if err != nil {
			return err
		}
		n, done, err := w.asset(ctx, tx, 1)
		if err == nil && (n != 1 || done || w.phase != 3 || w.after != item) {
			t.Fatalf("empty phases did not reach one consumer: n=%d done=%t phase=%d after=%d", n, done, w.phase, w.after)
		}
		if err != nil {
			return err
		}
		return w.checkpoint(ctx, tx)
	}); err != nil {
		t.Fatal(err)
	}
	if got := cc1Scalar(t, db, `SELECT source_revision FROM metadata_publication_heads WHERE item_id=?`, item); got != before+1 {
		t.Fatalf("owner revision %d, want %d", got, before+1)
	}
	stats := dbwork.Plans()
	if stats.Overruns != 0 || stats.Largest.Rows > dbwork.DefaultPlan(dbwork.ClassMaintenance).Rows {
		t.Fatalf("empty phases exceeded the existing maintenance plan: %+v", stats)
	}
	t.Logf("empty source prefix: statements=%d rows=%d pages=%d vmops=%d", measure().Statements, stats.Largest.Rows, stats.Largest.Pages, stats.Largest.VMOps)
}

func TestAssetSourceCapturedInventoryPageStillYieldsAfterOneRawRow(t *testing.T) {
	db := cc1OpenDB(t, "source-nonempty-phases.sqlite")
	lib := cc1Library(t, db, "nonempty-phases", "Nonempty phases", "movie", "/nonempty-phases")
	item := facetTestEntity(t, db, lib, "nonempty-phases:item")
	asset := sourceTestAsset(t, db, []int64{item}, "/nonempty-phases/file.mkv")
	cc1Drain(t, db)
	before := cc1Scalar(t, db, `SELECT source_revision FROM metadata_publication_heads WHERE item_id=?`, item)
	token := availabilityToken(t, db, asset)
	cc1Exec(t, db, `INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,revision,evidence_json,size,modified_ns,state)
 SELECT 'raw-1',id,?,incarnation,'one.mkv','r','{}',1,1,'missing' FROM library_sources WHERE id='nonempty-phases'`, token)
	cc1Exec(t, db, `INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,revision,evidence_json,size,modified_ns,state)
 SELECT 'raw-2',id,?,incarnation,'two.mkv','r','{}',1,1,'missing' FROM library_sources WHERE id='nonempty-phases'`, token)
	cc1Write(t, db, func(ctx context.Context, tx *sql.Tx) error {
		w, err := readUpstreamSource(ctx, tx, DomainAssetSources, asset)
		if err != nil {
			return err
		}
		n, done, err := w.asset(ctx, tx, 1)
		if err == nil && (n != 1 || done || w.phase != 1 || w.innerKey != "raw-1" || w.available) {
			t.Fatalf("raw inventory page did not yield: n=%d done=%t phase=%d key=%q available=%t", n, done, w.phase, w.innerKey, w.available)
		}
		if err != nil {
			return err
		}
		return w.checkpoint(ctx, tx)
	})
	var phase int
	var after string
	if err := db.QueryRow(`SELECT phase,inner_key FROM catalog_upstream_sources WHERE domain=41 AND source_id=?`, asset).Scan(&phase, &after); err != nil {
		t.Fatal(err)
	}
	if phase != 1 || after != "raw-1" {
		t.Fatalf("raw page frontier was not durable: phase=%d key=%q", phase, after)
	}
	if got := cc1Scalar(t, db, `SELECT source_revision FROM metadata_publication_heads WHERE item_id=?`, item); got != before {
		t.Fatalf("inventory page published a consumer revision: %d != %d", got, before)
	}
}
