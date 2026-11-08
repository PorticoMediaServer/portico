package writer

import (
	"bytes"
	"database/sql"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// One governed runtime with the Stage, Seal and Accept profiles installed
// together, over the seeded FIRST input a real Begin would have produced.
func basicFullWalkFixture(t *testing.T) (*Writer, *executor, *governor.Authority, basicStreamFactStage, basicOriginalScope, CommandIdentity) {
	t.Helper()
	e, s, original, identity := basicControlFixture(t)
	var seq int64
	var name, path string
	if err := e.db.QueryRowContext(t.Context(), "PRAGMA database_list").Scan(&seq, &name, &path); err != nil {
		t.Fatal(err)
	}
	primary := effectiveFixturePlan()
	stage, seal := basicControlProfilePlan(BasicStage), basicControlProfilePlan(BasicSeal)
	accept := effectiveFixturePlan()
	accept.steps = nil
	accept.basicAcceptBounds = basicAcceptBounds{nfo: true}
	accept.basicMemory = commandMemoryBudget{4 << 20, 4 << 20}
	accept.need[governor.MemoryBytes] = 8 << 20
	accept.recipe.limit.Rows, accept.recipe.limit.VMOps, accept.recipe.limit.Pages = 1<<20, 1<<32, 1<<20
	acceptDefs := basicFinalDefinitions(accept.basicAcceptBounds)
	for _, d := range acceptDefs {
		work := Work{Rows: basicFinalRows(accept.basicAcceptBounds, d.ID), VMOps: 160000, Pages: 16, Hold: 25 * time.Microsecond}
		if d.Kind == stmt.Write {
			work.WALBytes = 65536
		}
		accept.steps = append(accept.steps, statementStep{d.ID, work})
	}
	defs := append(enrichmentEffectiveBeginDefinitions(), enrichmentEffectiveProducerDefinitions()...)
	defs = append(defs, effectiveDispatchDefinitions()...)
	seen := map[stmt.ID]bool{}
	for _, d := range defs {
		seen[d.ID] = true
	}
	for _, group := range [][]stmt.Definition{basicControlCommandDefinitions(BasicStage), basicControlCommandDefinitions(BasicSeal), acceptDefs} {
		for _, d := range group {
			if !seen[d.ID] {
				seen[d.ID] = true
				defs = append(defs, d)
			}
		}
	}
	conn, err := e.db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	golden := captureEffectiveFixture(t, conn, e.config, defs, "basic_copy_end_to_end_test.go", "basic_control_test.go", "basic_stage_test.go")
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	primary.registration, stage.registration, seal.registration, accept.registration = golden.Receipt(), golden.Receipt(), golden.Receipt(), golden.Receipt()
	wal, err := governor.NewWALGate(governor.WALPolicy{HardBytes: 1 << 29, ReserveBytes: 1 << 20, SoftDebtBytes: 1 << 26, ActivePressureBytes: 1 << 28, MaxSampleAge: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	var partitions [4]governor.Amounts
	for i := range partitions {
		partitions[i] = primary.need
		partitions[i][governor.MemoryBytes] = max(primary.need[governor.MemoryBytes], stage.need[governor.MemoryBytes], accept.need[governor.MemoryBytes])
	}
	g, a, err := governor.New(governor.Config{Partitions: partitions, MaxGrantsPerClass: 2, Now: e.now, WAL: wal})
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := newEffectiveRuntime(e, path, g, a, wal, 1, primary)
	if err != nil {
		t.Fatal(err)
	}
	if err := composeBasicControlProfiles(t.Context(), runtime, stage, seal); err != nil {
		t.Fatal(err)
	}
	if err := composeBasicAcceptProfile(t.Context(), runtime, accept); err != nil {
		t.Fatal(err)
	}
	return &Writer{effective: runtime}, e, a, s, original, identity
}

func (c *basicControlChain) seal(in basicControlRequest) BasicResult {
	c.t.Helper()
	in.original, in.purpose = c.original, BasicPurposeSeal
	cmd, err := newBasicSealCommand(c.identity, in)
	if err != nil {
		c.t.Fatal("seal command", in.family, err)
	}
	q, _ := cmd.receiptRequest()
	disclosure := inventoryTestDisclosure(c.t, c.w, q.Scan)
	got, _, err := c.w.effective.runBasic(c.t.Context(), c.w, disclosure, basicInvocation{request: q, seal: cmd}, false)
	if err != nil {
		c.t.Fatal("seal", in.family, err)
	}
	if _, ack, e := c.w.effective.runBasic(c.t.Context(), c.w, disclosure, basicInvocation{request: q}, true); e != nil || ack.AcknowledgedThrough != c.identity.Sequence {
		c.t.Fatal("seal ACK", ack, e)
	}
	c.original.run.revision, c.original.run.lastSequence, c.original.run.acknowledgedSequence = got.AcceptedRunRevision, c.identity.Sequence, c.identity.Sequence
	c.identity.Sequence++
	c.identity.OperationID[1]++
	return got
}

// The real walk through pin: all nine families staged and sealed, probe scalars
// applied and the target shape pinned, at phase 0 with nothing frozen.
type basicWalk struct {
	w         *Writer
	e         *executor
	authority *governor.Authority
	s         basicStreamFactStage
	chain     *basicControlChain
	probe     basicProbeEffects
	shape     int64
	revision  int64
}

func basicWalkThroughPin(t *testing.T) basicWalk {
	t.Helper()
	w, e, authority, s, original, identity := basicFullWalkFixture(t)
	seedBasicFirstOrdinaryOwner(t, e)
	incarnation := [16]byte(bytes.Repeat([]byte{0x41}, 16))
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{sql: `UPDATE catalog_info_changes SET family_mask=511 WHERE id=51`},
		{sql: `UPDATE catalog_assets SET container='old',video_codec='vp9',audio_codec='aac',width=1920,height=720,duration=90,playback_origin_incarnation=?,playback_origin_revision=101,prepared_source_revision=103,file_size_revision=113,info_invalidation_revision=109 WHERE id=41`, args: []any{incarnation[:]}},
		{sql: `UPDATE catalog_asset_links SET part_index=2 WHERE entity_id=31 AND asset_id=41`},
		{sql: `UPDATE catalog_entities SET resolution_key=0,file_size_key=42,community_rating_key=7,critic_rating_key=8,scalar_source_revision=17,aggregate_revision=23 WHERE id=31`},
		{sql: `INSERT INTO catalog_scalar_contributions(library_id,parent_id,contributor_id,source,contributor_revision,membership_sequence,community_rating,critic_rating) VALUES(7,31,31,0,17,9,7,8)`},
		{sql: `INSERT INTO catalog_asset_contributions(library_id,parent_id,asset_id,membership_refs,file_size,file_size_revision,authored_refs) VALUES(7,31,41,1,42,113,0)`},
		{sql: `INSERT INTO catalog_info_input_families(change_id,family,witness_ordinal,rows_expected) VALUES(51,2,0,1),(51,3,0,1),(51,4,0,1),(51,5,0,1),(51,6,0,1),(51,7,0,1),(51,8,0,1),(51,9,0,1)`},
		// An older accepted fact with evidence: family 6 must replace the fact and
		// family 9 must leave THIS change's evidence on it, never the old one.
		{sql: `INSERT INTO asset_subtitle_facts(asset_id,revision,size,modified_ns,origin_us,timing_known,status,fingerprint,has_subtitle_inventory_evidence,inventory_revision,inventory_evidence) VALUES('basic-asset',30,42,-11,0,1,'known','subtitles:30',1,30,'inventory:30')`},
	} {
		if _, err := e.db.ExecContext(t.Context(), q.sql, q.args...); err != nil {
			t.Fatal(q.sql, err)
		}
	}
	chain := &basicControlChain{t: t, w: w, original: original, identity: identity}
	if _, err := chain.stage(basicControlRequest{operation: basicControlStreamFact, source: s, family: 1}); err != nil {
		t.Fatal("stage family 1", err)
	}
	chain.seal(basicControlRequest{operation: basicControlStreamFact, source: s, family: 1})
	for _, f := range []struct {
		family int64
		row    basicMediaRow
	}{
		{2, basicStreamRow{index: 0, streamType: "video", codec: "h264", title: "Main", isDefault: true}},
		{3, basicStreamBreadth{index: 0, fpsNumerator: 24, fpsDenominator: 1}},
		{4, basicChapterFact{37, "known", "chapters:37"}},
		{5, basicChapter{1, "Opening", 0, 3}},
		{6, basicSubtitleFact{41, 0, true, "known", "subtitles:41"}},
		{7, basicSubtitleSidecar{"sidecar-1", "/media/Arrival.srt", "sidecar:30", 30, -7}},
		{8, basicSubtitle{id: "subtitle-1", origin: "sidecar", locator: "/media/Arrival.srt", streamIndex: -1, sidecarID: "sidecar-1", format: "subrip", language: "en", title: "English"}},
		{9, basicSubtitleEvidence{41, "inventory:41"}},
	} {
		row, err := basicStageMediaRowOf(f.row)
		if err != nil {
			t.Fatal(f.family, err)
		}
		in := basicControlRequest{operation: basicControlMediaRows, source: s, family: f.family, rows: []basicStageMediaRow{row}}
		if f.family == 5 {
			in.rawChapters = 1
		}
		stage := in
		stage.end = 1
		if _, err = chain.stage(stage); err != nil {
			t.Fatal("stage family", f.family, err)
		}
		chain.seal(in)
	}
	probe := basicProbeEffects{source: s, changeRevision: 1, assetPath: "/media/Arrival.mkv", before: basicProbeScalars{"old", "vp9", "aac", 1920, 720, 90}, after: basicProbeScalars{"mkv", "h264", "", 0, 1080, 100}, playbackIncarnation: incarnation, playbackRevision: 101, preparedRevision: 103, fileSizeRevision: 113, invalidationRevision: 109}
	if _, err := chain.stage(basicControlRequest{operation: basicControlProbeScalars, source: s, probe: &probe}); err != nil {
		t.Fatal("probe scalars", err)
	}
	pin := probe
	pin.before = pin.after
	if err := e.db.QueryRowContext(t.Context(), `SELECT playback_origin_revision,prepared_source_revision FROM catalog_assets WHERE id=41`).Scan(&pin.playbackRevision, &pin.preparedRevision); err != nil {
		t.Fatal(err)
	}
	if _, err := chain.stage(basicControlRequest{operation: basicControlPinShape, source: s, probe: &pin}); err != nil {
		t.Fatal("pin", err)
	}
	var shape, revision int64
	if err := e.db.QueryRowContext(t.Context(), `SELECT target_shape_id,job_revision FROM catalog_info_changes WHERE id=51`).Scan(&shape, &revision); err != nil {
		t.Fatal(err)
	}
	return basicWalk{w, e, authority, s, chain, probe, shape, revision}
}

// The whole FIRST walk through one dispatcher: Stage and Seal of all nine
// families, probe scalars, pin, freeze, entity, the copy of families 1..9
// driven only by each turn's actual continuation, then Accept activation.
func TestBasicFirstWalkEndToEnd(t *testing.T) {
	walk := basicWalkThroughPin(t)
	w, e, authority, s, chain, shape, revision := walk.w, walk.e, walk.authority, walk.s, walk.chain, walk.shape, walk.revision
	head := basicControlRequest{source: s, entityID: 31, shapeID: shape}
	for _, op := range []basicControlOperation{basicControlFreeze, basicControlSelectEntity} {
		in := head
		in.operation, in.changeRevision = op, revision
		if _, err := chain.stage(in); err != nil {
			t.Fatal("transition", op, err)
		}
		revision++
	}
	var pending, fencedChange int64
	if err := e.db.QueryRowContext(t.Context(), `SELECT (SELECT info_publication_pending FROM catalog_assets WHERE id=41),(SELECT info_pending_change FROM catalog_asset_links WHERE entity_id=31 AND asset_id=41)`).Scan(&pending, &fencedChange); err != nil || pending != 1 || fencedChange != 51 {
		t.Fatal("publication fences before copy", pending, fencedChange, err)
	}
	// Each copy turn enters exactly where the previous saved receipt left off.
	entry, family := basicCopyCursor{}, int64(1)
	for turns := 0; ; turns++ {
		if turns > 32 {
			t.Fatal("copy walk did not finish")
		}
		in := head
		in.operation, in.family, in.changeRevision, in.copy = basicControlCopyFamily, family, revision, entry
		got, err := chain.stage(in)
		if err != nil {
			t.Fatal("copy family", family, entry, err)
		}
		revision++
		c := got.Continuation
		if c.NextFamily == 0 {
			if c != (BasicContinuation{AfterEntity: 31}) || family != 9 {
				t.Fatal("copy ended early or dirty", family, c)
			}
			break
		}
		entry = basicCopyCursor{family: c.NextFamily, phase: c.NextCopyPhase, id: c.NextCopyID, aux: c.NextCopyAux, key: c.NextCopyKey, key2: c.NextCopyKey2, key3: c.NextCopyKey3}
		family = c.NextFamily
	}
	var phase, copyFamily, copyPhase, copyID, copyAux int64
	var key, key2, key3 string
	if err := e.db.QueryRowContext(t.Context(), `SELECT phase,copy_family,copy_phase,copy_id,copy_aux,copy_key,copy_key2,copy_key3 FROM catalog_info_changes WHERE id=51`).Scan(&phase, &copyFamily, &copyPhase, &copyID, &copyAux, &key, &key2, &key3); err != nil {
		t.Fatal(err)
	}
	if phase != 3 || copyFamily != 0 || copyPhase != 0 || copyID != 0 || copyAux != 0 || key != "" || key2 != "" || key3 != "" {
		t.Fatal("final copy cursor", phase, copyFamily, copyPhase, copyID, copyAux, key, key2, key3)
	}
	var factRevision, evidenceRevision sql.NullInt64
	var evidence sql.NullString
	if err := e.db.QueryRowContext(t.Context(), `SELECT revision,inventory_revision,inventory_evidence FROM asset_subtitle_facts WHERE asset_id='basic-asset'`).Scan(&factRevision, &evidenceRevision, &evidence); err != nil ||
		factRevision.Int64 != 41 || evidenceRevision.Int64 != 41 || evidence.String != "inventory:41" {
		t.Fatal("family 6 revision did not end with family 9's staged evidence", factRevision, evidenceRevision, evidence, err)
	}
	var streams, chapters, subtitles, sidecars int64
	if err := e.db.QueryRowContext(t.Context(), `SELECT (SELECT count(*) FROM asset_streams WHERE asset_id='basic-asset' AND has_asset_stream_breadth=1),(SELECT count(*) FROM asset_chapters WHERE asset_id='basic-asset'),(SELECT count(*) FROM asset_subtitles WHERE asset_id='basic-asset' AND sidecar_id='sidecar-1'),(SELECT count(*) FROM subtitle_sidecars WHERE path='/media/Arrival.srt')`).Scan(&streams, &chapters, &subtitles, &sidecars); err != nil ||
		streams != 1 || chapters != 1 || subtitles != 1 || sidecars != 1 {
		t.Fatal("canonical rows after copy", streams, chapters, subtitles, sidecars, err)
	}
	basicDispatchReturned(t, e, authority)
	// Accept is the only 3->4 transition and needs exactly this post-copy state.
	analysis, err := prepareBasicObjectAnalysis(s, "basic-library", "actual-probe-algorithm", `["probe","local_metadata","subtitles"]`, `["probe","local_metadata","subtitles"]`, false)
	if err != nil {
		t.Fatal(err)
	}
	nfo, err := prepareBasicNFOSet(s, "basic-library", nil, nil, nil, basicNFOStatus{status: "observed"}, true)
	if err != nil {
		t.Fatal(err)
	}
	var invalidation, fileSize int64
	if err := e.db.QueryRowContext(t.Context(), `SELECT info_invalidation_revision,file_size_revision FROM catalog_assets WHERE id=41`).Scan(&invalidation, &fileSize); err != nil {
		t.Fatal(err)
	}
	old, err := readBasicOldScalarsForTest(t, e)
	if err != nil {
		t.Fatal(err)
	}
	command, err := newBasicAcceptCommand(chain.identity, basicAcceptRequest{original: chain.original, purpose: BasicPurposeAccept, operation: basicAcceptFirstOrdinaryLeaf,
		activation: basicActivationEffects{source: s, changeRevision: revision, familyMask: 511, shapeID: shape, invalidationRevision: invalidation, fileSizeRevision: fileSize},
		entityID:   31, duration: 100, height: 1080, analysis: *analysis, nfo: nfo, oldScalars: old})
	if err != nil {
		t.Fatal("accept command", err)
	}
	q, err := command.receiptRequest()
	if err != nil {
		t.Fatal(err)
	}
	accepted, _, err := w.effective.runBasic(t.Context(), w, inventoryTestDisclosure(t, w, q.Scan), basicInvocation{request: q, accept: command}, false)
	if err != nil || accepted.Outcome.State != Applied || len(accepted.Members) != 1 || accepted.Members[0].AcceptedAssetRevision != s.expectedEpoch+1 {
		t.Fatal("accept", accepted, err)
	}
	basicDispatchReturned(t, e, authority)
	var finalPhase, epoch int64
	if err := e.db.QueryRowContext(t.Context(), `SELECT (SELECT phase FROM catalog_info_changes WHERE id=51),(SELECT info_epoch FROM catalog_assets WHERE id=41)`).Scan(&finalPhase, &epoch); err != nil || finalPhase != 5 || epoch != s.expectedEpoch+1 {
		t.Fatal("after activation", finalPhase, epoch, err)
	}
	var cleared int64
	var linkPending, linkShape sql.NullInt64
	if err := e.db.QueryRowContext(t.Context(), `SELECT (SELECT info_publication_pending FROM catalog_assets WHERE id=41),(SELECT info_pending_change FROM catalog_asset_links WHERE entity_id=31 AND asset_id=41),(SELECT info_shape_id FROM catalog_asset_links WHERE entity_id=31 AND asset_id=41)`).Scan(&cleared, &linkPending, &linkShape); err != nil || cleared != 0 || linkPending.Valid || !linkShape.Valid {
		t.Fatal("publication fences after Accept", cleared, linkPending, linkShape, err)
	}
}

// The OLD scalars the real probe recorded, in the Accept command's field order.
func readBasicOldScalarsForTest(t *testing.T, e *executor) ([]basicFirstOldScalar, error) {
	t.Helper()
	rows, err := e.db.QueryContext(t.Context(), `SELECT field,kind,old_integer,old_real,old_text FROM catalog_info_scalar_old WHERE change_id=51 ORDER BY field`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []basicFirstOldScalar
	for rows.Next() {
		var v basicFirstOldScalar
		if err := rows.Scan(&v.field, &v.kind, &v.integer, &v.real, &v.text); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
