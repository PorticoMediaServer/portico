package writer

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/searchpolicy"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/stmttest"
)

const (
	iaExtraTestLibraryID      int64 = 7
	iaExtraTestLibraryKey           = "extra-library"
	iaExtraTestSourceID             = "extra-source"
	iaExtraTestRunID                = "extra-run"
	iaExtraTestAssetID        int64 = 41
	iaExtraTestRoot                 = "/media/extra"
	iaExtraTestObjectID             = "object:extra:1"
	iaExtraTestObjectRevision       = "object-revision:1"
	iaExtraTestOwnerID        int64 = 60
)

// One private W frame, the frozen clock and the exact group C draft subset. The
// fixture seeds only prepared source/run/asset/search witnesses; it is not a
// qualified population, admission profile or product wiring.
func iaExtraAcceptFixture(t *testing.T) (*executor, *iaEntryRegistry, *usOwnerAuthorityProof) {
	t.Helper()
	w, keeper, proof := iaEntrySecurityFixture(t)
	ddl, err := os.ReadFile("testdata/ia-episode-accept-draft.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = w.db.ExecContext(t.Context(), string(ddl)); err != nil {
		t.Fatal(err)
	}
	for _, query := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO catalog_kinds(id,name,playable,container,searchable,browsable,listening) VALUES(1,'movie',1,0,1,1,0),(2,'show',0,1,1,1,0),(3,'season',0,1,1,1,0),(4,'episode',1,0,1,1,0),(11,'extra',1,0,1,1,0)`, nil},
		{`INSERT INTO libraries(id,name,kind,root,configuration_present,configuration_revision,configuration_metadata_revision) VALUES('extra-library','Extra','movies','/media/extra',1,1,1)`, nil},
		{`INSERT INTO catalog_libraries(id,library_id,root) VALUES(7,'extra-library','/media/extra')`, nil},
		{`INSERT INTO library_sources(id,library_id,name,configured_root,root,classification,root_identity,follow_symlinks,interval_seconds,next_scan_at,identity_confirmed,incarnation,generation,health,enabled,kind) VALUES('extra-source','extra-library','Extra','/media/extra','/media/extra','local','extra-root-identity',0,0,'',1,'extra-incarnation',3,'ready',1,'local')`, nil},
		{`INSERT INTO jobs(id,library_id,status,processed,created_at) VALUES('extra-run','extra-library','running',0,'')`, nil},
		{`INSERT INTO inventory_runs(job_id,original_library_id,original_library_key,original_resolved_root,original_follow_symlinks,create_operation_id,create_request_digest,source_id,source_generation,root_incarnation,root_identity,policy_revision,phase,scan_mode,job_lease_owner,job_lease_generation,job_lease_expires_ms,job_revision,command_incarnation,last_command_sequence,acknowledged_command_sequence) VALUES('extra-run',7,'extra-library','/media/extra',0,?,?,'extra-source',3,'extra-incarnation','extra-root-identity',4,'inventory','first-import',11,13,?,5,?,1,1)`,
			[]any{iaEpisodeBytes(3), make([]byte, 32), w.now().Add(time.Hour).UnixMilli(), iaEpisodeBytes(1)}},
		{`INSERT INTO inventory_directories(job_id,relative_path,identity,revision,cursor,state,pages,skipped) VALUES('extra-run','Extras','extra-directory-identity','extra-directory-revision','','pending',2,0)`, nil},
		{`INSERT INTO catalog_assets(id,token,path,size,modified_ns,container,video_codec,audio_codec,width,height,duration,library_id) VALUES(41,'extra-token','/media/extra/Extras/Trailer.mkv',42,7,'','','',0,0,0,7)`, nil},
		{`INSERT INTO search_generations(id,bank_key,recipe_version,engine_version,unicode_fingerprint,state,created_ms) VALUES(1,'raw-active',?,?,?,'active',1000)`,
			[]any{searchpolicy.RecipeV1, searchpolicy.EngineVersion, searchInputExampleFingerprint}},
		{`UPDATE search_revision SET revision=1,active_generation_id=1,building_generation_id=NULL,input_sequence=0 WHERE singleton=1`, nil},
	} {
		if _, err = w.db.ExecContext(t.Context(), query.sql, query.args...); err != nil {
			t.Fatal(query.sql, err)
		}
	}
	registry, err := stmttest.New(t, iaExtraAcceptDefinitions())
	if err != nil {
		t.Fatal(err)
	}
	return w, &iaEntryRegistry{registry, keeper}, proof
}

// seedIAExtraOwner inserts one exact same-library movie owner for the resolved
// owner cell. It is prepared evidence, not authority.
func seedIAExtraOwner(t *testing.T, w *executor) {
	t.Helper()
	wc008NativeExec(t, w, `INSERT INTO catalog_subjects(id,public_id,library_id,kind) VALUES(60,X'60000000000000000000000000000000',7,1)`)
	wc008NativeExec(t, w, `INSERT INTO catalog_entities(id,public_id,library_id,kind,title,sort_title,sort_key,sort_head,sort_key_keep_articles,sort_head_keep_articles,random_key,added_at,metadata_incarnation,artwork_incarnation,playback_origin_incarnation,playback_origin_revision) VALUES(60,X'60000000000000000000000000000000',7,1,'Owner','','owner','o','owner','o',1,'2026-11-01T00:00:00.000000000Z',zeroblob(16),zeroblob(16),zeroblob(16),1)`)
	wc008NativeExec(t, w, `INSERT INTO catalog_identities(library_id,source_key,subject_id) VALUES(7,'extra-owner-key',60)`)
}

func newIAExtraAcceptRequest(w *executor) iaExtraAcceptRequest {
	return iaExtraAcceptRequest{
		scope:  inventoryScope{runID: iaExtraTestRunID, libraryKey: iaExtraTestLibraryKey, libraryID: iaExtraTestLibraryID},
		source: inventorySource{id: iaExtraTestSourceID, incarnation: "extra-incarnation", rootIdentity: "extra-root-identity", resolvedRoot: []byte(iaExtraTestRoot), generation: 3},
		run: inventoryRun{revision: 5, policyRevision: 4, leaseOwner: 11, leaseGeneration: 13,
			leaseExpiresMS: w.now().Add(time.Hour).UnixMilli(), lastSequence: 1, acknowledgedSequence: 1, phase: "inventory", scanMode: "first-import"},
		directory: inventoryDirectory{path: "Extras", identity: "extra-directory-identity", revision: "extra-directory-revision",
			cursorKind: inventoryLinuxCursor, expectedCursor: "", nextCursor: "42", pages: 2, skipped: 0, rawNames: 1, complete: true},
		files: []inventoryFile{{sourceKey: "extra-asset-source-key", relativePath: "Extras/Trailer.mkv", assetPath: iaExtraTestRoot + "/Extras/Trailer.mkv",
			objectIdentity: iaExtraTestObjectID, revision: iaExtraTestObjectRevision, size: 42, modifiedNS: 7, title: "Trailer",
			sortKey: "trailer", sortHead: "t", sortKeyKeepArticles: "trailer", sortHeadKeepArticles: "t",
			metadataLanguage: "en-US", year: 2020}},
		complete:   true,
		itemKey:    "extra-item-key",
		itemTitle:  "Trailer",
		extraKind:  "trailer",
		parentPath: "Extras",
	}
}

func iaExtraAcceptReference(t *testing.T, r iaExtraAcceptRequest, sequence int64, operation byte) ReceiptReference {
	t.Helper()
	id := CommandIdentity{Incarnation: [16]byte{1}, Sequence: sequence, OperationID: [16]byte{operation}}
	command, err := newIAExtraAcceptCommand(id, r)
	if err != nil {
		t.Fatal(err)
	}
	return ReceiptReference{Identity: id, RequestDigest: command.digest}
}

func iaExtraAcceptTurn(t *testing.T, w *executor, r *iaEntryRegistry, q iaExtraAcceptRequest) (*commandTurn, iaEntryBudget) {
	t.Helper()
	return iaEntryTurn(t, w, r, iaEntryRequest{purpose: IAAcceptExtraInventory, action: iaEntryApply, libraryID: q.scope.libraryID, libraryKey: q.scope.libraryKey})
}

func iaExtraAcceptRun(t *testing.T, w *executor, r *iaEntryRegistry, p *usOwnerAuthorityProof, q iaExtraAcceptRequest, ref ReceiptReference) (*commandTurn, iaExtraAcceptOutcome, error) {
	t.Helper()
	turn, budget := iaExtraAcceptTurn(t, w, r, q)
	out, err := acceptIAExtraInventory(t.Context(), turn, p, t.Context(), budget, ref, q)
	return turn, out, err
}

func iaExtraNoEffects(t *testing.T, w *executor) {
	t.Helper()
	for _, table := range []string{"catalog_identities", "catalog_entities", "item_extras", "catalog_asset_links", "catalog_asset_contributions",
		"scan_command_receipts", "scan_media_result_members", "search_publish_jobs"} {
		if got := iaEpisodeCount(t, w, `SELECT count(*) FROM `+table); got != 0 {
			t.Fatal("refused request wrote", table, got)
		}
	}
	if got := iaEpisodeCount(t, w, `SELECT last_command_sequence FROM inventory_runs WHERE job_id=?`, iaExtraTestRunID); got != 1 {
		t.Fatal("refused request advanced the run", got)
	}
	if got := iaEpisodeCount(t, w, `SELECT input_sequence FROM search_revision WHERE singleton=1`); got != 0 {
		t.Fatal("refused request advanced search input", got)
	}
}

// Fresh birth writes one complete unit: the extra identity, its exact
// item_extras row (unresolved owner is SQL NULL, never invented), the asset
// link/contribution, search intent, the directory/run advance and the typed
// receipt/child cohort. Replay returns the saved result after live source/lease
// changes without any new effect.
func TestIAExtraAcceptFreshReplayAndLiveCohort(t *testing.T) {
	w, reg, p := iaExtraAcceptFixture(t)
	q := newIAExtraAcceptRequest(w)
	ref := iaExtraAcceptReference(t, q, 2, 2)
	turn, out, err := iaExtraAcceptRun(t, w, reg, p, q, ref)
	if err != nil || out.decision != receiptFresh || out.digest == (ResultDigest{}) {
		t.Fatal("fresh extra accept", out, err)
	}
	if out.result.facts.acceptedRevision != 6 || out.result.facts.appliedMS != w.now().UnixMilli() ||
		out.result.facts.resultCount != 1 || out.result.facts.memberBytes <= 0 ||
		out.result.facts.objectID != iaExtraTestObjectID || string(out.result.facts.nextPayload) != "42" {
		t.Fatal("fresh extra facts", out.result.facts)
	}
	if _, err = turn.transaction.commit(); err != nil {
		t.Fatal(err)
	}
	for _, check := range []struct {
		query string
		args  []any
		want  int64
	}{
		{`SELECT count(*) FROM catalog_identities WHERE library_id=7 AND source_key='extra-item-key'`, nil, 1},
		{`SELECT count(*) FROM catalog_entities WHERE library_id=7 AND kind=11 AND title='Trailer'`, nil, 1},
		{`SELECT count(*) FROM item_extras WHERE library_id='extra-library' AND owner_id IS NULL AND kind='trailer' AND title='Trailer' AND asset_id='extra-token' AND relative_path='Extras/Trailer.mkv'`, nil, 1},
		{`SELECT count(*) FROM catalog_asset_links WHERE asset_id=41 AND part_index=0 AND start_seconds=0 AND end_seconds IS NULL`, nil, 1},
		{`SELECT count(*) FROM catalog_asset_contributions WHERE asset_id=41 AND membership_refs=1 AND file_size=42`, nil, 1},
		{`SELECT count(*) FROM scan_command_receipts WHERE command_kind='extra_inventory_accept' AND result_count=1 AND accepted_revision=6`, nil, 1},
		{`SELECT count(*) FROM scan_media_result_members WHERE command_kind='extra_inventory_accept' AND ordinal=0 AND member_kind='extra' AND subject_kind=11 AND extra_kind='trailer' AND parent_id IS NULL`, nil, 1},
		{`SELECT count(*) FROM search_publish_jobs`, nil, 1},
		{`SELECT count(*) FROM search_generations WHERE pending_subjects=1`, nil, 1},
		{`SELECT count(*) FROM search_revision WHERE input_sequence=1`, nil, 1},
		{`SELECT count(*) FROM inventory_directories WHERE cursor='42' AND pages=3`, nil, 1},
		{`SELECT count(*) FROM inventory_runs WHERE job_revision=6 AND last_command_sequence=2 AND acknowledged_command_sequence=1`, nil, 1},
	} {
		if got := iaEpisodeCount(t, w, check.query, check.args...); got != check.want {
			t.Fatal(check.query, got, check.want)
		}
	}
	// Live source/lease changes never edit or filter the saved result.
	wc008NativeExec(t, w, `UPDATE library_sources SET generation=9,root='/moved',configured_root='/moved' WHERE id='extra-source'`)
	wc008NativeExec(t, w, `UPDATE inventory_runs SET job_revision=99,job_lease_expires_ms=1 WHERE job_id='extra-run'`)
	turn, replay, err := iaExtraAcceptRun(t, w, reg, p, q, ref)
	if err != nil || replay.decision != receiptReplay || replay.digest != out.digest || replay.result.facts.resultCount != 1 ||
		replay.result.facts.acceptedRevision != out.result.facts.acceptedRevision {
		t.Fatal("saved extra replay", replay, err)
	}
	// The replay frame still holds the single writer connection, so the
	// no-new-receipt check reads the open transaction itself.
	var receipts int64
	if err := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT count(*) FROM scan_command_receipts`).Scan(&receipts); err != nil || receipts != 1 {
		t.Fatal("replay minted a receipt", receipts, err)
	}
	turn.transaction.rollback(nil)
}

