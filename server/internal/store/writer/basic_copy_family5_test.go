package writer

import (
	"errors"
	"fmt"
	"testing"
)

func seedBasicCopy5First(t *testing.T, e *executor, entry basicCopyCursor, revision int64) {
	t.Helper()
	if _, err := e.db.ExecContext(t.Context(), `INSERT INTO catalog_info_shapes(id,container,video_codec,height,ref_count) VALUES(61,'mkv','h264',1080,1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := e.db.ExecContext(t.Context(), `UPDATE catalog_info_changes SET phase=2,family_mask=511,target_shape_id=61,target_available=1,has_after=1,after_entity=31,upper_entity=31,copy_family=?,copy_phase=?,copy_id=?,copy_aux=?,copy_key='',copy_key2='',copy_key3='',job_revision=? WHERE id=51`,
		entry.family, entry.phase, entry.id, entry.aux, revision); err != nil {
		t.Fatal(err)
	}
}

func seedBasicCopy5Chapters(t *testing.T, e *executor, count int) {
	t.Helper()
	if _, err := e.db.ExecContext(t.Context(), `INSERT INTO asset_chapter_facts(asset_id,revision,size,modified_ns,status,fingerprint) VALUES('basic-asset',31,42,-11,'known','chapters:31')`); err != nil {
		t.Fatal(err)
	}
	if _, err := e.db.ExecContext(t.Context(), `INSERT INTO catalog_info_new_chapter_facts(change_id,input_ordinal,asset_id,revision,size,modified_ns,status,fingerprint) VALUES(51,0,'basic-asset',31,42,-11,'known','chapters:31')`); err != nil {
		t.Fatal(err)
	}
	for n := 0; n < count; n++ {
		if _, err := e.db.ExecContext(t.Context(), `INSERT INTO catalog_info_new_chapters(change_id,input_ordinal,asset_id,chapter_index,title,start_seconds,end_seconds) VALUES(51,?,'basic-asset',?,?,?,?)`,
			n, n, fmt.Sprintf("C%d", n), float64(n*10), float64(n*10+5)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := e.db.ExecContext(t.Context(), `INSERT INTO asset_chapters(asset_id,chapter_index,title,start_seconds,end_seconds) VALUES('basic-asset',20,'Obsolete',200,205)`); err != nil {
		t.Fatal(err)
	}
}

// Family 5 carries its cursors across four bounded turns for ten staged
// chapters: two install turns, then a two-turn canonical cleanup that removes
// the obsolete chapter and ends in {family:6}.
func TestBasicCopyFamily5ChaptersMultiTurn(t *testing.T) {
	w, e, authority, s, original, identity := basicControlDispatchFixture(t)
	seedBasicFirstOrdinaryOwner(t, e)
	seedBasicCopy5First(t, e, basicCopyCursor{family: 5}, 2)
	seedBasicCopy5Chapters(t, e, 10)
	chain := &basicControlChain{t: t, w: w, original: original, identity: identity}
	head := basicControlRequest{source: s, entityID: 31, shapeID: 61, family: 5, operation: basicControlCopyFamily}
	turn := func(revision int64, entry basicCopyCursor) (BasicResult, error) {
		in := head
		in.changeRevision, in.copy = revision, entry
		return chain.stage(in)
	}
	got, err := turn(2, basicCopyCursor{family: 5})
	if err != nil || got.Continuation != (BasicContinuation{NextFamily: 5, NextCopyID: 7, AfterEntity: 31}) {
		t.Fatal("first install turn", got, err)
	}
	var installed int64
	if err := e.db.QueryRowContext(t.Context(), `SELECT count(*) FROM asset_chapters WHERE asset_id='basic-asset'`).Scan(&installed); err != nil || installed != 9 {
		t.Fatal("first install batch", installed, err)
	}
	got, err = turn(3, basicCopyCursor{family: 5, id: 7})
	if err != nil || got.Continuation != (BasicContinuation{NextFamily: 5, NextCopyPhase: 1, AfterEntity: 31}) {
		t.Fatal("second install turn", got, err)
	}
	got, err = turn(4, basicCopyCursor{family: 5, phase: 1})
	if err != nil || got.Continuation != (BasicContinuation{NextFamily: 5, NextCopyPhase: 1, NextCopyID: 7, NextCopyAux: 1, AfterEntity: 31}) {
		t.Fatal("first cleanup turn", got, err)
	}
	got, err = turn(5, basicCopyCursor{family: 5, phase: 1, id: 7, aux: 1})
	if err != nil || got.Continuation != (BasicContinuation{NextFamily: 6, AfterEntity: 31}) {
		t.Fatal("second cleanup turn", got, err)
	}
	basicDispatchReturned(t, e, authority)
	rows, err := e.db.QueryContext(t.Context(), `SELECT chapter_index,title,start_seconds,end_seconds FROM asset_chapters WHERE asset_id='basic-asset' ORDER BY chapter_index`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var seen int
	for rows.Next() {
		var index int64
		var title string
		var start, end float64
		if err := rows.Scan(&index, &title, &start, &end); err != nil {
			t.Fatal(err)
		}
		if index != int64(seen) || title != fmt.Sprintf("C%d", seen) || start != float64(seen*10) || end != float64(seen*10+5) {
			t.Fatal("canonical chapter mismatch", index, title, start, end, seen)
		}
		seen++
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if seen != 10 {
		t.Fatal("canonical chapter count", seen)
	}
	var obsolete int64
	if err := e.db.QueryRowContext(t.Context(), `SELECT count(*) FROM asset_chapters WHERE asset_id='basic-asset' AND chapter_index=20`).Scan(&obsolete); err != nil || obsolete != 0 {
		t.Fatal("obsolete chapter survived", obsolete, err)
	}
	var phase, family, id, aux, revision int64
	if err := e.db.QueryRowContext(t.Context(), `SELECT copy_phase,copy_family,copy_id,copy_aux,job_revision FROM catalog_info_changes WHERE id=51`).Scan(&phase, &family, &id, &aux, &revision); err != nil ||
		phase != 0 || family != 6 || id != 0 || aux != 0 || revision != 6 {
		t.Fatal("chapter copy post-state", phase, family, id, aux, revision, err)
	}
	if _, err := turn(6, basicCopyCursor{family: 5, phase: 1}); err == nil || errors.Is(err, ErrInvalid) {
		t.Fatal("stale chapter entry was not refused", err)
	}
	basicDispatchReturned(t, e, authority)
	if err := e.db.QueryRowContext(t.Context(), `SELECT count(*) FROM asset_chapters`).Scan(&seen); err != nil || seen != 10 {
		t.Fatal("refusal mutated canonical chapters", seen, err)
	}
	if err := e.db.QueryRowContext(t.Context(), `SELECT job_revision FROM catalog_info_changes WHERE id=51`).Scan(&revision); err != nil || revision != 6 {
		t.Fatal("refusal moved the change", revision, err)
	}
}

// A staged chapter without its canonical per-asset fact parent is refused with
// no chapter-row effect.
func TestBasicCopyFamily5RefusesMissingFactParent(t *testing.T) {
	w, e, authority, s, original, identity := basicControlDispatchFixture(t)
	seedBasicFirstOrdinaryOwner(t, e)
	seedBasicCopy5First(t, e, basicCopyCursor{family: 5}, 2)
	if _, err := e.db.ExecContext(t.Context(), `INSERT INTO catalog_info_new_chapter_facts(change_id,input_ordinal,asset_id,revision,size,modified_ns,status,fingerprint) VALUES(51,0,'basic-asset',31,42,-11,'known','chapters:31')`); err != nil {
		t.Fatal(err)
	}
	if _, err := e.db.ExecContext(t.Context(), `INSERT INTO catalog_info_new_chapters(change_id,input_ordinal,asset_id,chapter_index,title,start_seconds,end_seconds) VALUES(51,0,'basic-asset',0,'Opening',0,3)`); err != nil {
		t.Fatal(err)
	}
	chain := &basicControlChain{t: t, w: w, original: original, identity: identity}
	in := basicControlRequest{source: s, entityID: 31, shapeID: 61, family: 5, operation: basicControlCopyFamily,
		changeRevision: 2, copy: basicCopyCursor{family: 5}}
	if _, err := chain.stage(in); !errors.Is(err, ErrConflict) {
		t.Fatal("missing chapter-fact parent was not refused", err)
	}
	basicDispatchReturned(t, e, authority)
	var chapters, revision int64
	if err := e.db.QueryRowContext(t.Context(), `SELECT count(*) FROM asset_chapters`).Scan(&chapters); err != nil || chapters != 0 {
		t.Fatal("refusal created a chapter row", chapters, err)
	}
	if err := e.db.QueryRowContext(t.Context(), `SELECT job_revision FROM catalog_info_changes WHERE id=51`).Scan(&revision); err != nil || revision != 2 {
		t.Fatal("refusal moved the change", revision, err)
	}
}
