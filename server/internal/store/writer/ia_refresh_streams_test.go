package writer

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/stmttest"
)

// IA21 walks only the journal and the live keyset. The fixture reuses IA20's
// adopted C019 journal fragment and security producers, and registers the walk
// statements next to the core ones so a Step and the key that names it share
// one snapshot. No new identity/session producer is added here.
func iaRefreshWalkDefinitions() []stmt.Definition {
	defs := append([]stmt.Definition{}, iaRefreshDefinitions()...)
	defs = append(defs, iaRefreshStreamDefinitions()...)
	defs = append(defs, iaRefreshChapterDefinitions()...)
	return defs
}

func iaRefreshWalkFixture(t *testing.T) (*executor, *iaEntryRegistry, *usOwnerAuthorityProof, iaRefreshRequest) {
	t.Helper()
	w, r, p, q := iaRefreshFixture(t)
	registry, err := stmttest.New(t, iaRefreshWalkDefinitions())
	if err != nil {
		t.Fatal(err)
	}
	r.Registry = registry
	return w, r, p, q
}

func iaRefreshRawStreamStep(t *testing.T, w *executor, r *iaEntryRegistry, q iaRefreshRequest, d iaRefreshStreamDesired) (iaRefreshWalkStep, error) {
	t.Helper()
	q.purpose, q.key, q.row = IARefreshStep, iaRefreshKey{}, nil
	turn, _ := iaRefreshTurn(t, w, r, q)
	step, err := iaRefreshNextStreamStep(t.Context(), turn, q, d)
	turn.transaction.rollback(err)
	return step, err
}

func iaRefreshRawChapterStep(t *testing.T, w *executor, r *iaEntryRegistry, q iaRefreshRequest, d iaRefreshChapterDesired) (iaRefreshWalkStep, error) {
	t.Helper()
	q.purpose, q.key, q.row = IARefreshStep, iaRefreshKey{}, nil
	turn, _ := iaRefreshTurn(t, w, r, q)
	step, err := iaRefreshNextChapterStep(t.Context(), turn, q, d)
	turn.transaction.rollback(err)
	return step, err
}

func iaRefreshRawStreamRestore(t *testing.T, w *executor, r *iaEntryRegistry, q iaRefreshRequest) (iaRefreshWalkStep, error) {
	t.Helper()
	q.purpose, q.key, q.row = IARefreshRollback, iaRefreshKey{}, nil
	turn, _ := iaRefreshTurn(t, w, r, q)
	step, err := iaRefreshNextStreamRestore(t.Context(), turn, q)
	turn.transaction.rollback(err)
	return step, err
}

func iaRefreshRawChapterRestore(t *testing.T, w *executor, r *iaEntryRegistry, q iaRefreshRequest) (iaRefreshWalkStep, error) {
	t.Helper()
	q.purpose, q.key, q.row = IARefreshRollback, iaRefreshKey{}, nil
	turn, _ := iaRefreshTurn(t, w, r, q)
	step, err := iaRefreshNextChapterRestore(t.Context(), turn, q)
	turn.transaction.rollback(err)
	return step, err
}

// iaRefreshStepRequest binds one named key to the journal revision the walker
// itself read and signs it. The caller never guesses an expected revision.
func iaRefreshAck(t *testing.T, w *executor, refreshID int64) {
	t.Helper()
	wc008NativeExec(t, w, `UPDATE catalog_info_refresh_undo SET acknowledged_command_sequence=last_command_sequence WHERE refresh_id=?`, refreshID)
}

func iaRefreshStepRequest(t *testing.T, q iaRefreshRequest, purpose CommandID, step iaRefreshWalkStep) iaRefreshRequest {
	t.Helper()
	q.purpose = purpose
	q.action = iaEntryApply
	q.key = step.key
	q.row = step.row
	q.expectedRevision = step.revision
	q.reference.Identity.Sequence++
	q.reference.Identity.OperationID[0]++
	iaRefreshSign(t, &q)
	return q
}

func iaRefreshStreamFact(rev int64) iaRefreshStreamFactRow {
	return iaRefreshStreamFactRow{"refresh-asset", rev, 42, 11, "old", 1}
}

func iaRefreshStreamFixtureRow(index int64, codec string) iaRefreshStreamRow {
	return iaRefreshStreamRow{AssetID: "refresh-asset", StreamIndex: index, Type: "video", Codec: codec, Title: ""}
}

