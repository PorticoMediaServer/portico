package compactcatalog

import (
	"context"
	"database/sql"
	"testing"
)

// An abandoned, untyped phase-0 input is reclaimed by the asset-source worker.
// Its reclaim walk keeps its cursor in copy_family, which also marks a resolved
// owner choice for live inputs. If the owner choice were prepared for it each
// turn, the cursor would be reset to 1 and recovery would advance it to 2 for
// ever: the worker re-derived the same asset every turn, and analysis of the
// file stayed deferred behind it, so its scan never finished.
func TestAbandonedOwnerInputIsReclaimedWithoutRepreparingItsOwner(t *testing.T) {
	db := cc1OpenDB(t, "reclaim-owner.sqlite")
	library := cc1Library(t, db, "films", "Films", "movie", "/films")
	item := cc1Entity(t, db, Entity{Library: library, Kind: Movie, Key: "film", Title: "Film"}, nil)
	var asset int64
	var token string
	cc1Write(t, db, func(ctx context.Context, tx *sql.Tx) error {
		var err error
		if asset, token, err = UpsertAssetTx(ctx, tx, Asset{Path: "/films/film.mkv", Size: 1000, ModifiedNS: 1, Container: "mkv", VideoCodec: "h264", AudioCodec: "aac", Width: 1920, Height: 1080, Duration: 5400}); err != nil {
			return err
		}
		return LinkAssetTx(ctx, tx, item, asset, Link{})
	})
	cc1Drain(t, db)

	// The state an abandoned owner-choice input leaves: phase 0, no witness and
	// no families, reclaim marker set, walk cursor at the start.
	cc1Write(t, db, func(ctx context.Context, tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO catalog_info_changes(asset_id,asset_token,slot,expected_epoch,fingerprint,created_ms)
 SELECT id,token,0,info_epoch,zeroblob(32),0 FROM catalog_assets WHERE id=?`, asset); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE catalog_info_changes SET fault='',copy_aux=-1,input_revision=input_revision+1,copy_family=0 WHERE asset_id=? AND phase=0`, asset); err != nil {
			return err
		}
		return queueSource(ctx, tx, DomainAssetSources, asset, token, sourceInfo)
	})
	cc1Drain(t, db)
	if n := cc1Scalar(t, db, `SELECT count(*) FROM catalog_info_changes WHERE asset_id=?`, asset); n != 0 {
		t.Fatalf("abandoned input still present after the worker drained (%d rows)", n)
	}
	if n := cc1Scalar(t, db, `SELECT count(*) FROM catalog_upstream_sources WHERE domain=? AND source_id=? AND flags&?<>0`, DomainAssetSources, asset, sourceInfo); n != 0 {
		t.Fatal("asset source still owes informational work")
	}
}
