package writer

import (
	"errors"
	"testing"
)

func seedBasicCopy4First(t *testing.T, e *executor, revision int64) {
	t.Helper()
	if _, err := e.db.ExecContext(t.Context(), `INSERT INTO catalog_info_shapes(id,container,video_codec,height,ref_count) VALUES(61,'mkv','h264',1080,1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := e.db.ExecContext(t.Context(), `UPDATE catalog_info_changes SET phase=2,family_mask=511,target_shape_id=61,target_available=1,has_after=1,after_entity=31,upper_entity=31,copy_family=4,copy_phase=0,copy_id=0,copy_aux=0,copy_key='',copy_key2='',copy_key3='',job_revision=? WHERE id=51`, revision); err != nil {
		t.Fatal(err)
	}
}

func basicCopy4Turn(t *testing.T, chain *basicControlChain, s basicStreamFactStage, revision int64) (BasicResult, error) {
	t.Helper()
	return chain.stage(basicControlRequest{source: s, entityID: 31, shapeID: 61, family: 4, operation: basicControlCopyFamily,
		changeRevision: revision, copy: basicCopyCursor{family: 4}})
}

// Family 4 replaces the one per-asset chapter fact in a single turn and leaves
// the family-5 cursor behind.
func TestBasicCopyFamily4ChapterFactDispatch(t *testing.T) {
	w, e, authority, s, original, identity := basicControlDispatchFixture(t)
	seedBasicFirstOrdinaryOwner(t, e)
	seedBasicCopy4First(t, e, 2)
	for _, q := range []string{
		`INSERT INTO asset_chapter_facts(asset_id,revision,size,modified_ns,status,fingerprint) VALUES('basic-asset',10,42,-11,'known','old')`,
		`INSERT INTO catalog_info_new_chapter_facts(change_id,input_ordinal,asset_id,revision,size,modified_ns,status,fingerprint) VALUES(51,0,'basic-asset',12,43,-12,'known','new')`,
	} {
		if _, err := e.db.ExecContext(t.Context(), q); err != nil {
			t.Fatal(q, err)
		}
	}
	chain := &basicControlChain{t: t, w: w, original: original, identity: identity}
	got, err := basicCopy4Turn(t, chain, s, 2)
	if err != nil || got.Continuation != (BasicContinuation{NextFamily: 5, AfterEntity: 31}) {
		t.Fatal("chapter-fact install turn", got, err)
	}
	basicDispatchReturned(t, e, authority)
	var revision, size, modified int64
	var status, fingerprint string
	if err := e.db.QueryRowContext(t.Context(), `SELECT revision,size,modified_ns,status,fingerprint FROM asset_chapter_facts WHERE asset_id='basic-asset'`).Scan(&revision, &size, &modified, &status, &fingerprint); err != nil ||
		revision != 12 || size != 43 || modified != -12 || status != "known" || fingerprint != "new" {
		t.Fatal("installed chapter fact", revision, size, modified, status, fingerprint, err)
	}
	var phase, family, id, aux, jobRevision int64
	if err := e.db.QueryRowContext(t.Context(), `SELECT copy_phase,copy_family,copy_id,copy_aux,job_revision FROM catalog_info_changes WHERE id=51`).Scan(&phase, &family, &id, &aux, &jobRevision); err != nil ||
		phase != 0 || family != 5 || id != 0 || aux != 0 || jobRevision != 3 {
		t.Fatal("chapter-fact post-state", phase, family, id, aux, jobRevision, err)
	}
	if _, err := basicCopy4Turn(t, chain, s, 3); err == nil || errors.Is(err, ErrInvalid) {
		t.Fatal("stale chapter-fact entry was not refused", err)
	}
	basicDispatchReturned(t, e, authority)
}

// Family 4 removes a canonical fact the staged set does not name, but a
// surviving chapter child vetoes the delete and a backwards staged revision is
// refused; neither refusal has an effect.
func TestBasicCopyFamily4CleanupAndRefusals(t *testing.T) {
	t.Run("delete", func(t *testing.T) {
		w, e, authority, s, original, identity := basicControlDispatchFixture(t)
		seedBasicFirstOrdinaryOwner(t, e)
		seedBasicCopy4First(t, e, 2)
		if _, err := e.db.ExecContext(t.Context(), `INSERT INTO asset_chapter_facts(asset_id,revision,size,modified_ns,status,fingerprint) VALUES('basic-asset',10,42,-11,'known','old')`); err != nil {
			t.Fatal(err)
		}
		chain := &basicControlChain{t: t, w: w, original: original, identity: identity}
		got, err := basicCopy4Turn(t, chain, s, 2)
		if err != nil || got.Continuation != (BasicContinuation{NextFamily: 5, AfterEntity: 31}) {
			t.Fatal("chapter-fact cleanup turn", got, err)
		}
		basicDispatchReturned(t, e, authority)
		var facts int64
		if err := e.db.QueryRowContext(t.Context(), `SELECT count(*) FROM asset_chapter_facts`).Scan(&facts); err != nil || facts != 0 {
			t.Fatal("obsolete chapter fact survived", facts, err)
		}
	})
	t.Run("child_veto", func(t *testing.T) {
		w, e, authority, s, original, identity := basicControlDispatchFixture(t)
		seedBasicFirstOrdinaryOwner(t, e)
		seedBasicCopy4First(t, e, 2)
		for _, q := range []string{
			`INSERT INTO asset_chapter_facts(asset_id,revision,size,modified_ns,status,fingerprint) VALUES('basic-asset',10,42,-11,'known','old')`,
			`INSERT INTO asset_chapters(asset_id,chapter_index,title,start_seconds,end_seconds) VALUES('basic-asset',0,'Opening',0,3)`,
		} {
			if _, err := e.db.ExecContext(t.Context(), q); err != nil {
				t.Fatal(q, err)
			}
		}
		chain := &basicControlChain{t: t, w: w, original: original, identity: identity}
		if _, err := basicCopy4Turn(t, chain, s, 2); !errors.Is(err, ErrConflict) {
			t.Fatal("surviving chapter child did not veto the delete", err)
		}
		basicDispatchReturned(t, e, authority)
		var facts, chapters, revision int64
		if err := e.db.QueryRowContext(t.Context(), `SELECT count(*) FROM asset_chapter_facts`).Scan(&facts); err != nil || facts != 1 {
			t.Fatal("child veto deleted the fact", facts, err)
		}
		if err := e.db.QueryRowContext(t.Context(), `SELECT count(*) FROM asset_chapters`).Scan(&chapters); err != nil || chapters != 1 {
			t.Fatal("child veto deleted the child", chapters, err)
		}
		if err := e.db.QueryRowContext(t.Context(), `SELECT job_revision FROM catalog_info_changes WHERE id=51`).Scan(&revision); err != nil || revision != 2 {
			t.Fatal("child veto moved the change", revision, err)
		}
	})
	t.Run("staged_revision_backwards", func(t *testing.T) {
		w, e, authority, s, original, identity := basicControlDispatchFixture(t)
		seedBasicFirstOrdinaryOwner(t, e)
		seedBasicCopy4First(t, e, 2)
		for _, q := range []string{
			`INSERT INTO asset_chapter_facts(asset_id,revision,size,modified_ns,status,fingerprint) VALUES('basic-asset',12,43,-12,'known','new')`,
			`INSERT INTO catalog_info_new_chapter_facts(change_id,input_ordinal,asset_id,revision,size,modified_ns,status,fingerprint) VALUES(51,0,'basic-asset',10,42,-11,'known','old')`,
		} {
			if _, err := e.db.ExecContext(t.Context(), q); err != nil {
				t.Fatal(q, err)
			}
		}
		chain := &basicControlChain{t: t, w: w, original: original, identity: identity}
		if _, err := basicCopy4Turn(t, chain, s, 2); !errors.Is(err, ErrConflict) {
			t.Fatal("backwards staged revision was not refused", err)
		}
		basicDispatchReturned(t, e, authority)
		var revision int64
		var fingerprint string
		if err := e.db.QueryRowContext(t.Context(), `SELECT revision,fingerprint FROM asset_chapter_facts WHERE asset_id='basic-asset'`).Scan(&revision, &fingerprint); err != nil ||
			revision != 12 || fingerprint != "new" {
			t.Fatal("backwards install rewrote the fact", revision, fingerprint, err)
		}
		if err := e.db.QueryRowContext(t.Context(), `SELECT job_revision FROM catalog_info_changes WHERE id=51`).Scan(&revision); err != nil || revision != 2 {
			t.Fatal("backwards install moved the change", revision, err)
		}
	})
}
