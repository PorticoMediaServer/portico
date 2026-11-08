package writer

import (
	"errors"
	"fmt"
	"testing"
)

// seedBasicCopyHeader puts the change at phase 2 with the frozen target and the
// requested copy family and job_revision.
func seedBasicCopyHeader(t *testing.T, e *executor, family, revision int64) {
	t.Helper()
	for _, q := range []string{
		`INSERT INTO catalog_info_shapes(id,container,video_codec,height,ref_count) VALUES(61,'mkv','h264',1080,1)`,
		fmt.Sprintf(`UPDATE catalog_info_changes SET family_mask=511,target_shape_id=61,target_available=1,phase=2,has_after=1,after_entity=31,upper_entity=31,copy_family=%d,job_revision=%d WHERE id=51`, family, revision),
	} {
		if _, err := e.db.ExecContext(t.Context(), q); err != nil {
			t.Fatal(q, err)
		}
	}
}

// basicCopyFamilyRequest is one copy turn of family at the exact entry cursor.
func basicCopyFamilyRequest(s basicStreamFactStage, family, revision int64, entry basicCopyCursor) basicControlRequest {
	return basicControlRequest{source: s, changeRevision: revision, entityID: 31, shapeID: 61,
		operation: basicControlCopyFamily, family: family, copy: entry}
}

// readBasicCopyState reads the frozen cursor and entity continuation of a change.
func readBasicCopyState(t *testing.T, e *executor) (phase, family, copyPhase, id, aux, after, upper, hasAfter, revision int64, key, key2, key3 string) {
	t.Helper()
	if err := e.db.QueryRowContext(t.Context(), `SELECT phase,copy_family,copy_phase,copy_id,copy_aux,copy_key,copy_key2,copy_key3,after_entity,upper_entity,has_after,job_revision FROM catalog_info_changes WHERE id=51`).
		Scan(&phase, &family, &copyPhase, &id, &aux, &key, &key2, &key3, &after, &upper, &hasAfter, &revision); err != nil {
		t.Fatal(err)
	}
	return
}

