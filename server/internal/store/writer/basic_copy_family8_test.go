package writer

import (
	"errors"
	"fmt"
	"testing"
)

// Family 8 installs staged tracks by input_ordinal (resolving shared sidecars to
// their canonical id and keeping a kept track's canonical id), then cleans up
// canonical tracks the staged identity set no longer names.
func TestBasicCopyFamily8Dispatch(t *testing.T) {
	t.Run("install_then_cleanup", func(t *testing.T) {
		w, e, authority, s, original, identity := basicControlDispatchFixture(t)
		seedBasicFirstOrdinaryOwner(t, e)
		seedBasicCopyHeader(t, e, 8, 4)
		for _, q := range []string{
			`INSERT INTO asset_subtitle_facts(asset_id,revision,size,modified_ns,origin_us,timing_known,status,fingerprint) VALUES('basic-asset',31,42,-11,0,1,'known','subtitles:31')`,
			`INSERT INTO catalog_info_new_subtitle_facts(change_id,input_ordinal,asset_id,revision,size,modified_ns,origin_us,timing_known,status,fingerprint) VALUES(51,0,'basic-asset',31,42,-11,0,1,'known','subtitles:31')`,
			`INSERT INTO catalog_info_new_subtitle_sidecars(change_id,input_ordinal,id,path,size,modified_ns,digest) VALUES(51,0,'sidecar-1','/media/Arrival.srt',30,-7,'sidecar:30')`,
			`INSERT INTO subtitle_sidecars(id,path,size,modified_ns,digest) VALUES('canonical-sidecar-1','/media/Arrival.srt',30,-7,'sidecar:30')`,
			`INSERT INTO catalog_info_new_subtitles(change_id,input_ordinal,id,asset_id,origin,locator,stream_index,sidecar_id,format,language,title,is_default,is_forced,reason,hearing_impaired) VALUES(51,0,'staged-1','basic-asset','embedded','2',2,NULL,'subrip','en','English',0,0,'',0)`,
			`INSERT INTO catalog_info_new_subtitles(change_id,input_ordinal,id,asset_id,origin,locator,stream_index,sidecar_id,format,language,title,is_default,is_forced,reason,hearing_impaired) VALUES(51,1,'staged-sidecar-track','basic-asset','sidecar','/media/Arrival.srt',-1,'sidecar-1','subrip','fr','French',0,0,'',0)`,
			`INSERT INTO asset_subtitles(id,asset_id,origin,locator,stream_index,sidecar_id,format,language,title,is_default,is_forced,reason,hearing_impaired) VALUES('keep-1','basic-asset','embedded','2',2,NULL,'old','en','Old',0,0,'',0)`,
			`INSERT INTO asset_subtitles(id,asset_id,origin,locator,stream_index,sidecar_id,format,language,title,is_default,is_forced,reason,hearing_impaired) VALUES('stale-1','basic-asset','embedded','stale',1,NULL,'old','en','Stale',0,0,'',0)`,
		} {
			if _, err := e.db.ExecContext(t.Context(), q); err != nil {
				t.Fatal(q, err)
			}
		}
		chain := &basicControlChain{t: t, w: w, original: original, identity: identity}
		got, err := chain.stage(basicCopyFamilyRequest(s, 8, 4, basicCopyCursor{family: 8}))
		if err != nil || got.Continuation != (BasicContinuation{NextFamily: 8, NextCopyPhase: 1, AfterEntity: 31}) {
			t.Fatal("family-8 install", got, err)
		}
		// The install keeps the canonical id, resolves the sidecar and leaves
		// cleanup for the next turn.
		var format, title, sidecarID string
		if err := e.db.QueryRowContext(t.Context(), `SELECT format,title FROM asset_subtitles WHERE id='keep-1'`).Scan(&format, &title); err != nil || format != "subrip" || title != "English" {
			t.Fatal("kept track was not updated in place", format, title, err)
		}
		if err := e.db.QueryRowContext(t.Context(), `SELECT sidecar_id FROM asset_subtitles WHERE id='staged-sidecar-track'`).Scan(&sidecarID); err != nil || sidecarID != "canonical-sidecar-1" {
			t.Fatal("sidecar reference was not resolved to the canonical id", sidecarID, err)
		}
		phase, family, copyPhase, id, aux, after, _, _, revision, key, _, _ := readBasicCopyState(t, e)
		if phase != 2 || family != 8 || copyPhase != 1 || id != 0 || aux != 0 || key != "" || after != 31 || revision != 5 {
			t.Fatal("family-8 install checkpoint", phase, family, copyPhase, id, aux, key, after, revision)
		}
		var stale int64
		if err := e.db.QueryRowContext(t.Context(), `SELECT count(*) FROM asset_subtitles WHERE id='stale-1'`).Scan(&stale); err != nil || stale != 1 {
			t.Fatal("cleanup ran inside the install turn", stale, err)
		}
		got, err = chain.stage(basicCopyFamilyRequest(s, 8, 5, basicCopyCursor{family: 8, phase: 1}))
		if err != nil || got.Continuation != (BasicContinuation{NextFamily: 9, AfterEntity: 31}) {
			t.Fatal("family-8 cleanup", got, err)
		}
		basicDispatchReturned(t, e, authority)
		var rows int64
		if err := e.db.QueryRowContext(t.Context(), `SELECT count(*) FROM asset_subtitles WHERE asset_id='basic-asset'`).Scan(&rows); err != nil || rows != 2 {
			t.Fatal("family-8 canonical population", rows, err)
		}
		if err := e.db.QueryRowContext(t.Context(), `SELECT count(*) FROM asset_subtitles WHERE id='stale-1'`).Scan(&stale); err != nil || stale != 0 {
			t.Fatal("obsolete track survived cleanup", stale, err)
		}
		if err := e.db.QueryRowContext(t.Context(), `SELECT count(*) FROM asset_subtitles WHERE id IN('keep-1','staged-sidecar-track')`).Scan(&rows); err != nil || rows != 2 {
			t.Fatal("staged identities were not kept", rows, err)
		}
		phase, family, copyPhase, id, aux, after, _, _, revision, _, _, _ = readBasicCopyState(t, e)
		if phase != 2 || family != 9 || copyPhase != 0 || id != 0 || aux != 0 || after != 31 || revision != 6 {
			t.Fatal("family-8 completion", phase, family, copyPhase, id, aux, after, revision)
		}
		if _, err := chain.stage(basicCopyFamilyRequest(s, 8, 5, basicCopyCursor{family: 8, phase: 1})); err == nil {
			t.Fatal("stale family-8 cleanup entry was not refused")
		}
	})

	t.Run("cleanup_cursor_across_turns", func(t *testing.T) {
		w, e, authority, s, original, identity := basicControlDispatchFixture(t)
		seedBasicFirstOrdinaryOwner(t, e)
		seedBasicCopyHeader(t, e, 8, 4)
		if _, err := e.db.ExecContext(t.Context(), `UPDATE catalog_info_changes SET copy_phase=1 WHERE id=51`); err != nil {
			t.Fatal(err)
		}
		if _, err := e.db.ExecContext(t.Context(), `INSERT INTO asset_subtitle_facts(asset_id,revision,size,modified_ns,origin_us,timing_known,status,fingerprint) VALUES('basic-asset',31,42,-11,0,1,'known','subtitles:31')`); err != nil {
			t.Fatal(err)
		}
		for n := 0; n < 10; n++ {
			if _, err := e.db.ExecContext(t.Context(), fmt.Sprintf(
				`INSERT INTO asset_subtitles(id,asset_id,origin,locator,stream_index,sidecar_id,format,language,title,is_default,is_forced,reason,hearing_impaired) VALUES('stale-%d','basic-asset','embedded','%d',1,NULL,'subrip','en','Stale',0,0,'',0)`, n, n)); err != nil {
				t.Fatal(err)
			}
		}
		chain := &basicControlChain{t: t, w: w, original: original, identity: identity}
		got, err := chain.stage(basicCopyFamilyRequest(s, 8, 4, basicCopyCursor{family: 8, phase: 1}))
		if err != nil || got.Continuation != (BasicContinuation{NextFamily: 8, NextCopyPhase: 1, NextCopyAux: 1, NextCopyKey: "stale-7", AfterEntity: 31}) {
			t.Fatal("family-8 cleanup prefix", got, err)
		}
		phase, family, copyPhase, id, aux, after, _, _, revision, key, _, _ := readBasicCopyState(t, e)
		if phase != 2 || family != 8 || copyPhase != 1 || id != 0 || aux != 1 || key != "stale-7" || after != 31 || revision != 5 {
			t.Fatal("family-8 cleanup checkpoint", phase, family, copyPhase, id, aux, key, after, revision)
		}
		var remaining int64
		if err := e.db.QueryRowContext(t.Context(), `SELECT count(*) FROM asset_subtitles WHERE asset_id='basic-asset'`).Scan(&remaining); err != nil || remaining != 2 {
			t.Fatal("family-8 first cleanup turn bound", remaining, err)
		}
		got, err = chain.stage(basicCopyFamilyRequest(s, 8, 5, basicCopyCursor{family: 8, phase: 1, aux: 1, key: "stale-7"}))
		if err != nil || got.Continuation != (BasicContinuation{NextFamily: 9, AfterEntity: 31}) {
			t.Fatal("family-8 cleanup remainder", got, err)
		}
		basicDispatchReturned(t, e, authority)
		if err := e.db.QueryRowContext(t.Context(), `SELECT count(*) FROM asset_subtitles WHERE asset_id='basic-asset'`).Scan(&remaining); err != nil || remaining != 0 {
			t.Fatal("family-8 cleanup remainder", remaining, err)
		}
	})

	t.Run("install_requires_fact", func(t *testing.T) {
		w, e, authority, s, original, identity := basicControlDispatchFixture(t)
		seedBasicFirstOrdinaryOwner(t, e)
		seedBasicCopyHeader(t, e, 8, 4)
		for _, q := range []string{
			`INSERT INTO catalog_info_new_subtitle_facts(change_id,input_ordinal,asset_id,revision,size,modified_ns,origin_us,timing_known,status,fingerprint) VALUES(51,0,'basic-asset',31,42,-11,0,1,'known','subtitles:31')`,
			`INSERT INTO catalog_info_new_subtitles(change_id,input_ordinal,id,asset_id,origin,locator,stream_index,sidecar_id,format,language,title,is_default,is_forced,reason,hearing_impaired) VALUES(51,0,'staged-1','basic-asset','embedded','2',2,NULL,'subrip','en','English',0,0,'',0)`,
		} {
			if _, err := e.db.ExecContext(t.Context(), q); err != nil {
				t.Fatal(q, err)
			}
		}
		chain := &basicControlChain{t: t, w: w, original: original, identity: identity}
		if _, err := chain.stage(basicCopyFamilyRequest(s, 8, 4, basicCopyCursor{family: 8})); !errors.Is(err, ErrConflict) {
			t.Fatal("missing canonical fact was not refused", err)
		}
		basicDispatchReturned(t, e, authority)
		var rows int64
		if err := e.db.QueryRowContext(t.Context(), `SELECT count(*) FROM asset_subtitles`).Scan(&rows); err != nil || rows != 0 {
			t.Fatal("refused family-8 install wrote tracks", rows, err)
		}
		if phase, family, _, _, _, after, _, _, revision, _, _, _ := readBasicCopyState(t, e); phase != 2 || family != 8 || after != 31 || revision != 4 {
			t.Fatal("refused family-8 install moved the change", phase, family, after, revision)
		}
	})
}
