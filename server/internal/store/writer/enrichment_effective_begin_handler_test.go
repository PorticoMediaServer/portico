package writer

import (
	"crypto/sha256"
	"errors"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

func effectiveBeginWholeTurn(t *testing.T, w *executor) (*commandTurn, map[stmt.ID]statementStep) {
	t.Helper()
	// Virtual control forecasts, not W's production recipe or latency evidence.
	recipe := turnRecipe{limit: Work{4096, 1000000, 4096, 32 << 20, 25 * time.Millisecond}, setup: Work{16, 500, 16, 65536, time.Millisecond}, terminal: Work{16, 500, 16, 65536, time.Millisecond}}
	tx, err := w.begin(t.Context(), EffectiveStage, recipe, &testCustody{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = tx.rollback(nil) })
	defs := append(enrichmentEffectiveBeginDefinitions(), enrichmentEffectiveProducerDefinitions()...)
	registry, err := stmt.New(defs)
	if err != nil {
		t.Fatal(err)
	}
	var steps []statementStep
	byID := map[stmt.ID]statementStep{}
	for _, d := range defs {
		step := statementStep{d.ID, Work{16, 2000, 16, 65536, 100 * time.Microsecond}}
		steps = append(steps, step)
		byID[d.ID] = step
	}
	turn, err := newCommandTurn(tx, registry, steps)
	if err != nil {
		t.Fatal(err)
	}
	return turn, byID
}

func effectiveBeginWholeFixture(t *testing.T) (*executor, *time.Time, effectiveStageBeginRequest, CommandIdentity) {
	t.Helper()
	w, _, now := testExecutor(t)
	*now = time.Date(2026, 11, 5, 10, 30, 0, 0, time.UTC)
	effectiveBeginNativePopulation(t, w)
	var assetInc, subjectInc, selectionInc, artInc, linkInc [16]byte
	for i := range assetInc {
		assetInc[i] = byte(160 + i)
		subjectInc[i] = byte(176 + i)
		selectionInc[i] = byte(120 + i)
		artInc[i] = byte(192 + i)
		linkInc[i] = byte(208 + i)
	}
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := w.db.ExecContext(t.Context(), q, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`UPDATE metadata_provider_operations SET phase='enrichment-v1',hierarchy_revision=0,selection_revision=0,descriptive_revision=0,acknowledged_command_sequence=11 WHERE id='operation-one'`)
	exec(`UPDATE catalog_entities SET artwork_incarnation=? WHERE id=101`, artInc[:])
	exec(`UPDATE libraries SET metadata_language='en-CA' WHERE id='library-native'`)
	exec(`INSERT INTO library_sources(id,library_id,configured_root,root,generation,incarnation,root_identity,identity_confirmed,enabled,health) VALUES('source-native','library-native','/configured','/physical',97,'root-incarnation:native','root-identity:211',1,1,'ready')`)
	exec(`INSERT INTO catalog_assets(id,token,path,size,modified_ns,container,video_codec,audio_codec,width,height,duration,library_id,playback_origin_incarnation,playback_origin_revision,info_epoch) VALUES(701,'asset-token:native','namespace/movie.mkv',123456,1793860000111,'matroska','h264','aac',1920,1080,99.5,7,?,89,83)`, assetInc[:])
	exec(`INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,object_identity,revision,evidence_json,size,modified_ns,state,retired) VALUES('object-native','source-native','asset-token:native','root-incarnation:native','movie.mkv','object-identity:109','object-revision:107','{}',123456,1793860000111,'available',0)`)
	exec(`UPDATE catalog_assets SET info_owner_object='object-native',info_owner_revision='object-revision:107' WHERE id=701`)
	exec(`INSERT INTO catalog_asset_links(entity_id,asset_id,part_index,start_seconds,end_seconds,library_id,playback_origin_incarnation,playback_origin_revision) VALUES(101,701,3,1.25,9.5,7,?,91)`, linkInc[:])
	exec(`INSERT INTO metadata_provider_operation_sources(operation_id,ordinal,asset_id,asset_incarnation,part_index,start_seconds,end_seconds,path,size,modified_ns,available,source_id,source_generation,root_incarnation,object_revision) VALUES('operation-one',0,701,?,3,1.25,9.5,'namespace/movie.mkv',123456,1793860000111,1,'source-native',97,'root-incarnation:native','object-revision:107')`, assetInc[:])
	exec(`INSERT INTO metadata_provider_policy(library_id,provider,incarnation,revision,enabled,priority,region,algorithm,refresh_mode,credit_enabled) SELECT 7,'fixture',policy_incarnation,37,1,1,'CA','native-algorithm','fill_missing',1 FROM metadata_provider_operations WHERE id='operation-one'`)
	exec(`INSERT INTO screen_metadata_consent(singleton,revision,confirmed) VALUES(1,41,1)`)
	exec(`INSERT INTO metadata_subject_providers(entity_id,provider,role,incarnation,revision,subject_kind,provider_id,status,observed_at) VALUES(101,'fixture','primary',?,71,'movie','opaque:selection','selected','2026-11-05T10:00:00.000000000Z')`, selectionInc[:])
	exec(`INSERT INTO metadata_provider_subjects(id,provider,subject_kind,provider_id,language,region,request_shape,incarnation,revision) VALUES(601,'fixture','movie','opaque:selection','en-CA','CA','detail-v1',?,73)`, subjectInc[:])
	digest := sha256.Sum256([]byte("native source observation"))
	exec(`INSERT INTO metadata_evidence(id,subject_id,parser_version,provider,subject_kind,provider_id,language,region,request_shape,digest,observed_at,source_url,sealed) VALUES('evidence-source',601,'parser/v1','fixture','movie','opaque:selection','en-CA','CA','detail-v1',lower(hex(?)),'2026-11-05T10:00:00.000000000Z','fixture://source',1)`, digest[:])
	exec(`INSERT INTO credit_source_publications(publication_id,entity_id,provider,accepted_record_reference,observed_at,member_count,sealed) VALUES('aabbccddeeff00112233445566778899aa',101,'fixture','fixture/movie/opaque:selection','2026-11-05T10:00:00.000000000Z',0,1)`)
	// Capture a deterministic native fixture, not the eventual governed external
	// producer/read-snapshot or current-disclosure bridge. Roll back this separate
	// preparatory control turn BEFORE entropy preparation and the tested command.
	turn, steps := effectiveBeginWholeTurn(t, w)
	owner, err := effectiveBeginOwnerFacts(t.Context(), turn, steps[enrichmentEffectiveOwner], "operation-one")
	if err != nil {
		t.Fatal(err)
	}
	entity, err := effectiveBeginEntityFacts(t.Context(), turn, steps[enrichmentEffectiveEntity], 101)
	if err != nil {
		t.Fatal(err)
	}
	producer, err := effectiveBeginReadProducer(t.Context(), turn, "operation-one", entity, "fixture", "evidence-source", "aabbccddeeff00112233445566778899aa", "object-native")
	if err != nil {
		t.Fatal(err)
	}
	if err := turn.closeRows(); err != nil {
		t.Fatal(err)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}
	header := effectiveBeginHeaderFixture()
	header.expectedCount, header.expectedBytes = 0, 0
	header.expectedManifest = effectiveBeginBuildSeed(header)
	request := effectiveStageBeginRequest{variant: "begin", ownerID: "operation-one", evidenceID: "evidence-source", publicationID: "aabbccddeeff00112233445566778899aa", objectID: "object-native", owner: owner, entity: entity, producer: producer, header: header}
	identity := CommandIdentity{Incarnation: owner.incarnation, Sequence: 12, OperationID: [16]byte{239, 241, 251}}
	return w, now, request, identity
}

func TestEffectiveBeginCommandCommitReplayAndOriginalResolution(t *testing.T) {
	w, now, request, identity := effectiveBeginWholeFixture(t)
	input, err := newEffectiveStageBeginCommand(identity, request)
	if err != nil {
		t.Fatal(err)
	}
	turn, _ := effectiveBeginWholeTurn(t, w)
	result, err := applyEffectiveStageBegin(t.Context(), turn, input)
	if err != nil {
		t.Fatal(err)
	}
	if result.ObservedCatalogRevision != 203 || result.EntityID != 101 || result.AfterItem != "opaque-page:61" || result.NextOrdinal != 0 || result.ImageID <= 0 || result.ImageRevision <= 0 || result.ProviderOperationID != "operation-one" {
		t.Fatal("wrong native/scalar result", result)
	}
	if err := turn.closeRows(); err != nil {
		t.Fatal(err)
	}
	if state, err := turn.transaction.commit(); err != nil || state.state != committed {
		t.Fatal(state, err)
	}
	// Neither mutable source/selection/lease/policy nor image reconstruction is a
	// prerequisite to returning this exact immutable ORIGINAL result.
	for _, q := range []string{
		`UPDATE metadata_provider_operations SET phase='superseded',status='stale',lease='expired',after_item='new-current-cursor',credit_image_id=NULL WHERE id='operation-one'`,
		`UPDATE catalog_entities SET revision=223,metadata_revision=227,tv_source_revision=0 WHERE id=101`,
		`UPDATE metadata_subject_providers SET status='moved',revision=revision+1`,
		`UPDATE metadata_provider_policy SET enabled=0`,
		`UPDATE screen_metadata_consent SET confirmed=0,revision=revision+1`,
		`DELETE FROM catalog_credit_images`,
	} {
		if _, err := w.db.ExecContext(t.Context(), q); err != nil {
			t.Fatal(err)
		}
	}
	*now = now.Add(3 * time.Hour)
	retry, err := newEffectiveStageBeginCommand(identity, request)
	if err != nil {
		t.Fatal(err)
	}
	turn, _ = effectiveBeginWholeTurn(t, w)
	replay, err := applyEffectiveStageBegin(t.Context(), turn, retry)
	if err != nil || replay != result {
		t.Fatal("historical result hidden by fresh state", replay, result, err)
	}
	resolved, _, decision, err := lookupEffectiveStageBegin(t.Context(), turn, "operation-one", receiptIdentity{identity, input.digest})
	if err != nil || decision != receiptReplay || resolved != result {
		t.Fatal("identity-only resolution", resolved, decision, err)
	}
	if err := turn.closeRows(); err != nil {
		t.Fatal(err)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}
	var count int64
	if err := w.db.QueryRowContext(t.Context(), "SELECT count(*) FROM catalog_credit_images").Scan(&count); err != nil || count != 0 {
		t.Fatal("replay allocated another image", count, err)
	}
}

func TestEffectiveBeginFreshNativeSourceFences(t *testing.T) {
	w, _, request, identity := effectiveBeginWholeFixture(t)
	input, err := newEffectiveStageBeginCommand(identity, request)
	if err != nil {
		t.Fatal(err)
	}
	cases := []string{
		`UPDATE metadata_provider_operations SET phase='arbitrary' WHERE id='operation-one'`,
		`UPDATE metadata_provider_operations SET hierarchy_revision=19 WHERE id='operation-one'`,
		`UPDATE metadata_provider_operations SET selection_revision=71 WHERE id='operation-one'`,
		`UPDATE metadata_provider_operations SET descriptive_revision=19 WHERE id='operation-one'`,
		`UPDATE catalog_entities SET tv_source_revision=0 WHERE id=101`,
		`UPDATE catalog_entities SET source_revision=source_revision+1 WHERE id=101`,
		`UPDATE catalog_entities SET credit_owner_locked=0 WHERE id=101`,
		`UPDATE catalog_entities SET credit_owner_revision=24 WHERE id=101`,
		`UPDATE metadata_subject_providers SET status='matched'`,
		`UPDATE metadata_subject_providers SET role='unadmitted'`,
		`UPDATE metadata_subject_providers SET subject_kind='series'`,
		`UPDATE metadata_subject_providers SET provider_id='another-opaque-id'`,
		`UPDATE metadata_subject_providers SET revision=revision+1`,
		`UPDATE metadata_provider_policy SET enabled=0`,
		`UPDATE metadata_provider_policy SET revision=revision+1`,
		`UPDATE screen_metadata_consent SET confirmed=0`,
		`UPDATE screen_metadata_consent SET revision=revision+1`,
		`UPDATE libraries SET metadata_language='fr-CA'`,
		`UPDATE metadata_evidence SET sealed=0 WHERE id='evidence-source'`,
		`UPDATE credit_source_publications SET sealed=0`,
		`UPDATE catalog_assets SET available=0 WHERE id=701`,
		`UPDATE catalog_assets SET size=size+1 WHERE id=701`,
		`UPDATE catalog_assets SET modified_ns=modified_ns+1 WHERE id=701`,
		`UPDATE catalog_assets SET playback_origin_incarnation=zeroblob(16) WHERE id=701`,
		`UPDATE metadata_provider_operation_sources SET asset_incarnation=zeroblob(16)`,
		`UPDATE metadata_provider_operation_sources SET source_generation=83`,
		`UPDATE metadata_provider_operation_sources SET source_generation=89`,
		`UPDATE catalog_asset_links SET available=0`,
		`UPDATE catalog_asset_links SET part_index=part_index+1`,
		`UPDATE catalog_asset_links SET start_seconds=start_seconds+1`,
		`UPDATE catalog_asset_links SET end_seconds=NULL`,
		`UPDATE catalog_asset_links SET cue_ordinal=1`,
		`UPDATE inventory_objects SET revision='new-object-revision' WHERE id='object-native'`,
		`UPDATE inventory_objects SET retired=1 WHERE id='object-native'`,
		`UPDATE inventory_objects SET state='missing' WHERE id='object-native'`,
		`UPDATE library_sources SET generation=generation+1 WHERE id='source-native'`,
		`UPDATE library_sources SET incarnation='new-root-incarnation' WHERE id='source-native'`,
		`UPDATE library_sources SET enabled=0 WHERE id='source-native'`,
		`UPDATE library_sources SET health='offline' WHERE id='source-native'`,
		`DELETE FROM metadata_provider_operation_sources`,
		`INSERT INTO metadata_provider_operation_sources SELECT operation_id,1,asset_id,asset_incarnation,part_index,start_seconds,end_seconds,path,size,modified_ns,available,source_id,source_generation,root_incarnation,object_revision FROM metadata_provider_operation_sources`,
	}
	for _, q := range cases {
		t.Run(q, func(t *testing.T) {
			turn, _ := effectiveBeginWholeTurn(t, w)
			// Deliberate native fixture change before the domain entry; virtual clock
			// and test custody make this a correctness control, not measured production.
			if _, err := turn.transaction.conn.ExecContext(t.Context(), q); err != nil {
				t.Fatal(err)
			}
			if _, err := applyEffectiveStageBegin(t.Context(), turn, input); err == nil {
				t.Fatal("changed native witness accepted")
			}
			var count int64
			if err := turn.transaction.conn.QueryRowContext(t.Context(), "SELECT count(*) FROM catalog_credit_images").Scan(&count); err != nil || count != 0 {
				t.Fatal("fresh refusal allocated hidden prefix", count, err)
			}
			if err := turn.closeRows(); err != nil {
				t.Fatal(err)
			}
			if _, err := turn.transaction.rollback(nil); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestEffectiveBeginCorruptMissingRetiredOrConflictingResult(t *testing.T) {
	w, _, request, identity := effectiveBeginWholeFixture(t)
	input, err := newEffectiveStageBeginCommand(identity, request)
	if err != nil {
		t.Fatal(err)
	}
	turn, _ := effectiveBeginWholeTurn(t, w)
	result, err := applyEffectiveStageBegin(t.Context(), turn, input)
	if err != nil {
		t.Fatal(err)
	}
	if err := turn.closeRows(); err != nil {
		t.Fatal(err)
	}
	if _, err := turn.transaction.commit(); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`UPDATE enrichment_command_receipts SET accepted_revision=1 WHERE command_sequence=12`,
		`UPDATE enrichment_command_receipts SET after_item='' WHERE command_sequence=12`,
		`UPDATE enrichment_command_receipts SET publication_id='' WHERE command_sequence=12`,
		`UPDATE enrichment_command_receipts SET effective_image_revision=NULL WHERE command_sequence=12`,
		`UPDATE enrichment_command_receipts SET result_count=1 WHERE command_sequence=12`,
		`UPDATE enrichment_command_receipts SET result_digest=zeroblob(32) WHERE command_sequence=12`,
		`DELETE FROM enrichment_command_receipts WHERE command_sequence=12`,
	} {
		t.Run(q, func(t *testing.T) {
			turn, _ := effectiveBeginWholeTurn(t, w)
			if _, err := turn.transaction.conn.ExecContext(t.Context(), q); err != nil {
				t.Fatal(err)
			}
			if _, _, _, err := lookupEffectiveStageBegin(t.Context(), turn, "operation-one", receiptIdentity{identity, input.digest}); !errors.Is(err, ErrResolveRequired) {
				t.Fatal("invalid/incomplete result reconstructed", err)
			}
			if err := turn.closeRows(); err != nil {
				t.Fatal(err)
			}
			if _, err := turn.transaction.rollback(nil); err != nil {
				t.Fatal(err)
			}
		})
	}
	turn, _ = effectiveBeginWholeTurn(t, w)
	altered := input.digest
	altered[0] ^= 1
	if _, _, _, err := lookupEffectiveStageBegin(t.Context(), turn, "operation-one", receiptIdentity{identity, altered}); !errors.Is(err, ErrConflict) {
		t.Fatal("digest mismatch", err)
	}
	if _, err := turn.transaction.conn.ExecContext(t.Context(), `UPDATE metadata_provider_operations SET acknowledged_command_sequence=12 WHERE id='operation-one'`); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := lookupEffectiveStageBegin(t.Context(), turn, "operation-one", receiptIdentity{identity, input.digest}); !errors.Is(err, errEffectiveBeginResultRetired) {
		t.Fatal("ACKed result disclosed", err)
	}
	if err := turn.closeRows(); err != nil {
		t.Fatal(err)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}
	_ = result
}
