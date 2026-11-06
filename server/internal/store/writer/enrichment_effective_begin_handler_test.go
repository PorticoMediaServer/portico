package writer

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
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
	config := w.config
	config.Synchronous = sqliteconn.Normal
	golden := captureEffectiveFixture(t, tx.conn, config, defs)
	registry, err := stmt.New(defs, golden.Receipt())
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
	*now = time.Date(2100, time.November, 5, 10, 30, 0, 0, time.UTC)
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
	exec(`UPDATE metadata_provider_operations SET phase='enrichment-v1',hierarchy_revision=0,selection_revision=0,descriptive_revision=0,acknowledged_command_sequence=11,lease_until=? WHERE id='operation-one'`, now.Add(2*time.Hour).Format(time.RFC3339Nano))
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
	exec(`INSERT INTO credit_source_publications(publication_id,entity_id,provider,accepted_record_reference,observed_at,member_count,sealed) VALUES('aabbccddeeff00112233445566778899',101,'fixture','opaque-sealed-source://record/239','2026-11-05T09:57:13.000000000Z',0,1)`)
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
	producer, err := effectiveBeginReadProducer(t.Context(), turn, "operation-one", entity, "fixture", "evidence-source", "aabbccddeeff00112233445566778899", "object-native")
	if err != nil {
		t.Fatal(err)
	}
	// Independent native-column expectations: do not turn the production reader
	// into its own oracle by silently accepting whatever it just decoded.
	policyInc := effectiveBeginHeaderFixture().policyIncarnation
	want := effectiveBeginProducer{
		Library:  effectiveBeginLibrary{ID: 7, ExternalID: "library-native", Root: "/namespace", Retired: 0, Kind: "movie", Language: "en-CA"},
		Policy:   effectiveBeginPolicy{Incarnation: policyInc, Revision: 37, Enabled: 1, Region: "CA", Algorithm: "native-algorithm", RefreshMode: "fill_missing", CreditEnabled: 1},
		Provider: effectiveBeginProvider{Adapter: "fixture-adapter", Enabled: 1}, ConsentRevision: 41,
		Selection:   effectiveBeginSelection{Incarnation: selectionInc, Revision: 71, Role: "primary", Kind: "movie", ID: "opaque:selection", Status: "selected"},
		Evidence:    effectiveBeginEvidence{SubjectID: 601, Provider: "fixture", Kind: "movie", ID: "opaque:selection", Language: "en-CA", Region: "CA", Shape: "detail-v1", Digest: hex.EncodeToString(digest[:]), Observed: "2026-11-05T10:00:00.000000000Z", Sealed: 1},
		Subject:     effectiveBeginSubject{Incarnation: subjectInc, Revision: 73, Provider: "fixture", Kind: "movie", ID: "opaque:selection", Language: "en-CA", Region: "CA", Shape: "detail-v1"},
		Publication: effectiveBeginPublication{EntityID: 101, Provider: "fixture", Record: "opaque-sealed-source://record/239", Observed: "2026-11-05T09:57:13.000000000Z", Count: 0, Sealed: 1},
		Capture:     effectiveBeginCapture{Ordinal: 0, AssetID: 701, Incarnation: assetInc, Part: 3, Start: 1.25, End: sql.NullFloat64{Float64: 9.5, Valid: true}, Path: "namespace/movie.mkv", Size: 123456, Modified: 1793860000111, Available: 1, SourceID: "source-native", Generation: 97, Root: "root-incarnation:native", ObjectRevision: "object-revision:107"},
		Association: effectiveBeginAssociation{AssetID: 701, LibraryID: 7, Part: 3, Available: 1, Start: 1.25, End: sql.NullFloat64{Float64: 9.5, Valid: true}},
		Asset:       effectiveBeginAsset{Token: "asset-token:native", Path: "namespace/movie.mkv", Size: 123456, Modified: 1793860000111, Available: 1, LibraryID: 7, Incarnation: assetInc, InfoOwner: sql.NullString{String: "object-native", Valid: true}, InfoRevision: sql.NullString{String: "object-revision:107", Valid: true}},
		Object:      effectiveBeginObject{ID: "object-native", SourceID: "source-native", AssetToken: "asset-token:native", Root: "root-incarnation:native", Revision: "object-revision:107", Size: 123456, Modified: 1793860000111, State: "available", Retired: 0, Unsupported: ""},
		Source:      effectiveBeginSource{LibraryID: "library-native", Generation: 97, Incarnation: "root-incarnation:native", Root: "/physical", RootIdentity: "root-identity:211", Confirmed: 1, Enabled: 1, Health: "ready"},
	}
	if producer != want {
		t.Fatalf("native producer readback differs:\n got %+v\nwant %+v", producer, want)
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
	request := effectiveStageBeginRequest{variant: "begin", ownerID: "operation-one", evidenceID: "evidence-source", publicationID: "aabbccddeeff00112233445566778899", objectID: "object-native", owner: owner, entity: entity, producer: producer, header: header}
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
		`UPDATE catalog_entities SET tv_incarnation=zeroblob(16) WHERE id=101`,
		`UPDATE catalog_entities SET tv_hierarchy_revision=0 WHERE id=101`,
		`UPDATE catalog_entities SET tv_selection_revision=0 WHERE id=101`,
		`UPDATE catalog_entities SET tv_descriptive_revision=0 WHERE id=101`,
		`UPDATE catalog_entities SET metadata_incarnation=zeroblob(16) WHERE id=101`,
		`UPDATE catalog_entities SET source_revision=source_revision+1 WHERE id=101`,
		`UPDATE catalog_entities SET identity_revision=identity_revision+1 WHERE id=101`,
		`UPDATE catalog_entities SET metadata_revision=metadata_revision+1 WHERE id=101`,
		`UPDATE catalog_entities SET metadata_language='fr' WHERE id=101`,
		`UPDATE catalog_entities SET retired=1 WHERE id=101`,
		`UPDATE catalog_entities SET credit_owner_locked=0 WHERE id=101`,
		`UPDATE catalog_entities SET credit_owner_revision=24 WHERE id=101`,
		`UPDATE metadata_subject_providers SET status='matched'`,
		`UPDATE metadata_subject_providers SET role='unadmitted'`,
		`UPDATE metadata_subject_providers SET subject_kind='series'`,
		`UPDATE metadata_subject_providers SET provider_id='another-opaque-id'`,
		`UPDATE metadata_subject_providers SET revision=revision+1`,
		`UPDATE metadata_subject_providers SET incarnation=zeroblob(16)`,
		`DELETE FROM metadata_subject_providers`,
		`UPDATE metadata_provider_policy SET enabled=0`,
		`UPDATE metadata_provider_policy SET revision=revision+1`,
		`UPDATE metadata_provider_policy SET incarnation=zeroblob(16)`,
		`UPDATE metadata_provider_policy SET region='GB'`,
		`UPDATE metadata_provider_policy SET algorithm='other-algorithm'`,
		`UPDATE metadata_provider_policy SET refresh_mode='replace_unlocked'`,
		`UPDATE metadata_provider_policy SET credit_enabled=0`,
		`UPDATE metadata_providers SET enabled=0`,
		`UPDATE metadata_providers SET adapter='other-adapter'`,
		`UPDATE screen_metadata_consent SET confirmed=0`,
		`UPDATE screen_metadata_consent SET revision=revision+1`,
		`UPDATE libraries SET metadata_language='fr-CA'`,
		`UPDATE metadata_evidence SET sealed=0 WHERE id='evidence-source'`,
		`UPDATE metadata_evidence SET observed_at='another-observation' WHERE id='evidence-source'`,
		`UPDATE credit_source_publications SET sealed=0`,
		`UPDATE credit_source_publications SET accepted_record_reference='other-opaque-record'`,
		`UPDATE credit_source_publications SET observed_at='other-source-observation'`,
		`UPDATE catalog_assets SET available=0 WHERE id=701`,
		`UPDATE catalog_assets SET size=size+1 WHERE id=701`,
		`UPDATE catalog_assets SET modified_ns=modified_ns+1 WHERE id=701`,
		`UPDATE catalog_assets SET path='moved/movie.mkv' WHERE id=701`,
		`UPDATE catalog_assets SET playback_origin_incarnation=zeroblob(16) WHERE id=701`,
		`UPDATE metadata_provider_operation_sources SET asset_incarnation=zeroblob(16)`,
		`UPDATE metadata_provider_operation_sources SET source_generation=83`,
		`UPDATE metadata_provider_operation_sources SET source_generation=89`,
		`UPDATE metadata_provider_operation_sources SET asset_incarnation=(SELECT artwork_incarnation FROM catalog_entities WHERE id=101)`,
		`UPDATE metadata_provider_operation_sources SET asset_incarnation=(SELECT playback_origin_incarnation FROM catalog_asset_links WHERE entity_id=101)`,
		`UPDATE metadata_provider_operation_sources SET asset_incarnation=X'04e7e248551646326916a463f599e73a'`,
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
		`UPDATE library_sources SET root_identity='other-root-identity' WHERE id='source-native'`,
		`UPDATE library_sources SET root='/other-root' WHERE id='source-native'`,
		`UPDATE library_sources SET identity_confirmed=0 WHERE id='source-native'`,
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
	_, err = applyEffectiveStageBegin(t.Context(), turn, input)
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
	if result, _, decision, err := lookupEffectiveStageBegin(t.Context(), turn, "operation-one", receiptIdentity{identity, input.digest}); err != nil || decision != receiptRetired || result != (EffectiveStageBeginResult{}) {
		t.Fatal("ACKed result disclosed", err)
	}
	if err := turn.closeRows(); err != nil {
		t.Fatal(err)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}
}