// A fully populated folded breadth, so a restore that drops any one breadth
// column is a visible failure rather than a default that happens to match.
func iaRefreshStreamBreadth(index int64, codec string) iaRefreshStreamRow {
	one, zero := int64(1), int64(0)
	empty, progressive, variable := "", "progressive", "variable"
	frames, ticks := int64(24000), int64(1001)
	return iaRefreshStreamRow{
		AssetID: "refresh-asset", StreamIndex: index, Type: "video", Codec: codec,
		Language: "eng", Title: "Main", Channels: 2, ChannelLayout: "stereo",
		IsDefault: 1, IsForced: 1, ColorTransfer: "bt709", ColorPrimaries: "bt709",
		DetailJSON: `{"profile":"high"}`, HasAssetStreamBreadth: 1,
		BreadthAudioDescription: &zero, BreadthAudioVariant: &empty,
		BreadthChromaSubsampling: &empty, BreadthFpsNumerator: &frames, BreadthFpsDenominator: &ticks,
		BreadthFrameRateMode: &variable, BreadthInterlaceAnalysis: &progressive,
		BreadthTelecine: &one, BreadthDoviRpu: &one, BreadthDoviBaseLayer: &zero,
		BreadthDoviMetadataKnown: &one, BreadthDoviEnhancementType: &empty,
		BreadthDoviCmVersion: &empty, BreadthPlaybackFramesRevision: &empty,
		BreadthPacketPeakRevision: &empty,
	}
}

// iaRefreshStreamImage reads every native column in frozen C019 order, so an
// exact old projection can be compared field by field.
func iaRefreshStreamImage(t *testing.T, w *executor, index int64) (iaRefreshStreamRow, bool) {
	t.Helper()
	var row iaRefreshStreamRow
	var variant, chroma, mode, interlace, doviType, cm, frames, peaks sql.NullString
	var audioDescription, fpsNumerator, fpsDenominator, telecine, doviRpu, doviBase, doviKnown sql.NullInt64
	err := w.db.QueryRow(`SELECT asset_id,stream_index,type,codec,language,title,channels,channel_layout,is_default,is_forced,color_transfer,color_primaries,detail_json,has_asset_stream_breadth,breadth_audio_description,breadth_audio_variant,breadth_chroma_subsampling,breadth_fps_numerator,breadth_fps_denominator,breadth_frame_rate_mode,breadth_interlace_analysis,breadth_telecine,breadth_dovi_rpu,breadth_dovi_base_layer,breadth_dovi_metadata_known,breadth_dovi_enhancement_type,breadth_dovi_cm_version,breadth_playback_frames_revision,breadth_packet_peak_revision FROM asset_streams WHERE asset_id=? AND stream_index=?`, "refresh-asset", index).Scan(
		&row.AssetID, &row.StreamIndex, &row.Type, &row.Codec, &row.Language, &row.Title, &row.Channels, &row.ChannelLayout, &row.IsDefault, &row.IsForced, &row.ColorTransfer, &row.ColorPrimaries, &row.DetailJSON, &row.HasAssetStreamBreadth,
		&audioDescription, &variant, &chroma, &fpsNumerator, &fpsDenominator, &mode, &interlace, &telecine, &doviRpu, &doviBase, &doviKnown, &doviType, &cm, &frames, &peaks,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return iaRefreshStreamRow{}, false
	}
	if err != nil {
		t.Fatal(err)
	}
	row.BreadthAudioDescription = iaRefreshNullableFromNull(audioDescription)
	row.BreadthAudioVariant = iaRefreshNullableText(variant)
	row.BreadthChromaSubsampling = iaRefreshNullableText(chroma)
	row.BreadthFpsNumerator = iaRefreshNullableFromNull(fpsNumerator)
	row.BreadthFpsDenominator = iaRefreshNullableFromNull(fpsDenominator)
	row.BreadthFrameRateMode = iaRefreshNullableText(mode)
	row.BreadthInterlaceAnalysis = iaRefreshNullableText(interlace)
	row.BreadthTelecine = iaRefreshNullableFromNull(telecine)
	row.BreadthDoviRpu = iaRefreshNullableFromNull(doviRpu)
	row.BreadthDoviBaseLayer = iaRefreshNullableFromNull(doviBase)
	row.BreadthDoviMetadataKnown = iaRefreshNullableFromNull(doviKnown)
	row.BreadthDoviEnhancementType = iaRefreshNullableText(doviType)
	row.BreadthDoviCmVersion = iaRefreshNullableText(cm)
	row.BreadthPlaybackFramesRevision = iaRefreshNullableText(frames)
	row.BreadthPacketPeakRevision = iaRefreshNullableText(peaks)
	return row, true
}

func iaRefreshNullableFromNull(v sql.NullInt64) *int64 {
	if !v.Valid {
		return nil
	}
	return &v.Int64
}

func iaRefreshNullableText(v sql.NullString) *string {
	if !v.Valid {
		return nil
	}
	return &v.String
}

