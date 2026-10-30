package catalog

import (
	"context"
	"database/sql"
	"fmt"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

func infoOwnerPinFixture(t *testing.T) (*catalogtest.Catalog, int64, string, string) {
	t.Helper()
	s, db, _, job, object, policy, probe := discPublicationFixture(t)
	var revision, token string
	var asset int64
	if err := db.QueryRow(`SELECT o.revision,o.asset_id,a.id FROM inventory_objects o JOIN catalog_assets a ON a.token=o.asset_id WHERE o.id=?`, object).Scan(&revision, &token, &asset); err != nil {
		t.Fatal(err)
	}
	if receipt, err := acceptDiscProbeForTest(t, s, context.Background(), job, object, revision, policy, probe); err != nil || receipt.State != compactcatalog.InfoInputAcceptedPending {
		t.Fatal("structure source receipt", receipt, err)
	}
	c := catalogtest.New(t, db)
	c.Drain()
	var chosen, accepted string
	if err := db.QueryRow(`SELECT info_owner_object,info_owner_revision FROM catalog_assets WHERE id=?`, asset).Scan(&chosen, &accepted); err != nil || chosen != object || accepted != revision {
		t.Fatal("initial accepted observation", chosen, accepted, err)
	}
	return c, asset, token, object
}

func TestInfoOwnerMissingNeverNeedsLedgerRoomAndPinsOldTree(t *testing.T) {
	c, asset, _, object := infoOwnerPinFixture(t)
	var used int64
	if err := c.DB.QueryRow(`SELECT used_bytes FROM catalog_info_input_budget WHERE singleton=1`).Scan(&used); err != nil {
		t.Fatal(err)
	}
	c.Exec(`UPDATE catalog_info_input_budget SET used_bytes=cap_bytes WHERE singleton=1`)
	// This is genuine per-file evidence, not source offline/online. It must be
	// accepted even when no informational source body can currently be admitted.
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE inventory_objects SET state='missing' WHERE id=?`, object)
		return err
	})
	var state string
	if err := c.DB.QueryRow(`SELECT state FROM inventory_objects WHERE id=?`, object).Scan(&state); err != nil || state != "missing" {
		t.Fatal("missing evidence refused", state, err)
	}
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		ready, err := compactcatalog.PrepareInfoObjectDeleteTx(ctx, tx, object)
		if ready {
			t.Fatal("physical GC passed accepted OLD pin")
		}
		return err
	})
	if _, err := c.DB.Exec(`DELETE FROM inventory_objects WHERE id=?`, object); err == nil {
		t.Fatal("OLD tree's FK pin did not veto physical GC")
	}
	var titles int
	if err := c.DB.QueryRow(`SELECT count(*) FROM inventory_disc_titles WHERE object_id=?`, object).Scan(&titles); err != nil || titles != 2 {
		t.Fatal("OLD disc children lost before release", titles, err)
	}
	worker := compactcatalog.NewWorker(c.DB)
	for i := 0; i < 8; i++ {
		if _, err := worker.Step(context.Background(), 1); err != nil {
			t.Fatal(err)
		}
	}
	var pinned bool
	if err := c.DB.QueryRow(`SELECT info_owner_object=? FROM catalog_assets WHERE id=?`, object, asset).Scan(&pinned); err != nil || !pinned {
		t.Fatal("capacity erased accepted OLD observation", pinned, err)
	}
	c.Exec(`UPDATE catalog_info_input_budget SET used_bytes=? WHERE singleton=1`, used)
	c.Drain()
	if err := c.DB.QueryRow(`SELECT info_owner_object IS NULL AND info_owner_revision IS NULL FROM catalog_assets WHERE id=?`, asset).Scan(&pinned); err != nil || !pinned {
		t.Fatal("new missing basis did not release OLD pin", pinned, err)
	}
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		ready, err := compactcatalog.PrepareInfoObjectDeleteTx(ctx, tx, object)
		if err != nil {
			return err
		}
		if !ready {
			t.Fatal("GC still held after publication release")
		}
		_, err = tx.ExecContext(ctx, `DELETE FROM inventory_objects WHERE id=?`, object)
		return err
	})
	if err := c.DB.QueryRow(`SELECT count(*) FROM inventory_disc_titles WHERE object_id=?`, object).Scan(&titles); err != nil || titles != 0 {
		t.Fatal("released physical FK cleanup failed", titles, err)
	}
}

func TestInfoOwnerTargetChoosesFactsFreeLowestIDBeforeProbe(t *testing.T) {
	c, asset, token, old := infoOwnerPinFixture(t)
	var library int64
	if err := c.DB.QueryRow(`SELECT l.id FROM inventory_objects o JOIN library_sources s ON s.id=o.source_id JOIN catalog_libraries l ON l.library_id=s.library_id WHERE o.id=?`, old).Scan(&library); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 9; i++ {
		item := c.Entity(compactcatalog.Entity{Library: library, Kind: compactcatalog.Movie, Key: fmt.Sprintf("owner:%d", i), Title: "Disc owner"}, nil)
		c.Write(func(ctx context.Context, tx *sql.Tx) error {
			return compactcatalog.LinkAssetTx(ctx, tx, item.ID, asset, compactcatalog.Link{})
		})
	}
	c.Drain()
	target := "!lower-current-object"
	c.Exec(`INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,revision,evidence_json,size,modified_ns,state)
 SELECT ?,source_id,asset_id,root_incarnation,'different-disc','new-revision','{}',size,modified_ns,'available' FROM inventory_objects WHERE id=?`, target, old)
	worker := compactcatalog.NewWorker(c.DB)
	found := false
	for i := 0; i < 1000; i++ {
		var yes bool
		if err := c.DB.QueryRow(`SELECT EXISTS(SELECT 1 FROM catalog_info_changes WHERE asset_id=? AND asset_token=? AND target_owner_object=? AND phase=1)`, asset, token, target).Scan(&yes); err != nil {
			t.Fatal(err)
		}
		if yes {
			found = true
			break
		}
		if _, err := worker.Step(context.Background(), 1); err != nil {
			t.Fatal(err)
		}
	}
	if !found {
		t.Fatal("target observation was never pinned before pre-fence")
	}
	for _, object := range []string{old, target} {
		if _, err := c.DB.Exec(`DELETE FROM inventory_objects WHERE id=?`, object); err == nil {
			t.Fatal("OLD/NEW lifetime pin failed", object)
		}
	}
	c.Drain()
	var chosen, revision string
	if err := c.DB.QueryRow(`SELECT info_owner_object,info_owner_revision FROM catalog_assets WHERE id=?`, asset).Scan(&chosen, &revision); err != nil || chosen != target || revision != "new-revision" {
		t.Fatal("probe-bearing later observation replaced lowest current object", chosen, revision, err)
	}
	var probe bool
	if err := c.DB.QueryRow(`SELECT EXISTS(SELECT 1 FROM inventory_disc_facts d JOIN catalog_assets a ON a.info_owner_object=d.object_id AND a.info_owner_revision=d.source_revision WHERE a.id=?)`, asset).Scan(&probe); err != nil || probe {
		t.Fatal("facts-free chosen observation borrowed another probe", probe, err)
	}
	// Keep the actual worker plan visible for the target's ordinary release path.
	stats := dbwork.Plans()
	if stats.Measured == 0 {
		t.Fatal("no measured writer admissions")
	}
}
