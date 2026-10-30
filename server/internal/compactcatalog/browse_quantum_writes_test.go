package compactcatalog

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
)

// Count complete SQLite trigger programs around one producer quantum, rather
// than counting only its Go statements or accepted raw rows.
func TestBrowseProducerQuantumPhysicalWrites(t *testing.T) {
	db := cc1OpenDB(t, "producer-quantum.sqlite")
	f := browseCounterWriteSeed(t, db, 64)
	browseCounterDrain(t, db)
	cc1Exec(t, db, `UPDATE catalog_entities SET title='Changed title' WHERE id=?`, f.item)
	cc1Exec(t, db, `UPDATE catalog_assets SET duration=duration+1 WHERE id=?`, f.move)
	for _, domain := range []int{DomainBrowseRows, DomainBrowseEdges, DomainItemMetrics} {
		t.Run(fmt.Sprint(domain), func(t *testing.T) {
			if domain != DomainBrowseRows {
				for _, container := range f.containers {
					cc1Exec(t, db, `INSERT OR IGNORE INTO catalog_browse_memberships(entity_id,item_id,source) VALUES(?,?,1)`, container, f.item)
				}
			}
			cc1Write(t, db, func(ctx context.Context, tx *sql.Tx) error { return TouchTx(ctx, tx, domain, f.item) })
			var revision int64
			if err := db.QueryRow(`SELECT revision FROM catalog_dirty WHERE domain=? AND entity_id=?`, domain, f.item).Scan(&revision); err != nil {
				t.Fatal(err)
			}
			k := Key{ID: f.item, Revision: revision}
			peak, turns := int64(0), 0
			for ; turns < 1000; turns++ {
				done := false
				cc1Write(t, db, func(ctx context.Context, tx *sql.Tx) error {
					var before, after int64
					if err := tx.QueryRowContext(ctx, `SELECT total_changes()`).Scan(&before); err != nil {
						return err
					}
					var err error
					switch domain {
					case DomainBrowseRows:
						done, err = deriveBrowseRowBounded(ctx, tx, domain, k, 8)
					case DomainBrowseEdges:
						done, err = deriveBrowseMembersBounded(ctx, tx, k, 8)
					case DomainItemMetrics:
						done, err = deriveItemMetricsBounded(ctx, tx, k, 8, map[int64]bool{})
					}
					if err != nil {
						return err
					}
					if err = tx.QueryRowContext(ctx, `SELECT total_changes()`).Scan(&after); err != nil {
						return err
					}
					peak = max(peak, after-before)
					return nil
				})
				if done {
					break
				}
			}
			if turns == 1000 {
				t.Fatal("producer failed to finish")
			}
			t.Logf("domain=%d raw quantum=8 turns=%d peak whole-trigger rows=%d", domain, turns+1, peak)
			if peak > 160 {
				t.Fatalf("domain %d quantum changed %d rows, over the unchanged maintenance plan160", domain, peak)
			}
		})
	}
}

func TestVisibilitySourceQuantumPhysicalWrites(t *testing.T) {
	db := cc1OpenDB(t, "visibility-source-physical.sqlite")
	lib := cc1Library(t, db, "source-physical", "Physical", "movie", "/source-physical")
	item := facetTestEntity(t, db, lib, "movie:physical")
	browseCounterDrain(t, db)
	for i := 0; i < 64; i++ {
		cc1Class(t, db, lib, fmt.Sprintf("physical-%d", i))
	}
	visibilityFanoutReset(t, db, 1)
	visibilityFanoutInsertJob(t, db, 1, lib, item, "", 1, 1)
	peak := int64(0)
	for i := 0; i < 20; i++ {
		n := 0
		cc1Write(t, db, func(ctx context.Context, tx *sql.Tx) error {
			var before, after int64
			if err := tx.QueryRow(`SELECT total_changes()`).Scan(&before); err != nil {
				return err
			}
			var err error
			n, err = StepVisibilitySourceFanout(ctx, tx, 8)
			if err != nil {
				return err
			}
			if err = tx.QueryRow(`SELECT total_changes()`).Scan(&after); err != nil {
				return err
			}
			peak = max(peak, after-before)
			return nil
		})
		if n == 0 {
			break
		}
	}
	t.Logf("64 demanded classes raw quantum8: peak whole-trigger rows=%d", peak)
	if peak > 160 {
		t.Fatalf("source quantum writes%d > unchanged160row plan", peak)
	}
	if got := cc1Scalar(t, db, `SELECT count(*) FROM compact_visibility_dirty`); got != 64 {
		t.Fatalf("dirtyclasses%d want64", got)
	}
}
