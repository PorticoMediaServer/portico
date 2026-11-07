package writer

import (
	"errors"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

func basicCopyChapterFixture(t *testing.T) (*executor, basicChapterCopyEffects) {
	t.Helper()
	w, fact := basicCopyFactFixture(t)
	// Reuse the source/freeze/prefence capture. The canonical chapter parent
	// is a separate copy component's prerequisite, not installed by this body.
	if _, err := w.db.ExecContext(t.Context(), `INSERT INTO asset_chapter_facts VALUES('basic-asset',37,42,-11,'known','chapters:37')`); err != nil {
		t.Fatal(err)
	}
	if _, err := w.db.ExecContext(t.Context(), `INSERT INTO asset_chapters VALUES('basic-asset',9,'retained chapter',12.125,18.5)`); err != nil {
		t.Fatal(err)
	}
	// Capture the exact native family rather than deriving normalized indexes,
	// trimming titles or rounding the staged floating-point times.
	rows, err := w.db.QueryContext(t.Context(), `SELECT chapter_index,title,start_seconds,end_seconds FROM catalog_info_new_chapters WHERE change_id=51 ORDER BY input_ordinal`)
	if err != nil {
		t.Fatal(err)
	}
	var chapters []basicMediaRow
	for rows.Next() {
		var c basicChapter
		if err = rows.Scan(&c.index, &c.title, &c.start, &c.end); err != nil {
			t.Fatal(err)
		}
		chapters = append(chapters, c)
	}
	if err = errors.Join(rows.Err(), rows.Close()); err != nil {
		t.Fatal(err)
	}
	family, err := prepareBasicMediaFamily(fact.source, 5, 2, chapters)
	if err != nil {
		t.Fatal(err)
	}
	return w, basicChapterCopyEffects{family: family, position: fact.position, shapeID: fact.shapeID,
		fileSizeRevision: fact.fileSizeRevision, invalidation: fact.invalidation, ordinal: 0}
}

func basicCopyChapterTestDefinitions() []stmt.Definition {
	return append(basicPrefenceTestDefinitions(), basicCopyChapterDefinitions()...)
}

func basicCopyChapterSeedPrior(t *testing.T, w *executor, in *basicChapterCopyEffects) {
	t.Helper()
	in.hadBefore = true
	in.before = basicChapter{1, "Old opening", 0.25, 2.75}
	if _, err := w.db.ExecContext(t.Context(), `INSERT INTO asset_chapters VALUES('basic-asset',1,'Old opening',0.25,2.75)`); err != nil {
		t.Fatal(err)
	}
}

func TestBasicCopyChapterInsertAndReplacePreserveOtherRows(t *testing.T) {
	for _, name := range []string{"insert", "replace"} {
		t.Run(name, func(t *testing.T) {
			w, in := basicCopyChapterFixture(t)
			if name == "replace" {
				basicCopyChapterSeedPrior(t, w, &in)
			}
			turn := basicBoundedComponentTurn(t, w, basicCopyChapterTestDefinitions())
			got, err := installBasicChapter(t.Context(), turn, in)
			want := in.family.rows[in.ordinal].(basicChapter)
			if err != nil || got != want {
				t.Fatal(got, err)
			}
			basicMediaCommit(t, turn)
			var actual basicChapter
			if err = w.db.QueryRowContext(t.Context(), `SELECT chapter_index,title,start_seconds,end_seconds FROM asset_chapters WHERE asset_id='basic-asset' AND chapter_index=1`).Scan(&actual.index, &actual.title, &actual.start, &actual.end); err != nil || actual != want {
				t.Fatal(actual, err)
			}
			var exact bool
			if err = w.db.QueryRowContext(t.Context(), `SELECT
 (SELECT count(*)=2 FROM asset_chapters)
 AND (SELECT title='retained chapter' AND start_seconds=12.125 AND end_seconds=18.5 FROM asset_chapters WHERE asset_id='basic-asset' AND chapter_index=9)
 AND (SELECT info_epoch=17 AND info_publication_pending=1 FROM catalog_assets WHERE id=41)
 AND (SELECT info_pending_change=51 FROM catalog_asset_links WHERE entity_id=31 AND asset_id=41)
 AND (SELECT phase=2 AND job_revision=15 AND copy_family=0 AND copy_phase=0 AND copy_id=0 AND copy_aux=0 AND copy_key='' AND copy_key2='' AND copy_key3='' FROM catalog_info_changes WHERE id=51)
 AND (SELECT rows_expected=2 AND rows_staged=2 AND sealed=1 FROM catalog_info_input_families WHERE change_id=51 AND family=5)
 AND (SELECT count(*)=2 FROM catalog_info_new_chapters WHERE change_id=51)
 AND (SELECT count(*)=0 FROM scan_command_receipts)`).Scan(&exact); err != nil || !exact {
				t.Fatal("chapter copy altered sibling/staging/publication/cursor/receipt", exact, err)
			}
		})
	}
}

func TestBasicCopyChapterRejectsDriftAndRawCap(t *testing.T) {
	for _, name := range []string{"captured_before", "sealed_row", "raw_chapter_cap"} {
		t.Run(name, func(t *testing.T) {
			w, in := basicCopyChapterFixture(t)
			basicCopyChapterSeedPrior(t, w, &in)
			wantErr := ErrConflict
			switch name {
			case "captured_before":
				in.before.end = 2.5
			case "sealed_row":
				if _, err := w.db.ExecContext(t.Context(), `UPDATE catalog_info_new_chapters SET title='uncaptured title' WHERE change_id=51 AND input_ordinal=0`); err != nil {
					t.Fatal(err)
				}
			case "raw_chapter_cap":
				in.family.rawChapterCount = MaxRawChapters + 1
				wantErr = ErrCap
			}
			turn := basicBoundedComponentTurn(t, w, basicCopyChapterTestDefinitions())
			got, err := installBasicChapter(t.Context(), turn, in)
			if !errors.Is(err, wantErr) || got != (basicChapter{}) {
				t.Fatal(got, err)
			}
			_ = turn.closeRows()
			_, _ = turn.transaction.rollback(err)
			var title string
			if err = w.db.QueryRowContext(t.Context(), `SELECT title FROM asset_chapters WHERE asset_id='basic-asset' AND chapter_index=1`).Scan(&title); err != nil || title != "Old opening" {
				t.Fatal("failed copy changed canonical chapter", title, err)
			}
		})
	}
}

func TestBasicCopyChapterReturningCorruptionRollsBackNativeWrite(t *testing.T) {
	w, in := basicCopyChapterFixture(t)
	basicCopyChapterSeedPrior(t, w, &in)
	defs := basicCopyChapterTestDefinitions()
	for n := range defs {
		if defs[n].ID == basicCopyChapterUpdate {
			// Real native UPDATE and Capture receipt, with a wrong returned time.
			defs[n].SQL = strings.Replace(defs[n].SQL, "RETURNING asset_id,chapter_index,title,start_seconds,end_seconds", "RETURNING asset_id,chapter_index,title,end_seconds,start_seconds", 1)
		}
	}
	turn := basicBoundedComponentTurn(t, w, defs)
	got, err := installBasicChapter(t.Context(), turn, in)
	if !errors.Is(err, ErrConflict) || got != (basicChapter{}) {
		t.Fatal(got, err)
	}
	var title string
	if e := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT title FROM asset_chapters WHERE asset_id='basic-asset' AND chapter_index=1`).Scan(&title); e != nil || title != "Opening" {
		t.Fatal("corruption did not follow actual native write", title, e)
	}
	_ = turn.closeRows()
	_, _ = turn.transaction.rollback(err)
	var exact bool
	if err = w.db.QueryRowContext(t.Context(), `SELECT
 (SELECT title='Old opening' AND start_seconds=0.25 AND end_seconds=2.75 FROM asset_chapters WHERE asset_id='basic-asset' AND chapter_index=1)
 AND (SELECT title='retained chapter' AND start_seconds=12.125 AND end_seconds=18.5 FROM asset_chapters WHERE asset_id='basic-asset' AND chapter_index=9)`).Scan(&exact); err != nil || !exact {
		t.Fatal("late failure escaped rollback", exact, err)
	}
}
