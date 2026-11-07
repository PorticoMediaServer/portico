package writer

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

func basicMediaFixture(t *testing.T) (*executor, basicStreamFactStage) {
	t.Helper()
	w, s := basicStreamFixture(t)
	if _, err := w.db.ExecContext(t.Context(), `UPDATE catalog_info_changes SET family_mask=511 WHERE id=51`); err != nil {
		t.Fatal(err)
	}
	for _, pair := range [][2]int64{{4, 1}, {5, 2}, {6, 1}, {7, 1}, {8, 2}, {9, 1}} {
		if _, err := w.db.ExecContext(t.Context(), `INSERT INTO catalog_info_input_families(change_id,family,witness_ordinal,rows_expected) VALUES(51,?,0,?)`, pair[0], pair[1]); err != nil {
			t.Fatal(err)
		}
	}
	return w, s
}
func basicMediaTestDefs() []stmt.Definition {
	return append(basicStreamStageDefinitions(), basicMediaDefinitions()...)
}
func basicMediaCommit(t *testing.T, turn *commandTurn) {
	t.Helper()
	if err := turn.closeRows(); err != nil {
		t.Fatal(err)
	}
	if _, err := turn.transaction.commit(); err != nil {
		t.Fatal(err)
	}
}
func TestBasicMediaNativeStageSealAllSixFamilies(t *testing.T) {
	w, source := basicMediaFixture(t)
	families := []struct {
		number int64
		rows   []basicMediaRow
	}{
		{4, []basicMediaRow{basicChapterFact{37, "known", "chapters:37"}}},
		{5, []basicMediaRow{basicChapter{1, "Opening", 0, 3}, basicChapter{2, "Closing", 3, 9}}},
		{6, []basicMediaRow{basicSubtitleFact{41, 0, true, "known", "subtitles:41"}}},
		{7, []basicMediaRow{basicSubtitleSidecar{"sidecar-1", "/media/Arrival.srt", "sidecar:30", 30, -7}}},
		{8, []basicMediaRow{basicSubtitle{id: "subtitle-1", origin: "embedded", locator: "2", streamIndex: 2, format: "subrip", language: "en", title: "English"}, basicSubtitle{id: "subtitle-2", origin: "sidecar", locator: "/media/Arrival.srt", sidecarID: "sidecar-1", streamIndex: -1, format: "subrip", language: "fr", title: "French", hearingImpaired: true}}},
		{9, []basicMediaRow{basicSubtitleEvidence{41, "inventory:41"}}},
	}
	wantDebt := int64(1000)
	for _, f := range families {
		prepared, err := prepareBasicMediaFamily(source, f.number, 2, f.rows)
		if err != nil {
			t.Fatal(f.number, err)
		}
		for n, row := range f.rows {
			_, _, _, charge, err := basicMediaValues(source, int64(n), row)
			if err != nil {
				t.Fatal(err)
			}
			wantDebt += charge
		}
		turn := basicComponentTurn(t, w, basicMediaTestDefs())
		got, err := stageBasicMediaRows(t.Context(), turn, prepared, 0, int64(len(f.rows)))
		if err != nil || got.rowsStaged != int64(len(f.rows)) || got.sealed != 0 {
			t.Fatal(f.number, got, err)
		}
		basicMediaCommit(t, turn)
		turn = basicComponentTurn(t, w, basicMediaTestDefs())
		got, err = sealBasicMediaFamily(t.Context(), turn, prepared)
		if err != nil || got.rowsStaged != int64(len(f.rows)) || got.sealed != 1 {
			t.Fatal(f.number, got, err)
		}
		basicMediaCommit(t, turn)
	}
	var debt, phase, epoch int64
	if err := w.db.QueryRowContext(t.Context(), `SELECT used_bytes FROM catalog_info_input_budget WHERE singleton=1`).Scan(&debt); err != nil {
		t.Fatal(err)
	}
	if err := w.db.QueryRowContext(t.Context(), `SELECT phase FROM catalog_info_changes WHERE id=51`).Scan(&phase); err != nil {
		t.Fatal(err)
	}
	if err := w.db.QueryRowContext(t.Context(), `SELECT info_epoch FROM catalog_assets WHERE id=41`).Scan(&epoch); err != nil {
		t.Fatal(err)
	}
	if debt != wantDebt || phase != 0 || epoch != 17 {
		t.Fatal(debt, wantDebt, phase, epoch)
	}
}
func TestBasicMediaRawCapsAndFrozenGeometry(t *testing.T) {
	source := basicStreamFactStage{assetToken: "token"}
	if _, err := prepareBasicMediaFamily(source, 5, 513, nil); !errors.Is(err, ErrCap) {
		t.Fatal("raw cap lost after cleanup", err)
	}
	if _, err := prepareBasicMediaFamily(source, 5, 2, []basicMediaRow{basicChapter{1, "a", 0, 2}, basicChapter{1, "b", 2, 4}}); !errors.Is(err, ErrInvalid) {
		t.Fatal("duplicate index", err)
	}
	if _, err := prepareBasicMediaFamily(source, 5, 2, []basicMediaRow{basicChapter{1, "a", 0, 3}, basicChapter{2, "b", 2, 4}}); !errors.Is(err, ErrInvalid) {
		t.Fatal("overlap", err)
	}
	rows := []basicMediaRow{basicChapter{1, "Original", 0, 2}}
	prepared, err := prepareBasicMediaFamily(source, 5, 1, rows)
	if err != nil {
		t.Fatal(err)
	}
	rows[0] = basicChapter{1, "Changed", 0, 4}
	if prepared.rows[0].(basicChapter).title != "Original" {
		t.Fatal("mutable input", prepared)
	}
	if _, err := prepareBasicMediaFamily(source, 9, 0, []basicMediaRow{basicSubtitleEvidence{1, strings.Repeat("x", 33554432)}}); !errors.Is(err, ErrCap) {
		t.Fatal("whole byte cap", err)
	}
}