func TestIARefreshStreamWalkRestoresExactOldBreadth(t *testing.T) {
	w, r, p, q := iaRefreshWalkFixture(t)
	video := iaRefreshStreamBreadth(1, "h264")
	audio := iaRefreshStreamBreadth(2, "aac")
	audio.Type, audio.Title = "audio", "Commentary"
	iaRefreshSeed(t, w, iaRefreshStreamFact(10))
	iaRefreshSeed(t, w, video)
	iaRefreshSeed(t, w, audio)
	begin := iaRefreshApply(t, w, r, p, q)
	q.refreshID = begin.result.refreshID
	iaRefreshAck(t, w, begin.result.refreshID)
	desired := iaRefreshStreamDesired{
		Fact: &iaRefreshStreamFactRow{AssetID: "refresh-asset", Revision: 11, Size: 42, ModifiedNS: 12, Fingerprint: "new", DetailVersion: 2},
		Rows: []iaRefreshStreamRow{iaRefreshStreamFixtureRow(1, "hevc"), iaRefreshStreamFixtureRow(3, "av1")},
	}
	named := []iaRefreshWalkStep{}
	last := begin
	for {
		step, err := iaRefreshRawStreamStep(t, w, r, q, desired)
		if err != nil {
			t.Fatal(err)
		}
		if !step.more {
			break
		}
		named = append(named, step)
		q = iaRefreshStepRequest(t, q, IARefreshStep, step)
		last = iaRefreshApply(t, w, r, p, q)
		iaRefreshAck(t, w, begin.result.refreshID)
	}
	want := []iaRefreshKey{{1, "refresh-asset", 0}, {2, "refresh-asset", 1}, {2, "refresh-asset", 2}, {2, "refresh-asset", 3}}
	if len(named) != len(want) {
		t.Fatalf("walk named %d keys, want %d: %+v", len(named), len(want), named)
	}
	for j := range want {
		if named[j].key != want[j] {
			t.Fatalf("dependency order %d: %+v want %+v", j, named[j].key, want[j])
		}
	}
	if got := iaRefreshCount(t, w, `SELECT count(*) FROM catalog_info_undo_asset_streams WHERE refresh_id=? AND old_exists=0`, last.result.refreshID); got != 1 {
		t.Fatal("new key did not keep an absence tombstone", got)
	}
	if got := iaRefreshCount(t, w, `SELECT count(*) FROM catalog_info_undo_asset_streams WHERE refresh_id=? AND old_exists=1`, last.result.refreshID); got != 2 {
		t.Fatal("old children were not captured once each", got)
	}
	if row, ok := iaRefreshStreamImage(t, w, 2); ok {
		t.Fatal("removed child survived the walk", row)
	}
	if row, ok := iaRefreshStreamImage(t, w, 3); !ok || row.Codec != "av1" {
		t.Fatal(row, ok)
	}

	restored := []iaRefreshKey{}
	for {
		step, err := iaRefreshRawStreamRestore(t, w, r, q)
		if err != nil {
			t.Fatal(err)
		}
		if !step.more {
			break
		}
		restored = append(restored, step.key)
		q = iaRefreshStepRequest(t, q, IARefreshRollback, step)
		iaRefreshApply(t, w, r, p, q)
		iaRefreshAck(t, w, begin.result.refreshID)
	}
	wantRestore := []iaRefreshKey{{1, "refresh-asset", 0}, {2, "refresh-asset", 3}, {2, "refresh-asset", 2}, {2, "refresh-asset", 1}}
	if !reflect.DeepEqual(restored, wantRestore) {
		t.Fatalf("restore order %+v want %+v", restored, wantRestore)
	}
	if row, ok := iaRefreshStreamImage(t, w, 3); ok {
		t.Fatal("new child survived the restore", row)
	}
	for _, expected := range []iaRefreshStreamRow{video, audio} {
		if row, ok := iaRefreshStreamImage(t, w, expected.StreamIndex); !ok || !reflect.DeepEqual(row, expected) {
			t.Fatalf("complete before-image lost at index %d: %+v", expected.StreamIndex, row)
		}
	}
	if got := iaRefreshCount(t, w, `SELECT revision FROM asset_stream_facts WHERE asset_id='refresh-asset'`); got != 10 {
		t.Fatal("parent fact was not restored", got)
	}
	if got := iaRefreshCount(t, w, `SELECT info_publication_pending FROM catalog_assets WHERE id=41`); got != 1 {
		t.Fatal("core cleared IA23's fence", got)
	}
}

