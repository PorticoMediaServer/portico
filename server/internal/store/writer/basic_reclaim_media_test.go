package writer

import (
	"errors"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

func basicReclaimMediaFixture(t *testing.T, defs []stmt.Definition, setup string) (*executor, *commandTurn, basicMediaReclaimInput, int64) {
	t.Helper()
	w, input := basicCompleteAcceptFixture(t)
	f, e := prepareBasicMediaFamily(input.stream.source, 5, 2, []basicMediaRow{basicChapter{1, "Opening", 0, 3}, basicChapter{2, "Closing", 3, 9}})
	if e != nil {
		t.Fatal(e)
	}
	var charge int64
	for n, r := range f.rows {
		_, _, _, c, e := basicMediaValues(f.source, int64(n), r)
		if e != nil {
			t.Fatal(e)
		}
		charge += c
	}
	for _, q := range []string{`UPDATE catalog_info_changes SET phase=5,job_revision=13,target_shape_id=61,target_owner_object=NULL,target_owner_revision=NULL WHERE id=51`, `UPDATE catalog_info_input_budget SET used_bytes=10000 WHERE singleton=1`} {
		if _, e = w.db.ExecContext(t.Context(), q); e != nil {
			t.Fatal(e)
		}
	}
	if setup != "" {
		if _, e = w.db.ExecContext(t.Context(), setup); e != nil {
			t.Fatal(e)
		}
	}
	// All setup precedes actual FULL startup Capture; component executes through
	// the existing NORMAL handler turn, without changing any production profile.
	registry := nativeInventoryFixtureRegistry(t, w, defs, "basic_reclaim_media.go", "basic_reclaim_media_statements.go", "basic_reclaim_media_test.go")
	return w, basicHandlerTurn(t, w, BasicAccept, defs, registry), basicMediaReclaimInput{family: f, start: 0, end: 2, jobRevision: 13, targetShapeID: 61}, charge
}
func basicReclaimMediaTestDefinitions() []stmt.Definition {
	return append(basicStreamStageDefinitions(), basicReclaimMediaDefinitions()...)
}
func TestBasicReclaimMediaPhysicalRowsAndDebt(t *testing.T) {
	w, turn, in, charge := basicReclaimMediaFixture(t, basicReclaimMediaTestDefinitions(), "")
	deleted, refunded, e := reclaimBasicMediaRows(t.Context(), turn, in)
	if e != nil || deleted != 2 || refunded != charge {
		t.Fatal(deleted, refunded, e)
	}
	basicMediaCommit(t, turn)
	var exact bool
	e = w.db.QueryRowContext(t.Context(), `SELECT (SELECT count(*)=0 FROM catalog_info_new_chapters WHERE change_id=51) AND (SELECT used_bytes=? FROM catalog_info_input_budget WHERE singleton=1) AND (SELECT count(*)=2 FROM catalog_info_new_subtitles WHERE change_id=51) AND (SELECT count(*)=1 FROM catalog_info_new_chapter_facts WHERE change_id=51) AND (SELECT count(*)=9 FROM catalog_info_input_families WHERE change_id=51) AND (SELECT phase=5 AND job_revision=13 AND target_shape_id=61 FROM catalog_info_changes WHERE id=51)`, 10000-charge).Scan(&exact)
	if e != nil || !exact {
		t.Fatal(exact, e)
	}
}
func TestBasicReclaimMediaLateFailureRollsBackPhysicalPrefix(t *testing.T) {
	for _, mode := range []string{"missing", "drift", "returning", "budget"} {
		t.Run(mode, func(t *testing.T) {
			defs := basicReclaimMediaTestDefinitions()
			setup := ""
			switch mode {
			case "missing":
				setup = `DELETE FROM catalog_info_new_chapters WHERE change_id=51 AND chapter_index=2`
			case "drift":
				setup = `UPDATE catalog_info_new_chapters SET title='drift' WHERE change_id=51 AND chapter_index=2`
			case "budget":
				setup = `UPDATE catalog_info_input_budget SET used_bytes=1 WHERE singleton=1`
			case "returning":
				for n := range defs {
					if defs[n].ID == basicReclaimMediaID(5) {
						defs[n].SQL = strings.Replace(defs[n].SQL, "RETURNING change_id,input_ordinal,asset_id,chapter_index", "RETURNING change_id,chapter_index,asset_id,input_ordinal", 1)
					}
				}
			}
			w, turn, in, _ := basicReclaimMediaFixture(t, defs, setup)
			deleted, refunded, e := reclaimBasicMediaRows(t.Context(), turn, in)
			if !errors.Is(e, ErrConflict) || deleted != 0 || refunded != 0 {
				t.Fatal(deleted, refunded, e)
			}
			var prefixGone bool
			if e = turn.transaction.conn.QueryRowContext(t.Context(), `SELECT count(*)=0 FROM catalog_info_new_chapters WHERE change_id=51 AND chapter_index=1`).Scan(&prefixGone); e != nil || !prefixGone {
				t.Fatal("first physical delete did not happen", prefixGone, e)
			}
			_, _ = turn.transaction.rollback(nil)
			var restored bool
			debt := int64(10000)
			if mode == "budget" {
				debt = 1
			}
			e = w.db.QueryRowContext(t.Context(), `SELECT (SELECT count(*)=1 FROM catalog_info_new_chapters WHERE change_id=51 AND chapter_index=1 AND title='Opening') AND (SELECT used_bytes=? FROM catalog_info_input_budget WHERE singleton=1)`, debt).Scan(&restored)
			if e != nil || !restored {
				t.Fatal(restored, e)
			}
		})
	}
}

func TestBasicReclaimMediaInvalidIsZero(t *testing.T) {
	deleted, refunded, err := reclaimBasicMediaRows(t.Context(), nil, basicMediaReclaimInput{})
	if !errors.Is(err, ErrInvalid) || deleted != 0 || refunded != 0 {
		t.Fatal(deleted, refunded, err)
	}
}
