package writer

import (
	"math"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
)

func TestBasicSettleLeafComponent(t *testing.T) {
	for _, scenario := range []string{"delta", "preserved_max", "late_returning"} {
		t.Run(scenario, func(t *testing.T) {
			w, a := basicActivationFixture(t)
			a.familyMask = 511
			a.fileSizeRevision = math.MaxInt64
			a.invalidationRevision = math.MaxInt64
			for _, q := range []string{
				`UPDATE catalog_info_changes SET family_mask=511,has_after=1,after_entity=31,upper_entity=31 WHERE id=51`,
				`UPDATE catalog_assets SET duration=100,file_size_revision=9223372036854775807,info_invalidation_revision=9223372036854775807,playback_origin_revision=9223372036854775807,prepared_source_revision=9223372036854775807 WHERE id=41`,
				`UPDATE catalog_asset_links SET info_pending_change=51,part_index=2,playback_origin_revision=9223372036854775807 WHERE entity_id=31`,
				`UPDATE catalog_entities SET resolution_key=0,file_size_key=42,community_rating_key=7,critic_rating_key=8,metadata_revision=9223372036854775807,scalar_source_revision=17,aggregate_revision=23 WHERE id=31`,
				`INSERT INTO catalog_membership_clock(singleton,sequence) VALUES(1,9223372036854775807) ON CONFLICT(singleton) DO UPDATE SET sequence=excluded.sequence`,
				`INSERT INTO catalog_scalar_contributions(library_id,parent_id,contributor_id,source,contributor_revision,membership_sequence,community_rating,critic_rating) VALUES(7,31,31,0,17,9,7,8)`,
				`INSERT INTO catalog_asset_contributions(library_id,parent_id,asset_id,membership_refs,file_size,file_size_revision,authored_refs) VALUES(7,31,41,1,42,9223372036854775807,0)`,
			} {
				if _, err := w.db.ExecContext(t.Context(), q); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "preserved_max" {
				for _, q := range []string{
					`INSERT INTO catalog_info_shapes(id,container,video_codec,height,ref_count) VALUES(62,'mkv','h264',2160,1)`,
					`INSERT INTO catalog_assets(id,token,library_id,path,size,modified_ns,container,video_codec,audio_codec,width,height,duration) VALUES(42,'other-basic-asset',7,'/media/Other.mkv',42,-11,'mkv','h264','',3840,2160,200)`,
					`INSERT INTO catalog_asset_links(library_id,entity_id,asset_id,info_shape_id,info_duration) VALUES(7,31,42,62,200)`,
				} {
					if _, err := w.db.ExecContext(t.Context(), q); err != nil {
						t.Fatal(err)
					}
				}
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
						defs[i].SQL = strings.Replace(defs[i].SQL, "RETURNING id,library_id,duration_key,resolution_key,scalar_source_revision,aggregate_revision", "RETURNING id,library_id,duration_key,resolution_key,aggregate_revision,scalar_source_revision", 1)
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
				if err = w.db.QueryRowContext(t.Context(), `SELECT scalar_source_revision FROM catalog_entities WHERE id=31`).Scan(&rev); err != nil || epoch != 17 || rev != 17 {
					t.Fatal(epoch, rev, err)
				}
				var installed int64
				if err = w.db.QueryRowContext(t.Context(), `SELECT (SELECT count(*) FROM catalog_asset_links WHERE entity_id=31 AND (info_shape_id IS NOT NULL OR info_duration<>0))+(SELECT count(*) FROM catalog_scalar_contributions WHERE parent_id=31 AND (duration IS NOT NULL OR resolution IS NOT NULL OR contributor_revision<>17))`).Scan(&installed); err != nil || installed != 0 {
					t.Fatal(installed, err)
				}

				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if scenario == "delta" {
				if got.scalarRevision != 18 || got.aggregateRevision != 24 || got.duration.Float64 != 100 || got.resolution.Int64 != 1080 || got.membershipSequence != 9 {
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
			var shapeRefs int64
			if err = w.db.QueryRowContext(t.Context(), `SELECT ref_count FROM catalog_info_shapes WHERE id=61`).Scan(&shapeRefs); err != nil || shapeRefs != 2 {
				t.Fatal(shapeRefs, err)
			}
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
	for _, d := range basicSettleLeafDefinitions() {
		if issues := plan.LintSQL(d.SQL); len(issues) != 0 {
			t.Fatal(d.ID, issues)
		}
	}
}
