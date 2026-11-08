package writer

import (
	"testing"
	"testing/synctest"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
)

// Native FIRST source/selection/policy inputs, separate from the historical
// stage/seal lineage. This seed supplies no fixture permit or verified flag.
func verifiedEffectiveRequest(t *testing.T) (*executor, *effectiveStageBeginCommand, nativeEnrichmentSeals) {
	t.Helper()
	w, f := nativeEnrichmentSealsFixture(t)
	var assetInc, linkInc [16]byte
	for i := range assetInc {
		assetInc[i] = byte(160 + i)
		linkInc[i] = byte(208 + i)
	}
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := w.db.ExecContext(t.Context(), q, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`UPDATE metadata_provider_operations SET hierarchy_revision=0,selection_revision=0,descriptive_revision=0,proof_stage_slot=0,proof_pin_units=2,proof_reserved_bytes=? WHERE id='operation-one'`, f.evidenceFacts.RetainedBytes+f.sourceFacts.RetainedBytes)
	exec(`UPDATE libraries SET metadata_language='en-CA' WHERE id='library-native'`)
	exec(`INSERT INTO library_sources(id,library_id,configured_root,root,generation,incarnation,root_identity,identity_confirmed,enabled,health) VALUES('source-native','library-native','/configured','/physical',97,'root-incarnation:native','root-identity:211',1,1,'ready')`)
	exec(`INSERT INTO catalog_assets(id,token,path,size,modified_ns,container,video_codec,audio_codec,width,height,duration,library_id,playback_origin_incarnation,playback_origin_revision,info_epoch) VALUES(701,'asset-token:native','namespace/movie.mkv',123456,1793860000111,'matroska','h264','aac',1920,1080,99.5,7,?,89,83)`, assetInc[:])
	exec(`INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,object_identity,revision,evidence_json,size,modified_ns,state,retired) VALUES('object-native','source-native','asset-token:native','root-incarnation:native','movie.mkv','object-identity:109','object-revision:107','{}',123456,1793860000111,'available',0)`)
	exec(`UPDATE catalog_assets SET info_owner_object='object-native',info_owner_revision='object-revision:107' WHERE id=701`)
	exec(`INSERT INTO catalog_asset_links(entity_id,asset_id,part_index,start_seconds,end_seconds,library_id,playback_origin_incarnation,playback_origin_revision) VALUES(101,701,3,1.25,9.5,7,?,91)`, linkInc[:])
	exec(`INSERT INTO metadata_provider_operation_sources(operation_id,ordinal,asset_id,asset_incarnation,part_index,start_seconds,end_seconds,path,size,modified_ns,available,source_id,source_generation,root_incarnation,object_revision) VALUES('operation-one',0,701,?,3,1.25,9.5,'namespace/movie.mkv',123456,1793860000111,1,'source-native',97,'root-incarnation:native','object-revision:107')`, assetInc[:])
	exec(`INSERT INTO metadata_provider_policy(library_id,provider,incarnation,revision,enabled,priority,region,algorithm,refresh_mode,credit_enabled) SELECT 7,'tmdb',policy_incarnation,37,1,1,'CA','native-algorithm','fill_missing',1 FROM metadata_provider_operations WHERE id='operation-one'`)
	exec(`INSERT INTO screen_metadata_consent(singleton,revision,confirmed) VALUES(1,41,1)`)
	turn, steps := effectiveBeginWholeTurn(t, w)
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
	r := effectiveStageBeginRequest{variant: "begin", ownerID: f.scope.ownerID, evidenceID: f.build.evidenceID, publicationID: f.source.publicationID, objectID: "object-native", owner: owner, entity: entity, producer: producer, header: h}
	refs := effectiveBeginSourceReferences{evidence: effectiveBeginSealReference{f.scope.ownerID, EvidenceSeal, 1, f.evidenceReceipt, f.evidenceResult}, source: effectiveBeginSealReference{f.scope.ownerID, CreditSourceSeal, 1, f.sourceReceipt, f.sourceResult}}
	c, err := newEffectiveStageBeginFromSeals(f.scope.identity, r, refs)
	if err != nil {
		t.Fatal(err)
	}
	return w, c, f
}

