package writer

import (
	"database/sql"
	"errors"
	"testing"
)

// Family 9 installs the one-per-asset subtitle inventory evidence (folded into
// asset_subtitle_facts in schema v1) and completes the copy: the change moves
// from phase 2 to phase 3 with a zeroed cursor and the entity unchanged.
func TestBasicCopyFamily9Dispatch(t *testing.T) {
	t.Run("install_completes_copy", func(t *testing.T) {
		w, e, authority, s, original, identity := basicControlDispatchFixture(t)
		seedBasicFirstOrdinaryOwner(t, e)
		seedBasicCopyHeader(t, e, 9, 4)
		for _, q := range []string{
			`INSERT INTO asset_subtitle_facts(asset_id,revision,size,modified_ns,origin_us,timing_known,status,fingerprint) VALUES('basic-asset',31,42,-11,0,1,'known','subtitles:31')`,
			`INSERT INTO catalog_info_new_subtitle_inventory_evidence(change_id,input_ordinal,asset_id,revision,evidence) VALUES(51,0,'basic-asset',31,'inventory:31')`,
		} {
			if _, err := e.db.ExecContext(t.Context(), q); err != nil {
				t.Fatal(q, err)
			}
		}
		chain := &basicControlChain{t: t, w: w, original: original, identity: identity}
		got, err := chain.stage(basicCopyFamilyRequest(s, 9, 4, basicCopyCursor{family: 9}))
		if err != nil || got.Continuation != (BasicContinuation{AfterEntity: 31}) {
			t.Fatal("family-9 completion", got, err)
		}
		basicDispatchReturned(t, e, authority)
		phase, family, copyPhase, id, aux, after, upper, hasAfter, revision, key, key2, key3 := readBasicCopyState(t, e)
		if phase != 3 || family != 0 || copyPhase != 0 || id != 0 || aux != 0 || after != 31 || upper != 31 || hasAfter != 1 || revision != 5 ||
			key != "" || key2 != "" || key3 != "" {
			t.Fatal("family-9 post-state", phase, family, copyPhase, id, aux, key, key2, key3, after, upper, hasAfter, revision)
		}
		var has int64
		var evidenceRevision sql.NullInt64
		var evidence sql.NullString
		if err := e.db.QueryRowContext(t.Context(), `SELECT has_subtitle_inventory_evidence,inventory_revision,inventory_evidence FROM asset_subtitle_facts WHERE asset_id='basic-asset'`).
			Scan(&has, &evidenceRevision, &evidence); err != nil || has != 1 || !evidenceRevision.Valid || evidenceRevision.Int64 != 31 ||
			!evidence.Valid || evidence.String != "inventory:31" {
			t.Fatal("folded family-9 evidence", has, evidenceRevision, evidence, err)
		}
		// Replaying the completed entry is refused and leaves phase 3 intact.
		if _, err := chain.stage(basicCopyFamilyRequest(s, 9, 4, basicCopyCursor{family: 9})); err == nil {
			t.Fatal("stale family-9 entry was not refused")
		}
		if phase, family, _, _, _, _, _, _, revision, _, _, _ := readBasicCopyState(t, e); phase != 3 || family != 0 || revision != 5 {
			t.Fatal("refused family-9 replay moved the change", phase, family, revision)
		}
	})

	t.Run("exact_evidence_still_completes", func(t *testing.T) {
		w, e, _, s, original, identity := basicControlDispatchFixture(t)
		seedBasicFirstOrdinaryOwner(t, e)
		seedBasicCopyHeader(t, e, 9, 4)
		for _, q := range []string{
			`INSERT INTO asset_subtitle_facts(asset_id,revision,size,modified_ns,origin_us,timing_known,status,fingerprint,has_subtitle_inventory_evidence,inventory_revision,inventory_evidence) VALUES('basic-asset',31,42,-11,0,1,'known','subtitles:31',1,31,'inventory:31')`,
			`INSERT INTO catalog_info_new_subtitle_inventory_evidence(change_id,input_ordinal,asset_id,revision,evidence) VALUES(51,0,'basic-asset',31,'inventory:31')`,
		} {
			if _, err := e.db.ExecContext(t.Context(), q); err != nil {
				t.Fatal(q, err)
			}
		}
		chain := &basicControlChain{t: t, w: w, original: original, identity: identity}
		if got, err := chain.stage(basicCopyFamilyRequest(s, 9, 4, basicCopyCursor{family: 9})); err != nil || got.Continuation != (BasicContinuation{AfterEntity: 31}) {
			t.Fatal("exact family-9 completion", got, err)
		}
		var has, evidenceRevision int64
		var evidence string
		if err := e.db.QueryRowContext(t.Context(), `SELECT has_subtitle_inventory_evidence,inventory_revision,inventory_evidence FROM asset_subtitle_facts WHERE asset_id='basic-asset'`).
			Scan(&has, &evidenceRevision, &evidence); err != nil || has != 1 || evidenceRevision != 31 || evidence != "inventory:31" {
			t.Fatal("exact family-9 evidence", has, evidenceRevision, evidence, err)
		}
	})

	t.Run("staged_revision_must_match_fact", func(t *testing.T) {
		w, e, authority, s, original, identity := basicControlDispatchFixture(t)
		seedBasicFirstOrdinaryOwner(t, e)
		seedBasicCopyHeader(t, e, 9, 4)
		for _, q := range []string{
			`INSERT INTO asset_subtitle_facts(asset_id,revision,size,modified_ns,origin_us,timing_known,status,fingerprint) VALUES('basic-asset',32,42,-11,0,1,'known','subtitles:32')`,
			`INSERT INTO catalog_info_new_subtitle_inventory_evidence(change_id,input_ordinal,asset_id,revision,evidence) VALUES(51,0,'basic-asset',31,'inventory:31')`,
		} {
			if _, err := e.db.ExecContext(t.Context(), q); err != nil {
				t.Fatal(q, err)
			}
		}
		chain := &basicControlChain{t: t, w: w, original: original, identity: identity}
		if _, err := chain.stage(basicCopyFamilyRequest(s, 9, 4, basicCopyCursor{family: 9})); !errors.Is(err, ErrConflict) {
			t.Fatal("mismatched evidence revision was not refused", err)
		}
		basicDispatchReturned(t, e, authority)
		var has int64
		var evidence sql.NullString
		if err := e.db.QueryRowContext(t.Context(), `SELECT has_subtitle_inventory_evidence,inventory_evidence FROM asset_subtitle_facts WHERE asset_id='basic-asset'`).Scan(&has, &evidence); err != nil || has != 0 || evidence.Valid {
			t.Fatal("refused family-9 install wrote evidence", has, evidence, err)
		}
		if phase, family, _, _, _, after, _, _, revision, _, _, _ := readBasicCopyState(t, e); phase != 2 || family != 9 || after != 31 || revision != 4 {
			t.Fatal("refused family-9 install moved the change", phase, family, after, revision)
		}
	})

	t.Run("unstaged_evidence_is_cleared", func(t *testing.T) {
		w, e, _, s, original, identity := basicControlDispatchFixture(t)
		seedBasicFirstOrdinaryOwner(t, e)
		seedBasicCopyHeader(t, e, 9, 4)
		if _, err := e.db.ExecContext(t.Context(), `INSERT INTO asset_subtitle_facts(asset_id,revision,size,modified_ns,origin_us,timing_known,status,fingerprint,has_subtitle_inventory_evidence,inventory_revision,inventory_evidence) VALUES('basic-asset',31,42,-11,0,1,'known','subtitles:31',1,30,'inventory:30')`); err != nil {
			t.Fatal(err)
		}
		chain := &basicControlChain{t: t, w: w, original: original, identity: identity}
		if got, err := chain.stage(basicCopyFamilyRequest(s, 9, 4, basicCopyCursor{family: 9})); err != nil || got.Continuation != (BasicContinuation{AfterEntity: 31}) {
			t.Fatal("family-9 clear completion", got, err)
		}
		var has int64
		var evidenceRevision sql.NullInt64
		var evidence sql.NullString
		if err := e.db.QueryRowContext(t.Context(), `SELECT has_subtitle_inventory_evidence,inventory_revision,inventory_evidence FROM asset_subtitle_facts WHERE asset_id='basic-asset'`).
			Scan(&has, &evidenceRevision, &evidence); err != nil || has != 0 || evidenceRevision.Valid || evidence.Valid {
			t.Fatal("obsolete evidence survived", has, evidenceRevision, evidence, err)
		}
		if phase, family, _, _, _, after, _, _, revision, _, _, _ := readBasicCopyState(t, e); phase != 3 || family != 0 || after != 31 || revision != 5 {
			t.Fatal("family-9 clear completion", phase, family, after, revision)
		}
	})
}
