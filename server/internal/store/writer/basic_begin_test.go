package writer

import (
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

func basicBeginFixture(t *testing.T) (*executor, basicBeginEffects) {
	t.Helper()
	w, source := basicStreamFixture(t)
	// Remove the previous component's staged input in owned child-first order.
	// The actual asset and logical debt ledger remain deliberately distinct.
	for _, q := range []string{`DELETE FROM catalog_info_input_families`, `DELETE FROM catalog_info_input_witnesses`, `DELETE FROM catalog_info_changes`} {
		if _, err := w.db.ExecContext(t.Context(), q); err != nil {
			t.Fatal(err)
		}
	}
	seedBasicFirstOrdinaryOwner(t, w)
	source.changeID, source.inputRevision = 0, 0
	return w, basicBeginEffects{source: source, shape: ScanShape{LibraryID: 7, OrdinaryOwners: 1, Streams: []Stream{{Index: 0, Class: VideoStream}}}, counts: [9]int64{1, 1, 1, 1, 0, 1, 0, 0, 1}}
}
func basicBeginTestDefinitions() []stmt.Definition {
	return append(basicStreamStageDefinitions(), basicBeginDefinitions()...)
}
func basicBeginAbsent(t *testing.T, w *executor) {
	t.Helper()
	var intact bool
	err := w.db.QueryRowContext(t.Context(), `SELECT (SELECT count(*)=0 FROM catalog_info_changes) AND (SELECT count(*)=0 FROM catalog_info_input_witnesses) AND (SELECT count(*)=0 FROM catalog_info_input_families) AND (SELECT used_bytes=1000 FROM catalog_info_input_budget) AND (SELECT info_epoch=17 AND info_publication_pending=0 FROM catalog_assets WHERE id=41)`).Scan(&intact)
	if err != nil || !intact {
		t.Fatal("partial Basic begin survived", intact, err)
	}
}
func TestBasicBeginNativeAllocationWitnessAndManifest(t *testing.T) {
	w, in := basicBeginFixture(t)
	turn := basicComponentTurn(t, w, basicBeginTestDefinitions())
	out, err := beginBasicInput(t.Context(), turn, in, 2000)
	if err != nil || out.source.changeID <= 0 || out.source.inputRevision != 1 || out.changeRevision != 1 {
		t.Fatal(out, err)
	}
	expected := in.source
	expected.changeID, expected.inputRevision = out.source.changeID, 1
	if out.source != expected {
		t.Fatal("original native values changed", out.source)
	}
	var exact bool
	err = turn.transaction.conn.QueryRowContext(t.Context(), `SELECT
 (SELECT count(*)=9 FROM catalog_info_input_families WHERE change_id=?) AND
 (SELECT count(*)=1 FROM catalog_info_input_witnesses WHERE change_id=? AND ordinal=0 AND kind=0 AND object_revision='object:019') AND
 (SELECT expected_epoch=17 AND phase=0 AND family_mask=511 AND created_ms=2000 AND job_revision=1 FROM catalog_info_changes WHERE id=?) AND
 (SELECT info_epoch=17 AND info_publication_pending=0 FROM catalog_assets WHERE id=41)`, out.source.changeID, out.source.changeID, out.source.changeID).Scan(&exact)
	if err != nil || !exact {
		t.Fatal("wrong allocated Basic image", exact, err)
	}
	if err = turn.closeRows(); err != nil {
		t.Fatal(err)
	}
	if _, err = turn.transaction.commit(); err != nil {
		t.Fatal(err)
	}
	var charged int64
	if err = w.db.QueryRowContext(t.Context(), `SELECT used_bytes FROM catalog_info_input_budget WHERE singleton=1`).Scan(&charged); err != nil || charged != 3287 {
		t.Fatal("wrong adopted header/witness/manifest debt", charged, err)
	}
	// The allocated values feed a LATER bounded component turn, not extra work
	// crammed into begin's quantum or an invented purpose/admission profile.
	turn = basicStreamComponentTurn(t, w)
	if _, err = stageBasicStreamFact(t.Context(), turn, out.source); err != nil {
		t.Fatal(err)
	}
	if _, err = sealBasicStreamFact(t.Context(), turn, out.source); err != nil {
		t.Fatal(err)
	}
	if err = turn.closeRows(); err != nil {
		t.Fatal(err)
	}
	if _, err = turn.transaction.commit(); err != nil {
		t.Fatal(err)
	}
	if err = w.db.QueryRowContext(t.Context(), `SELECT (SELECT sealed=1 AND rows_staged=1 FROM catalog_info_input_families WHERE change_id=? AND family=1) AND (SELECT phase=0 FROM catalog_info_changes WHERE id=?) AND (SELECT info_epoch=17 AND info_publication_pending=0 FROM catalog_assets WHERE id=41)`, out.source.changeID, out.source.changeID).Scan(&exact); err != nil || !exact {
		t.Fatal("stage/seal changed readiness", exact, err)
	}
}
func TestBasicBeginWholeUnitRollbackAndZeroResult(t *testing.T) {
	for _, scenario := range []string{"debt_capacity", "existing_input", "late_family_refusal", "raw_chapter_cap"} {
		t.Run(scenario, func(t *testing.T) {
			w, in := basicBeginFixture(t)
			turn := basicComponentTurn(t, w, basicBeginTestDefinitions())
			switch scenario {
			case "debt_capacity":
				if _, err := turn.transaction.conn.ExecContext(t.Context(), `UPDATE catalog_info_input_budget SET used_bytes=33554432`); err != nil {
					t.Fatal(err)
				}
			case "existing_input":
				if _, err := turn.transaction.conn.ExecContext(t.Context(), `INSERT INTO catalog_info_changes(asset_id,asset_token,slot,expected_epoch,fingerprint,created_ms) VALUES(41,'basic-asset',1,17,zeroblob(32),1000)`); err != nil {
					t.Fatal(err)
				}
			case "late_family_refusal":
				delete(turn.steps, basicBeginFamiliesInsert)
			case "raw_chapter_cap":
				in.shape.RawChapterCount = MaxRawChapters + 1
			}
			out, err := beginBasicInput(t.Context(), turn, in, 2000)
			if err == nil || out != (basicBegunInput{}) {
				t.Fatal("partial begin result", out, err)
			}
			if scenario == "late_family_refusal" {
				var header, witness int64
				if e := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT (SELECT count(*) FROM catalog_info_changes),(SELECT count(*) FROM catalog_info_input_witnesses)`).Scan(&header, &witness); e != nil || header != 1 || witness != 1 {
					t.Fatal("control failed before intended late boundary", header, witness, e)
				}
			}
			if scenario == "raw_chapter_cap" && !errors.Is(err, ErrCap) {
				t.Fatal(err)
			}
			if closeErr := turn.closeRows(); closeErr != nil {
				t.Fatal(closeErr)
			}
			if _, rollbackErr := turn.transaction.rollback(err); !errors.Is(rollbackErr, err) {
				t.Fatal(rollbackErr)
			}
			basicBeginAbsent(t, w)
		})
	}
}

func TestBasicBeginFiniteDefinitions(t *testing.T) {
	defs := basicBeginDefinitions()
	if len(defs) != 6 || basicBeginMaxUses(basicBeginFamiliesInsert) != 9 || basicBeginMaxUses(basicStageAsset) != 0 {
		t.Fatal("wrong bounded begin recipe")
	}
	for _, d := range defs {
		if basicBeginMaxUses(d.ID) < 1 || len(plan.LintSQL(d.SQL)) != 0 {
			t.Fatal("begin escaped finite grammar", d.ID)
		}
	}
	if _, err := stmt.Describe(defs); err != nil {
		t.Fatal(err)
	}
}