func TestIARefreshStreamWalkRepeatedKeyKeepsFirstOldImage(t *testing.T) {
	w, r, p, q := iaRefreshWalkFixture(t)
	original := iaRefreshStreamBreadth(1, "h264")
	iaRefreshSeed(t, w, iaRefreshStreamFact(10))
	iaRefreshSeed(t, w, original)
	begin := iaRefreshApply(t, w, r, p, q)
	q.refreshID = begin.result.refreshID
	iaRefreshAck(t, w, begin.result.refreshID)
	q = iaRefreshStepRequest(t, q, IARefreshStep, iaRefreshWalkStep{key: iaRefreshKey{1, "refresh-asset", 0}, row: iaRefreshStreamFact(11), revision: begin.result.revision})
	iaRefreshApply(t, w, r, p, q)
	iaRefreshAck(t, w, begin.result.refreshID)
	// Two reads of the same position name the same key; the second Step
	// re-presents it with a newer replacement. The first old image must win.
	second := iaRefreshStreamFixtureRow(1, "hevc")
	third := iaRefreshStreamFixtureRow(1, "av1")
	fact := iaRefreshStreamFactRow{AssetID: "refresh-asset", Revision: 11}
	peek, err := iaRefreshRawStreamStep(t, w, r, q, iaRefreshStreamDesired{Fact: &fact, Rows: []iaRefreshStreamRow{second}})
	if err != nil || !peek.more || peek.key != (iaRefreshKey{2, "refresh-asset", 1}) {
		t.Fatal(peek, err)
	}
	again, err := iaRefreshRawStreamStep(t, w, r, q, iaRefreshStreamDesired{Fact: &fact, Rows: []iaRefreshStreamRow{third}})
	if err != nil || again.key != peek.key {
		t.Fatal("repeated read moved the key", again, err)
	}
	q = iaRefreshStepRequest(t, q, IARefreshStep, peek)
	out := iaRefreshApply(t, w, r, p, q)
	iaRefreshAck(t, w, begin.result.refreshID)
	q = iaRefreshStepRequest(t, q, IARefreshStep, iaRefreshWalkStep{key: again.key, row: again.row, revision: out.result.revision})
	iaRefreshApply(t, w, r, p, q)
	iaRefreshAck(t, w, begin.result.refreshID)
	if got := iaRefreshCount(t, w, `SELECT count(*) FROM catalog_info_undo_asset_streams WHERE refresh_id=?`, out.result.refreshID); got != 1 {
		t.Fatal("repeated key journalled twice", got)
	}
	if row, ok := iaRefreshStreamImage(t, w, 1); !ok || row.Codec != "av1" {
		t.Fatal("newest replacement not applied", row, ok)
	}
	for {
		step, err := iaRefreshRawStreamRestore(t, w, r, q)
		if err != nil {
			t.Fatal(err)
		}
		if !step.more {
			break
		}
		q = iaRefreshStepRequest(t, q, IARefreshRollback, step)
		iaRefreshApply(t, w, r, p, q)
		iaRefreshAck(t, w, begin.result.refreshID)
	}
	if row, ok := iaRefreshStreamImage(t, w, 1); !ok || !reflect.DeepEqual(row, original) {
		t.Fatal("first old image was replaced", row, ok)
	}
}

func TestIARefreshStreamWalkResumesAcrossTurnsOnFakeClock(t *testing.T) {
	w, r, p, q := iaRefreshWalkFixture(t)
	clock := w.now()
	w.now = func() time.Time { return clock }
	iaRefreshSeed(t, w, iaRefreshStreamFact(10))
	for index := int64(0); index < 3; index++ {
		iaRefreshSeed(t, w, iaRefreshStreamFixtureRow(index, "h264"))
	}
	begin := iaRefreshApply(t, w, r, p, q)
	q.refreshID = begin.result.refreshID
	iaRefreshAck(t, w, begin.result.refreshID)
	desired := iaRefreshStreamDesired{
		Fact: &iaRefreshStreamFactRow{AssetID: "refresh-asset", Revision: 11, Size: 42, ModifiedNS: 11, Fingerprint: "new", DetailVersion: 2},
		Rows: []iaRefreshStreamRow{iaRefreshStreamFixtureRow(0, "hevc"), iaRefreshStreamFixtureRow(2, "av1"), iaRefreshStreamFixtureRow(4, "vp9")},
	}
	seen := []iaRefreshWalkStep{}
	last := begin.result.revision
	for {
		clock = clock.Add(time.Millisecond)
		step, err := iaRefreshRawStreamStep(t, w, r, q, desired)
		if err != nil {
			t.Fatal(err)
		}
		if !step.more {
			break
		}
		if step.revision != last {
			t.Fatalf("resume read revision %d want %d", step.revision, last)
		}
		seen = append(seen, step)
		q = iaRefreshStepRequest(t, q, IARefreshStep, step)
		out := iaRefreshApply(t, w, r, p, q)
		last = out.result.revision
		iaRefreshAck(t, w, begin.result.refreshID)
	}
	if len(seen) != 5 {
		t.Fatalf("multi-turn walk named %d keys", len(seen))
	}
	for j := 1; j < len(seen); j++ {
		if seen[j].key == seen[j-1].key {
			t.Fatalf("resume repeated key %+v", seen[j].key)
		}
	}
	if got := iaRefreshCount(t, w, `SELECT count(*) FROM catalog_info_refresh_receipts WHERE refresh_id=? AND command_kind='refresh_step'`, begin.result.refreshID); got != 5 {
		t.Fatal("per-turn receipts", got)
	}
	restored := []iaRefreshKey{}
	for {
		clock = clock.Add(time.Millisecond)
		step, err := iaRefreshRawStreamRestore(t, w, r, q)
		if err != nil {
			t.Fatal(err)
		}
		if !step.more {
			break
		}
		restored = append(restored, step.key)
		q = iaRefreshStepRequest(t, q, IARefreshRollback, step)
		iaRefreshApply(t, w, r, p, q)
		iaRefreshAck(t, w, begin.result.refreshID)
	}
	if !reflect.DeepEqual(restored, []iaRefreshKey{{1, "refresh-asset", 0}, {2, "refresh-asset", 4}, {2, "refresh-asset", 2}, {2, "refresh-asset", 1}, {2, "refresh-asset", 0}}) {
		t.Fatalf("restore resume order %+v", restored)
	}
	for index := int64(0); index < 3; index++ {
		if row, ok := iaRefreshStreamImage(t, w, index); !ok || row.Codec != "h264" {
			t.Fatal("old row not restored", index, row, ok)
		}
	}
	if row, ok := iaRefreshStreamImage(t, w, 4); ok {
		t.Fatal("new row survived", row)
	}
}