// A resolved owner is written to item_extras and saved truthfully in the typed
// member. An unresolved owner stays SQL NULL and keeps no parent edge.
func TestIAExtraAcceptResolvedOwner(t *testing.T) {
	t.Run("resolved", func(t *testing.T) {
		w, reg, p := iaExtraAcceptFixture(t)
		seedIAExtraOwner(t, w)
		q := newIAExtraAcceptRequest(w)
		q.ownerKey, q.ownerKind = "extra-owner-key", 1
		ref := iaExtraAcceptReference(t, q, 2, 2)
		turn, out, err := iaExtraAcceptRun(t, w, reg, p, q, ref)
		if err != nil || out.decision != receiptFresh {
			t.Fatal(out, err)
		}
		if _, err = turn.transaction.commit(); err != nil {
			t.Fatal(err)
		}
		if got := iaEpisodeCount(t, w, `SELECT count(*) FROM item_extras WHERE owner_id=60`); got != 1 {
			t.Fatal("owner not persisted", got)
		}
		if got := iaEpisodeCount(t, w, `SELECT count(*) FROM scan_media_result_members WHERE parent_id=60 AND parent_kind=1`); got != 1 {
			t.Fatal("saved parent missing", got)
		}
	})
	t.Run("wrong_owner_kind", func(t *testing.T) {
		w, reg, p := iaExtraAcceptFixture(t)
		seedIAExtraOwner(t, w)
		q := newIAExtraAcceptRequest(w)
		q.ownerKey, q.ownerKind = "extra-owner-key", 2
		ref := iaExtraAcceptReference(t, q, 2, 2)
		turn, out, err := iaExtraAcceptRun(t, w, reg, p, q, ref)
		if !errors.Is(err, ErrConflict) || out.decision != 0 {
			t.Fatal("wrong owner kind", out, err)
		}
		turn.transaction.rollback(nil)
		if got := iaEpisodeCount(t, w, `SELECT count(*) FROM item_extras`); got != 0 {
			t.Fatal("wrong owner wrote", got)
		}
	})
	t.Run("missing_owner", func(t *testing.T) {
		w, reg, p := iaExtraAcceptFixture(t)
		q := newIAExtraAcceptRequest(w)
		q.ownerKey, q.ownerKind = "extra-owner-key", 1
		ref := iaExtraAcceptReference(t, q, 2, 2)
		turn, out, err := iaExtraAcceptRun(t, w, reg, p, q, ref)
		if !errors.Is(err, ErrConflict) || out.decision != 0 {
			t.Fatal("missing owner", out, err)
		}
		turn.transaction.rollback(nil)
		if got := iaEpisodeCount(t, w, `SELECT count(*) FROM item_extras`); got != 0 {
			t.Fatal("missing owner wrote an item", got)
		}
		if got := iaEpisodeCount(t, w, `SELECT count(*) FROM scan_command_receipts`); got != 0 {
			t.Fatal("missing owner wrote a receipt", got)
		}
	})
}