func TestBasicMediaUnmaskedFamilyRefusesBeforeDebt(t *testing.T) {
	w, source := basicMediaFixture(t)
	if _, err := w.db.ExecContext(t.Context(), `UPDATE catalog_info_changes SET family_mask=503 WHERE id=51`); err != nil {
		t.Fatal(err)
	}
	prepared, err := prepareBasicMediaFamily(source, 4, 0, []basicMediaRow{basicChapterFact{37, "known", "chapters:37"}})
	if err != nil {
		t.Fatal(err)
	}
	turn := basicComponentTurn(t, w, basicMediaTestDefs())
	got, err := stageBasicMediaRows(t.Context(), turn, prepared, 0, 1)
	if !errors.Is(err, ErrConflict) || got != (basicFamilyProgress{}) {
		t.Fatal(got, err)
	}
	_ = turn.closeRows()
	_, _ = turn.transaction.rollback(err)
	var debt int64
	if err := w.db.QueryRowContext(t.Context(), `SELECT used_bytes FROM catalog_info_input_budget WHERE singleton=1`).Scan(&debt); err != nil || debt != 1000 {
		t.Fatal(debt, err)
	}
}
func TestBasicMediaBatchFailureRollsBackDebtRowsAndCount(t *testing.T) {
	w, source := basicMediaFixture(t)
	// The child FK is deliberately absent; even the prior debt CAS must roll back.
	frame, err := prepareBasicMediaFamily(source, 5, 2, []basicMediaRow{basicChapter{1, "Opening", 0, 3}, basicChapter{2, "Closing", 3, 9}})
	if err != nil {
		t.Fatal(err)
	}
	turn := basicComponentTurn(t, w, basicMediaTestDefs())
	got, err := stageBasicMediaRows(t.Context(), turn, frame, 0, 2)
	if err == nil || !reflect.DeepEqual(got, basicFamilyProgress{}) {
		t.Fatal("failed FK leaked output", got, err)
	}
	_ = turn.closeRows()
	_, _ = turn.transaction.rollback(err)
	var debt, count, staged int64
	if err := w.db.QueryRowContext(t.Context(), `SELECT used_bytes FROM catalog_info_input_budget WHERE singleton=1`).Scan(&debt); err != nil {
		t.Fatal(err)
	}
	if err := w.db.QueryRowContext(t.Context(), `SELECT count(*) FROM catalog_info_new_chapters WHERE change_id=51`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if err := w.db.QueryRowContext(t.Context(), `SELECT rows_staged FROM catalog_info_input_families WHERE change_id=51 AND family=5`).Scan(&staged); err != nil {
		t.Fatal(err)
	}
	if debt != 1000 || count != 0 || staged != 0 {
		t.Fatal(debt, count, staged)
	}
}
func TestBasicMediaSealRejectsHiddenFamilyRow(t *testing.T) {
	w, source := basicMediaFixture(t)
	// Header counters cannot hide a second asset's fact in the SAME family.
	if _, err := w.db.ExecContext(t.Context(), `INSERT INTO catalog_info_new_chapter_facts VALUES(51,0,'basic-asset',37,42,-11,'known','chapters:37'),(51,1,'foreign-asset',1,0,0,'known','foreign')`); err != nil {
		t.Fatal(err)
	}
	if _, err := w.db.ExecContext(t.Context(), `UPDATE catalog_info_input_families SET rows_staged=1 WHERE change_id=51 AND family=4`); err != nil {
		t.Fatal(err)
	}
	frame, err := prepareBasicMediaFamily(source, 4, 0, []basicMediaRow{basicChapterFact{37, "known", "chapters:37"}})
	if err != nil {
		t.Fatal(err)
	}
	turn := basicComponentTurn(t, w, basicMediaTestDefs())
	got, err := sealBasicMediaFamily(t.Context(), turn, frame)
	if !errors.Is(err, ErrConflict) || got != (basicFamilyProgress{}) {
		t.Fatal(got, err)
	}
}
func TestBasicMediaEmptyChapterFamilyAndStageBound(t *testing.T) {
	w, source := basicMediaFixture(t)
	if _, err := w.db.ExecContext(t.Context(), `UPDATE catalog_info_input_families SET rows_expected=0 WHERE change_id=51 AND family=5`); err != nil {
		t.Fatal(err)
	}
	empty, err := prepareBasicMediaFamily(source, 5, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	turn := basicComponentTurn(t, w, basicMediaTestDefs())
	got, err := sealBasicMediaFamily(t.Context(), turn, empty)
	if err != nil || got.rowsStaged != 0 || got.sealed != 1 {
		t.Fatal(got, err)
	}
	basicMediaCommit(t, turn)
	chapters := make([]basicMediaRow, 9)
	for n := range chapters {
		chapters[n] = basicChapter{int64(n + 1), "", float64(n), float64(n + 1)}
	}
	frame, err := prepareBasicMediaFamily(source, 5, 9, chapters)
	if err != nil {
		t.Fatal(err)
	}
	turn = basicComponentTurn(t, w, basicMediaTestDefs())
	_, err = stageBasicMediaRows(t.Context(), turn, frame, 0, 9)
	if !errors.Is(err, ErrInvalid) {
		t.Fatal("unbounded batch", err)
	}
}

func TestBasicMediaNativeSidecarPathCauses(t *testing.T) {
	source := basicStreamFactStage{assetToken: "asset"}
	for _, path := range []string{string([]byte{'/', 0xff}), string([]byte{'/', 0, 0xff})} {
		for _, family := range []int64{7, 8} {
			var row basicMediaRow = basicSubtitleSidecar{id: "sidecar", path: path, digest: "digest", size: 1}
			if family == 8 {
				row = basicSubtitle{id: "subtitle", origin: "sidecar", locator: path, sidecarID: "sidecar", streamIndex: -1, format: "srt"}
			}
			prepared, err := prepareBasicMediaFamily(source, family, 0, []basicMediaRow{row})
			if prepared != nil {
				t.Fatal("bad native path produced input", prepared)
			}
			if strings.IndexByte(path, 0) >= 0 {
				if !errors.Is(err, ErrInvalid) || errors.Is(err, ErrFilenameUnrepresentable) {
					t.Fatal("NUL became filename poison", err)
				}
			} else if !errors.Is(err, ErrFilenameUnrepresentable) {
				t.Fatal("UTF8 path lost typed cause", err)
			}
		}
	}
	// Complete-family capacity wins even when the first path is unrepresentable.
	rows := []basicMediaRow{basicSubtitleSidecar{id: "a", path: string([]byte{'/', 0xff}), digest: "a"}, basicSubtitleSidecar{id: "b", path: "/good.srt", digest: strings.Repeat("x", 33554432)}}
	if got, err := prepareBasicMediaFamily(source, 7, 0, rows); got != nil || !errors.Is(err, ErrCap) || errors.Is(err, ErrFilenameUnrepresentable) {
		t.Fatal(got, err)
	}
	if got, err := prepareBasicMediaFamily(source, 7, 0, []basicMediaRow{basicSubtitleSidecar{id: "a", path: "/good.srt", digest: string([]byte{0xff})}}); got != nil || !errors.Is(err, ErrInvalid) || errors.Is(err, ErrFilenameUnrepresentable) {
		t.Fatal("opaque digest became path cause", got, err)
	}
}
