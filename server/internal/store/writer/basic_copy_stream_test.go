package writer

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

func basicCopyStreamTestDefinitions() []stmt.Definition {
	return append(basicPrefenceTestDefinitions(), basicCopyStreamDefinitions()...)
}

func basicCopyStreamDefinition(t *testing.T, defs []stmt.Definition, id stmt.ID) stmt.Definition {
	t.Helper()
	for _, d := range defs {
		if d.ID == id {
			return d
		}
	}
	t.Fatal("missing literal copy definition", id)
	return stmt.Definition{}
}

func basicCopyStreamOldCore() basicStreamRow {
	return basicStreamRow{index: 0, streamType: "video", codec: "old-codec", language: "fr", title: "retained core", channels: 2, channelLayout: "stereo", isForced: true, colorTransfer: "old-transfer", colorPrimaries: "old-primaries", detail: `{"old":true}`}
}
func basicCopyStreamOldBreadth(index int64) basicStreamBreadth {
	return basicStreamBreadth{index: index, audioDescription: true, audioVariant: "retained variant", chroma: "4:2:2", fpsNumerator: 25, fpsDenominator: 1, frameMode: "variable", interlace: "retained interlace", telecine: true, doviRPU: true, doviBase: true, doviKnown: true, doviEnhancement: "fel", doviCM: "old-cm", playbackFramesRevision: "old-frames", packetPeakRevision: "old-peak"}
}

// All raw fixture setup precedes one real FULL-startup Capture. The shared
// handler fixture then supplies the existing NORMAL command frame: no private
// authority/governor/frame clone and no guarded registry bypass. Freeze,
// prefence and the tested component share that one frame and Capture receipt.
func basicCopyStreamFixture(t *testing.T, family int64, replace bool, defs []stmt.Definition) (*executor, *commandTurn, basicStreamCopyFence) {
	t.Helper()
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
	registry := nativeInventoryFixtureRegistry(t, w, defs, "basic_copy_stream.go", "basic_copy_stream_statements.go", "basic_copy_stream_test.go")
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
	return w, turn, basicStreamCopyFence{family: f, position: p, shapeID: input.stream.shapeID, fileSizeRevision: input.snapshot.fileSizeRevision, invalidation: input.snapshot.invalidationRevision}
}

func basicCopyStreamReadCore(t *testing.T, conn interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, index int64) basicStreamRow {
	t.Helper()
	var r basicStreamRow
	if err := conn.QueryRowContext(t.Context(), `SELECT stream_index,type,codec,language,title,channels,channel_layout,is_default,is_forced,color_transfer,color_primaries,detail_json FROM asset_streams WHERE asset_id='basic-asset' AND stream_index=?`, index).Scan(&r.index, &r.streamType, &r.codec, &r.language, &r.title, &r.channels, &r.channelLayout, &r.isDefault, &r.isForced, &r.colorTransfer, &r.colorPrimaries, &r.detail); err != nil {
		t.Fatal(err)
	}
	return r
}
func basicCopyStreamReadBreadth(t *testing.T, conn interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, index int64) basicStreamBreadth {
	t.Helper()
	var r basicStreamBreadth
	var present int64
	if err := conn.QueryRowContext(t.Context(), `SELECT stream_index,has_asset_stream_breadth,breadth_audio_description,breadth_audio_variant,breadth_chroma_subsampling,breadth_fps_numerator,breadth_fps_denominator,breadth_frame_rate_mode,breadth_interlace_analysis,breadth_telecine,breadth_dovi_rpu,breadth_dovi_base_layer,breadth_dovi_metadata_known,breadth_dovi_enhancement_type,breadth_dovi_cm_version,breadth_playback_frames_revision,breadth_packet_peak_revision FROM asset_streams WHERE asset_id='basic-asset' AND stream_index=?`, index).Scan(&r.index, &present, &r.audioDescription, &r.audioVariant, &r.chroma, &r.fpsNumerator, &r.fpsDenominator, &r.frameMode, &r.interlace, &r.telecine, &r.doviRPU, &r.doviBase, &r.doviKnown, &r.doviEnhancement, &r.doviCM, &r.playbackFramesRevision, &r.packetPeakRevision); err != nil || present != 1 {
		t.Fatal(r, present, err)
	}
	return r
}

