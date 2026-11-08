package writer

import (
	"fmt"
	"testing"
)

// Family 7 installs shared sidecars by path in input_ordinal order, keeps a
// canonical id and never deletes: 10 staged rows span TWO turns with the
// last-installed ordinal as the checkpoint.
func TestBasicCopyFamily7Dispatch(t *testing.T) {
	w, e, authority, s, original, identity := basicControlDispatchFixture(t)
	seedBasicFirstOrdinaryOwner(t, e)
	seedBasicCopyHeader(t, e, 7, 4)
	for n := 0; n < 10; n++ {
		if _, err := e.db.ExecContext(t.Context(), fmt.Sprintf(
			`INSERT INTO catalog_info_new_subtitle_sidecars(change_id,input_ordinal,id,path,size,modified_ns,digest) VALUES(51,%d,'sidecar-%d','/media/s%d.srt',%d,-7,'sidecar:%d')`,
			n, n, n, 30+n, n)); err != nil {
			t.Fatal(err)
		}
	}
	for _, q := range []string{
		`INSERT INTO subtitle_sidecars(id,path,size,modified_ns,digest) VALUES('canonical-0','/media/s0.srt',11,-1,'stale')`,
		`INSERT INTO subtitle_sidecars(id,path,size,modified_ns,digest) VALUES('keep','/media/keep.srt',12,-2,'keep')`,
	} {
		if _, err := e.db.ExecContext(t.Context(), q); err != nil {
			t.Fatal(q, err)
		}
	}
	chain := &basicControlChain{t: t, w: w, original: original, identity: identity}
	got, err := chain.stage(basicCopyFamilyRequest(s, 7, 4, basicCopyCursor{family: 7}))
	if err != nil || got.Continuation != (BasicContinuation{NextFamily: 7, NextCopyPhase: 0, NextCopyID: 7, AfterEntity: 31}) {
		t.Fatal("family-7 first turn", got, err)
	}
	phase, family, copyPhase, id, aux, after, _, _, revision, key, key2, key3 := readBasicCopyState(t, e)
	if phase != 2 || family != 7 || copyPhase != 0 || id != 7 || aux != 0 || after != 31 || revision != 5 ||
		key != "" || key2 != "" || key3 != "" {
		t.Fatal("family-7 first checkpoint", phase, family, copyPhase, id, aux, key, key2, key3, after, revision)
	}
	got, err = chain.stage(basicCopyFamilyRequest(s, 7, 5, basicCopyCursor{family: 7, phase: 0, id: 7}))
	if err != nil || got.Continuation != (BasicContinuation{NextFamily: 8, AfterEntity: 31}) {
		t.Fatal("family-7 second turn", got, err)
	}
	basicDispatchReturned(t, e, authority)
	var canonicalID, digest string
	var size, modifiedNS int64
	if err := e.db.QueryRowContext(t.Context(), `SELECT id,size,modified_ns,digest FROM subtitle_sidecars WHERE path='/media/s0.srt'`).Scan(&canonicalID, &size, &modifiedNS, &digest); err != nil ||
		canonicalID != "canonical-0" || size != 30 || modifiedNS != -7 || digest != "sidecar:0" {
		t.Fatal("kept sidecar id/values", canonicalID, size, modifiedNS, digest, err)
	}
	var rows int64
	if err := e.db.QueryRowContext(t.Context(), `SELECT count(*) FROM subtitle_sidecars`).Scan(&rows); err != nil || rows != 11 {
		t.Fatal("sidecar population", rows, err)
	}
	if err := e.db.QueryRowContext(t.Context(), `SELECT count(*) FROM subtitle_sidecars WHERE path LIKE '/media/s%.srt'`).Scan(&rows); err != nil || rows != 10 {
		t.Fatal("installed sidecars", rows, err)
	}
	if err := e.db.QueryRowContext(t.Context(), `SELECT count(*) FROM subtitle_sidecars WHERE id='keep' AND digest='keep'`).Scan(&rows); err != nil || rows != 1 {
		t.Fatal("unstaged shared sidecar was deleted", rows, err)
	}
	phase, family, copyPhase, id, aux, after, _, _, revision, _, _, _ = readBasicCopyState(t, e)
	if phase != 2 || family != 8 || copyPhase != 0 || id != 0 || aux != 0 || after != 31 || revision != 6 {
		t.Fatal("family-7 completion", phase, family, copyPhase, id, aux, after, revision)
	}
	// A replayed entry cursor is refused and moves nothing.
	if _, err := chain.stage(basicCopyFamilyRequest(s, 7, 5, basicCopyCursor{family: 7, phase: 0, id: 7})); err == nil {
		t.Fatal("stale family-7 entry was not refused")
	}
	if phase, family, _, _, _, _, _, _, revision, _, _, _ := readBasicCopyState(t, e); phase != 2 || family != 8 || revision != 6 {
		t.Fatal("refused family-7 replay moved the change", phase, family, revision)
	}
}