// Every fresh witness is re-proven after the receipt is Fresh. A changed
// library, source, directory, asset, lease or run revision refuses the complete
// unit before any catalogue/receipt row exists.
func TestIAExtraAcceptFreshGates(t *testing.T) {
	for name, mutate := range map[string]func(*testing.T, *executor, *iaExtraAcceptRequest){
		"retired_library": func(t *testing.T, w *executor, _ *iaExtraAcceptRequest) {
			wc008NativeExec(t, w, `UPDATE catalog_libraries SET retired=1 WHERE id=7`)
		},
		"stale_source":       func(_ *testing.T, _ *executor, q *iaExtraAcceptRequest) { q.source.generation++ },
		"stale_directory":    func(_ *testing.T, _ *executor, q *iaExtraAcceptRequest) { q.directory.expectedCursor = "9" },
		"changed_asset":      func(_ *testing.T, _ *executor, q *iaExtraAcceptRequest) { q.files[0].size++ },
		"changed_lease":      func(t *testing.T, w *executor, q *iaExtraAcceptRequest) { q.run.leaseExpiresMS = w.now().UnixMilli() },
		"stale_run_revision": func(_ *testing.T, _ *executor, q *iaExtraAcceptRequest) { q.run.revision++ },
	} {
		t.Run(name, func(t *testing.T) {
			w, reg, p := iaExtraAcceptFixture(t)
			q := newIAExtraAcceptRequest(w)
			mutate(t, w, &q)
			ref := iaExtraAcceptReference(t, q, 2, 2)
			turn, out, err := iaExtraAcceptRun(t, w, reg, p, q, ref)
			if !errors.Is(err, ErrConflict) || out.decision != 0 || out.digest != (ResultDigest{}) || len(out.result.facts.members) != 0 {
				t.Fatal("fresh gate", out, err)
			}
			turn.transaction.rollback(nil)
			for _, table := range []string{"catalog_entities", "item_extras", "catalog_asset_links", "scan_command_receipts"} {
				if got := iaEpisodeCount(t, w, `SELECT count(*) FROM `+table); got != 0 {
					t.Fatal("gate wrote", table, got)
				}
			}
		})
	}
}