// Read physical column names independently of production INSERT/SELECT order.
// Called only outside a held command; this is native fixture inspection.
func effectiveBeginSnapshot(t *testing.T, db *sql.DB, query string) map[string]any {
	t.Helper()
	rows, err := db.QueryContext(t.Context(), query)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil || !rows.Next() {
		t.Fatal("snapshot missing", err, rows.Err())
	}
	values := make([]any, len(columns))
	dest := make([]any, len(columns))
	for i := range values {
		dest[i] = &values[i]
	}
	if err := rows.Scan(dest...); err != nil {
		t.Fatal(err)
	}
	out := map[string]any{}
	for i, name := range columns {
		if b, ok := values[i].([]byte); ok {
			values[i] = append([]byte(nil), b...)
		}
		out[name] = values[i]
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatal("snapshot not singleton", rows.Err())
	}
	return out
}

func TestEffectiveBeginAtomicEffectsPreserveProgressAndServing(t *testing.T) {
	w, now, request, identity := effectiveBeginWholeFixture(t)
	beforeOwner := effectiveBeginSnapshot(t, w.db, "SELECT * FROM metadata_provider_operations WHERE id='operation-one'")
	beforeEntity := effectiveBeginSnapshot(t, w.db, "SELECT * FROM catalog_entities WHERE id=101")
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
	var buildID sql.NullString
	if err := w.db.QueryRowContext(t.Context(), "SELECT evidence_build_id FROM metadata_provider_operations WHERE id='operation-one'").Scan(&buildID); err != nil {
		t.Fatal(err)
	}
	if buildID.Valid {
		t.Fatal("image BEGIN created evidence-build custody", buildID.String)
	}
	beforeOwner["last_command_sequence"], beforeOwner["credit_image_id"] = int64(12), result.ImageID
	if got := effectiveBeginSnapshot(t, w.db, "SELECT * FROM metadata_provider_operations WHERE id='operation-one'"); !reflect.DeepEqual(got, beforeOwner) {
		t.Fatalf("BEGIN changed progress/other owner facts: got %v want %v", got, beforeOwner)
	}
	if got := effectiveBeginSnapshot(t, w.db, "SELECT * FROM catalog_entities WHERE id=101"); !reflect.DeepEqual(got, beforeEntity) {
		t.Fatal("BEGIN changed serving state")
	}
	expected := map[string]any{
		"provider_operation_id": "operation-one", "command_incarnation": identity.Incarnation[:], "command_sequence": int64(12), "operation_id": identity.OperationID[:], "request_digest": input.digest[:], "command_kind": "effective_stage",
		"accepted_revision": int64(203), "applied_ms": now.UnixMilli(), "entity_id": int64(101), "after_item": "opaque-page:61", "after_ordinal": int64(0), "publication_id": nil, "effective_image_id": result.ImageID, "effective_image_revision": result.ImageRevision, "result_count": int64(0), "result_digest": result.Digest[:],
	}
	if got := effectiveBeginSnapshot(t, w.db, "SELECT * FROM enrichment_command_receipts WHERE command_sequence=12"); !reflect.DeepEqual(got, expected) {
		t.Fatalf("receipt column readback differs: got %v want %v", got, expected)
	}
}

