package writer

import (
	"testing"
	"testing/synctest"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
)

// acceptBeginFixture seeds the exact post-BEGIN state a real V2 BEGIN leaves:
// a hidden zero-member credit image, its immutable BEGIN receipt and the staged,
// acknowledged owner. It uses the actual native E/S seal lineage and the real
// header insert/attach/record effects, so acceptance verifies persisted rows.
// Nothing here mints a source proof, disclosure or fixture permit.
func acceptBeginFixture(t *testing.T) (*executor, nativeEnrichmentSeals, *effectiveStageBeginCommand, effectiveBeginImage, EffectiveStageBeginResult) {
	t.Helper()
	e, f := nativeEnrichmentSealsFixture(t)
	var assetInc, linkInc [16]byte
	for i := range assetInc {
		assetInc[i] = byte(160 + i)
		linkInc[i] = byte(208 + i)
	}
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := e.db.ExecContext(t.Context(), q, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`UPDATE catalog_entities SET credit_owner_locked=0 WHERE id=101`)
	exec(`UPDATE metadata_provider_operations SET hierarchy_revision=0,selection_revision=0,descriptive_revision=0,proof_stage_slot=0,proof_current_slot=NULL,proof_pin_units=2,proof_reserved_bytes=?,proof_cleanup_phase='idle',status='staged',phase='enrichment-v1',lease='native-lease',lease_until='2100-11-05T12:30:00Z',page=59,after_item='opaque-page:61',credit_image_id=NULL WHERE id='operation-one'`, f.evidenceFacts.RetainedBytes+f.sourceFacts.RetainedBytes)
	exec(`UPDATE libraries SET metadata_language='en-CA' WHERE id='library-native'`)
	exec(`INSERT INTO library_sources(id,library_id,configured_root,root,generation,incarnation,root_identity,identity_confirmed,enabled,health) VALUES('source-native','library-native','/configured','/physical',97,'root-incarnation:native','root-identity:211',1,1,'ready')`)
	exec(`INSERT INTO catalog_assets(id,token,path,size,modified_ns,container,video_codec,audio_codec,width,height,duration,library_id,playback_origin_incarnation,playback_origin_revision,info_epoch) VALUES(701,'asset-token:native','namespace/movie.mkv',123456,1793860000111,'matroska','h264','aac',1920,1080,99.5,7,?,89,83)`, assetInc[:])
	exec(`INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,object_identity,revision,evidence_json,size,modified_ns,state,retired) VALUES('object-native','source-native','asset-token:native','root-incarnation:native','movie.mkv','object-identity:109','object-revision:107','{}',123456,1793860000111,'available',0)`)
	exec(`UPDATE catalog_assets SET info_owner_object='object-native',info_owner_revision='object-revision:107' WHERE id=701`)
	exec(`INSERT INTO catalog_asset_links(entity_id,asset_id,part_index,start_seconds,end_seconds,library_id,playback_origin_incarnation,playback_origin_revision) VALUES(101,701,3,1.25,9.5,7,?,91)`, linkInc[:])
	exec(`INSERT INTO metadata_provider_operation_sources(operation_id,ordinal,asset_id,asset_incarnation,part_index,start_seconds,end_seconds,path,size,modified_ns,available,source_id,source_generation,root_incarnation,object_revision) VALUES('operation-one',0,701,?,3,1.25,9.5,'namespace/movie.mkv',123456,1793860000111,1,'source-native',97,'root-incarnation:native','object-revision:107')`, assetInc[:])
	exec(`INSERT INTO metadata_provider_policy(library_id,provider,incarnation,revision,enabled,priority,region,algorithm,refresh_mode,credit_enabled) SELECT 7,'tmdb',policy_incarnation,37,1,1,'CA','native-algorithm','fill_missing',1 FROM metadata_provider_operations WHERE id='operation-one'`)
	exec(`INSERT INTO screen_metadata_consent(singleton,revision,confirmed) VALUES(1,41,1)`)
	turn, steps := effectiveBeginWholeTurn(t, e)
	owner, err := effectiveBeginOwnerFacts(t.Context(), turn, steps[enrichmentEffectiveOwner], f.scope.ownerID)
	if err != nil {
		t.Fatal(err)
	}
	entity, err := effectiveBeginEntityFacts(t.Context(), turn, steps[enrichmentEffectiveEntity], f.scope.entityID)
	if err != nil {
		t.Fatal(err)
	}
	producer, err := effectiveBeginReadProducer(t.Context(), turn, f.scope.ownerID, entity, f.scope.provider, f.build.evidenceID, f.source.publicationID, "object-native")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}
	h := effectiveBeginHeaderFixture()
	h.sourceManifest = [32]byte(f.source.manifest.commitment)
	h.expectedCount, h.expectedBytes = 0, 0
	h.expectedManifest = effectiveBeginBuildSeed(h)
	req := effectiveStageBeginRequest{variant: "begin", ownerID: f.scope.ownerID, evidenceID: f.build.evidenceID, publicationID: f.source.publicationID, objectID: "object-native", owner: owner, entity: entity, producer: producer, header: h}
	refs := effectiveBeginSourceReferences{evidence: effectiveBeginSealReference{f.scope.ownerID, EvidenceSeal, 1, f.evidenceReceipt, f.evidenceResult}, source: effectiveBeginSealReference{f.scope.ownerID, CreditSourceSeal, 1, f.sourceReceipt, f.sourceResult}}
	begin, err := newEffectiveStageBeginFromSeals(f.scope.identity, req, refs)
	if err != nil {
		t.Fatal(err)
	}
	// Persist the same effects a verified BEGIN commits: hidden header, owner
	// attach, immutable BEGIN receipt, image/result source pins, then its ACK.
	turn, steps = effectiveBeginWholeTurn(t, e)
	owner, err = effectiveBeginOwnerFacts(t.Context(), turn, steps[enrichmentEffectiveOwner], begin.request.ownerID)
	if err != nil {
		t.Fatal(err)
	}
	entity, err = effectiveBeginEntityFacts(t.Context(), turn, steps[enrichmentEffectiveEntity], begin.request.entity.id)
	if err != nil {
		t.Fatal(err)
	}
	producer, err = effectiveBeginReadProducer(t.Context(), turn, begin.request.ownerID, entity, owner.provider, begin.request.evidenceID, begin.request.publicationID, begin.request.objectID)
	if err != nil {
		t.Fatal(err)
	}
	if owner != begin.request.owner || entity != begin.request.entity || producer != begin.request.producer {
		t.Fatal("BEGIN input is not the actual native state")
	}
	now, err := turn.transaction.clockTime()
	if err != nil {
		t.Fatal(err)
	}
	image, err := effectiveBeginInsertHiddenHeader(t.Context(), turn, steps[enrichmentEffectiveInsert], begin.request.header, begin.digest, begin.retirementIncarnation)
	if err != nil {
		t.Fatal(err)
	}
	if err = effectiveBeginAttachHiddenHeader(t.Context(), turn, steps[enrichmentEffectiveAttach], begin.request.ownerID, owner, begin.identity, image); err != nil {
		t.Fatal(err)
	}
	result := EffectiveStageBeginResult{ProviderOperationID: begin.request.ownerID, EntityID: entity.id, ObservedCatalogRevision: entity.revision, AppliedMS: now.UnixMilli(), AfterItem: owner.afterItem, ImageID: image.id, ImageRevision: image.revision}
	digest, err := effectiveBeginResultDigest(begin.identity, begin.digest, effectiveBeginResult{ProviderOperationID: result.ProviderOperationID, EntityID: result.EntityID, ObservedCatalogRevision: result.ObservedCatalogRevision, AppliedMS: result.AppliedMS, AfterItem: result.AfterItem, NextOrdinal: 0, ImageID: image.id, ImageRevision: image.revision, ResultCount: 0, MemberCohortLength: 0})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = turn.exec(t.Context(), steps[enrichmentEffectiveRecord], begin.request.ownerID, begin.identity.Incarnation[:], begin.identity.Sequence, begin.identity.OperationID[:], begin.digest[:], "effective_stage", entity.revision, result.AppliedMS, entity.id, owner.afterItem, int64(0), nil, image.id, image.revision, int64(0), digest[:]); err != nil {
		t.Fatal(err)
	}
	result.Digest = digest
	// Mirror the verified BEGIN's retained source lineage: the ORIGINAL result
	// and the hidden image each reference the exact source seal.
	s := begin.sourceReferences.source
	inTurn := func(q string, args ...any) {
		t.Helper()
		if _, err := turn.transaction.conn.ExecContext(t.Context(), q, args...); err != nil {
			t.Fatal(err)
		}
	}
	inTurn(`UPDATE enrichment_command_receipts SET source_dependency_owner=?,source_dependency_incarnation=?,source_dependency_sequence=?,source_dependency_publication_id=?,source_dependency_provider=? WHERE provider_operation_id=? AND command_incarnation=? AND command_sequence=? AND command_kind='effective_stage'`, s.owner, s.receipt.Identity.Incarnation[:], s.receipt.Identity.Sequence, f.source.publicationID, f.sourceFacts.Provider, begin.request.ownerID, begin.identity.Incarnation[:], begin.identity.Sequence)
	inTurn(`UPDATE catalog_credit_images SET source_proof_owner=?,source_proof_incarnation=?,source_proof_sequence=?,source_proof_publication_id=?,source_proof_provider=? WHERE id=?`, s.owner, s.receipt.Identity.Incarnation[:], s.receipt.Identity.Sequence, f.source.publicationID, f.sourceFacts.Provider, image.id)
	// Both the hidden image and the unresolved BEGIN result retain the SAME
	// charged cohort, exactly as the verified BEGIN's two incoming references do.
	inTurn(`UPDATE enrichment_command_receipts SET proof_pin_count=proof_pin_count+2,proof_revision=proof_revision+1 WHERE provider_operation_id=? AND command_incarnation=? AND command_sequence=? AND command_kind='credit_source_seal'`, s.owner, s.receipt.Identity.Incarnation[:], s.receipt.Identity.Sequence)
	// The original BEGIN result is acknowledged before the final command; the
	// accept sequence is last+1 over an acked predecessor.
	inTurn(`UPDATE metadata_provider_operations SET acknowledged_command_sequence=? WHERE id=?`, begin.identity.Sequence, begin.request.ownerID)
	if _, err = turn.transaction.commit(); err != nil {
		t.Fatal(err)
	}
	return e, f, begin, image, result
}