// A reused item key is accepted only with the exact same observed evidence. A
// changed title, parent path, owner or asset is a conflict, never a silent
// rewrite or a second identity.
func TestIAExtraAcceptReusedItemConflict(t *testing.T) {
	w, reg, p := iaExtraAcceptFixture(t)
	first := newIAExtraAcceptRequest(w)
	ref := iaExtraAcceptReference(t, first, 2, 2)
	turn, out, err := iaExtraAcceptRun(t, w, reg, p, first, ref)
	if err != nil || out.decision != receiptFresh {
		t.Fatal(out, err)
	}
	if _, err = turn.transaction.commit(); err != nil {
		t.Fatal(err)
	}
	wc008NativeExec(t, w, `UPDATE inventory_runs SET acknowledged_command_sequence=2 WHERE job_id='extra-run'`)
	second := first
	second.run = inventoryRun{revision: 6, policyRevision: 4, leaseOwner: 11, leaseGeneration: 13,
		leaseExpiresMS: w.now().Add(time.Hour).UnixMilli(), lastSequence: 2, acknowledgedSequence: 2, phase: "inventory", scanMode: "first-import"}
	second.directory.expectedCursor, second.directory.nextCursor, second.directory.pages = "42", "43", 3
	second.itemTitle = "Different title"
	ref2 := iaExtraAcceptReference(t, second, 3, 3)
	turn, out, err = iaExtraAcceptRun(t, w, reg, p, second, ref2)
	if !errors.Is(err, ErrConflict) || out.decision != 0 {
		t.Fatal("reused item changed title", out, err)
	}
	turn.transaction.rollback(nil)
	if got := iaEpisodeCount(t, w, `SELECT count(*) FROM catalog_entities WHERE kind=11`); got != 1 {
		t.Fatal("reused item minted an identity", got)
	}
	if got := iaEpisodeCount(t, w, `SELECT count(*) FROM item_extras`); got != 1 {
		t.Fatal("reused item minted a row", got)
	}
}

