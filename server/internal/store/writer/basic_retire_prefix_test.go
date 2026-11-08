package writer

import (
	"errors"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

func basicRetirePrefixFixture(t *testing.T) (*executor, *commandTurn, basicStageRequest, int64) {
	t.Helper()
	w, source := basicStreamFixture(t)
	retire, metadataCharge := seedBasicRetiredFirstInput(t, w, source)
	rows := basicRowsRequest{family: 5, chapters: []basicChapter{{index: 20, title: "Opening", start: 0, end: 3}, {index: 30, title: "Closing", start: 4, end: 8}}}
	ordinals := []int64{8, 10}
	var charge int64
	for n, row := range rows.chapters {
		_, _, args, bytes, err := basicMediaValues(source, ordinals[n], row)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = w.db.ExecContext(t.Context(), `INSERT INTO catalog_info_new_chapters(change_id,input_ordinal,asset_id,chapter_index,title,start_seconds,end_seconds) VALUES(?,?,?,?,?,?,?)`, append([]any{source.changeID}, args...)...); err != nil {
			t.Fatal(err)
		}
		charge += bytes
	}
	// Nine earlier rows were already physically retired by earlier commands.
	// The sealed manifest keeps its original count; the remaining keys have gaps.
	retire.counts[4] = 11
	if _, err := w.db.ExecContext(t.Context(), `UPDATE catalog_info_input_families SET rows_expected=11,rows_staged=11 WHERE change_id=51 AND family=5`); err != nil {
		t.Fatal(err)
	}
	if _, err := w.db.ExecContext(t.Context(), `UPDATE catalog_info_input_budget SET used_bytes=? WHERE singleton=1`, 777+metadataCharge+charge); err != nil {
		t.Fatal(err)
	}
	var defs []stmt.Definition
	for _, d := range basicRetireFirstDefinitions() {
		switch d.ID {
		case basicRetireHeader, basicRetireAsset, basicRetireWitnessMatch, basicRetireEmptyPendingLinks:
			defs = append(defs, d)
		}
	}
	for _, d := range basicStreamStageDefinitions() {
		if d.ID == basicStageFamily || d.ID == basicStageBudget || d.ID == basicStageBudgetCAS {
			defs = append(defs, d)
		}
	}
	for _, d := range basicReclaimMediaDefinitions() {
		if d.ID == basicReclaimMediaHeader || d.ID == basicReclaimMediaID(5) {
			defs = append(defs, d)
		}
	}
	for _, d := range basicRetirePrefixDefinitions() {
		if d.ID == basicRetireMediaKeys5 {
			defs = append(defs, d)
		}
	}
	registry := nativeInventoryFixtureRegistry(t, w, defs, "basic_retire_prefix_test.go", "basic_retire_first_test.go")
	return w, basicHandlerTurn(t, w, BasicStage, defs, registry), basicStageRequest{purpose: BasicPurposeStage, operation: basicStageRetireInput, retire: retire, rows: rows, retireOrdinals: ordinals}, charge
}

func TestBasicRetirePrefixDeletesCapturedGappedKeys(t *testing.T) {
	w, turn, in, charge := basicRetirePrefixFixture(t)
	var before int64
	if err := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT used_bytes FROM catalog_info_input_budget`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if err := applyBasicRetirePrefix(t.Context(), turn, in); err != nil {
		t.Fatal(err)
	}
	basicMediaCommit(t, turn)
	var exact bool
	if err := w.db.QueryRowContext(t.Context(), `SELECT (SELECT count(*)=0 FROM catalog_info_new_chapters WHERE change_id=51) AND (SELECT used_bytes=? FROM catalog_info_input_budget) AND (SELECT rows_expected=11 AND rows_staged=11 AND sealed=1 FROM catalog_info_input_families WHERE change_id=51 AND family=5) AND (SELECT phase=5 AND job_revision=15 FROM catalog_info_changes WHERE id=51) AND (SELECT count(*)=1 FROM catalog_info_input_witnesses WHERE change_id=51) AND (SELECT ref_count=2 FROM catalog_info_shapes WHERE id=61)`, before-charge).Scan(&exact); err != nil || !exact {
		t.Fatal("prefix deletion changed retained metadata or refunded the wrong debt", exact, err)
	}
}

func TestBasicRetirePrefixRefusesSkippedHeadAndRollsBackDrift(t *testing.T) {
	for _, mode := range []string{"skipped_head", "row_drift"} {
		t.Run(mode, func(t *testing.T) {
			w, turn, in, _ := basicRetirePrefixFixture(t)
			if mode == "skipped_head" {
				in.rows.chapters = in.rows.chapters[1:]
				in.retireOrdinals = in.retireOrdinals[1:]
			} else {
				in.rows.chapters[1].title = "stale"
			}
			var before, after int64
			if err := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT total_changes()`).Scan(&before); err != nil {
				t.Fatal(err)
			}
			if err := applyBasicRetirePrefix(t.Context(), turn, in); !errors.Is(err, ErrConflict) {
				t.Fatal("unproved prefix accepted", err)
			}
			if err := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT total_changes()`).Scan(&after); err != nil || mode == "skipped_head" && after != before || mode == "row_drift" && after != before+1 {
				t.Fatal("wrong failure boundary", before, after, err)
			}
			_ = turn.closeRows()
			_, _ = turn.transaction.rollback(ErrConflict)
			var count int64
			if err := w.db.QueryRowContext(t.Context(), `SELECT count(*) FROM catalog_info_new_chapters WHERE change_id=51 AND input_ordinal IN (8,10)`).Scan(&count); err != nil || count != 2 {
				t.Fatal("rollback lost retained native rows", count, err)
			}
		})
	}
}
