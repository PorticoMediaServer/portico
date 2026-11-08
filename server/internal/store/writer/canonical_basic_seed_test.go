package writer

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"testing"
	"time"
)

// Explicit fixture birth of already-frozen ordinary inputs. These are not
// probe/Stage clocks, not a producer API, and not an escape from the runtime.
// All setup completes before the measured Accept population begins. The 1M
// resident library is preserved and the new leaves use its actual namespace.
type canonicalBasicSeed struct {
	original basicOriginalScope
	source   basicStreamFactStage
	identity CommandIdentity
	count    int
}

func canonicalBasicBlob(tag byte, n int) [16]byte {
	b := [16]byte{'B', 'A', 'S', 'I', 'C', tag}
	binary.BigEndian.PutUint64(b[8:], uint64(n))
	return b
}
func (s canonicalBasicSeed) packet(n int, original basicOriginalScope, identity CommandIdentity) (*basicAcceptCommand, error) {
	if n < 0 || n >= s.count {
		return nil, ErrInvalid
	}
	source := s.source
	source.assetID = int64(41 + n)
	source.changeID = int64(51 + n)
	source.assetToken = fmt.Sprintf("canonical-basic-asset-%d", n)
	source.objectID = fmt.Sprintf("canonical-basic-object-%d", n)
	analysis, err := prepareBasicObjectAnalysis(source, original.scope.libraryKey, "canonical-basic-probe-v1", `["probe"]`, `["probe"]`, false)
	if err != nil {
		return nil, err
	}
	return newBasicAcceptCommand(identity, basicAcceptRequest{original: original, purpose: BasicPurposeAccept, operation: basicAcceptFirstOrdinaryLeaf,
		activation: basicActivationEffects{source: source, changeRevision: 13, familyMask: 511, shapeID: 61, invalidationRevision: 1, fileSizeRevision: 1},
		entityID:   int64(31 + n), duration: 100, height: 1080, analysis: *analysis})
}
func seedCanonicalBasicInputs(t *testing.T, e *executor, count int) canonicalBasicSeed {
	t.Helper()
	// 10k measured originals plus bounded warmup fit the adopted retained-input
	// ledger. Larger class configurations fail before setup; no cap is raised.
	if count < 1 || count > 10008 {
		t.Fatal("Basic class supports at most 10000 originals plus 8 warmup")
	}
	s := canonicalBasicSeed{count: count,
		identity: CommandIdentity{Incarnation: canonicalBasicBlob(1, 0), Sequence: 19, OperationID: canonicalBasicBlob(2, 0)},
		original: basicOriginalScope{scope: inventoryScope{runID: "canonical-basic-run", libraryID: 7, libraryKey: "qualification-movies"},
			source: inventorySource{id: "canonical-basic-source", incarnation: "canonical-basic-incarnation", rootIdentity: "canonical-basic-root", resolvedRoot: []byte(qualificationMovieRoot), generation: 23},
			run:    inventoryRun{revision: 41, policyRevision: 29, phase: "inventory", scanMode: "first-import", leaseOwner: 13, leaseGeneration: 17, leaseExpiresMS: time.Now().Add(2 * time.Hour).UnixMilli(), lastSequence: 18, acknowledgedSequence: 18}},
	}
	s.source = basicStreamFactStage{inputRevision: 7, libraryID: 7, expectedEpoch: 17, runID: s.original.scope.runID, sourceID: s.original.source.id, incarnation: s.original.source.incarnation, objectRevision: "canonical-basic-object-revision", sourceGeneration: 23, policyRevision: 29, witnessFingerprint: [32]byte(bytes.Repeat([]byte{0x31}, 32)), factRevision: 31, size: 42, modifiedNS: -11, detailVersion: 1, fingerprint: "canonical-basic-probe"}
	tx, err := e.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := tx.ExecContext(t.Context(), q, args...); err != nil {
			t.Fatal(err)
		}
	}
	var membership, oldDebt, cap int64
	if err := tx.QueryRowContext(t.Context(), `SELECT sequence FROM catalog_membership_clock WHERE singleton=1`).Scan(&membership); err != nil {
		t.Fatal(err)
	}
	if membership > math.MaxInt64-int64(count) {
		t.Fatal(ErrCap)
	}
	exec(`INSERT INTO library_sources(id,library_id,configured_root,root,incarnation,root_identity,generation,identity_confirmed) VALUES(?,?,?,?,?,?,23,1)`, s.original.source.id, s.original.scope.libraryKey, qualificationMovieRoot, qualificationMovieRoot, s.original.source.incarnation, s.original.source.rootIdentity)
	exec(`INSERT INTO library_scan_policies(library_id,revision) VALUES(?,29)`, s.original.scope.libraryKey)
	exec(`INSERT INTO jobs(id,library_id,status,created_at,processed) VALUES(?,?,'running','2026-11-08T00:00:00Z',0)`, s.original.scope.runID, s.original.scope.libraryKey)
	exec(`INSERT INTO inventory_runs(job_id,original_library_id,original_library_key,original_resolved_root,original_follow_symlinks,create_operation_id,create_request_digest,source_id,source_generation,root_incarnation,root_identity,policy_revision,scan_mode,job_lease_owner,job_lease_generation,job_lease_expires_ms,job_revision,command_incarnation,last_command_sequence,acknowledged_command_sequence,discovered)
 VALUES(?,7,?,?,0,?,?,?,23,?,?,29,'first-import',13,17,?,41,?,18,18,?)`, s.original.scope.runID, s.original.scope.libraryKey, qualificationMovieRoot, bytes.Repeat([]byte{5}, 16), bytes.Repeat([]byte{6}, 32), s.original.source.id, s.original.source.incarnation, s.original.source.rootIdentity, s.original.run.leaseExpiresMS, s.identity.Incarnation[:], count)
	exec(`INSERT INTO inventory_source_active VALUES(?,?)`, s.original.source.id, s.original.scope.runID)
	exec(`INSERT INTO catalog_info_shapes(id,container,video_codec,height,ref_count) VALUES(61,'mkv','h264',1080,?)`, count)
	exec(`INSERT INTO catalog_info_input_budget(singleton,used_bytes) VALUES(1,0)`)
	if err := tx.QueryRowContext(t.Context(), `SELECT used_bytes,cap_bytes FROM catalog_info_input_budget WHERE singleton=1`).Scan(&oldDebt, &cap); err != nil {
		t.Fatal(err)
	}
	charged := int64(0)
	for n := 0; n < count; n++ {
		var nextMembership int64
		if err := tx.QueryRowContext(t.Context(), `UPDATE catalog_membership_clock SET sequence=? WHERE singleton=1 AND sequence=? RETURNING sequence`, membership+1, membership).Scan(&nextMembership); err != nil {
			t.Fatal(err)
		}
		membership = nextMembership
		c, err := s.packet(n, s.original, s.identity)
		if err != nil {
			t.Fatal(err)
		}
		source := c.request.activation.source
		id := c.request.entityID
		public, inc, art, origin := canonicalBasicBlob(3, n), canonicalBasicBlob(4, n), canonicalBasicBlob(5, n), canonicalBasicBlob(6, n)
		path := fmt.Sprintf("%s/Basic %d.mkv", qualificationMovieRoot, n)
		exec(`INSERT INTO catalog_assets(id,token,library_id,path,size,modified_ns,container,video_codec,height,duration,info_epoch,info_publication_pending) VALUES(?,?,7,?,42,-11,'mkv','h264',1080,100,17,1)`, source.assetID, source.assetToken, path)
		exec(`INSERT INTO catalog_subjects(id,public_id,library_id,kind) VALUES(?,?,7,1)`, id, public[:])
		exec(`INSERT INTO catalog_entities(id,public_id,library_id,kind,title,sort_title,sort_key,sort_head,sort_key_keep_articles,sort_head_keep_articles,random_key,added_at,metadata_language,metadata_incarnation,artwork_incarnation,playback_origin_incarnation,playback_origin_revision,file_size_key,scalar_source_revision,aggregate_revision) VALUES(?,?,7,1,'Basic','','basic','b','basic','b',?,'2026-11-08T00:00:00.000000000Z','en-US',?,?,?,1,42,17,23)`, id, public[:], n+1, inc[:], art[:], origin[:])
		exec(`INSERT INTO catalog_info_changes(id,asset_id,asset_token,slot,expected_epoch,input_revision,family_mask,fingerprint,created_ms,phase,job_revision,target_shape_id,target_available,has_after,after_entity,upper_entity) VALUES(?,?,?,0,17,7,511,?,1000,3,13,61,1,1,?,?)`, source.changeID, source.assetID, source.assetToken, source.witnessFingerprint[:], id, id)
		exec(`INSERT INTO catalog_asset_links(library_id,entity_id,asset_id,info_pending_change) VALUES(7,?,?,?)`, id, source.assetID, source.changeID)
		exec(`INSERT INTO catalog_scalar_contributions(library_id,parent_id,contributor_id,source,contributor_revision,membership_sequence) VALUES(7,?,?,0,17,?)`, id, id, membership)
		exec(`INSERT INTO catalog_asset_contributions(library_id,parent_id,asset_id,membership_refs,file_size,file_size_revision,authored_refs) VALUES(7,?,?,1,42,1,0)`, id, source.assetID)
		exec(`INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,revision,evidence_json,size,modified_ns,seen_job) VALUES(?,?,?,?,?,?,'{}',42,-11,?)`, source.objectID, source.sourceID, source.assetToken, source.incarnation, fmt.Sprintf("Basic %d.mkv", n), source.objectRevision, source.runID)
		exec(`INSERT INTO asset_stream_facts VALUES(?,31,42,-11,?,1)`, source.assetToken, source.fingerprint)
		exec(`INSERT INTO catalog_info_input_witnesses(change_id,ordinal,kind,job_id,source_id,source_generation,root_incarnation,object_id,object_revision,policy_revision,fingerprint) VALUES(?,0,0,?,?,23,?,?,?,29,?)`, source.changeID, source.runID, source.sourceID, source.incarnation, source.objectID, source.objectRevision, source.witnessFingerprint[:])
		for family := 1; family <= 9; family++ {
			rows := 0
			if family == 1 {
				rows = 1
			}
			exec(`INSERT INTO catalog_info_input_families(change_id,family,witness_ordinal,rows_expected,rows_staged,sealed) VALUES(?,?,0,?,?,1)`, source.changeID, family, rows, rows)
		}
		exec(`INSERT INTO catalog_info_new_stream_facts(change_id,asset_id,revision,size,modified_ns,fingerprint,detail_version,input_ordinal) VALUES(?,?,31,42,-11,?,1,0)`, source.changeID, source.assetToken, source.fingerprint)
		charge, err := basicRetireFirstCharge(basicRetireFirstEffects{source: source, currentAssetEpoch: 18, changeRevision: 15, shapeID: 61, entityID: id, counts: [9]int64{1}})
		if err != nil || charge > cap-oldDebt-charged {
			t.Fatal("prepared input ledger", charge, charged, cap, err)
		}
		charged += charge
	}
	exec(`UPDATE libraries SET data_revision=data_revision+?,data_order_revision=data_order_revision+?,data_metadata_revision=data_metadata_revision+?,age_input_revision=age_input_revision+?,age_published_revision=CASE WHEN age_input_revision=age_published_revision AND age_pending_operations=0 THEN age_input_revision+? ELSE age_published_revision END WHERE id=?`, count, count, count, count, count, s.original.scope.libraryKey)
	exec(`UPDATE catalog_age_item_counts SET total=total+? WHERE library_id=7 AND kind=1 AND block_unrated=0 AND admission_age=-1`, count)
	exec(`UPDATE catalog_info_input_budget SET used_bytes=? WHERE singleton=1 AND used_bytes=?`, oldDebt+charged, oldDebt)
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return s
}