func TestEffectiveBeginPreservesEvidenceBuildCustody(t *testing.T) {
	w, _, request, identity := effectiveBeginWholeFixture(t)
	input, err := newEffectiveStageBeginCommand(identity, request)
	if err != nil {
		t.Fatal(err)
	}
	// Install distinct, FK-valid custody AFTER freezing the image request. This
	// is a native preservation fixture, not a qualified evidence-stage/sealer.
	// Its pin is not image authority and must not enter the image request/CAS.
	if _, err := w.db.ExecContext(t.Context(), `INSERT INTO metadata_evidence_builds(id,evidence_id,generation,owner_operation,owner_incarnation,owner_generation,state,phase,cursor,expected_rows,materialized_rows,expected_shape_digest,materialized_digest,materialized_charge,expires_at)
 SELECT 'evidence-build:custody','evidence-source',277,id,command_incarnation,generation,'ready','evidence-v1',1,1,1,'declared-shape:custody','materialized:custody',1024,'2100-11-05T13:30:00Z' FROM metadata_provider_operations WHERE id='operation-one'`); err != nil {
		t.Fatal(err)
	}
	if _, err := w.db.ExecContext(t.Context(), `UPDATE metadata_provider_operations SET evidence_build_id='evidence-build:custody' WHERE id='operation-one'`); err != nil {
		t.Fatal(err)
	}
	beforeOwner := effectiveBeginSnapshot(t, w.db, "SELECT * FROM metadata_provider_operations WHERE id='operation-one'")
	beforeBuild := effectiveBeginSnapshot(t, w.db, "SELECT * FROM metadata_evidence_builds WHERE id='evidence-build:custody'")
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
	beforeOwner["last_command_sequence"], beforeOwner["credit_image_id"] = int64(12), result.ImageID
	if got := effectiveBeginSnapshot(t, w.db, "SELECT * FROM metadata_provider_operations WHERE id='operation-one'"); !reflect.DeepEqual(got, beforeOwner) {
		t.Fatal("image BEGIN changed existing evidence-build custody or other owner facts")
	}
	if got := effectiveBeginSnapshot(t, w.db, "SELECT * FROM metadata_evidence_builds WHERE id='evidence-build:custody'"); !reflect.DeepEqual(got, beforeBuild) {
		t.Fatal("image BEGIN changed the retained evidence build")
	}
}

