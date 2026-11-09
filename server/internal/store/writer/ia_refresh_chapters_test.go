package writer

import (
	"database/sql"
	"errors"
	"reflect"
	"testing"
	"time"
)

func iaRefreshChapterFact(rev int64) iaRefreshChapterFactRow {
	return iaRefreshChapterFactRow{AssetID: "refresh-asset", Revision: rev, Size: 42, ModifiedNS: 11, Status: "ready", Fingerprint: "old"}
}

func iaRefreshChapterFixtureRow(index int64, title string) iaRefreshChapterRow {
	start := float64(index) * 60
	return iaRefreshChapterRow{AssetID: "refresh-asset", ChapterIndex: index, Title: title, StartSeconds: start, EndSeconds: start + 59.5}
}

func iaRefreshChapterImage(t *testing.T, w *executor, index int64) (iaRefreshChapterRow, bool) {
	t.Helper()
	var row iaRefreshChapterRow
	err := w.db.QueryRow(`SELECT asset_id,chapter_index,title,start_seconds,end_seconds FROM asset_chapters WHERE asset_id=? AND chapter_index=?`, "refresh-asset", index).
		Scan(&row.AssetID, &row.ChapterIndex, &row.Title, &row.StartSeconds, &row.EndSeconds)
	if errors.Is(err, sql.ErrNoRows) {
		return iaRefreshChapterRow{}, false
	}
	if err != nil {
		t.Fatal(err)
	}
	return row, true
}

// The chapter pair shares IA21's bounded keyset walk. A fresh turn resumes from
// the journal cursor on a fake clock; the restore re-reads only the journal, so
// a mutable new projection can never be mistaken for the old chapter rows.
func TestIARefreshChapterWalkResumesAndRestores(t *testing.T) {
	w, r, p, q := iaRefreshWalkFixture(t)
	clock := w.now()
	w.now = func() time.Time { return clock }
	iaRefreshSeed(t, w, iaRefreshChapterFact(10))
	first := iaRefreshChapterFixtureRow(0, "Opening")
	second := iaRefreshChapterFixtureRow(1, "Middle")
	third := iaRefreshChapterFixtureRow(2, "End")
	for _, row := range []iaRefreshChapterRow{first, second, third} {
		iaRefreshSeed(t, w, row)
	}
	begin := iaRefreshApply(t, w, r, p, q)
	q.refreshID = begin.result.refreshID
	iaRefreshAck(t, w, begin.result.refreshID)
	desired := iaRefreshChapterDesired{
		Fact: &iaRefreshChapterFactRow{AssetID: "refresh-asset", Revision: 11, Size: 42, ModifiedNS: 12, Status: "ready", Fingerprint: "new"},
		Rows: []iaRefreshChapterRow{iaRefreshChapterFixtureRow(0, "Opening (new)"), third, iaRefreshChapterFixtureRow(3, "Credits")},
	}
	named := []iaRefreshWalkStep{}
	for {
		clock = clock.Add(time.Millisecond)
		step, err := iaRefreshRawChapterStep(t, w, r, q, desired)
		if err != nil {
			t.Fatal(err)
		}
		if !step.more {
			break
		}
		named = append(named, step)
		q = iaRefreshStepRequest(t, q, IARefreshStep, step)
		iaRefreshApply(t, w, r, p, q)
		iaRefreshAck(t, w, begin.result.refreshID)
	}
	want := []iaRefreshKey{{3, "refresh-asset", 0}, {4, "refresh-asset", 0}, {4, "refresh-asset", 1}, {4, "refresh-asset", 2}, {4, "refresh-asset", 3}}
	if len(named) != len(want) {
		t.Fatalf("walk named %d keys: %+v", len(named), named)
	}
	for j := range want {
		if named[j].key != want[j] {
			t.Fatalf("chapter order %d: %+v want %+v", j, named[j].key, want[j])
		}
	}
	if row, ok := iaRefreshChapterImage(t, w, 1); ok {
		t.Fatal("removed chapter survived", row)
	}
	if row, ok := iaRefreshChapterImage(t, w, 3); !ok || row.Title != "Credits" {
		t.Fatal(row, ok)
	}
	restored := []iaRefreshKey{}
	for {
		clock = clock.Add(time.Millisecond)
		step, err := iaRefreshRawChapterRestore(t, w, r, q)
		if err != nil {
			t.Fatal(err)
		}
		if !step.more {
			break
		}
		restored = append(restored, step.key)
		q = iaRefreshStepRequest(t, q, IARefreshRollback, step)
		iaRefreshApply(t, w, r, p, q)
		iaRefreshAck(t, w, begin.result.refreshID)
	}
	if !reflect.DeepEqual(restored, []iaRefreshKey{{3, "refresh-asset", 0}, {4, "refresh-asset", 3}, {4, "refresh-asset", 2}, {4, "refresh-asset", 1}, {4, "refresh-asset", 0}}) {
		t.Fatalf("restore order %+v", restored)
	}
	if row, ok := iaRefreshChapterImage(t, w, 3); ok {
		t.Fatal("new chapter survived", row)
	}
	for _, expected := range []iaRefreshChapterRow{first, second, third} {
		if row, ok := iaRefreshChapterImage(t, w, expected.ChapterIndex); !ok || !reflect.DeepEqual(row, expected) {
			t.Fatal("exact old chapter lost", expected.ChapterIndex, row, ok)
		}
	}
	if got := iaRefreshCount(t, w, `SELECT revision FROM asset_chapter_facts WHERE asset_id='refresh-asset'`); got != 10 {
		t.Fatal("chapter parent fact was not restored", got)
	}
}