// The same extra+asset link is never silently upserted: a second original that
// reaches the link insert conflicts and leaves the first cohort untouched.
func TestIAExtraAcceptRepeatedAssetConflicts(t *testing.T) {
	w, reg, p := iaExtraAcceptFixture(t)
	first := newIAExtraAcceptRequest(w)
	ref := iaExtraAcceptReference(t, first, 2, 2)
	turn, out, err := iaExtraAcceptRun(t, w, reg, p, first, ref)
	if err != nil || out.decision != receiptFresh {
		t.Fatal(out, err)
	}
	if _, err = turn.transaction.commit(); err != nil {
		t.Fatal(err)
	}
	wc008NativeExec(t, w, `UPDATE inventory_runs SET acknowledged_command_sequence=2 WHERE job_id='extra-run'`)
	second := first
	second.run = inventoryRun{revision: 6, policyRevision: 4, leaseOwner: 11, leaseGeneration: 13,
		leaseExpiresMS: w.now().Add(time.Hour).UnixMilli(), lastSequence: 2, acknowledgedSequence: 2, phase: "inventory", scanMode: "first-import"}
	second.directory.expectedCursor, second.directory.nextCursor, second.directory.pages = "42", "43", 3
	ref2 := iaExtraAcceptReference(t, second, 3, 3)
	turn, out, err = iaExtraAcceptRun(t, w, reg, p, second, ref2)
	if !errors.Is(err, ErrConflict) || out.decision != 0 {
		t.Fatal("repeated asset link", out, err)
	}
	turn.transaction.rollback(nil)
	if got := iaEpisodeCount(t, w, `SELECT count(*) FROM catalog_asset_links`); got != 1 {
		t.Fatal("repeated asset changed links", got)
	}
	if got := iaEpisodeCount(t, w, `SELECT count(*) FROM scan_command_receipts`); got != 1 {
		t.Fatal("repeated asset minted a receipt", got)
	}
}

