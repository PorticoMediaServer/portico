package compactcatalog

import (
	"context"
	"database/sql"
	"fmt"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

// The drain is a modeled three-quantum walk, using the real domain33 durable
// revision/epoch reader and checkpoint. No source mutation or timer is involved.
func TestRebuildEnumerationPreservesContinuation(t *testing.T) {
	for _, scenario := range []string{"unchanged", "touch", "absent"} {
		t.Run(scenario, func(t *testing.T) {
			db := cc1OpenDB(t, "continuation.sqlite")
			const domain = DomainFacetCounts
			original, _ := derivation(domain)
			Register(domain, Derivation{Version: 1,
				Backfill: func(_ context.Context, _ *sql.Tx, after int64, _ int) ([]int64, error) {
					if after < 1 {
						return []int64{1}, nil
					}
					return nil, nil
				},
				Drain: func(ctx context.Context, tx *sql.Tx, keys []Key, _ int) ([]int64, error) {
					var epoch int64
					if err := tx.QueryRowContext(ctx, `SELECT source_epoch FROM catalog_facet_source_state WHERE singleton=1`).Scan(&epoch); err != nil {
						return nil, err
					}
					for _, key := range keys {
						work, err := readFacetWork(ctx, tx, key, epoch)
						if err != nil {
							return nil, err
						}
						work.afterID++ // modeled raw progress; not a real facet-family cursor
						if err := work.checkpoint(ctx, tx); err != nil {
							return nil, err
						}
						if work.afterID == 3 {
							return []int64{key.ID}, nil
						}
					}
					return nil, nil
				},
			})
			t.Cleanup(func() { Register(domain, original) })
			cc1Exec(t, db, `INSERT INTO catalog_derivations(domain,version,rebuilding,first_build) VALUES(?,1,1,1)`, domain)
			epoch := cc1Scalar(t, db, `SELECT source_epoch FROM catalog_facet_source_state WHERE singleton=1`)
			if scenario != "absent" {
				cc1Exec(t, db, `INSERT INTO catalog_dirty(domain,entity_id,revision) VALUES(?,1,7)`, domain)
			}
			worker := NewWorker(db)
			step := func(rebuilding int) {
				t.Helper()
				cc1Write(t, db, func(ctx context.Context, tx *sql.Tx) error {
					_, _, _, err := worker.drainDomain(ctx, tx, domain, rebuilding, 1, false)
					return err
				})
			}
			check := func(revision, progress, cursor int) {
				t.Helper()
				gotRevision := cc1Scalar(t, db, `SELECT revision FROM catalog_dirty WHERE domain=? AND entity_id=1`, domain)
				gotProgress := cc1Scalar(t, db, `SELECT after_id FROM catalog_facet_work WHERE entity_id=1`)
				gotCursor := cc1Scalar(t, db, `SELECT rebuild_cursor FROM catalog_derivations WHERE domain=?`, domain)
				gotEpoch := cc1Scalar(t, db, `SELECT source_epoch FROM catalog_facet_source_state WHERE singleton=1`)
				t.Logf("revision=%d progress=%d rebuild_cursor=%d source_epoch=%d", gotRevision, gotProgress, gotCursor, gotEpoch)
				if gotRevision != revision || gotProgress != progress || gotCursor != cursor || gotEpoch != epoch {
					t.Errorf("revision/progress/cursor/epoch=(%d,%d,%d,%d), want (%d,%d,%d,%d)", gotRevision, gotProgress, gotCursor, gotEpoch, revision, progress, cursor, epoch)
				}
				if stageRevision := cc1Scalar(t, db, `SELECT revision FROM catalog_facet_work WHERE entity_id=1`); stageRevision != gotRevision {
					t.Errorf("stage revision %d != dirty revision %d", stageRevision, gotRevision)
				}
			}
			step(1)
			if scenario == "absent" {
				check(1, 1, 1)
				return
			}
			check(7, 1, 0) // unfinished head cannot acknowledge the rebuild prefix
			if scenario == "touch" {
				cc1Write(t, db, func(ctx context.Context, tx *sql.Tx) error { return TouchTx(ctx, tx, domain, 1) })
				step(0) // isolate the producer touch from rebuild enumeration
				check(8, 1, 0)
				return
			}
			step(1) // same source facts, backfill revisits the unfinished head
			check(7, 2, 1)
		})
	}
}

// Exercise the installed production derivation, including its real raw walker.
func TestRegisteredFacetRebuildContinuation(t *testing.T) {
	if testing.Short() {
		t.Skip("multi-quantum fixture cohort measured above two seconds; merge-tier coverage")
	}
	for _, scenario := range []string{"unchanged", "touch", "absent", "explicit", "upgrade"} {
		t.Run(scenario, func(t *testing.T) {
			db := cc1OpenDB(t, "registered.sqlite")
			lib := cc1Library(t, db, "continuation", "Continuation", "movie", "/continuation")
			id := facetTestEntity(t, db, lib, "continuation:movie")
			cc1Drain(t, db)
			values := make([]string, 32)
			for i := range values {
				values[i] = fmt.Sprintf("studio-%02d", i)
			}
			cc1Write(t, db, func(ctx context.Context, tx *sql.Tx) error { return SetAttributesTx(ctx, tx, id, "studio", values) })
			w := NewWorker(db)
			step := func(rebuilding int) {
				t.Helper()
				cc1Write(t, db, func(ctx context.Context, tx *sql.Tx) error {
					_, _, _, err := w.drainDomain(ctx, tx, DomainFacetCounts, rebuilding, 8, false)
					return err
				})
			}
			// Two real eight-row raw quanta leave at least two further raw quanta.
			step(0)
			first := cc1Scalar(t, db, `SELECT after_id FROM catalog_facet_work WHERE entity_id=?`, id)
			step(0)
			second := cc1Scalar(t, db, `SELECT after_id FROM catalog_facet_work WHERE entity_id=?`, id)
			if first <= 0 || second <= first {
				t.Fatalf("real raw progress %d -> %d", first, second)
			}
			revision := cc1Scalar(t, db, `SELECT revision FROM catalog_dirty WHERE domain=33 AND entity_id=?`, id)
			epoch := cc1Scalar(t, db, `SELECT source_epoch FROM catalog_facet_source_state`)
			cc1Exec(t, db, `UPDATE catalog_derivations SET rebuilding=1,rebuild_cursor=0 WHERE domain=33`)
			switch scenario {
			case "touch":
				cc1Write(t, db, func(ctx context.Context, tx *sql.Tx) error { return TouchTx(ctx, tx, 33, id) })
			case "absent":
				cc1Exec(t, db, `DELETE FROM catalog_dirty WHERE domain=33 AND entity_id=?`, id)
			case "explicit":
				if err := w.Rebuild(context.Background(), 33); err != nil {
					t.Fatal(err)
				}
			case "upgrade":
				cc1Exec(t, db, `UPDATE catalog_derivations SET version=version-1 WHERE domain=33`)
				if err := w.prepare(context.Background()); err != nil {
					t.Fatal(err)
				}
			}
			stagedBefore := cc1Scalar(t, db, `SELECT count(*) FROM catalog_facet_stage WHERE entity_id=?`, id)
			// Force the next worker turn through actual rebuild enumeration.
			w.backfillTurn[33] = true
			dbwork.ResetPlans()
			step(1)
			stats := dbwork.Plans()
			plan := dbwork.DefaultPlan(dbwork.ClassMaintenance)
			if stats.Measured == 0 || stats.Overruns != 0 || stats.Largest.Rows > plan.Rows || stats.Largest.Pages > plan.Pages || stats.Largest.VMOps > plan.VMOps {
				t.Fatalf("enumeration escaped unchanged maintenance plan: %+v", stats)
			}
			t.Logf("enumeration rows/pages/VM=%d/%d/%d", stats.Largest.Rows, stats.Largest.Pages, stats.Largest.VMOps)
			got := cc1Scalar(t, db, `SELECT after_id FROM catalog_facet_work WHERE entity_id=?`, id)
			gotRevision := cc1Scalar(t, db, `SELECT revision FROM catalog_dirty WHERE domain=33 AND entity_id=?`, id)
			gotEpoch := cc1Scalar(t, db, `SELECT source_epoch FROM catalog_facet_source_state`)
			wantRevision := revision
			if scenario == "touch" {
				wantRevision++
			}
			if scenario == "absent" {
				wantRevision = 1
			}
			if gotRevision != wantRevision {
				t.Fatalf("revision %d want %d", gotRevision, wantRevision)
			}
			if scenario == "unchanged" {
				if got <= second || gotEpoch != epoch {
					t.Fatalf("continuation %d -> %d, epoch %d -> %d", second, got, epoch, gotEpoch)
				}
			} else {
				// Reset first reclaims the old stage under the same raw-eight budget.
				phase := cc1Scalar(t, db, `SELECT phase FROM catalog_facet_work WHERE entity_id=?`, id)
				staged := cc1Scalar(t, db, `SELECT count(*) FROM catalog_facet_stage WHERE entity_id=?`, id)
				if stagedBefore == 0 || got != 0 || phase != facetClear || staged != max(0, stagedBefore-8) {
					t.Fatalf("reset must clear old stage before raw progress: cursor=%d phase=%d staged=%d before=%d", got, phase, staged, stagedBefore)
				}
				for turn := 0; staged > 0 && turn < (stagedBefore+7)/8; turn++ {
					before := staged
					step(0)
					staged = cc1Scalar(t, db, `SELECT count(*) FROM catalog_facet_stage WHERE entity_id=?`, id)
					cursor := cc1Scalar(t, db, `SELECT after_id FROM catalog_facet_work WHERE entity_id=?`, id)
					if staged != max(0, before-8) || cursor != 0 {
						t.Fatalf("bounded old-stage clear: staged %d -> %d cursor=%d", before, staged, cursor)
					}
				}
				if staged != 0 {
					t.Fatal("old stage was not fully reclaimed")
				}
				// The empty stage was observed before any new raw cursor can count.
				step(0)
				fresh := cc1Scalar(t, db, `SELECT after_id FROM catalog_facet_work WHERE entity_id=?`, id)
				if fresh != first {
					t.Fatalf("fresh raw progress %d want %d after complete old-stage clear", fresh, first)
				}
			}
			if scenario == "explicit" || scenario == "upgrade" {
				if gotEpoch != epoch+1 {
					t.Fatalf("epoch %d want %d", gotEpoch, epoch+1)
				}
			}
			t.Logf("raw quantum=8 cursor=%d -> %d -> %d revision=%d -> %d epoch=%d -> %d", first, second, got, revision, gotRevision, epoch, gotEpoch)
			cc1Drain(t, db)
			if n := cc1Scalar(t, db, `SELECT count(*) FROM catalog_facet_members WHERE entity_id=? AND field='studio'`, id); n != 32 {
				t.Fatalf("accepted studios %d", n)
			}
			for _, table := range []string{"catalog_dirty", "catalog_derivation_failures", "catalog_facet_work", "catalog_facet_stage"} {
				if n := cc1Scalar(t, db, `SELECT count(*) FROM `+table); n != 0 {
					t.Errorf("%s leftovers %d", table, n)
				}
			}
			if err := CheckFacetCounts(context.Background(), db); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestExplicitFacetRebuildSharedSourceContracts(t *testing.T) {
	db := cc1OpenDB(t, "shared-restart.sqlite")
	lib := cc1Library(t, db, "shared", "Shared", "movie", "/shared")
	id := facetTestEntity(t, db, lib, "shared:movie")
	cc1Drain(t, db)
	cc1Exec(t, db, `WITH RECURSIVE n(i) AS(SELECT 1 UNION ALL SELECT i+1 FROM n WHERE i<24)
 INSERT INTO catalog_assets(token,path,size,modified_ns,container,video_codec,audio_codec,width,height,duration,available)
 SELECT printf('shared-%d',i),printf('/shared/%d.mkv',i),1,1,'mkv','h264','aac',1,1,60,0 FROM n`)
	cc1Exec(t, db, `INSERT INTO catalog_asset_links(entity_id,asset_id,part_index,available) SELECT ?,id,id,0 FROM catalog_assets WHERE token LIKE 'shared-%'`, id)
	// Finish upstream producers before isolating two real interrupted holders.
	cc1Drain(t, db)
	cc1Write(t, db, func(ctx context.Context, tx *sql.Tx) error {
		if err := TouchTx(ctx, tx, 20, id); err != nil {
			return err
		}
		return queueMissingSource(ctx, tx, id)
	})
	availabilityTurn(t, db, 1)
	facetTestTurn(t, db, 44, 1)
	oldEpoch := cc1Scalar(t, db, `SELECT source_epoch FROM catalog_facet_source_state`)
	oldAvailabilityPhase := cc1Scalar(t, db, `SELECT phase FROM catalog_availability_work`)
	oldAvailabilityAsset := cc1Scalar(t, db, `SELECT COALESCE(asset_id,0) FROM catalog_availability_work`)
	if n := cc1Scalar(t, db, `SELECT count(*) FROM catalog_availability_work`); n != 1 {
		t.Fatalf("availability not interrupted: %d", n)
	}
	if n := cc1Scalar(t, db, `SELECT count(*) FROM catalog_upstream_sources WHERE domain=44`); n != 1 {
		t.Fatalf("missing not interrupted: %d", n)
	}
	if err := NewWorker(db).Rebuild(context.Background(), 33); err != nil {
		t.Fatal(err)
	}
	availabilityTurn(t, db, 1)
	facetTestTurn(t, db, 44, 1)
	newEpoch := cc1Scalar(t, db, `SELECT source_epoch FROM catalog_facet_source_state`)
	if newEpoch != oldEpoch+1 {
		t.Errorf("explicit epoch %d -> %d", oldEpoch, newEpoch)
	}
	if got := cc1Scalar(t, db, `SELECT source_epoch FROM catalog_availability_work`); got != oldEpoch {
		t.Errorf("availability must retain captured epoch: %d want %d", got, oldEpoch)
	}
	phase := cc1Scalar(t, db, `SELECT phase FROM catalog_availability_work`)
	asset := cc1Scalar(t, db, `SELECT COALESCE(asset_id,0) FROM catalog_availability_work`)
	if asset != oldAvailabilityAsset || phase <= oldAvailabilityPhase {
		t.Errorf("availability must continue existing raw pass: asset %d -> %d phase %d -> %d", oldAvailabilityAsset, asset, oldAvailabilityPhase, phase)
	}
	if got := cc1Scalar(t, db, `SELECT source_epoch FROM catalog_upstream_sources WHERE domain=44`); got != newEpoch {
		t.Errorf("missing-source did not restart on new epoch: %d want %d", got, newEpoch)
	}
	cc1Drain(t, db)
	availabilityParity(t, db)
	for _, table := range []string{"catalog_dirty", "catalog_derivation_failures", "catalog_availability_work", "catalog_upstream_sources", "catalog_facet_work", "catalog_facet_stage"} {
		if n := cc1Scalar(t, db, `SELECT count(*) FROM `+table); n != 0 {
			t.Errorf("%s leftovers %d", table, n)
		}
	}
}