func TestIARefreshChapterWalkEmptyProjectionClosesParentOnce(t *testing.T) {
	w, r, p, q := iaRefreshWalkFixture(t)
	begin := iaRefreshApply(t, w, r, p, q)
	q.refreshID = begin.result.refreshID
	iaRefreshAck(t, w, begin.result.refreshID)
	// No live rows and no desired rows: only the parent presence tombstone.
	step, err := iaRefreshRawChapterStep(t, w, r, q, iaRefreshChapterDesired{})
	if err != nil || !step.more || step.key != (iaRefreshKey{3, "refresh-asset", 0}) {
		t.Fatal(step, err)
	}
	q = iaRefreshStepRequest(t, q, IARefreshStep, step)
	iaRefreshApply(t, w, r, p, q)
	iaRefreshAck(t, w, begin.result.refreshID)
	if got := iaRefreshCount(t, w, `SELECT old_exists FROM catalog_info_undo_asset_chapter_facts WHERE refresh_id=?`, begin.result.refreshID); got != 0 {
		t.Fatal("old parent absence was not journalled", got)
	}
	restore, err := iaRefreshRawChapterRestore(t, w, r, q)
	if err != nil || !restore.more || restore.key != (iaRefreshKey{3, "refresh-asset", 0}) {
		t.Fatal(restore, err)
	}
	q = iaRefreshStepRequest(t, q, IARefreshRollback, restore)
	iaRefreshApply(t, w, r, p, q)
	iaRefreshAck(t, w, begin.result.refreshID)
	tail, err := iaRefreshRawChapterRestore(t, w, r, q)
	if err != nil || tail.more {
		t.Fatal("empty restore named a second key", tail, err)
	}
	if got := iaRefreshCount(t, w, `SELECT count(*) FROM asset_chapter_facts`); got != 0 {
		t.Fatal("absence tombstone created a row", got)
	}
}

func TestIARefreshChapterWalkRefusesMisboundProjection(t *testing.T) {
	w, r, p, q := iaRefreshWalkFixture(t)
	begin := iaRefreshApply(t, w, r, p, q)
	q.refreshID = begin.result.refreshID
	iaRefreshAck(t, w, begin.result.refreshID)
	fact := iaRefreshChapterFactRow{AssetID: "refresh-asset", Revision: 11}
	foreign := iaRefreshChapterFixtureRow(0, "x")
	foreign.AssetID = "other-asset"
	rows := make([]iaRefreshChapterRow, iaRefreshWalkKeys)
	for j := range rows {
		rows[j] = iaRefreshChapterFixtureRow(int64(j), "chapter")
	}
	for _, cell := range []struct {
		name string
		d    iaRefreshChapterDesired
		want error
	}{
		{"child_without_parent", iaRefreshChapterDesired{Rows: []iaRefreshChapterRow{foreign}}, ErrInvalid},
		{"foreign_child_asset", iaRefreshChapterDesired{Fact: &fact, Rows: []iaRefreshChapterRow{foreign}}, ErrInvalid},
		{"duplicate_child", iaRefreshChapterDesired{Fact: &fact, Rows: []iaRefreshChapterRow{iaRefreshChapterFixtureRow(1, "a"), iaRefreshChapterFixtureRow(1, "b")}}, ErrInvalid},
		{"descending_children", iaRefreshChapterDesired{Fact: &fact, Rows: []iaRefreshChapterRow{iaRefreshChapterFixtureRow(2, "a"), iaRefreshChapterFixtureRow(1, "b")}}, ErrInvalid},
	} {
		t.Run(cell.name, func(t *testing.T) {
			if _, err := iaRefreshRawChapterStep(t, w, r, q, cell.d); !errors.Is(err, cell.want) {
				t.Fatal(err)
			}
		})
	}
	t.Run("capacity", func(t *testing.T) {
		over := append(rows, iaRefreshChapterFixtureRow(iaRefreshWalkKeys, "over"))
		_, err := iaRefreshRawChapterStep(t, w, r, q, iaRefreshChapterDesired{Fact: &fact, Rows: over})
		var refusal *Refusal
		if !errors.As(err, &refusal) || refusal.Code != RefusalCapacity {
			t.Fatal(err)
		}
	})
	t.Run("max_projection", func(t *testing.T) {
		step, err := iaRefreshRawChapterStep(t, w, r, q, iaRefreshChapterDesired{Fact: &fact, Rows: rows})
		if err != nil || !step.more || step.key != (iaRefreshKey{3, "refresh-asset", 0}) {
			t.Fatal(step, err)
		}
	})
}
