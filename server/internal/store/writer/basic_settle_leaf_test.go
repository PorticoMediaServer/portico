package writer

import (
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"math"
	"testing"
)

func TestBasicSettleLeafComponent(t *testing.T) {
	for _, scenario := range []string{"delta", "preserved_max", "late_returning"} {
		t.Run(scenario, func(t *testing.T) {
			w, a := basicActivationFixture(t)
			a.familyMask = 511
			a.fileSizeRevision = math.MaxInt64
			a.invalidationRevision = math.MaxInt64
			for _, q := range []string{
				`UPDATE catalog_info_changes SET family_mask=511 WHERE id=51`,
				`UPDATE catalog_assets SET duration=100,file_size_revision=9223372036854775807,info_invalidation_revision=9223372036854775807,playback_origin_revision=9223372036854775807,prepared_source_revision=9223372036854775807 WHERE id=41`,
				`UPDATE catalog_asset_links SET info_pending_change=51,part_index=2,playback_origin_revision=9223372036854775807 WHERE entity_id=31`,
				`UPDATE catalog_entities SET resolution_key=0,community_rating_key=7,critic_rating_key=8,metadata_revision=9223372036854775807 WHERE id=31`,
				`INSERT INTO catalog_membership_clock(singleton,sequence) VALUES(1,9223372036854775807) ON CONFLICT(singleton) DO UPDATE SET sequence=excluded.sequence`,
				`INSERT INTO catalog_scalar_contributions(library_id,parent_id,contributor_id,source,contributor_revision,membership_sequence) VALUES(7,31,31,0,1,9)`,
				`INSERT INTO catalog_asset_contributions(library_id,parent_id,asset_id,membership_refs,file_size,file_size_revision,authored_refs) VALUES(7,31,41,1,42,9223372036854775807,0)`,
			} {
				if _, err := w.db.ExecContext(t.Context(), q); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "preserved_max" {
				for _, q := range []string{`UPDATE catalog_scalar_contributions SET duration=200,resolution=2160,contributor_revision=9223372036854775807,membership_sequence=9223372036854775807`, `UPDATE catalog_entities SET duration_key=200,resolution_key=2160,scalar_source_revision=9223372036854775807,aggregate_revision=9223372036854775807,revision=9223372036854775807,playback_origin_revision=9223372036854775807 WHERE id=31`} {
					if _, err := w.db.ExecContext(t.Context(), q); err != nil {
						t.Fatal(err)
					}
				}
			}
			defs := append(basicActivationTestDefs(), basicSettleLeafDefinitions()...)
			if scenario == "late_returning" {
				for i := range defs {
					if defs[i].ID == basicSettleEntityCAS {
						defs[i].SQL = defs[i].SQL[:len(defs[i].SQL)-len("aggregate_revision")] + "aggregate_revision+1"
					}
				}
			}
			registry := nativeInventoryFixtureRegistry(t, w, defs, "basic_settle_leaf_test.go")
			turn := basicHandlerTurn(t, w, BasicAccept, defs, registry)
			activated, err := activateBasicAsset(t.Context(), turn, a)
			if err != nil {
				t.Fatal(err)
			}
			a.changeRevision = activated.changeRevision
			got, err := settleBasicFirstLeaf(t.Context(), turn, basicLeafSettlement{a, 31, 100, 1080})
			if scenario == "late_returning" {
				if err == nil {
					t.Fatal("late RETURNING mismatch accepted")
				}
				_ = turn.closeRows()
				_, _ = turn.transaction.rollback(err)
				var epoch, rev int64
				if err = w.db.QueryRowContext(t.Context(), `SELECT info_epoch FROM catalog_assets WHERE id=41`).Scan(&epoch); err != nil {
					t.Fatal(err)
				}
				if err = w.db.QueryRowContext(t.Context(), `SELECT scalar_source_revision FROM catalog_entities WHERE id=31`).Scan(&rev); err != nil || epoch != 17 || rev != 1 {
					t.Fatal(epoch, rev, err)
				}
				var installed int64
				if err = w.db.QueryRowContext(t.Context(), `SELECT (SELECT count(*) FROM catalog_asset_links WHERE entity_id=31 AND (info_shape_id IS NOT NULL OR info_duration<>0))+(SELECT count(*) FROM catalog_scalar_contributions WHERE parent_id=31 AND (duration IS NOT NULL OR resolution IS NOT NULL OR contributor_revision<>1))`).Scan(&installed); err != nil || installed != 0 {
					t.Fatal(installed, err)
				}

				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if scenario == "delta" {
				if got.scalarRevision != 2 || got.aggregateRevision != 2 || got.duration.Float64 != 100 || got.resolution.Int64 != 1080 || got.membershipSequence != 9 {
					t.Fatal(got)
				}
			} else {
				if got.scalarRevision != math.MaxInt64 || got.aggregateRevision != math.MaxInt64 || got.scalarChanged || got.aggregateChanged {
					t.Fatal(got)
				}
			}
			basicMediaCommit(t, turn)
			var sizeRev, inv, member, metadata int64
			var rating, critic float64
			if err = w.db.QueryRowContext(t.Context(), `SELECT a.file_size_revision,a.info_invalidation_revision,c.sequence,e.metadata_revision,e.community_rating_key,e.critic_rating_key FROM catalog_assets a,catalog_membership_clock c,catalog_entities e WHERE a.id=41 AND e.id=31 AND c.singleton=1`).Scan(&sizeRev, &inv, &member, &metadata, &rating, &critic); err != nil || sizeRev != math.MaxInt64 || inv != math.MaxInt64 || member != math.MaxInt64 || metadata != math.MaxInt64 || rating != 7 || critic != 8 {
				t.Fatal(sizeRev, inv, member, metadata, rating, critic, err)
			}
			var pending, phase, epoch, link, origin int64
			if err = w.db.QueryRowContext(t.Context(), `SELECT a.info_publication_pending,j.phase,a.info_epoch,l.info_pending_change,l.playback_origin_revision FROM catalog_assets a JOIN catalog_info_changes j ON j.asset_id=a.id JOIN catalog_asset_links l ON l.asset_id=a.id WHERE a.id=41`).Scan(&pending, &phase, &epoch, &link, &origin); err != nil || pending != 1 || phase != 4 || epoch != 18 || link != 51 || origin != math.MaxInt64 {
				t.Fatal(pending, phase, epoch, link, origin, err)
			}
		})
	}
}

// Catalogue validation is pure; component Capture is intentionally not run here.
func TestBasicSettleLeafDefinitions(t *testing.T) {
	if _, err := stmt.Describe(basicSettleLeafDefinitions()); err != nil {
		t.Fatal(err)
	}
}