// acceptRequestFixture builds the frozen final request from the committed
// post-BEGIN rows. No caller-supplied proof or flag exists.
func acceptRequestFixture(t *testing.T, e *executor, f nativeEnrichmentSeals, begin *effectiveStageBeginCommand, image effectiveBeginImage, result EffectiveStageBeginResult) *enrichAcceptCommand {
	t.Helper()
	turn, steps := effectiveBeginWholeTurn(t, e)
	defer func() { _, _ = turn.transaction.rollback(nil) }()
	owner, err := effectiveBeginOwnerFacts(t.Context(), turn, steps[enrichmentEffectiveOwner], begin.request.ownerID)
	if err != nil {
		t.Fatal(err)
	}
	entity, err := effectiveBeginEntityFacts(t.Context(), turn, steps[enrichmentEffectiveEntity], begin.request.entity.id)
	if err != nil {
		t.Fatal(err)
	}
	producer, err := effectiveBeginReadProducer(t.Context(), turn, begin.request.ownerID, entity, owner.provider, begin.request.evidenceID, begin.request.publicationID, begin.request.objectID)
	if err != nil {
		t.Fatal(err)
	}
	h := begin.request.header
	seed := effectiveBeginBuildSeed(h)
	id := CommandIdentity{Incarnation: begin.identity.Incarnation, Sequence: begin.identity.Sequence + 1, OperationID: [16]byte{231}}
	req := enrichAcceptRequest{
		variant:       "accept_provider_credits_v1",
		beginIdentity: begin.identity,
		beginRequest:  begin.digest,
		beginResult:   result,
		owner:         owner,
		entity:        entity,
		producer:      producer,
		image:         enrichAcceptImage{id: image.id, revision: image.revision, header: h, stagedCount: h.expectedCount, stagedBytes: h.expectedBytes, seed: seed, chain: seed},
		stageSlot:     0,
		reservedBytes: f.evidenceFacts.RetainedBytes + f.sourceFacts.RetainedBytes,
	}
	cmd, err := newEnrichAcceptCommand(id, begin, req)
	if err != nil {
		t.Fatal(err)
	}
	return cmd
}