func basicCopyStreamAssertRetained(t *testing.T, w *executor) {
	t.Helper()
	sibling := basicCopyStreamOldCore()
	sibling.index, sibling.title = 9, "retained sibling"
	if got := basicCopyStreamReadCore(t, w.db, 9); got != sibling {
		t.Fatal("sibling core changed", got)
	}
	if got := basicCopyStreamReadBreadth(t, w.db, 9); got != basicCopyStreamOldBreadth(9) {
		t.Fatal("sibling breadth changed", got)
	}
	var exact bool
	if err := w.db.QueryRowContext(t.Context(), `SELECT
 (SELECT count(*)=2 FROM asset_streams)
 AND (SELECT revision=31 AND size=42 AND modified_ns=-11 AND fingerprint='probe-fingerprint' AND detail_version=1 FROM asset_stream_facts WHERE asset_id='basic-asset')
 AND (SELECT info_epoch=17 AND info_publication_pending=1 FROM catalog_assets WHERE id=41)
 AND (SELECT info_pending_change=51 FROM catalog_asset_links WHERE entity_id=31 AND asset_id=41)
 AND (SELECT phase=2 AND job_revision=15 AND copy_family=0 AND copy_phase=0 AND copy_id=0 AND copy_aux=0 AND copy_key='' AND copy_key2='' AND copy_key3='' FROM catalog_info_changes WHERE id=51)
 AND (SELECT count(*)=2 FROM catalog_info_input_families WHERE change_id=51 AND family IN(2,3) AND rows_expected=2 AND rows_staged=2 AND sealed=1)
 AND (SELECT count(*)=2 FROM catalog_info_new_streams WHERE change_id=51)
 AND (SELECT count(*)=2 FROM catalog_info_new_stream_breadth WHERE change_id=51)
 AND (SELECT count(*)=0 FROM scan_command_receipts)`).Scan(&exact); err != nil || !exact {
		t.Fatal("copy altered parent/staging/fences/cursor/receipt", exact, err)
	}
}

func TestBasicCopyStreamRowInsertAndReplacePreserveBreadthAndSibling(t *testing.T) {
	for _, replace := range []bool{false, true} {
		t.Run(map[bool]string{false: "insert", true: "replace"}[replace], func(t *testing.T) {
			w, turn, fence := basicCopyStreamFixture(t, 2, replace, basicCopyStreamTestDefinitions())
			in := basicStreamRowCopyEffects{basicStreamCopyFence: fence, hadBefore: replace}
			if replace {
				in.before = basicCopyStreamOldCore()
			}
			got, err := installBasicStreamRow(t.Context(), turn, in)
			want := fence.family.rows[0].(basicStreamRow)
			if err != nil || got != want {
				t.Fatal(got, err)
			}
			basicMediaCommit(t, turn)
			if actual := basicCopyStreamReadCore(t, w.db, 0); actual != want {
				t.Fatal(actual)
			}
			if replace {
				if actual := basicCopyStreamReadBreadth(t, w.db, 0); actual != basicCopyStreamOldBreadth(0) {
					t.Fatal("core update lost retained breadth", actual)
				}
			} else {
				var present int64
				if err = w.db.QueryRowContext(t.Context(), `SELECT has_asset_stream_breadth FROM asset_streams WHERE asset_id='basic-asset' AND stream_index=0`).Scan(&present); err != nil || present != 0 {
					t.Fatal(present, err)
				}
			}
			basicCopyStreamAssertRetained(t, w)
		})
	}
}
func TestBasicCopyStreamBreadthInsertAndReplacePreserveCoreAndSibling(t *testing.T) {
	for _, replace := range []bool{false, true} {
		t.Run(map[bool]string{false: "insert", true: "replace"}[replace], func(t *testing.T) {
			w, turn, fence := basicCopyStreamFixture(t, 3, replace, basicCopyStreamTestDefinitions())
			in := basicStreamBreadthCopyEffects{basicStreamCopyFence: fence, hadBefore: replace}
			if replace {
				in.before = basicCopyStreamOldBreadth(0)
			}
			got, err := installBasicStreamBreadth(t.Context(), turn, in)
			want := fence.family.rows[0].(basicStreamBreadth)
			if err != nil || got != want {
				t.Fatal(got, err)
			}
			basicMediaCommit(t, turn)
			if actual := basicCopyStreamReadBreadth(t, w.db, 0); actual != want {
				t.Fatal(actual)
			}
			if actual := basicCopyStreamReadCore(t, w.db, 0); actual != basicCopyStreamOldCore() {
				t.Fatal("breadth update changed parent core", actual)
			}
			basicCopyStreamAssertRetained(t, w)
		})
	}
}