// Current authority precedes every retained read: a forged issuer, a demoted
// owner and a cancelled caller lifetime all deny with zero public output.
func TestIAExtraAcceptAuthorityCells(t *testing.T) {
	t.Run("forged_issuer", func(t *testing.T) {
		w, reg, p := iaExtraAcceptFixture(t)
		q := newIAExtraAcceptRequest(w)
		ref := iaExtraAcceptReference(t, q, 2, 2)
		fake := *p
		fake.witness.issuer.family = "forged"
		turn, out, err := iaExtraAcceptRun(t, w, reg, &fake, q, ref)
		if !errors.Is(err, ErrDisclosureDenied) || out.decision != 0 || len(out.result.facts.members) != 0 {
			t.Fatal("forged issuer", out, err)
		}
		turn.transaction.rollback(nil)
		iaExtraNoEffects(t, w)
	})
	t.Run("stale_authority", func(t *testing.T) {
		for name, mutation := range map[string]string{
			"demoted_owner":  `UPDATE direct_memberships SET role='member'`,
			"retired_token":  `UPDATE authorization_family_tokens SET retired=1`,
			"denied_device":  `UPDATE identity_security_fences SET denied=1`,
			"profile_reset":  `UPDATE direct_profiles SET lifetime='dddddddddddddddddddddddddddddddd'`,
			"reset_session":  `INSERT INTO reset_file_sessions(family_id,kind) VALUES('issuer-family','owner')`,
			"narrowed_grant": `INSERT INTO authorization_family_grants(family_id,grant_kind) VALUES('issuer-family','cast_target')`,
		} {
			t.Run(name, func(t *testing.T) {
				w, reg, p := iaExtraAcceptFixture(t)
				wc008NativeExec(t, w, mutation)
				q := newIAExtraAcceptRequest(w)
				ref := iaExtraAcceptReference(t, q, 2, 2)
				turn, out, err := iaExtraAcceptRun(t, w, reg, p, q, ref)
				if !errors.Is(err, ErrDisclosureDenied) || out.decision != 0 || len(out.result.facts.members) != 0 {
					t.Fatal("stale authority", out, err)
				}
				turn.transaction.rollback(nil)
				iaExtraNoEffects(t, w)
			})
		}
	})
	t.Run("cancelled_lifetime", func(t *testing.T) {
		w, reg, p := iaExtraAcceptFixture(t)
		q := newIAExtraAcceptRequest(w)
		ref := iaExtraAcceptReference(t, q, 2, 2)
		turn, budget := iaExtraAcceptTurn(t, w, reg, q)
		cancelled, cancel := context.WithCancel(t.Context())
		cancel()
		out, err := acceptIAExtraInventory(cancelled, turn, p, cancelled, budget, ref, q)
		if err == nil || out.decision != 0 || len(out.result.facts.members) != 0 {
			t.Fatal("cancelled lifetime", out, err)
		}
		turn.transaction.rollback(nil)
		iaExtraNoEffects(t, w)
	})
}

// Current authority is re-observed after the fresh gates and again after the
// writes. A same-turn revocation injected by the last read or by a catalogue
// write must deny the fresh result and leave no committed effect.
func TestIAExtraAcceptLateAuthorityRechecks(t *testing.T) {
	t.Run("before_writes", func(t *testing.T) {
		w, reg, p := iaExtraAcceptFixture(t)
		state := installIAEntryQueryRevocation(t, w, "FROM catalog_assets")
		q := newIAExtraAcceptRequest(w)
		ref := iaExtraAcceptReference(t, q, 2, 2)
		turn, out, err := iaExtraAcceptRun(t, w, reg, p, q, ref)
		if !state.fired || state.err != nil {
			t.Fatal("asset-close mutation did not run", state.err)
		}
		if !errors.Is(err, ErrDisclosureDenied) || out.decision != 0 || len(out.result.facts.members) != 0 {
			t.Fatal("pre-write recheck released a stale result", out, err)
		}
		turn.transaction.rollback(nil)
		iaExtraNoEffects(t, w)
	})
	t.Run("after_writes", func(t *testing.T) {
		w, reg, p := iaExtraAcceptFixture(t)
		wc008NativeExec(t, w, `CREATE TRIGGER ia_extra_late_revocation AFTER INSERT ON catalog_asset_links BEGIN UPDATE identity_security_fences SET denied=1; END`)
		q := newIAExtraAcceptRequest(w)
		ref := iaExtraAcceptReference(t, q, 2, 2)
		turn, out, err := iaExtraAcceptRun(t, w, reg, p, q, ref)
		if !errors.Is(err, ErrDisclosureDenied) || out.decision != 0 || len(out.result.facts.members) != 0 {
			t.Fatal("post-write recheck released a stale result", out, err)
		}
		var links int64
		if err = turn.transaction.conn.QueryRowContext(t.Context(), `SELECT count(*) FROM catalog_asset_links`).Scan(&links); err != nil || links != 1 {
			t.Fatal("link write not reached before the final recheck", links, err)
		}
		turn.transaction.rollback(nil)
		iaExtraNoEffects(t, w)
	})
}

