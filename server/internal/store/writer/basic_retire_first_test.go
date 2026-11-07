package writer

import (
	"errors"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

func basicRetireFirstTestDefinitions() []stmt.Definition {
	defs := basicRetireFirstDefinitions()
	for _, d := range basicStreamStageDefinitions() {
		if d.ID == basicStageBudget || d.ID == basicStageBudgetCAS {
			defs = append(defs, d)
		}
	}
	return defs
}

func basicRetireFirstFixture(t *testing.T, setup string, wrongShapeReturn bool) (*executor, *commandTurn, basicRetireFirstEffects, int64) {
	t.Helper()
	w, s := basicStreamFixture(t)
	if _, err := w.db.ExecContext(t.Context(), `INSERT INTO catalog_kinds VALUES(1,'movie',1,0,1,1,0)`); err != nil {
		t.Fatal(err)
	}
	seedBasicFirstOrdinaryOwner(t, w)
	for _, q := range []string{
		`INSERT INTO catalog_info_shapes(id,container,video_codec,height,ref_count) VALUES(61,'mkv','h264',1080,2)`,
		`UPDATE catalog_assets SET info_epoch=18,info_publication_pending=0 WHERE id=41`,
		`UPDATE catalog_asset_links SET info_shape_id=61,info_duration=100 WHERE entity_id=31 AND asset_id=41`,
		`UPDATE catalog_info_changes SET phase=5,job_revision=15,family_mask=511,target_shape_id=61,target_available=1,has_after=1,after_entity=31,upper_entity=31 WHERE id=51`,
		`UPDATE catalog_info_input_families SET rows_staged=1,sealed=1 WHERE change_id=51 AND family=1`,
		`INSERT INTO catalog_info_new_stream_facts VALUES(51,0,'basic-asset',31,42,-11,'probe-fingerprint',1)`,
	} {
		if _, err := w.db.ExecContext(t.Context(), q); err != nil {
			t.Fatal(err)
		}
	}
	for n := 2; n <= 9; n++ {
		if _, err := w.db.ExecContext(t.Context(), `INSERT INTO catalog_info_input_families VALUES(51,?,0,0,0,1)`, n); err != nil {
			t.Fatal(err)
		}
	}
	in := basicRetireFirstEffects{source: s, currentAssetEpoch: 18, changeRevision: 15, shapeID: 61, entityID: 31, counts: [9]int64{1}}
	charge, err := basicRetireFirstCharge(in)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = w.db.ExecContext(t.Context(), `UPDATE catalog_info_input_budget SET used_bytes=? WHERE singleton=1`, 777+charge); err != nil {
		t.Fatal(err)
	}
	if setup != "" {
		if _, err = w.db.ExecContext(t.Context(), setup); err != nil {
			t.Fatal(err)
		}
	}
	defs := basicRetireFirstTestDefinitions()
	if wrongShapeReturn {
		for n := range defs {
			if defs[n].ID == basicRetireShapeRelease {
				defs[n].SQL = strings.Replace(defs[n].SQL, "RETURNING id,ref_count", "RETURNING ref_count,id", 1)
			}
		}
	}
	registry := nativeInventoryFixtureRegistry(t, w, defs, "basic_retire_first.go", "basic_retire_first_statements.go", "basic_retire_first_test.go")
	return w, basicHandlerTurn(t, w, BasicStage, defs, registry), in, charge
}

func TestBasicRetireFirstDeletesOnlyInputAndRefundsActualDebt(t *testing.T) {
	w, turn, in, charge := basicRetireFirstFixture(t, "", false)
	got, err := retireBasicFirstInput(t.Context(), turn, in)
	want := basicRetiredFirstInput{51, 17, 18, 12, charge}
	if err != nil || got != want {
		t.Fatal(got, want, err)
	}
	basicMediaCommit(t, turn)
	var exact bool
	err = w.db.QueryRowContext(t.Context(), `SELECT
 (SELECT count(*)=0 FROM catalog_info_changes WHERE id=51)
 AND (SELECT count(*)=0 FROM catalog_info_input_witnesses WHERE change_id=51)
 AND (SELECT count(*)=0 FROM catalog_info_input_families WHERE change_id=51)
 AND (SELECT count(*)=0 FROM catalog_info_new_stream_facts WHERE change_id=51)
 AND (SELECT ref_count=1 FROM catalog_info_shapes WHERE id=61)
 AND (SELECT used_bytes=777 FROM catalog_info_input_budget WHERE singleton=1)
 AND (SELECT info_epoch=18 AND available=1 AND info_publication_pending=0 FROM catalog_assets WHERE id=41)
 AND (SELECT info_shape_id=61 AND info_duration=100 AND info_pending_change IS NULL FROM catalog_asset_links WHERE entity_id=31 AND asset_id=41)`).Scan(&exact)
	if err != nil || !exact {
		t.Fatal(exact, err)
	}
}

func TestBasicRetireFirstUndrainedPrefixAndEpochRefuse(t *testing.T) {
	for _, state := range []string{"media_prefix", "extra_manifest", "fresh_epoch"} {
		t.Run(state, func(t *testing.T) {
			setup := ""
			if state == "media_prefix" {
				setup = `INSERT INTO catalog_info_new_chapter_facts VALUES(51,0,'basic-asset',1,42,-11,'ok','fingerprint')`
			}
			if state == "extra_manifest" {
				setup = `INSERT INTO catalog_info_input_families VALUES(51,10,0,0,0,1)`
			}
			_, turn, in, _ := basicRetireFirstFixture(t, setup, false)
			if state == "fresh_epoch" {
				in.currentAssetEpoch = 19
			}
			var before, after int64
			if err := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT total_changes()`).Scan(&before); err != nil {
				t.Fatal(err)
			}
			got, err := retireBasicFirstInput(t.Context(), turn, in)
			if err == nil || got != (basicRetiredFirstInput{}) {
				t.Fatal(got, err)
			}
			if err := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT total_changes()`).Scan(&after); err != nil || after != before {
				t.Fatal(before, after, err)
			}
		})
	}
}