func TestEffectiveBeginEveryHiddenWritePrefixRollsBack(t *testing.T) {
	w, _, request, identity := effectiveBeginWholeFixture(t)
	input, err := newEffectiveStageBeginCommand(identity, request)
	if err != nil {
		t.Fatal(err)
	}
	originalOwner := effectiveBeginSnapshot(t, w.db, "SELECT * FROM metadata_provider_operations WHERE id='operation-one'")
	originalEntity := effectiveBeginSnapshot(t, w.db, "SELECT * FROM catalog_entities WHERE id=101")
	for _, failure := range []stmt.ID{enrichmentEffectiveInsert, enrichmentEffectiveAttach, enrichmentEffectiveRecord} {
		t.Run(string(failure), func(t *testing.T) {
			turn, _ := effectiveBeginWholeTurn(t, w)
			delete(turn.steps, failure) // W refuses the missing compiled step BEFORE SQL.
			if _, err := applyEffectiveStageBegin(t.Context(), turn, input); err == nil {
				t.Fatal("failed hidden-write prefix reported success")
			}
			if err := turn.closeRows(); err != nil {
				t.Fatal(err)
			}
			if _, err := turn.transaction.rollback(nil); err != nil {
				t.Fatal(err)
			}
			if got := effectiveBeginSnapshot(t, w.db, "SELECT * FROM metadata_provider_operations WHERE id='operation-one'"); !reflect.DeepEqual(got, originalOwner) {
				t.Fatal("owner prefix survived rollback")
			}
			if got := effectiveBeginSnapshot(t, w.db, "SELECT * FROM catalog_entities WHERE id=101"); !reflect.DeepEqual(got, originalEntity) {
				t.Fatal("serving prefix survived rollback")
			}
			var images, receipts int64
			if err := w.db.QueryRowContext(t.Context(), "SELECT count(*) FROM catalog_credit_images").Scan(&images); err != nil {
				t.Fatal(err)
			}
			if err := w.db.QueryRowContext(t.Context(), "SELECT count(*) FROM enrichment_command_receipts WHERE command_sequence=12").Scan(&receipts); err != nil {
				t.Fatal(err)
			}
			if images != 0 || receipts != 0 {
				t.Fatal("orphan hidden image or receipt prefix", images, receipts)
			}
		})
	}
}