func TestIARefreshStreamWalkAbsentParentDropsChildrenFirst(t *testing.T) {
	w, r, p, q := iaRefreshWalkFixture(t)
	first := iaRefreshStreamBreadth(1, "h264")
	second := iaRefreshStreamBreadth(2, "aac")
	iaRefreshSeed(t, w, iaRefreshStreamFact(10))
	iaRefreshSeed(t, w, first)
	iaRefreshSeed(t, w, second)
	begin := iaRefreshApply(t, w, r, p, q)
	q.refreshID = begin.result.refreshID
	iaRefreshAck(t, w, begin.result.refreshID)
	desired := iaRefreshStreamDesired{}
	order := []int64{}
	for {
		step, err := iaRefreshRawStreamStep(t, w, r, q, desired)
		if err != nil {
			t.Fatal(err)
		}
		if !step.more {
			break
		}
		order = append(order, int64(step.key.family))
		q = iaRefreshStepRequest(t, q, IARefreshStep, step)
		iaRefreshApply(t, w, r, p, q)
		iaRefreshAck(t, w, begin.result.refreshID)
	}
	if !reflect.DeepEqual(order, []int64{2, 2, 1}) {
		t.Fatalf("parent delete did not follow its children: %v", order)
	}
	if got := iaRefreshCount(t, w, `SELECT count(*) FROM asset_stream_facts`); got != 0 {
		t.Fatal("parent fact survived", got)
	}
	if got := iaRefreshCount(t, w, `SELECT count(*) FROM asset_streams`); got != 0 {
		t.Fatal("child survived", got)
	}
	restored := []iaRefreshKey{}
	for {
		step, err := iaRefreshRawStreamRestore(t, w, r, q)
		if err != nil {
			t.Fatal(err)
		}
		if !step.more {
			break
		}
		restored = append(restored, step.key)
		q = iaRefreshStepRequest(t, q, IARefreshRollback, step)
		iaRefreshApply(t, w, r, p, q)
		iaRefreshAck(t, w, begin.result.refreshID)
	}
	if !reflect.DeepEqual(restored, []iaRefreshKey{{1, "refresh-asset", 0}, {2, "refresh-asset", 2}, {2, "refresh-asset", 1}}) {
		t.Fatalf("parent did not restore before its children: %+v", restored)
	}
	for _, expected := range []iaRefreshStreamRow{first, second} {
		if row, ok := iaRefreshStreamImage(t, w, expected.StreamIndex); !ok || !reflect.DeepEqual(row, expected) {
			t.Fatal("old child not restored", expected.StreamIndex, row, ok)
		}
	}
}

