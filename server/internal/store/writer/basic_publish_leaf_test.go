package writer

import (
	"errors"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
)

func basicPublishLeafTestDefinitions() []stmt.Definition {
	defs := append(basicActivationTestDefs(), basicSettleLeafDefinitions()...)
	return append(defs, basicPublishLeafDefinitions()...)
}
func basicPublishLeafFixture(t *testing.T, defs []stmt.Definition) (*executor, *commandTurn, basicLeafPublication) {
	t.Helper()
	w, a := basicActivationFixture(t)
	a.familyMask = 511
	for _, q := range []string{
		`UPDATE catalog_info_changes SET family_mask=511,has_after=1,after_entity=31,upper_entity=31 WHERE id=51`,
		`UPDATE catalog_assets SET duration=100 WHERE id=41`,
		`UPDATE catalog_asset_links SET info_pending_change=51,part_index=2 WHERE entity_id=31 AND asset_id=41`,
		`UPDATE catalog_entities SET resolution_key=0,file_size_key=42,community_rating_key=7,critic_rating_key=8,scalar_source_revision=17,aggregate_revision=23 WHERE id=31`,
		`INSERT INTO catalog_scalar_contributions(library_id,parent_id,contributor_id,source,contributor_revision,membership_sequence,community_rating,critic_rating) VALUES(7,31,31,0,17,9,7,8)`,
		`INSERT INTO catalog_asset_contributions(library_id,parent_id,asset_id,membership_refs,file_size,file_size_revision,authored_refs) VALUES(7,31,41,1,42,1,0)`,
	} {
		if _, err := w.db.ExecContext(t.Context(), q); err != nil {
			t.Fatal(err)
		}
	}
	registry := nativeInventoryFixtureRegistry(t, w, defs, "basic_publish_leaf.go", "basic_publish_leaf_statements.go", "basic_publish_leaf_test.go")
	turn := basicHandlerTurn(t, w, BasicAccept, defs, registry)
	activated, err := activateBasicAsset(t.Context(), turn, a)
	if err != nil {
		t.Fatal(err)
	}
	a.changeRevision = activated.changeRevision
	settled, err := settleBasicFirstLeaf(t.Context(), turn, basicLeafSettlement{a, 31, 100, 1080})
	if err != nil {
		t.Fatal(err)
	}
	return w, turn, basicLeafPublication{a, 31, 100, settled}
}
func TestBasicPublishLeafClearsBothFencesWithActualEpoch(t *testing.T) {
	w, turn, in := basicPublishLeafFixture(t, basicPublishLeafTestDefinitions())
	got, err := publishBasicFirstLeaf(t.Context(), turn, in)
	if err != nil || got != (basicPublishedLeaf{31, 41, 18, 15}) {
		t.Fatal(got, err)
	}
	var exact bool
	if err = turn.transaction.conn.QueryRowContext(t.Context(), `SELECT
 (SELECT info_epoch=18 AND info_publication_pending=0 AND file_size_revision=1 AND info_invalidation_revision=1 FROM catalog_assets WHERE id=41)
 AND (SELECT info_pending_change IS NULL AND info_shape_id=61 AND info_duration=100 AND part_index=2 FROM catalog_asset_links WHERE entity_id=31 AND asset_id=41)
 AND (SELECT phase=5 AND expected_epoch=17 AND input_revision=7 AND job_revision=15 AND target_shape_id=61 AND has_after=1 AND after_entity=31 FROM catalog_info_changes WHERE id=51)
 AND (SELECT ref_count=2 FROM catalog_info_shapes WHERE id=61)
 AND (SELECT contributor_revision=18 AND membership_sequence=9 FROM catalog_scalar_contributions WHERE parent_id=31 AND contributor_id=31 AND source=0)
 AND (SELECT count(*)=0 FROM scan_command_receipts)`).Scan(&exact); err != nil || !exact {
		t.Fatal(exact, err)
	}
	basicMediaCommit(t, turn)
	var epoch int64
	if err = w.db.QueryRowContext(t.Context(), `SELECT info_epoch FROM catalog_assets WHERE id=41`).Scan(&epoch); err != nil || epoch != got.infoEpoch {
		t.Fatal(epoch, err)
	}
}
func TestBasicPublishLeafLateFailureRollsBackFencesAndSettlement(t *testing.T) {
	for _, fault := range []string{"late_returning", "old_path", "scalar_old"} {
		t.Run(fault, func(t *testing.T) {
			defs := basicPublishLeafTestDefinitions()
			if fault == "late_returning" {
				for n := range defs {
					if defs[n].ID == basicPublishAssetCAS {
						defs[n].SQL = strings.Replace(defs[n].SQL, "RETURNING id,library_id,info_epoch,info_publication_pending", "RETURNING info_epoch,library_id,id,info_publication_pending", 1)
					}
				}
			}
			w, turn, in := basicPublishLeafFixture(t, defs)
			if fault == "old_path" {
				if _, err := turn.transaction.conn.ExecContext(t.Context(), `UPDATE catalog_info_changes SET old_path='/retained/old.mkv' WHERE id=51`); err != nil {
					t.Fatal(err)
				}
			}
			if fault == "scalar_old" {
				if _, err := turn.transaction.conn.ExecContext(t.Context(), `INSERT INTO catalog_info_scalar_old(change_id,field,kind,old_integer) VALUES(51,1,1,1)`); err != nil {
					t.Fatal(err)
				}
			}
			got, err := publishBasicFirstLeaf(t.Context(), turn, in)
			if err == nil || got != (basicPublishedLeaf{}) {
				t.Fatal(got, err)
			}
			if fault == "late_returning" && !errors.Is(err, ErrConflict) {
				t.Fatal(err)
			}
			if fault == "late_returning" {
				var cleared bool
				if e := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT (SELECT info_pending_change IS NULL FROM catalog_asset_links WHERE entity_id=31 AND asset_id=41) AND (SELECT info_publication_pending=0 FROM catalog_assets WHERE id=41)`).Scan(&cleared); e != nil || !cleared {
					t.Fatal("expected preceding physical clears", cleared, e)
				}
			}
			_ = turn.closeRows()
			_, _ = turn.transaction.rollback(err)
			var kept bool
			if e := w.db.QueryRowContext(t.Context(), `SELECT
 (SELECT info_epoch=17 AND info_publication_pending=1 FROM catalog_assets WHERE id=41)
 AND (SELECT info_pending_change=51 AND info_shape_id IS NULL AND info_duration=0 FROM catalog_asset_links WHERE entity_id=31 AND asset_id=41)
 AND (SELECT phase=3 AND job_revision=13 FROM catalog_info_changes WHERE id=51)
 AND (SELECT scalar_source_revision=17 AND aggregate_revision=23 AND duration_key=0 FROM catalog_entities WHERE id=31)
 AND (SELECT ref_count=1 FROM catalog_info_shapes WHERE id=61)`).Scan(&kept); e != nil || !kept {
				t.Fatal(kept, e)
			}
		})
	}
}
func TestBasicPublishLeafDefinitionsStayClosed(t *testing.T) {
	defs := basicPublishLeafDefinitions()
	if _, err := stmt.Describe(defs); err != nil {
		t.Fatal(err)
	}
	for _, d := range defs {
		if issues := plan.LintSQL(d.SQL); len(issues) != 0 {
			t.Fatal(d.ID, issues)
		}
	}
}