// A saved cohort must be complete and digest-exact before it is served.
func TestIAExtraAcceptReplayRejectsTamperedCohort(t *testing.T) {
	for name, mutate := range map[string]string{
		"missing_member": `DELETE FROM scan_media_result_members WHERE ordinal=0`,
		"member_bytes":   `UPDATE scan_command_receipts SET ia_media_member_bytes=1`,
		"result_digest":  `UPDATE scan_command_receipts SET result_digest=zeroblob(32)`,
	} {
		t.Run(name, func(t *testing.T) {
			w, reg, p := iaExtraAcceptFixture(t)
			q := newIAExtraAcceptRequest(w)
			ref := iaExtraAcceptReference(t, q, 2, 2)
			turn, out, err := iaExtraAcceptRun(t, w, reg, p, q, ref)
			if err != nil || out.decision != receiptFresh {
				t.Fatal(out, err)
			}
			if _, err = turn.transaction.commit(); err != nil {
				t.Fatal(err)
			}
			wc008NativeExec(t, w, mutate)
			turn, replay, err := iaExtraAcceptRun(t, w, reg, p, q, ref)
			if !errors.Is(err, ErrResolveRequired) || replay.decision != 0 || replay.digest != (ResultDigest{}) || len(replay.result.facts.members) != 0 {
				t.Fatal("tampered cohort served", replay, err)
			}
			turn.transaction.rollback(nil)
		})
	}
}

func TestIAExtraAcceptRefusesUnusableSearchBank(t *testing.T) {
	w, reg, p := iaExtraAcceptFixture(t)
	wc008NativeExec(t, w, `UPDATE search_generations SET unicode_fingerprint='bad'`)
	q := newIAExtraAcceptRequest(w)
	ref := iaExtraAcceptReference(t, q, 2, 2)
	turn, out, err := iaExtraAcceptRun(t, w, reg, p, q, ref)
	if err == nil || out.decision != 0 || out.digest != (ResultDigest{}) || len(out.result.facts.members) != 0 {
		t.Fatal("unusable search bank", out, err)
	}
	turn.transaction.rollback(nil)
	iaExtraNoEffects(t, w)
}

// A lost COMMIT reply is never success. The committed file is read back through
// a FRESH read-only connection, never the fenced writer pool.
func TestIAExtraAcceptUnknownCommitIsNotSuccess(t *testing.T) {
	w, reg, p := iaExtraAcceptFixture(t)
	path := iaEpisodePath(t, w)
	if err := w.db.Close(); err != nil {
		t.Fatal(err)
	}
	base, err := sqliteconn.Connector(path, w.config)
	if err != nil {
		t.Fatal(err)
	}
	installFaultPool(w, &faultConnector{Connector: base, afterCommit: true, replyError: true})
	q := newIAExtraAcceptRequest(w)
	ref := iaExtraAcceptReference(t, q, 2, 2)
	turn, out, err := iaExtraAcceptRun(t, w, reg, p, q, ref)
	if err != nil || out.decision != receiptFresh {
		t.Fatal("fresh before commit", out, err)
	}
	result, err := turn.transaction.commit()
	if result.state != commitUnknown || !errors.Is(err, ErrResolveRequired) {
		t.Fatal(result, err)
	}
	readCfg := sqliteconn.Config{Role: sqliteconn.Reader, Connections: 1, CacheKiB: 1024, Synchronous: sqliteconn.Normal}
	db, _, err := sqliteconn.Open(t.Context(), path, readCfg)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, check := range []struct {
		query string
		want  int64
	}{
		{`SELECT count(*) FROM catalog_entities WHERE kind=11 AND library_id=7`, 1},
		{`SELECT count(*) FROM item_extras WHERE asset_id='extra-token'`, 1},
		{`SELECT count(*) FROM catalog_asset_links WHERE asset_id=41`, 1},
		{`SELECT count(*) FROM scan_command_receipts WHERE command_kind='extra_inventory_accept' AND result_count=1`, 1},
		{`SELECT count(*) FROM scan_media_result_members WHERE command_kind='extra_inventory_accept'`, 1},
	} {
		var got int64
		if err = db.QueryRowContext(t.Context(), check.query).Scan(&got); err != nil || got != check.want {
			t.Fatal(check.query, got, check.want, err)
		}
	}
	if _, err = w.begin(t.Context(), IAAcceptExtraInventory, testRecipe(), &testCustody{}); !errors.Is(err, ErrResolveRequired) {
		t.Fatal("advanced past unknown commit", err)
	}
}