func TestIARefreshStreamWalkNewParentRestoresChildrenLast(t *testing.T) {
	w, r, p, q := iaRefreshWalkFixture(t)
	begin := iaRefreshApply(t, w, r, p, q)
	q.refreshID = begin.result.refreshID
	iaRefreshAck(t, w, begin.result.refreshID)
	desired := iaRefreshStreamDesired{
		Fact: &iaRefreshStreamFactRow{AssetID: "refresh-asset", Revision: 11, Size: 42, ModifiedNS: 11, Fingerprint: "new", DetailVersion: 2},
		Rows: []iaRefreshStreamRow{iaRefreshStreamFixtureRow(0, "hevc"), iaRefreshStreamFixtureRow(1, "aac")},
	}
	for {
		step, err := iaRefreshRawStreamStep(t, w, r, q, desired)
		if err != nil {
			t.Fatal(err)
		}
		if !step.more {
			break
		}
		q = iaRefreshStepRequest(t, q, IARefreshStep, step)
		iaRefreshApply(t, w, r, p, q)
		iaRefreshAck(t, w, begin.result.refreshID)
	}
	if got := iaRefreshCount(t, w, `SELECT count(*) FROM catalog_info_undo_asset_streams WHERE refresh_id=? AND old_exists=0`, begin.result.refreshID); got != 2 {
		t.Fatal("new children did not keep absence tombstones", got)
	}
	restored := []iaRefreshKey{}
	for {
		step, err := iaRefreshRawStreamRestore(t, w, r, q)
		if err != nil {
			t.Fatal(err)
		}
		if !step.more {
			break
		}
		restored = append(restored, step.key)
		q = iaRefreshStepRequest(t, q, IARefreshRollback, step)
		iaRefreshApply(t, w, r, p, q)
		iaRefreshAck(t, w, begin.result.refreshID)
	}
	if !reflect.DeepEqual(restored, []iaRefreshKey{{2, "refresh-asset", 1}, {2, "refresh-asset", 0}, {1, "refresh-asset", 0}}) {
		t.Fatalf("new parent did not restore last: %+v", restored)
	}
	if got := iaRefreshCount(t, w, `SELECT count(*) FROM asset_stream_facts`); got != 0 {
		t.Fatal("absent old parent was invented", got)
	}
	if got := iaRefreshCount(t, w, `SELECT count(*) FROM asset_streams`); got != 0 {
		t.Fatal("new children survived", got)
	}
}

func TestIARefreshStreamWalkRefusesMisboundProjection(t *testing.T) {
	w, r, p, q := iaRefreshWalkFixture(t)
	begin := iaRefreshApply(t, w, r, p, q)
	q.refreshID = begin.result.refreshID
	iaRefreshAck(t, w, begin.result.refreshID)
	fact := iaRefreshStreamFactRow{AssetID: "refresh-asset", Revision: 11}
	foreign := iaRefreshStreamFixtureRow(1, "hevc")
	foreign.AssetID = "other-asset"
	rows := make([]iaRefreshStreamRow, iaRefreshWalkKeys)
	for j := range rows {
		rows[j] = iaRefreshStreamFixtureRow(int64(j), "hevc")
	}
	over := append(rows, iaRefreshStreamFixtureRow(iaRefreshWalkKeys, "hevc"))
	cases := []struct {
		name string
		q    iaRefreshRequest
		d    iaRefreshStreamDesired
		want error
	}{
		{"child_without_parent", q, iaRefreshStreamDesired{Rows: []iaRefreshStreamRow{foreign}}, ErrInvalid},
		{"foreign_fact_asset", q, iaRefreshStreamDesired{Fact: &iaRefreshStreamFactRow{AssetID: "other-asset", Revision: 11}}, ErrInvalid},
		{"foreign_child_asset", q, iaRefreshStreamDesired{Fact: &fact, Rows: []iaRefreshStreamRow{foreign}}, ErrInvalid},
		{"duplicate_child", q, iaRefreshStreamDesired{Fact: &fact, Rows: []iaRefreshStreamRow{iaRefreshStreamFixtureRow(1, "a"), iaRefreshStreamFixtureRow(1, "b")}}, ErrInvalid},
		{"descending_children", q, iaRefreshStreamDesired{Fact: &fact, Rows: []iaRefreshStreamRow{iaRefreshStreamFixtureRow(2, "a"), iaRefreshStreamFixtureRow(1, "b")}}, ErrInvalid},
		{"negative_child", q, iaRefreshStreamDesired{Fact: &fact, Rows: []iaRefreshStreamRow{iaRefreshStreamFixtureRow(-1, "a")}}, ErrInvalid},
		{"unbegun_journal", func() iaRefreshRequest { n := q; n.refreshID = 0; return n }(), iaRefreshStreamDesired{Fact: &fact}, ErrInvalid},
		{"unknown_journal", func() iaRefreshRequest { n := q; n.refreshID = begin.result.refreshID + 900; return n }(), iaRefreshStreamDesired{Fact: &fact}, ErrResolveRequired},
	}
	for _, cell := range cases {
		t.Run(cell.name, func(t *testing.T) {
			if _, err := iaRefreshRawStreamStep(t, w, r, cell.q, cell.d); !errors.Is(err, cell.want) {
				t.Fatal(err)
			}
		})
	}
	t.Run("not_a_step", func(t *testing.T) {
		turn, _ := iaRefreshTurn(t, w, r, q)
		_, err := iaRefreshNextStreamStep(t.Context(), turn, iaRefreshRequest{purpose: IARefreshBegin, action: iaEntryApply, refreshID: begin.result.refreshID, scope: q.scope, reference: q.reference}, iaRefreshStreamDesired{Fact: &fact})
		turn.transaction.rollback(err)
		if !errors.Is(err, ErrInvalid) {
			t.Fatal(err)
		}
	})
	t.Run("preselected_key", func(t *testing.T) {
		turn, _ := iaRefreshTurn(t, w, r, q)
		_, err := iaRefreshNextStreamStep(t.Context(), turn, iaRefreshRequest{purpose: IARefreshStep, action: iaEntryApply, refreshID: begin.result.refreshID, scope: q.scope, key: iaRefreshKey{2, "refresh-asset", 0}, reference: q.reference}, iaRefreshStreamDesired{Fact: &fact})
		turn.transaction.rollback(err)
		if !errors.Is(err, ErrInvalid) {
			t.Fatal(err)
		}
	})
	t.Run("journal_epoch_mismatch", func(t *testing.T) {
		other, or, op, oq := iaRefreshWalkFixture(t)
		ob := iaRefreshApply(t, other, or, op, oq)
		wc008NativeExec(t, other, `UPDATE catalog_info_refresh_undo SET expected_epoch=9 WHERE refresh_id=?`, ob.result.refreshID)
		oq.refreshID = ob.result.refreshID
		if _, err := iaRefreshRawStreamStep(t, other, or, oq, iaRefreshStreamDesired{Fact: &fact}); !errors.Is(err, ErrResolveRequired) {
			t.Fatal(err)
		}
	})
	t.Run("journal_cursor_tampered", func(t *testing.T) {
		other, or, op, oq := iaRefreshWalkFixture(t)
		ob := iaRefreshApply(t, other, or, op, oq)
		wc008NativeExec(t, other, `UPDATE catalog_info_refresh_undo SET after_family=2,after_key='not-a-cursor' WHERE refresh_id=?`, ob.result.refreshID)
		oq.refreshID = ob.result.refreshID
		if _, err := iaRefreshRawStreamStep(t, other, or, oq, iaRefreshStreamDesired{Fact: &fact}); !errors.Is(err, ErrResolveRequired) {
			t.Fatal(err)
		}
	})
	t.Run("capacity", func(t *testing.T) {
		_, err := iaRefreshRawStreamStep(t, w, r, q, iaRefreshStreamDesired{Fact: &fact, Rows: over})
		var refusal *Refusal
		if !errors.As(err, &refusal) || refusal.Code != RefusalCapacity {
			t.Fatal(err)
		}
	})
	t.Run("max_projection", func(t *testing.T) {
		step, err := iaRefreshRawStreamStep(t, w, r, q, iaRefreshStreamDesired{Fact: &fact, Rows: rows})
		if err != nil || !step.more || step.key.family != 1 {
			t.Fatal(step, err)
		}
	})
}