func TestBasicRetireFirstLateFailureRestoresHeaderPinAndDebt(t *testing.T) {
	for _, state := range []string{"shape_returning", "witness_changed"} {
		t.Run(state, func(t *testing.T) {
			setup := ""
			if state == "witness_changed" {
				setup = `UPDATE catalog_info_input_witnesses SET object_revision='changed' WHERE change_id=51 AND ordinal=0`
			}
			w, turn, in, charge := basicRetireFirstFixture(t, setup, state == "shape_returning")
			got, err := retireBasicFirstInput(t.Context(), turn, in)
			if !errors.Is(err, ErrConflict) || got != (basicRetiredFirstInput{}) {
				t.Fatal(got, err)
			}
			var families int64
			if err := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT count(*) FROM catalog_info_input_families WHERE change_id=51`).Scan(&families); err != nil || families != 0 {
				t.Fatal("late boundary not reached", families, err)
			}
			_ = turn.closeRows()
			_, _ = turn.transaction.rollback(err)
			var exact bool
			if err := w.db.QueryRowContext(t.Context(), `SELECT (SELECT count(*)=1 FROM catalog_info_changes WHERE id=51) AND (SELECT count(*)=9 FROM catalog_info_input_families WHERE change_id=51) AND (SELECT count(*)=1 FROM catalog_info_input_witnesses WHERE change_id=51) AND (SELECT count(*)=1 FROM catalog_info_new_stream_facts WHERE change_id=51) AND (SELECT ref_count=2 FROM catalog_info_shapes WHERE id=61) AND (SELECT used_bytes=? FROM catalog_info_input_budget WHERE singleton=1)`, 777+charge).Scan(&exact); err != nil || !exact {
				t.Fatal(exact, err)
			}
		})
	}
}