func TestBasicCopyStreamRefusesCapturedStagedOwnerAndPhaseDrift(t *testing.T) {
	for _, family := range []int64{2, 3} {
		name := map[int64]string{2: "stream", 3: "breadth"}[family]
		for _, drift := range []string{"captured", "staged", "owner", "phase"} {
			t.Run(name+"/"+drift, func(t *testing.T) {
				w, turn, fence := basicCopyStreamFixture(t, family, true, basicCopyStreamTestDefinitions())
				core := basicStreamRowCopyEffects{basicStreamCopyFence: fence, hadBefore: true, before: basicCopyStreamOldCore()}
				breadth := basicStreamBreadthCopyEffects{basicStreamCopyFence: fence, hadBefore: true, before: basicCopyStreamOldBreadth(0)}
				var q string
				switch drift {
				case "captured":
					core.before.detail = `{"uncaptured":true}`
					breadth.before.packetPeakRevision = "uncaptured-peak"
				case "staged":
					q = `UPDATE catalog_info_new_streams SET color_primaries='uncaptured' WHERE change_id=51 AND input_ordinal=0`
					if family == 3 {
						q = `UPDATE catalog_info_new_stream_breadth SET packet_peak_revision='uncaptured' WHERE change_id=51 AND input_ordinal=0`
					}
				case "owner":
					q = `UPDATE catalog_subjects SET kind=2 WHERE id=31`
				case "phase":
					q = `UPDATE catalog_info_changes SET phase=3 WHERE id=51`
				}
				if q != "" {
					if _, err := turn.transaction.conn.ExecContext(t.Context(), q); err != nil {
						t.Fatal(err)
					}
				}
				if family == 2 {
					got, err := installBasicStreamRow(t.Context(), turn, core)
					if !errors.Is(err, ErrConflict) || got != (basicStreamRow{}) {
						t.Fatal(got, err)
					}
				} else {
					got, err := installBasicStreamBreadth(t.Context(), turn, breadth)
					if !errors.Is(err, ErrConflict) || got != (basicStreamBreadth{}) {
						t.Fatal(got, err)
					}
				}
				_ = turn.closeRows()
				_, _ = turn.transaction.rollback(nil)
				if actual := basicCopyStreamReadCore(t, w.db, 0); actual != basicCopyStreamOldCore() {
					t.Fatal(actual)
				}
				if actual := basicCopyStreamReadBreadth(t, w.db, 0); actual != basicCopyStreamOldBreadth(0) {
					t.Fatal(actual)
				}
			})
		}
	}
}

func TestBasicCopyBreadthRequiresActualNativeParent(t *testing.T) {
	_, turn, fence := basicCopyStreamFixture(t, 3, false, basicCopyStreamTestDefinitions())
	if _, err := turn.transaction.conn.ExecContext(t.Context(), `DELETE FROM asset_streams WHERE asset_id='basic-asset' AND stream_index=0`); err != nil {
		t.Fatal(err)
	}
	got, err := installBasicStreamBreadth(t.Context(), turn, basicStreamBreadthCopyEffects{basicStreamCopyFence: fence})
	if !errors.Is(err, ErrConflict) || got != (basicStreamBreadth{}) {
		t.Fatal(got, err)
	}
}