func acceptRuntime(t *testing.T, e *executor, begin *effectiveStageBeginCommand, cmd *enrichAcceptCommand) (*Writer, *EnrichmentDisclosure, EnrichmentReceiptRequest, *governor.Authority) {
	t.Helper()
	var seq int
	var name, path string
	if err := e.db.QueryRowContext(t.Context(), "PRAGMA database_list").Scan(&seq, &name, &path); err != nil {
		t.Fatal(err)
	}
	base := effectiveFixturePlan()
	baseDefs := append(enrichmentEffectiveBeginDefinitions(), enrichmentEffectiveProducerDefinitions()...)
	baseDefs = append(baseDefs, effectiveDispatchDefinitions()...)
	conn, err := e.db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	config := e.config
	config.Synchronous = sqliteconn.Normal
	base.registration = captureEffectiveFixture(t, conn, config, baseDefs).Receipt()
	historyDefs := enrichmentSealHistoryCommandDefinitions()
	history := effectiveFixturePlan()
	history.registration = captureEffectiveFixture(t, conn, config, historyDefs, "enrichment_accept_test.go").Receipt()
	history.steps = nil
	history.need[governor.MemoryBytes] = 4 << 20
	history.enrichmentMemory = commandMemoryBudget{2 << 20, 2 << 20}
	for _, d := range historyDefs {
		history.steps = append(history.steps, statementStep{d.ID, Work{16, 2000, 16, 65536, 100 * time.Microsecond}})
	}
	acceptDefs := enrichmentAcceptCommandDefinitions()
	accept := effectiveFixturePlan()
	accept.registration = captureEffectiveFixture(t, conn, config, acceptDefs, "enrichment_accept_test.go").Receipt()
	accept.steps = nil
	accept.recipe.limit = Work{65536, 100000000, 65536, 256 << 20, 25 * time.Millisecond}
	accept.need[governor.WALBytes] = 256 << 20
	accept.need[governor.MemoryBytes] = 128 << 20
	accept.effectiveSourceMemory = commandMemoryBudget{64 << 20, 64 << 20}
	for _, d := range acceptDefs {
		accept.steps = append(accept.steps, statementStep{d.ID, Work{64, 50000, 128, 65536, 10 * time.Microsecond}})
	}
	if err = conn.Close(); err != nil {
		t.Fatal(err)
	}
	wal, err := governor.NewWALGate(governor.WALPolicy{HardBytes: 1 << 29, ReserveBytes: 1 << 20, SoftDebtBytes: 1 << 26, ActivePressureBytes: 1 << 28, MaxSampleAge: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	var partitions [4]governor.Amounts
	for i := range partitions {
		partitions[i] = accept.need
	}
	g, a, err := governor.New(governor.Config{Partitions: partitions, MaxGrantsPerClass: 4, Now: e.now, WAL: wal})
	if err != nil {
		t.Fatal(err)
	}
	r, err := newEffectiveRuntime(e, path, g, a, wal, 1, base)
	if err != nil {
		t.Fatal(err)
	}
	if err = composeEnrichmentSealHistory(t.Context(), r, history, history, accept); err != nil {
		t.Fatal(err)
	}
	w := &Writer{effective: r}
	cap := &EnrichmentDisclosure{issuer: w, scope: &enrichmentScope{ownerID: begin.request.ownerID, libraryKey: "library-native", libraryID: 7, entityID: 101, lifetime: t.Context()}}
	q := EnrichmentReceiptRequest{Receipt: ReceiptReference{cmd.identity, cmd.digest}, ProviderOperationID: begin.request.ownerID, EntityID: 101, LibraryID: 7}
	return w, cap, q, a
}

func acceptInvocation(cmd *enrichAcceptCommand, cap *EnrichmentDisclosure, q EnrichmentReceiptRequest) *enrichmentInvocation {
	var scope sealOwnerScope
	scope.identity, scope.request, scope.ownerID, scope.entityID = cmd.identity, cmd.digest, q.ProviderOperationID, q.EntityID
	return &enrichmentInvocation{action: enrichmentApply, purpose: EnrichAccept, disclosure: cap, request: q,
		payload: &enrichmentFrozenPayload{scope: scope, variant: enrichmentAccept, accept: cmd}}
}

func TestEnrichmentAcceptMaterializedImageSealAndHeadFlip(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e, f, begin, image, result := acceptBeginFixture(t)
		cmd := acceptRequestFixture(t, e, f, begin, image, result)
		w, cap, q, a := acceptRuntime(t, e, begin, cmd)
		got, ack, err := w.effective.runEnrichment(t.Context(), w, acceptInvocation(cmd, cap, q))
		if err != nil || got.Outcome.State != Applied || got.Outcome.Replayed || got.Value.accepted == nil || ack != (AckResult{}) {
			t.Fatal("accept", got, ack, err)
		}
		v := *got.Value.accepted
		if v.EntityID != 101 || v.ImageID != image.id || v.ImageRevision <= image.revision || v.RetainedCount != 0 || got.Digest == (ResultDigest{}) {
			t.Fatal("accepted result", v, got.Digest)
		}
		var sealed, head, imageRevision int64
		var ownerState string
		if err = e.db.QueryRowContext(t.Context(), `SELECT i.sealed,e.credit_image_id,i.revision FROM catalog_credit_images i JOIN catalog_entities e ON e.id=i.entity_id WHERE i.id=?`, image.id).Scan(&sealed, &head, &imageRevision); err != nil {
			t.Fatal(err)
		}
		if sealed != 1 || head != image.id || imageRevision != v.ImageRevision {
			t.Fatal("image/head", sealed, head, imageRevision)
		}
		if err = e.db.QueryRowContext(t.Context(), `SELECT status,credit_image_id FROM metadata_provider_operations WHERE id=?`, begin.request.ownerID).Scan(&ownerState, &head); err != nil {
			t.Fatal(err)
		}
		if ownerState != "applied" || head != image.id {
			t.Fatal("owner completion", ownerState, head)
		}
		var n int64
		if err = e.db.QueryRowContext(t.Context(), `SELECT count(*) FROM enrichment_command_receipts WHERE provider_operation_id=? AND command_incarnation=? AND command_sequence=? AND command_kind='effective_stage'`, begin.request.ownerID, begin.identity.Incarnation[:], begin.identity.Sequence).Scan(&n); err != nil || n != 0 {
			t.Fatal("BEGIN result not dropped", n, err)
		}
		if err = e.db.QueryRowContext(t.Context(), `SELECT count(*) FROM enrichment_command_receipts WHERE provider_operation_id=? AND command_kind='enrich_accept'`, begin.request.ownerID).Scan(&n); err != nil || n != 1 {
			t.Fatal("accept receipt", n, err)
		}
		if err = e.db.QueryRowContext(t.Context(), `SELECT proof_pin_count FROM enrichment_command_receipts WHERE provider_operation_id=? AND command_sequence=? AND command_kind='credit_source_seal'`, f.scope.ownerID, f.sourceReceipt.Identity.Sequence).Scan(&n); err != nil || n != 3 {
			t.Fatal("source pins", n, err)
		}
		replay, ack, err := w.effective.runEnrichment(t.Context(), w, acceptInvocation(cmd, cap, q))
		if err != nil || replay.Outcome.State != Applied || !replay.Outcome.Replayed || replay.Value.accepted == nil || *replay.Value.accepted != v || replay.Digest != got.Digest || ack != (AckResult{}) {
			t.Fatal("replay", replay, ack, err)
		}
		ackIn := acceptInvocation(cmd, cap, q)
		ackIn.action = enrichmentAcknowledge
		ackIn.payload = nil
		if acked, ackRes, err := w.effective.runEnrichment(t.Context(), w, ackIn); err != nil || ackRes.AcknowledgedThrough != cmd.identity.Sequence {
			t.Fatal("ack", acked, ackRes, err)
		}
		if retired, _, err := w.effective.runEnrichment(t.Context(), w, acceptInvocation(cmd, cap, q)); err != nil || retired.Outcome.State != ResultRetired || retired.Value.accepted != nil {
			t.Fatal("floor", retired, err)
		}
		if a.Usage() != ([4]governor.Amounts{}) || e.db.Stats().InUse != 0 {
			t.Fatal("custody not returned", a.Usage(), e.db.Stats())
		}
	})
}