func TestBasicCopyFamily6Dispatch(t *testing.T) {
	t.Run("install_replaces_canonical", func(t *testing.T) {
		w, e, authority, s, original, identity := basicControlDispatchFixture(t)
		seedBasicFirstOrdinaryOwner(t, e)
		seedBasicCopyHeader(t, e, 6, 4)
		for _, q := range []string{
			`INSERT INTO asset_subtitle_facts(asset_id,revision,size,modified_ns,origin_us,timing_known,status,fingerprint) VALUES('basic-asset',30,41,-10,0,1,'known','subtitle-old')`,
			`INSERT INTO catalog_info_new_subtitle_facts(change_id,input_ordinal,asset_id,revision,size,modified_ns,origin_us,timing_known,status,fingerprint) VALUES(51,0,'basic-asset',31,42,-11,0,1,'known','subtitles:31')`,
		} {
			if _, err := e.db.ExecContext(t.Context(), q); err != nil {
				t.Fatal(q, err)
			}
		}
		chain := &basicControlChain{t: t, w: w, original: original, identity: identity}
		got, err := chain.stage(basicCopyFamilyRequest(s, 6, 4, basicCopyCursor{family: 6}))
		if err != nil || got.Continuation != (BasicContinuation{NextFamily: 7, AfterEntity: 31}) {
			t.Fatal("copy family 6", got, err)
		}
		basicDispatchReturned(t, e, authority)
		var revision, size, modified, originUS, timing, hasEvidence int64
		var status, fingerprint string
		if err := e.db.QueryRowContext(t.Context(), `SELECT revision,size,modified_ns,origin_us,timing_known,status,fingerprint,has_subtitle_inventory_evidence FROM asset_subtitle_facts WHERE asset_id='basic-asset'`).
			Scan(&revision, &size, &modified, &originUS, &timing, &status, &fingerprint, &hasEvidence); err != nil ||
			revision != 31 || size != 42 || modified != -11 || originUS != 0 || timing != 1 || status != "known" || fingerprint != "subtitles:31" || hasEvidence != 0 {
			t.Fatal("canonical family-6 row", revision, size, modified, originUS, timing, status, fingerprint, hasEvidence, err)
		}
		phase, family, copyPhase, id, aux, after, upper, hasAfter, jobRevision, key, key2, key3 := readBasicCopyState(t, e)
		if phase != 2 || family != 7 || copyPhase != 0 || id != 0 || aux != 0 || after != 31 || upper != 31 || hasAfter != 1 || jobRevision != 5 ||
			key != "" || key2 != "" || key3 != "" {
			t.Fatal("family-6 post-state", phase, family, copyPhase, id, aux, key, key2, key3, after, upper, hasAfter, jobRevision)
		}
		// The cursor already moved; the same entry at the old revision is refused.
		if _, err := chain.stage(basicCopyFamilyRequest(s, 6, 4, basicCopyCursor{family: 6})); err == nil {
			t.Fatal("stale family-6 entry was not refused")
		}
	})

	t.Run("child_veto_then_delete", func(t *testing.T) {
		w, e, authority, s, original, identity := basicControlDispatchFixture(t)
		seedBasicFirstOrdinaryOwner(t, e)
		seedBasicCopyHeader(t, e, 6, 4)
		for _, q := range []string{
			`INSERT INTO asset_subtitle_facts(asset_id,revision,size,modified_ns,origin_us,timing_known,status,fingerprint) VALUES('basic-asset',30,41,-10,0,1,'known','subtitle-old')`,
			`INSERT INTO asset_subtitles(id,asset_id,origin,locator,stream_index,sidecar_id,format,language,title,is_default,is_forced,reason,hearing_impaired) VALUES('child-1','basic-asset','embedded','1',1,NULL,'subrip','en','English',0,0,'',0)`,
		} {
			if _, err := e.db.ExecContext(t.Context(), q); err != nil {
				t.Fatal(q, err)
			}
		}
		chain := &basicControlChain{t: t, w: w, original: original, identity: identity}
		if _, err := chain.stage(basicCopyFamilyRequest(s, 6, 4, basicCopyCursor{family: 6})); !errors.Is(err, ErrConflict) {
			t.Fatal("child veto was not refused", err)
		}
		basicDispatchReturned(t, e, authority)
		if phase, family, _, _, _, after, _, _, revision, _, _, _ := readBasicCopyState(t, e); phase != 2 || family != 6 || after != 31 || revision != 4 {
			t.Fatal("refused family-6 turn moved the change", phase, family, after, revision)
		}
		var facts int64
		if err := e.db.QueryRowContext(t.Context(), `SELECT count(*) FROM asset_subtitle_facts WHERE asset_id='basic-asset'`).Scan(&facts); err != nil || facts != 1 {
			t.Fatal("refused family-6 turn deleted the fact", facts, err)
		}
		// With the only child gone the same obsolete canonical row is deleted.
		if _, err := e.db.ExecContext(t.Context(), `DELETE FROM asset_subtitles WHERE id='child-1'`); err != nil {
			t.Fatal(err)
		}
		got, err := chain.stage(basicCopyFamilyRequest(s, 6, 4, basicCopyCursor{family: 6}))
		if err != nil || got.Continuation != (BasicContinuation{NextFamily: 7, AfterEntity: 31}) {
			t.Fatal("family-6 delete", got, err)
		}
		basicDispatchReturned(t, e, authority)
		if err := e.db.QueryRowContext(t.Context(), `SELECT count(*) FROM asset_subtitle_facts WHERE asset_id='basic-asset'`).Scan(&facts); err != nil || facts != 0 {
			t.Fatal("obsolete family-6 row survived", facts, err)
		}
	})

	t.Run("stale_revision_is_refused", func(t *testing.T) {
		w, e, authority, s, original, identity := basicControlDispatchFixture(t)
		seedBasicFirstOrdinaryOwner(t, e)
		seedBasicCopyHeader(t, e, 6, 4)
		for _, q := range []string{
			`INSERT INTO asset_subtitle_facts(asset_id,revision,size,modified_ns,origin_us,timing_known,status,fingerprint) VALUES('basic-asset',31,41,-10,0,1,'known','subtitle-old')`,
			`INSERT INTO catalog_info_new_subtitle_facts(change_id,input_ordinal,asset_id,revision,size,modified_ns,origin_us,timing_known,status,fingerprint) VALUES(51,0,'basic-asset',30,42,-11,0,1,'known','subtitles:30')`,
		} {
			if _, err := e.db.ExecContext(t.Context(), q); err != nil {
				t.Fatal(q, err)
			}
		}
		chain := &basicControlChain{t: t, w: w, original: original, identity: identity}
		if _, err := chain.stage(basicCopyFamilyRequest(s, 6, 4, basicCopyCursor{family: 6})); !errors.Is(err, ErrConflict) {
			t.Fatal("older staged revision was not refused", err)
		}
		basicDispatchReturned(t, e, authority)
		var revision int64
		var fingerprint string
		if err := e.db.QueryRowContext(t.Context(), `SELECT revision,fingerprint FROM asset_subtitle_facts WHERE asset_id='basic-asset'`).Scan(&revision, &fingerprint); err != nil || revision != 31 || fingerprint != "subtitle-old" {
			t.Fatal("refused stale family-6 install changed the row", revision, fingerprint, err)
		}
	})
}