// Two real native fault definitions mutate ONLY RETURNING. Each keeps its own
// actual Capture receipt, performs the intended UPDATE, then must release zero
// output and let the caller roll back that write and preserve retained rows.
func TestBasicCopyStreamReturningMutantsRollbackNativeWrites(t *testing.T) {
	for _, family := range []int64{2, 3} {
		t.Run(map[int64]string{2: "stream", 3: "breadth"}[family], func(t *testing.T) {
			defs := basicCopyStreamTestDefinitions()
			id, from, to := basicCopyStreamUpdate, "RETURNING asset_id,stream_index,type,codec,language,title,", "RETURNING asset_id,stream_index,type,title,language,codec,"
			if family == 3 {
				id, from, to = basicCopyBreadthUpdate, "breadth_fps_numerator,breadth_fps_denominator,", "breadth_fps_denominator,breadth_fps_numerator,"
			}
			for n := range defs {
				if defs[n].ID == id {
					parts := strings.SplitN(defs[n].SQL, " RETURNING ", 2)
					if family == 2 {
						defs[n].SQL = strings.Replace(defs[n].SQL, from, to, 1)
					} else {
						defs[n].SQL = parts[0] + " RETURNING " + strings.Replace(parts[1], from, to, 1)
					}
				}
			}
			w, turn, fence := basicCopyStreamFixture(t, family, true, defs)
			if family == 2 {
				got, err := installBasicStreamRow(t.Context(), turn, basicStreamRowCopyEffects{basicStreamCopyFence: fence, hadBefore: true, before: basicCopyStreamOldCore()})
				if !errors.Is(err, ErrConflict) || got != (basicStreamRow{}) {
					t.Fatal(got, err)
				}
				if actual := basicCopyStreamReadCore(t, turn.transaction.conn, 0); actual != fence.family.rows[0].(basicStreamRow) {
					t.Fatal("mutant did not perform actual core write", actual)
				}
			} else {
				got, err := installBasicStreamBreadth(t.Context(), turn, basicStreamBreadthCopyEffects{basicStreamCopyFence: fence, hadBefore: true, before: basicCopyStreamOldBreadth(0)})
				if !errors.Is(err, ErrConflict) || got != (basicStreamBreadth{}) {
					t.Fatal(got, err)
				}
				if actual := basicCopyStreamReadBreadth(t, turn.transaction.conn, 0); actual != fence.family.rows[0].(basicStreamBreadth) {
					t.Fatal("mutant did not perform actual breadth write", actual)
				}
			}
			_ = turn.closeRows()
			_, _ = turn.transaction.rollback(nil)
			if actual := basicCopyStreamReadCore(t, w.db, 0); actual != basicCopyStreamOldCore() {
				t.Fatal("core escaped rollback", actual)
			}
			if actual := basicCopyStreamReadBreadth(t, w.db, 0); actual != basicCopyStreamOldBreadth(0) {
				t.Fatal("breadth escaped rollback", actual)
			}
			if actual := basicCopyStreamReadBreadth(t, w.db, 9); actual != basicCopyStreamOldBreadth(9) {
				t.Fatal("sibling escaped rollback", actual)
			}
		})
	}
}

func TestBasicCopyStreamPreflightsCompleteRawPayload(t *testing.T) {
	// The last before-field alone exhausts admission. No JSON parse, clone,
	// hashing, native setup or read is needed to reject either closed family.
	oversized := strings.Repeat("x", maxCanonicalRequestBytes)
	for _, family := range []int64{2, 3} {
		f := &basicMediaFamily{valid: true, family: family, source: basicStreamFactStage{changeID: 51}, rows: []basicMediaRow{basicStreamRow{}}}
		fence := basicStreamCopyFence{family: f, position: basicCopyPosition{changeID: 51, changeRevision: 15, phase: 2, afterEntity: 31}, shapeID: 61, fileSizeRevision: 1, invalidation: 1}
		var next, before basicMediaRow = basicStreamRow{streamType: "video"}, basicStreamRow{streamType: "video", detail: oversized}
		if family == 3 {
			next, before = basicStreamBreadth{}, basicStreamBreadth{packetPeakRevision: oversized}
		}
		if err := preflightBasicStreamCopy(fence, next, before); !errors.Is(err, ErrCap) {
			t.Fatal(family, err)
		}
	}
}