func verifiedEffectiveRuntime(t *testing.T, w *executor, c *effectiveStageBeginCommand) (*Writer, *EnrichmentDisclosure, *governor.Authority) {
	t.Helper()
	var seq int
	var name, path string
	if err := w.db.QueryRowContext(t.Context(), "PRAGMA database_list").Scan(&seq, &name, &path); err != nil {
		t.Fatal(err)
	}
	defs := append(enrichmentEffectiveBeginDefinitions(), enrichmentEffectiveProducerDefinitions()...)
	defs = append(defs, effectiveDispatchDefinitions()...)
	defs = append(defs, enrichmentEffectiveSourcePinDefinitions()...)
	defs = append(defs, enrichmentEffectiveSourceControlDefinitions()...)
	defs = append(defs, enrichmentEffectiveVerifiedDefinitions()...)
	cfg := w.config
	cfg.Synchronous = sqliteconn.Normal
	conn, err := w.db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	golden := captureEffectiveFixture(t, conn, cfg, defs, "enrichment_native_lineage_test.go", "enrichment_effective_verified_test.go")
	if err = conn.Close(); err != nil {
		t.Fatal(err)
	}
	p := effectiveFixturePlan()
	p.registration = golden.Receipt()
	p.steps = nil
	for _, d := range defs {
		p.steps = append(p.steps, statementStep{d.ID, Work{64, 50000, 128, 65536, 100 * time.Microsecond}})
	}
	p.recipe.limit = Work{8192, 4000000, 8192, 64 << 20, 25 * time.Millisecond}
	p.need[governor.WALBytes] = 64 << 20
	p.effectiveSourceMemory = commandMemoryBudget{64 << 20, 64 << 20}
	wal, err := governor.NewWALGate(governor.WALPolicy{HardBytes: 1 << 29, ReserveBytes: 1 << 20, SoftDebtBytes: 1 << 26, ActivePressureBytes: 1 << 28, MaxSampleAge: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	var partitions [4]governor.Amounts
	for i := range partitions {
		partitions[i] = p.need
	}
	g, a, err := governor.New(governor.Config{Partitions: partitions, MaxGrantsPerClass: 2, Now: w.now, WAL: wal})
	if err != nil {
		t.Fatal(err)
	}
	r, err := newEffectiveRuntime(w, path, g, a, wal, 1, p)
	if err != nil {
		t.Fatal(err)
	}
	out := &Writer{effective: r}
	cap := &EnrichmentDisclosure{issuer: out, scope: &enrichmentScope{ownerID: c.request.ownerID, libraryKey: "library-native", libraryID: 7, entityID: 101, lifetime: t.Context()}}
	return out, cap, a
}

func TestVerifiedEffectiveBeginNativePinsReplayAndAck(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e, c, f := verifiedEffectiveRequest(t)
		w, cap, a := verifiedEffectiveRuntime(t, e, c)
		got, err := w.StageEffective(t.Context(), cap, c)
		if err != nil || got.Outcome.State != Applied || got.Outcome.Replayed || got.Value.ImageID <= 0 {
			t.Fatal("native verified BEGIN", got, err)
		}
		var imageOwner, resultOwner string
		var imageSeq, resultSeq, pins, revision int64
		if err = e.db.QueryRowContext(t.Context(), `SELECT i.source_proof_owner,i.source_proof_sequence,r.source_dependency_owner,r.source_dependency_sequence,s.proof_pin_count,s.proof_revision FROM catalog_credit_images i JOIN enrichment_command_receipts r ON r.effective_image_id=i.id JOIN enrichment_command_receipts s ON s.provider_operation_id=i.source_proof_owner AND s.command_incarnation=i.source_proof_incarnation AND s.command_sequence=i.source_proof_sequence WHERE i.id=?`, got.Value.ImageID).Scan(&imageOwner, &imageSeq, &resultOwner, &resultSeq, &pins, &revision); err != nil {
			t.Fatal(err)
		}
		if imageOwner != f.scope.ownerID || resultOwner != imageOwner || imageSeq != f.sourceReceipt.Identity.Sequence || resultSeq != imageSeq || pins != 3 || revision != 2 {
			t.Fatal("missing image/result retention", imageOwner, resultOwner, imageSeq, resultSeq, pins, revision)
		}
		replay, err := w.StageEffective(t.Context(), cap, c)
		if err != nil || replay.Outcome.State != Applied || !replay.Outcome.Replayed || replay.Value != got.Value {
			t.Fatal("original replay", replay, err)
		}
		if _, err = w.AckEffective(t.Context(), cap, effectiveReference(c)); err != nil {
			t.Fatal(err)
		}
		retired, err := w.ResolveEffective(t.Context(), cap, effectiveReference(c))
		if err != nil || retired.Outcome.State != ResultRetired || retired.Value != (EffectiveStageBeginResult{}) {
			t.Fatal("floor", retired, err)
		}
		if err = e.db.QueryRowContext(t.Context(), `SELECT proof_pin_count FROM enrichment_command_receipts WHERE provider_operation_id=? AND command_incarnation=? AND command_sequence=?`, f.scope.ownerID, c.identity.Incarnation[:], f.sourceReceipt.Identity.Sequence).Scan(&pins); err != nil || pins != 3 {
			t.Fatal("replay/ACK changed pins", pins, err)
		}
		if a.Usage() != ([4]governor.Amounts{}) || e.db.Stats().InUse != 0 {
			t.Fatal("custody not returned", a.Usage(), e.db.Stats())
		}
	})
}

func TestVerifiedEffectiveBeginRefusesBrokenSourceAndReservation(t *testing.T) {
	for _, q := range []string{`UPDATE metadata_provider_operations SET proof_reserved_bytes=0 WHERE id='operation-one'`, `UPDATE enrichment_command_receipts SET result_digest=zeroblob(32) WHERE command_kind='credit_source_seal'`} {
		t.Run(q, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				e, c, _ := verifiedEffectiveRequest(t)
				w, cap, a := verifiedEffectiveRuntime(t, e, c)
				if _, err := e.db.ExecContext(t.Context(), q); err != nil {
					t.Fatal(err)
				}
				got, err := w.StageEffective(t.Context(), cap, c)
				if err == nil || got != (EffectiveStageResult{}) {
					t.Fatal("broken source/reservation accepted", got, err)
				}
				var n, pins int64
				if err = e.db.QueryRowContext(t.Context(), `SELECT (SELECT count(*) FROM catalog_credit_images),proof_pin_count FROM enrichment_command_receipts WHERE command_kind='credit_source_seal'`).Scan(&n, &pins); err != nil || n != 0 || pins != 1 {
					t.Fatal("partial fresh effect", n, pins, err)
				}
				if a.Usage() != ([4]governor.Amounts{}) {
					t.Fatal("leaked grant", a.Usage())
				}
			})
		})
	}
}

// Compile-time interface check: no private proof or fixture permit is required
// by callers; this is the existing immutable command facade marker only.
var _ EffectiveStageBeginCommand = (*effectiveStageBeginCommand)(nil)