func TestEffectiveBeginProducerKeepsNullableHeadsAndRanges(t *testing.T) {
	w, _, request, _ := effectiveBeginWholeFixture(t)
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := w.db.ExecContext(t.Context(), query, args...); err != nil {
			t.Fatal(err)
		}
	}
	policyInc := request.owner.policyIncarnation
	selectionInc := request.producer.Selection.Incarnation
	exec(`INSERT INTO metadata_publication_sets(id,entity_id,provider,incarnation,revision,generation,subject_kind,provider_id,consent_revision,policy_incarnation,policy_revision) VALUES('set-native:127',101,'fixture',?,137,31,'movie','opaque:selection',41,?,37)`, selectionInc[:], policyInc[:])
	exec(`INSERT INTO metadata_publications(id,operation_id,entity_id,provider,subject_kind,provider_id,evidence_id,set_id,selection_revision,generation,decision,observed_at) VALUES('publication-native:131','operation-one',101,'fixture','movie','opaque:selection','evidence-source','set-native:127',71,31,'accepted','2026-11-05T09:00:00Z')`)
	exec(`UPDATE metadata_subject_providers SET accepted_evidence_id='evidence-source',accepted_set_id='set-native:127',accepted_publication_id='publication-native:131'`)
	exec(`UPDATE metadata_provider_subjects SET accepted_evidence_id='evidence-source'`)
	exec(`INSERT INTO credit_source_heads(entity_id,provider,publication_id,revision) VALUES(101,'fixture','aabbccddeeff00112233445566778899',113)`)
	expected := request.producer
	expected.Selection.Evidence = sql.NullString{String: "evidence-source", Valid: true}
	expected.Selection.Set = sql.NullString{String: "set-native:127", Valid: true}
	expected.Selection.Publication = sql.NullString{String: "publication-native:131", Valid: true}
	expected.CreditHead = effectiveBeginCreditHead{Exists: true, PublicationID: "aabbccddeeff00112233445566778899", Revision: 113}
	for _, variant := range []string{"populated_heads", "null_range_info", "present_empty_revision"} {
		t.Run(variant, func(t *testing.T) {
			exec := func(query string) {
				t.Helper()
				if _, err := w.db.ExecContext(t.Context(), query); err != nil {
					t.Fatal(err)
				}
			}
			switch variant {
			case "null_range_info":
				exec(`UPDATE catalog_asset_links SET end_seconds=NULL`)
				exec(`UPDATE metadata_provider_operation_sources SET end_seconds=NULL`)
				exec(`UPDATE catalog_assets SET info_owner_object=NULL,info_owner_revision=NULL`)
				expected.Capture.End, expected.Association.End = sql.NullFloat64{}, sql.NullFloat64{}
				expected.Asset.InfoOwner, expected.Asset.InfoRevision = sql.NullString{}, sql.NullString{}
			case "present_empty_revision":
				exec(`UPDATE catalog_assets SET info_owner_object='object-native',info_owner_revision=''`)
				expected.Asset.InfoOwner, expected.Asset.InfoRevision = sql.NullString{String: "object-native", Valid: true}, sql.NullString{Valid: true}
			}
			turn, _ := effectiveBeginWholeTurn(t, w)
			got, err := effectiveBeginReadProducer(t.Context(), turn, request.ownerID, request.entity, request.owner.provider, request.evidenceID, request.publicationID, request.objectID)
			if err != nil || got != expected {
				t.Fatalf("nullable producer facts differ: got %+v want %+v err %v", got, expected, err)
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

func TestEffectiveBeginIgnoresUnrelatedGlobalCacheHead(t *testing.T) {
	w, _, request, identity := effectiveBeginWholeFixture(t)
	input, err := newEffectiveStageBeginCommand(identity, request)
	if err != nil {
		t.Fatal(err)
	}
	// A different sealed candidate in the same global cache subject is not the
	// current primary binding or the credit source head for this entity. Neither
	// candidate=current-cache-head nor a timestamp TTL is a BEGIN predicate.
	if _, err := w.db.ExecContext(t.Context(), `INSERT INTO metadata_evidence(id,subject_id,parser_version,provider,subject_kind,provider_id,language,region,request_shape,digest,observed_at,source_url,sealed)
 SELECT 'unrelated-cache-evidence',subject_id,parser_version,provider,subject_kind,provider_id,language,region,request_shape,lower(hex(zeroblob(32))),'2026-11-05T11:00:00Z','fixture://unrelated-cache',1 FROM metadata_evidence WHERE id='evidence-source'`); err != nil {
		t.Fatal(err)
	}
	if _, err := w.db.ExecContext(t.Context(), `UPDATE metadata_provider_subjects SET accepted_evidence_id='unrelated-cache-evidence' WHERE id=601`); err != nil {
		t.Fatal(err)
	}
	turn, _ := effectiveBeginWholeTurn(t, w)
	if _, err := applyEffectiveStageBegin(t.Context(), turn, input); err != nil {
		t.Fatal("unrelated global cache head refused BEGIN", err)
	}
	if err := turn.closeRows(); err != nil {
		t.Fatal(err)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}
}