func TestEnrichmentAcceptRefusesBrokenImageOrContext(t *testing.T) {
	for _, tc := range []struct {
		name string
		sql  string
		bind bool
	}{
		{name: "staged chain", sql: `UPDATE catalog_credit_images SET build_chain=zeroblob(32) WHERE id=?`, bind: true},
		{name: "head revision", sql: `UPDATE catalog_entities SET revision=revision+1 WHERE id=101`},
		{name: "source seal digest", sql: `UPDATE enrichment_command_receipts SET result_digest=zeroblob(32) WHERE command_kind='credit_source_seal'`},
		{name: "expired lease", sql: `UPDATE metadata_provider_operations SET lease_until='2000-01-01T00:00:00Z' WHERE id='operation-one'`},
		{name: "source proof cleared", sql: `UPDATE catalog_credit_images SET source_proof_owner=NULL,source_proof_incarnation=NULL,source_proof_sequence=NULL,source_proof_publication_id=NULL,source_proof_provider=NULL WHERE id=?`, bind: true},
		{name: "reservation short", sql: `UPDATE metadata_provider_operations SET proof_reserved_bytes=1 WHERE id='operation-one'`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				e, f, begin, image, result := acceptBeginFixture(t)
				cmd := acceptRequestFixture(t, e, f, begin, image, result)
				w, cap, q, a := acceptRuntime(t, e, begin, cmd)
				var args []any
				if tc.bind {
					args = []any{image.id}
				}
				if _, err := e.db.ExecContext(t.Context(), tc.sql, args...); err != nil {
					t.Fatal(err)
				}
				got, _, err := w.effective.runEnrichment(t.Context(), w, acceptInvocation(cmd, cap, q))
				if err == nil || got != (enrichmentCommandResult{}) {
					t.Fatal("broken image/context accepted", got, err)
				}
				var sealed, acceptRows, headNull int64
				if err = e.db.QueryRowContext(t.Context(), `SELECT i.sealed,(SELECT count(*) FROM enrichment_command_receipts WHERE command_kind='enrich_accept'),(SELECT credit_image_id IS NULL FROM catalog_entities WHERE id=101) FROM catalog_credit_images i WHERE i.id=?`, image.id).Scan(&sealed, &acceptRows, &headNull); err != nil {
					t.Fatal(err)
				}
				if sealed != 0 || acceptRows != 0 || headNull != 1 {
					t.Fatal("partial accept effect", sealed, acceptRows, headNull)
				}
				if a.Usage() != ([4]governor.Amounts{}) {
					t.Fatal("leaked grant", a.Usage())
				}
			})
		})
	}
}

func TestEnrichmentAcceptStatementInventoryIsClosed(t *testing.T) {
	seen := map[string]bool{}
	for _, d := range enrichmentAcceptCommandDefinitions() {
		if seen[string(d.ID)] {
			t.Fatalf("duplicate accept definition %s", d.ID)
		}
		seen[string(d.ID)] = true
		if n := commandStatementMaxUses(EnrichAccept, d.ID); n <= 0 {
			t.Fatalf("missing accept max uses for %s", d.ID)
		}
	}
}