// At or below the durable ACK floor the original is Retired: no result, no
// digest and no new effect, even while the physical rows still exist.
func TestIAExtraAcceptRetiredAfterAck(t *testing.T) {
	w, reg, p := iaExtraAcceptFixture(t)
	q := newIAExtraAcceptRequest(w)
	ref := iaExtraAcceptReference(t, q, 2, 2)
	turn, out, err := iaExtraAcceptRun(t, w, reg, p, q, ref)
	if err != nil || out.decision != receiptFresh {
		t.Fatal(out, err)
	}
	if _, err = turn.transaction.commit(); err != nil {
		t.Fatal(err)
	}
	wc008NativeExec(t, w, `UPDATE inventory_runs SET acknowledged_command_sequence=2 WHERE job_id='extra-run'`)
	before := map[string]int64{}
	for _, table := range []string{"catalog_entities", "item_extras", "catalog_asset_links", "scan_command_receipts", "scan_media_result_members", "search_publish_jobs"} {
		before[table] = iaEpisodeCount(t, w, `SELECT count(*) FROM `+table)
	}
	turn, out, err = iaExtraAcceptRun(t, w, reg, p, q, ref)
	if err != nil || out.decision != receiptRetired || out.digest != (ResultDigest{}) || out.result.facts.resultCount != 0 || len(out.result.facts.members) != 0 {
		t.Fatal("retired original", out, err)
	}
	turn.transaction.rollback(nil)
	for table, want := range before {
		if got := iaEpisodeCount(t, w, `SELECT count(*) FROM `+table); got != want {
			t.Fatal("retired replay wrote", table, got, want)
		}
	}
}

// A conflicting digest and a non-contiguous sequence are conflicts, never a
// fresh admission or a new receipt.
func TestIAExtraAcceptRejectsConflictingOriginal(t *testing.T) {
	w, reg, p := iaExtraAcceptFixture(t)
	q := newIAExtraAcceptRequest(w)
	ref := iaExtraAcceptReference(t, q, 2, 2)
	changed := q
	changed.files[0].revision += "x"
	turn, out, err := iaExtraAcceptRun(t, w, reg, p, changed, ref)
	if !errors.Is(err, ErrConflict) || out.decision != 0 || out.digest != (ResultDigest{}) {
		t.Fatal("changed request digest", out, err)
	}
	turn.transaction.rollback(nil)
	gap := q
	gap.run.lastSequence, gap.run.acknowledgedSequence = 2, 2
	gapRef := iaExtraAcceptReference(t, gap, 3, 4)
	turn, out, err = iaExtraAcceptRun(t, w, reg, p, gap, gapRef)
	if !errors.Is(err, ErrConflict) || out.decision != 0 {
		t.Fatal("sequence gap", out, err)
	}
	turn.transaction.rollback(nil)
	iaExtraNoEffects(t, w)
}

func TestIAExtraAcceptRollbackLeavesNoEffect(t *testing.T) {
	w, reg, p := iaExtraAcceptFixture(t)
	q := newIAExtraAcceptRequest(w)
	ref := iaExtraAcceptReference(t, q, 2, 2)
	turn, out, err := iaExtraAcceptRun(t, w, reg, p, q, ref)
	if err != nil || out.decision != receiptFresh {
		t.Fatal(out, err)
	}
	turn.transaction.rollback(nil)
	iaExtraNoEffects(t, w)
	if got := iaEpisodeCount(t, w, `SELECT count(*) FROM inventory_directories WHERE cursor='' AND pages=2`); got != 1 {
		t.Fatal("rollback advanced the directory", got)
	}
}
