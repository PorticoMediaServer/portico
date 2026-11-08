package writer

import (
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// P1 draft: compose the Basic Accept profile on the SAME writer runtime that
// owns the issued scan driver. This is the producer seam; no product wiring.
func basicProducerFixture(t *testing.T) (*scanDriverFixture, *ScanRunLease, ScanRunReference, *Writer) {
	t.Helper()
	f, lease, last := basicSourceIssuerFixture(t)
	r := f.driver.writer.effective
	bounds := basicAcceptBounds{}
	acceptDefs := basicFinalDefinitions(bounds)
	var all []stmt.Definition
	seen := map[stmt.ID]bool{}
	for _, entry := range r.registry.Manifest() {
		all = append(all, entry.Definition)
		seen[entry.Definition.ID] = true
	}
	for _, d := range acceptDefs {
		if seen[d.ID] {
			continue
		}
		all = append(all, d)
		seen[d.ID] = true
	}
	conn, err := r.executor.db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	// The driver fixture acquires writer turns at NORMAL, not the startup FULL
	// policy, so capture against the actual connection policy.
	actual, err := sqliteconn.Assert(t.Context(), conn, r.executor.config)
	if err != nil {
		cfg := r.executor.config
		cfg.Synchronous = sqliteconn.SyncMode(actual.Synchronous)
		if _, retry := sqliteconn.Assert(t.Context(), conn, cfg); retry != nil {
			t.Fatal(retry)
		}
		r.executor.config = cfg
	}
	golden := captureEffectiveFixture(t, conn, r.executor.config, all, "basic_producer_test.go")
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	accept := effectivePlan{
		recipe:            turnRecipe{limit: Work{4096, 1000000, 4096, 32 << 20, 25 * time.Millisecond}, setup: Work{16, 500, 16, 65536, time.Millisecond}, terminal: Work{16, 500, 16, 65536, 8 * time.Millisecond}},
		headerSync:        Work{WALBytes: 32, Hold: 5 * time.Millisecond},
		basicAcceptBounds: bounds,
		basicMemory:       commandMemoryBudget{4 << 20, 4 << 20},
	}
	accept.need[governor.MemoryBytes] = 8 << 20
	accept.need[governor.CPUMilli] = 100
	accept.need[governor.WALBytes] = int64(accept.recipe.limit.WALBytes)
	accept.need[governor.IOBytes] = 64 << 20
	accept.io = []governor.DeviceIO{{DeviceID: "protocol-fixture", Class: governor.Seeking, Outstanding: 1, Bytes: 64 << 20}}
	accept.recipe.limit.Rows, accept.recipe.limit.VMOps, accept.recipe.limit.Pages = 1<<20, 1<<32, 1<<20
	for _, d := range acceptDefs {
		work := Work{Rows: basicFinalRows(bounds, d.ID), VMOps: 160000, Pages: 16, Hold: 25 * time.Microsecond}
		if d.Kind == stmt.Write {
			work.WALBytes = 65536
		}
		accept.steps = append(accept.steps, statementStep{d.ID, work})
	}
	accept.registration = golden.Receipt()
	if err := composeBasicAcceptProfile(t.Context(), r, accept); err != nil {
		t.Fatal(err)
	}
	return f, lease, last, f.driver.writer
}

// Seed the native state an applied FIRST ordinary Accept needs on the driver's
// own run: probe-complete asset, staged change with a zeroed copy cursor,
// witnesses, nine sealed families, subject/entity/link and scalar evidence.
func seedProducerNativeState(t *testing.T, w *executor, original basicOriginalScope) {
	t.Helper()
	// The asset must exist before the owner seed links entity 31 to it.
	if _, err := w.db.ExecContext(t.Context(), `INSERT INTO catalog_assets(id,token,library_id,path,size,modified_ns,container,video_codec,audio_codec,width,height,duration,info_epoch,info_publication_pending) VALUES(41,'producer-asset',7,'/media-new/a.mkv',42,-11,'mkv','h264','',1920,720,100,17,1)`); err != nil {
		t.Fatal(err)
	}
	seedBasicFirstOrdinaryOwner(t, w)
	fingerprint := make([]byte, 32)
	for i := range fingerprint {
		fingerprint[i] = 0x31
	}
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT OR IGNORE INTO catalog_info_input_budget(singleton,used_bytes) VALUES(1,1000)`, nil},
		{`INSERT INTO catalog_info_shapes(id,container,video_codec,height,ref_count) VALUES(61,'mkv','h264',1080,1)`, nil},
		{`INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,revision,evidence_json,size,modified_ns,seen_job) VALUES('producer-object',?,'producer-asset',?,'a.mkv','producer-object-revision','{}',42,-11,?)`, []any{original.source.id, original.source.incarnation, original.scope.runID}},
		{`INSERT INTO catalog_info_changes(id,asset_id,asset_token,slot,phase,expected_epoch,input_revision,family_mask,fingerprint,created_ms,job_revision,target_shape_id,target_available,has_after,after_entity,upper_entity) VALUES(51,41,'producer-asset',0,3,17,7,511,?,1000,13,61,1,1,31,31)`, []any{fingerprint}},
		{`INSERT INTO catalog_info_input_witnesses(change_id,ordinal,kind,job_id,source_id,source_generation,root_incarnation,object_id,object_revision,policy_revision,fingerprint) VALUES(51,0,0,?,?,?,?,'producer-object','producer-object-revision',?,?)`, []any{original.scope.runID, original.source.id, original.source.generation, original.source.incarnation, original.run.policyRevision, fingerprint}},
		{`UPDATE catalog_asset_links SET info_pending_change=51,part_index=2 WHERE entity_id=31 AND asset_id=41`, nil},
		{`UPDATE catalog_entities SET resolution_key=0,file_size_key=42,community_rating_key=7,critic_rating_key=8,scalar_source_revision=17,aggregate_revision=23 WHERE id=31`, nil},
		{`INSERT INTO catalog_scalar_contributions(library_id,parent_id,contributor_id,source,contributor_revision,membership_sequence,community_rating,critic_rating) VALUES(7,31,31,0,17,9,7,8)`, nil},
		{`INSERT INTO catalog_asset_contributions(library_id,parent_id,asset_id,membership_refs,file_size,file_size_revision,authored_refs) VALUES(7,31,41,1,42,1,0)`, nil},
		{`INSERT INTO asset_stream_facts VALUES('producer-asset',31,42,-11,'producer-probe',1)`, nil},
		{`INSERT INTO catalog_info_scalar_old VALUES(51,4,3,NULL,NULL,'old'),(51,5,3,NULL,NULL,'vp9'),(51,6,3,NULL,NULL,'aac'),(51,7,1,1920,NULL,NULL),(51,8,1,720,NULL,NULL),(51,9,2,NULL,90,NULL)`, nil},
	} {
		if _, err := w.db.ExecContext(t.Context(), q.sql, q.args...); err != nil {
			t.Fatalf("%s: %v", q.sql, err)
		}
	}
	for family := 1; family <= 9; family++ {
		rows := int64(0)
		if family == 1 {
			rows = 1
		}
		if _, err := w.db.ExecContext(t.Context(), `INSERT INTO catalog_info_input_families(change_id,family,witness_ordinal,rows_expected,rows_staged,sealed) VALUES(51,?,0,?,?,1)`, family, rows, rows); err != nil {
			t.Fatal(err)
		}
	}
}

// The prepared original is funded in an earlier turn, so the dispatch deadline
// is clipped to it and the issued driver binds the frame. The body then reads
// native rows this draft fixture does not seed; binding the frame (not the body
// outcome) is what this slice proves.
func TestBasicProducerPreparedAcceptBindsTheIssuedFrame(t *testing.T) {
	f, lease, last, w := basicProducerFixture(t)
	d := f.driver
	identity := basicBindingIdentity(last)
	p, original, err := prepareBasicCommandOriginal(t.Context(), d, lease, last, BasicPurposeAccept, identity)
	if err != nil {
		t.Fatal(err)
	}
	seedProducerNativeState(t, w.effective.executor, original)
	if !p.expected.Deadline.Before(w.effective.executor.now().Add(MaxCommandSafetyHold)) {
		t.Fatal("preparation deadline is not earlier than the safety ceiling")
	}
	source := basicStreamFactStage{
		changeID: 51, inputRevision: 7, libraryID: original.scope.libraryID, assetID: 41, expectedEpoch: 17,
		assetToken: "producer-asset", runID: original.scope.runID, sourceID: original.source.id, incarnation: original.source.incarnation,
		objectID: "producer-object", objectRevision: "producer-object-revision", sourceGeneration: original.source.generation,
		policyRevision: original.run.policyRevision, witnessFingerprint: [32]byte{1}, factRevision: 31, size: 42, modifiedNS: -11,
		detailVersion: 1, fingerprint: "producer-probe",
	}
	analysis, err := prepareBasicObjectAnalysis(source, original.scope.libraryKey, "producer-probe-v1", `["probe","local_metadata","subtitles"]`, `["probe","local_metadata","subtitles"]`, false)
	if err != nil {
		t.Fatal(err)
	}
	nfo, err := prepareBasicNFOSet(source, original.scope.libraryKey, nil, nil, nil, basicNFOStatus{status: "observed"}, true)
	if err != nil {
		t.Fatal(err)
	}
	command, err := newBasicAcceptCommand(identity, basicAcceptRequest{
		original: original, purpose: BasicPurposeAccept, operation: basicAcceptFirstOrdinaryLeaf,
		activation: basicActivationEffects{source: source, changeRevision: 13, familyMask: 511, shapeID: 61, invalidationRevision: 1, fileSizeRevision: 1},
		entityID:   31, duration: 100, height: 1080, analysis: *analysis, nfo: nfo, oldScalars: []basicFirstOldScalar{
			{field: 4, kind: 3, text: sql.NullString{String: "old", Valid: true}},
			{field: 5, kind: 3, text: sql.NullString{String: "vp9", Valid: true}},
			{field: 6, kind: 3, text: sql.NullString{String: "aac", Valid: true}},
			{field: 7, kind: 1, integer: sql.NullInt64{Int64: 1920, Valid: true}},
			{field: 8, kind: 1, integer: sql.NullInt64{Int64: 720, Valid: true}},
			{field: 9, kind: 2, real: sql.NullFloat64{Float64: 90, Valid: true}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	q, err := command.receiptRequest()
	if err != nil {
		t.Fatal(err)
	}
	work, err := d.prepareWork(t.Context(), BasicAccept, q.Scan, 1, p)
	if err != nil {
		t.Fatal(err)
	}
	in := basicInvocation{request: q, accept: command, work: work}
	_, _, runErr := w.effective.runBasic(t.Context(), w, d.sourceDisclosure(last.Scan), in, false)
	if work.submission != scanFrameBound {
		var ae *governor.AdmissionError
		if errors.As(runErr, &ae) {
			t.Fatalf("prepared frame was not bound: %v cause=%v refusal=%v", runErr, ae.Cause, ae.Err)
		}
		t.Fatalf("prepared frame was not bound: %v (%#v)", runErr, runErr)
	}
	if runErr == nil {
		t.Fatal("unseeded body unexpectedly applied")
	}
}