// The accepting Step keeps every required cell IA20 defined, now driven from a
// walker-named child key: forged issuer, stale authority, cancelled lifetime,
// ACK/Retired replay, source drift and an unknown COMMIT read back through a
// FRESH read-only connection.
func TestIARefreshStreamWalkStepRequiredCells(t *testing.T) {
	fact := iaRefreshStreamFactRow{AssetID: "refresh-asset", Revision: 11}
	prepare := func(t *testing.T) (*executor, *iaEntryRegistry, *usOwnerAuthorityProof, iaRefreshRequest, iaRefreshOutcome) {
		t.Helper()
		w, r, p, q := iaRefreshWalkFixture(t)
		begin := iaRefreshApply(t, w, r, p, q)
		q.refreshID = begin.result.refreshID
		iaRefreshAck(t, w, begin.result.refreshID)
		step, err := iaRefreshRawStreamStep(t, w, r, q, iaRefreshStreamDesired{Fact: &fact})
		if err != nil || !step.more {
			t.Fatal(step, err)
		}
		return w, r, p, iaRefreshStepRequest(t, q, IARefreshStep, step), begin
	}
	t.Run("forged_issuer", func(t *testing.T) {
		w, r, p, q, begin := prepare(t)
		p.witness.issuer.memberIncarnation = strings.Repeat("x", 16)
		turn, b := iaRefreshTurn(t, w, r, q)
		if out, err := runIARefresh(t.Context(), turn, q, p, t.Context(), b); err == nil || !reflect.DeepEqual(out, iaRefreshOutcome{}) {
			t.Fatal(out, err)
		}
		turn.transaction.rollback(nil)
		if got := iaRefreshCount(t, w, `SELECT count(*) FROM catalog_info_undo_asset_stream_facts WHERE refresh_id=?`, begin.result.refreshID); got != 0 {
			t.Fatal("forged owner wrote custody", got)
		}
	})
	t.Run("stale_authority", func(t *testing.T) {
		w, r, p, q, begin := prepare(t)
		p.tokenExpiry = w.now().Add(-time.Second)
		turn, b := iaRefreshTurn(t, w, r, q)
		if out, err := runIARefresh(t.Context(), turn, q, p, t.Context(), b); err == nil || !reflect.DeepEqual(out, iaRefreshOutcome{}) {
			t.Fatal(out, err)
		}
		turn.transaction.rollback(nil)
		if got := iaRefreshCount(t, w, `SELECT count(*) FROM catalog_info_undo_asset_stream_facts WHERE refresh_id=?`, begin.result.refreshID); got != 0 {
			t.Fatal("stale authority wrote custody", got)
		}
	})
	t.Run("cancelled_lifetime", func(t *testing.T) {
		w, r, p, q, begin := prepare(t)
		life, cancel := context.WithCancel(t.Context())
		cancel()
		turn, b := iaRefreshTurn(t, w, r, q)
		if out, err := runIARefresh(t.Context(), turn, q, p, life, b); err == nil || !reflect.DeepEqual(out, iaRefreshOutcome{}) {
			t.Fatal(out, err)
		}
		turn.transaction.rollback(nil)
		if got := iaRefreshCount(t, w, `SELECT count(*) FROM catalog_info_undo_asset_stream_facts WHERE refresh_id=?`, begin.result.refreshID); got != 0 {
			t.Fatal("cancelled lifetime wrote custody", got)
		}
	})
	t.Run("replay_after_ack", func(t *testing.T) {
		w, r, p, q, begin := prepare(t)
		applied := iaRefreshApply(t, w, r, p, q)
		replay := iaRefreshApply(t, w, r, p, q)
		if replay.decision != receiptReplay || replay.result != applied.result || replay.digest != applied.digest {
			t.Fatal("saved result did not replay", replay)
		}
		wc008NativeExec(t, w, `UPDATE catalog_info_refresh_undo SET acknowledged_command_sequence=last_command_sequence WHERE refresh_id=?`, begin.result.refreshID)
		retired := iaRefreshApply(t, w, r, p, q)
		if retired.decision != receiptRetired || retired.result != (iaRefreshResult{}) || retired.digest != (ResultDigest{}) {
			t.Fatal("retired receipt disclosed its payload", retired)
		}
	})
	t.Run("source_drift", func(t *testing.T) {
		w, r, p, q, begin := prepare(t)
		wc008NativeExec(t, w, `UPDATE catalog_assets SET size=99,modified_ns=88 WHERE id=41`)
		turn, b := iaRefreshTurn(t, w, r, q)
		if out, err := runIARefresh(t.Context(), turn, q, p, t.Context(), b); !errors.Is(err, ErrConflict) || !reflect.DeepEqual(out, iaRefreshOutcome{}) {
			t.Fatal(out, err)
		}
		turn.transaction.rollback(nil)
		if got := iaRefreshCount(t, w, `SELECT count(*) FROM catalog_info_undo_asset_stream_facts WHERE refresh_id=?`, begin.result.refreshID); got != 0 {
			t.Fatal("drifted source wrote custody", got)
		}
	})
	t.Run("unknown_commit", func(t *testing.T) {
		w, r, p, q, begin := prepare(t)
		path := iaConfigureLibraryPath(t, w)
		if err := w.db.Close(); err != nil {
			t.Fatal(err)
		}
		base, err := sqliteconn.Connector(path, w.config)
		if err != nil {
			t.Fatal(err)
		}
		installFaultPool(w, &faultConnector{Connector: base, afterCommit: true, replyError: true})
		turn, b := iaRefreshTurn(t, w, r, q)
		out, err := runIARefresh(t.Context(), turn, q, p, t.Context(), b)
		if err != nil || out.decision != receiptFresh {
			t.Fatal(out, err)
		}
		result, err := turn.transaction.commit()
		if result.state != commitUnknown || !errors.Is(err, ErrResolveRequired) {
			t.Fatal(result, err)
		}
		cfg := sqliteconn.Config{Role: sqliteconn.Reader, Connections: 1, CacheKiB: 1024, Synchronous: sqliteconn.Normal}
		db, _, err := sqliteconn.Open(t.Context(), path, cfg)
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		var receipts, images int64
		if err = db.QueryRowContext(t.Context(), `SELECT count(*) FROM catalog_info_refresh_receipts WHERE refresh_id=? AND command_kind='refresh_step'`, begin.result.refreshID).Scan(&receipts); err != nil || receipts != 1 {
			t.Fatal(receipts, err)
		}
		if err = db.QueryRowContext(t.Context(), `SELECT count(*) FROM catalog_info_undo_asset_stream_facts WHERE refresh_id=?`, begin.result.refreshID).Scan(&images); err != nil || images != 1 {
			t.Fatal(images, err)
		}
		if _, err = w.begin(t.Context(), IARefreshStep, testRecipe(), &testCustody{}); !errors.Is(err, ErrResolveRequired) {
			t.Fatal("unknown commit was reused", err)
		}
	})
}
