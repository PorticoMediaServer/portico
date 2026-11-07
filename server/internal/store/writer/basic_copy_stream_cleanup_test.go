package writer

import (
	"errors"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

func basicCleanupStreamTestDefinitions() []stmt.Definition {
	return append(basicCopyStepTestDefinitions(), basicCopyStreamCleanupDefinitions()...)
}

// Setup precedes actual FULL Capture. Accept, prefence, fact/row copy,
// cleanup and caller cursor persistence all use the SAME basicHandlerTurn.
func basicCleanupStreamFixture(t *testing.T, defs []stmt.Definition) (*executor, *commandTurn, basicStreamCopyFence) {
	t.Helper()
	family, replace := int64(2), false
	w, input := basicCompleteAcceptFixture(t)
	for _, q := range []string{
		`UPDATE catalog_info_new_streams SET language='en',title='captured video',color_transfer='bt709',color_primaries='bt2020',detail_json='{"width":1920}' WHERE change_id=51 AND input_ordinal=0`,
		`UPDATE catalog_info_new_stream_breadth SET audio_variant='main',chroma_subsampling='4:2:0',fps_numerator=24000,fps_denominator=1001,frame_rate_mode='constant',interlace_analysis='progressive',dovi_rpu=1,dovi_base_layer=1,dovi_metadata_known=1,dovi_enhancement_type='mel',dovi_cm_version='4.0',playback_frames_revision='frames:31',packet_peak_revision='peak:31' WHERE change_id=51 AND input_ordinal=0`,
		`INSERT INTO asset_stream_facts VALUES('basic-asset',31,42,-11,'probe-fingerprint',1)`,
	} {
		if _, err := w.db.ExecContext(t.Context(), q); err != nil {
			t.Fatal(err)
		}
	}
	core := basicCopyStreamOldCore()
	sibling := core
	sibling.index, sibling.title = 9, "retained sibling"
	seedCore := func(r basicStreamRow) {
		_, _, args, _, err := basicMediaValues(input.stream.source, 0, r)
		if err != nil {
			t.Fatal(err)
		}
		d := basicCopyStreamDefinition(t, basicCopyStreamDefinitions(), basicCopyStreamInsert)
		if _, err = w.db.ExecContext(t.Context(), d.SQL, args[1:]...); err != nil {
			t.Fatal(err)
		}
	}
	seedBreadth := func(r basicStreamBreadth) {
		_, _, args, _, err := basicMediaValues(input.stream.source, 0, r)
		if err != nil {
			t.Fatal(err)
		}
		args = append(append([]any{int64(1)}, args[3:]...), "basic-asset", r.index, int64(0))
		d := basicCopyStreamDefinition(t, basicCopyStreamDefinitions(), basicCopyBreadthInsert)
		if _, err = w.db.ExecContext(t.Context(), d.SQL, args...); err != nil {
			t.Fatal(err)
		}
	}
	for index := int64(10); index <= 18; index++ {
		r := core
		r.index = index
		seedCore(r)
	}
	// A second asset shares the same stream index but is outside custody.
	for _, q := range []string{
		`INSERT INTO catalog_assets(id,token,library_id,path,size,modified_ns,container,video_codec,audio_codec,width,height,duration,info_epoch) VALUES(42,'other-asset',7,'/media/Other.mkv',42,-11,'','','',0,0,0,17)`,
		`INSERT INTO asset_stream_facts VALUES('other-asset',31,42,-11,'other-fingerprint',1)`,
		`INSERT INTO asset_streams(asset_id,stream_index,type,codec,language,title,channels,channel_layout,is_default,is_forced) VALUES('other-asset',9,'audio','aac','','other',2,'stereo',0,0)`,
	} {
		if _, err := w.db.ExecContext(t.Context(), q); err != nil {
			t.Fatal(err)
		}
	}
	seedCore(sibling)
	seedBreadth(basicCopyStreamOldBreadth(9))
	if family == 3 || replace {
		seedCore(core)
	}
	if replace {
		seedBreadth(basicCopyStreamOldBreadth(0))
	}

	// Capture complete native source payloads; neither the constructor nor the
	// test derives a stream type/index from legacy aliases or normalizes text.
	id := basicCopyStreamStaged
	if family == 3 {
		id = basicCopyBreadthStaged
	}
	d := basicCopyStreamDefinition(t, defs, id)
	query := strings.Replace(d.SQL, "input_ordinal=?", "input_ordinal>=? ORDER BY input_ordinal", 1)
	rows, err := w.db.QueryContext(t.Context(), query, int64(51), int64(0))
	if err != nil {
		t.Fatal(err)
	}
	var captured []basicMediaRow
	for rows.Next() {
		var ordinal int64
		var token string
		if family == 2 {
			var r basicStreamRow
			err = rows.Scan(&ordinal, &token, &r.index, &r.streamType, &r.codec, &r.language, &r.title, &r.channels, &r.channelLayout, &r.isDefault, &r.isForced, &r.colorTransfer, &r.colorPrimaries, &r.detail)
			captured = append(captured, r)
		} else {
			var r basicStreamBreadth
			err = rows.Scan(&ordinal, &token, &r.index, &r.audioDescription, &r.audioVariant, &r.chroma, &r.fpsNumerator, &r.fpsDenominator, &r.frameMode, &r.interlace, &r.telecine, &r.doviRPU, &r.doviBase, &r.doviKnown, &r.doviEnhancement, &r.doviCM, &r.playbackFramesRevision, &r.packetPeakRevision)
			captured = append(captured, r)
		}
		if err != nil || ordinal != int64(len(captured)-1) || token != input.stream.source.assetToken {
			t.Fatal(ordinal, token, err)
		}
	}
	if err = errors.Join(rows.Err(), rows.Close()); err != nil {
		t.Fatal(err)
	}
	f, err := prepareBasicMediaFamily(input.stream.source, family, 0, captured)
	if err != nil {
		t.Fatal(err)
	}
	registry := nativeInventoryFixtureRegistry(t, w, defs, "basic_copy_stream_cleanup.go", "basic_copy_stream_cleanup_statements.go", "basic_copy_stream_cleanup_test.go")
	turn := basicHandlerTurn(t, w, BasicAccept, defs, registry)
	accepted, err := acceptBasicOrdinaryInput(t.Context(), turn, input, 2000)
	if err != nil {
		t.Fatal(err)
	}
	input.stream.changeRevision = accepted.changeRevision
	input.snapshot.changeRevision = accepted.changeRevision
	p, err := prefenceBasicOrdinary(t.Context(), turn, basicPrefenceEffects{input: input})
	if err != nil {
		t.Fatal(err)
	}
	fence := basicStreamCopyFence{family: f, position: p, shapeID: input.stream.shapeID, fileSizeRevision: input.snapshot.fileSizeRevision, invalidation: input.snapshot.invalidationRevision}
	p, err = basicCopyStepStart(t, turn, fence)
	if err != nil {
		t.Fatal(err)
	}
	for ordinal := int64(0); ordinal < int64(len(f.rows)); ordinal++ {
		fence.position, fence.ordinal = p, ordinal
		p, err = copyBasicMediaRowStep(t.Context(), turn, basicMediaCopyStep{fence: fence, before: basicStreamRow{}})
		if err != nil {
			t.Fatal(err)
		}
	}
	fence.position = p
	return w, turn, fence
}

func TestBasicStreamCleanupRetainedProofAndBoundedContinuation(t *testing.T) {
	w, turn, fence := basicCleanupStreamFixture(t, basicCleanupStreamTestDefinitions())
	got, err := cleanupBasicStreams(t.Context(), turn, fence)
	// Keys 0 and 3 are retained by full-PK staging proofs; 9..14 are
	// obsolete. 15 is the ninth native lookahead and must remain untouched.
	if err != nil || got != (basicStreamCleanupResult{14, true, true}) {
		t.Fatal(got, err)
	}
	for _, index := range []int64{0, 3, 15, 18} {
		basicCopyStreamReadCore(t, turn.transaction.conn, index)
	}
	var count int
	if err = turn.transaction.conn.QueryRowContext(t.Context(), `SELECT count(*) FROM asset_streams WHERE asset_id='basic-asset' AND stream_index BETWEEN 9 AND 14`).Scan(&count); err != nil || count != 0 {
		t.Fatal(count, err)
	}
	next := fence.position
	next.id, next.aux = got.id, 1
	fence.position, err = advanceBasicCopiedPosition(t.Context(), turn, fence.family.source, fence.position, next, fence.shapeID)
	if err != nil {
		t.Fatal(err)
	}
	got, err = cleanupBasicStreams(t.Context(), turn, fence)
	if err != nil || got != (basicStreamCleanupResult{18, true, false}) {
		t.Fatal(got, err)
	}
	next = fence.position
	next.id, next.aux = got.id, 1
	if _, err = advanceBasicCopiedPosition(t.Context(), turn, fence.family.source, fence.position, next, fence.shapeID); err != nil {
		t.Fatal(err)
	}
	basicMediaCommit(t, turn)
	var exact bool
	if err = w.db.QueryRowContext(t.Context(), `SELECT
 (SELECT count(*)=2 FROM asset_streams WHERE asset_id='basic-asset')
 AND (SELECT count(*)=1 FROM asset_streams WHERE asset_id='other-asset' AND stream_index=9 AND title='other')
 AND (SELECT count(*)=2 FROM catalog_info_new_streams WHERE change_id=51)
 AND (SELECT count(*)=2 FROM catalog_info_new_stream_breadth WHERE change_id=51)
 AND (SELECT revision=31 FROM asset_stream_facts WHERE asset_id='basic-asset')
 AND (SELECT info_epoch=17 AND info_publication_pending=1 FROM catalog_assets WHERE id=41)
 AND (SELECT info_pending_change=51 FROM catalog_asset_links WHERE asset_id=41 AND entity_id=31)
 AND (SELECT count(*)=0 FROM scan_command_receipts)`).Scan(&exact); err != nil || !exact {
		t.Fatal(exact, err)
	}
}

func TestBasicStreamCleanupLateReturningCorruptionRollsBack(t *testing.T) {
	defs := basicCleanupStreamTestDefinitions()
	for n := range defs {
		if defs[n].ID == basicCleanupStreamDelete {
			defs[n].SQL = strings.Replace(defs[n].SQL, "RETURNING asset_id,stream_index", "RETURNING asset_id,stream_index+1", 1)
		}
	}
	w, turn, fence := basicCleanupStreamFixture(t, defs)
	got, err := cleanupBasicStreams(t.Context(), turn, fence)
	if !errors.Is(err, ErrConflict) || got != (basicStreamCleanupResult{}) {
		t.Fatal(got, err)
	}
	var count int
	if err = turn.transaction.conn.QueryRowContext(t.Context(), `SELECT count(*) FROM asset_streams WHERE asset_id='basic-asset' AND stream_index=9`).Scan(&count); err != nil || count != 0 {
		t.Fatal("delete did not occur", count, err)
	}
	_ = turn.closeRows()
	_, _ = turn.transaction.rollback(nil)
	basicCopyStreamReadCore(t, w.db, 9)
	basicCopyStreamReadBreadth(t, w.db, 9)
}

// Pure Go structural gate: these finite statements own no command/publication
// writes and retain separate first/after shapes (zero is a real stream key).
func TestBasicStreamCleanupLiteralBounds(t *testing.T) {
	defs := basicCopyStreamCleanupDefinitions()
	if _, err := stmt.Describe(defs); err != nil {
		t.Fatal(err)
	}
	for _, d := range defs {
		if strings.Contains(d.SQL, "EXISTS") {
			t.Fatal(d.ID)
		}
		if d.ID == basicCleanupStreamFirst || d.ID == basicCleanupStreamAfter {
			if !strings.HasSuffix(d.SQL, "ORDER BY stream_index LIMIT 9") {
				t.Fatal(d.SQL)
			}
		}
	}
}

// A corrupted retained proof must stop before any delete, even though the
// full-PK query found a row. Presence alone cannot establish key identity.
func TestBasicStreamCleanupRetainedProofControl(t *testing.T) {
	defs := basicCleanupStreamTestDefinitions()
	for n := range defs {
		if defs[n].ID == basicCleanupStreamRetained {
			defs[n].SQL = strings.Replace(defs[n].SQL, "SELECT change_id,asset_id,stream_index ", "SELECT change_id,asset_id,stream_index+1 ", 1)
		}
	}
	_, turn, fence := basicCleanupStreamFixture(t, defs)
	got, err := cleanupBasicStreams(t.Context(), turn, fence)
	if !errors.Is(err, ErrConflict) || got != (basicStreamCleanupResult{}) {
		t.Fatal(got, err)
	}
	basicCopyStreamReadCore(t, turn.transaction.conn, 0)
	basicCopyStreamReadCore(t, turn.transaction.conn, 9)
}
