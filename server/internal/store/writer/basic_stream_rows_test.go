package writer

import (
	"errors"
	"testing"
)

func TestBasicMediaNativeStreamsAndBreadth(t *testing.T) {
	w, source := basicMediaFixture(t)
	for _, family := range []int64{2, 3} {
		if _, err := w.db.ExecContext(t.Context(), `INSERT INTO catalog_info_input_families(change_id,family,witness_ordinal,rows_expected) VALUES(51,?,0,2)`, family); err != nil {
			t.Fatal(err)
		}
	}
	turn := basicBoundedComponentTurn(t, w, basicMediaTestDefs())
	if _, err := stageBasicStreamFact(t.Context(), turn, source); err != nil {
		t.Fatal(err)
	}
	basicMediaCommit(t, turn)
	families := []struct {
		number int64
		rows   []basicMediaRow
	}{
		{2, []basicMediaRow{basicStreamRow{index: 0, streamType: "video", codec: "h264", isDefault: true, colorTransfer: "bt709", colorPrimaries: "bt709", detail: `{"width":1920}`}, basicStreamRow{index: 3, streamType: "audio", codec: "aac", language: "en", channels: 2, channelLayout: "stereo", isDefault: true}}},
		{3, []basicMediaRow{basicStreamBreadth{index: 0, chroma: "4:2:0", fpsNumerator: 24000, fpsDenominator: 1001, frameMode: "constant", interlace: "progressive", doviKnown: true}, basicStreamBreadth{index: 3, audioDescription: true, audioVariant: "main"}}},
	}
	for _, family := range families {
		frame, err := prepareBasicMediaFamily(source, family.number, 0, family.rows)
		if err != nil {
			t.Fatal(err)
		}
		turn = basicBoundedComponentTurn(t, w, basicMediaTestDefs())
		got, err := stageBasicMediaRows(t.Context(), turn, frame, 0, 2)
		if err != nil || got.rowsStaged != 2 {
			t.Fatal(family.number, got, err)
		}
		basicMediaCommit(t, turn)
		turn = basicBoundedComponentTurn(t, w, basicMediaTestDefs())
		got, err = sealBasicMediaFamily(t.Context(), turn, frame)
		if err != nil || got.sealed != 1 {
			t.Fatal(family.number, got, err)
		}
		basicMediaCommit(t, turn)
	}
	var audioDescription int64
	var fpsNumerator, fpsDenominator int64
	var detail string
	if err := w.db.QueryRowContext(t.Context(), `SELECT audio_description FROM catalog_info_new_stream_breadth WHERE change_id=51 AND stream_index=3`).Scan(&audioDescription); err != nil {
		t.Fatal(err)
	}
	if err := w.db.QueryRowContext(t.Context(), `SELECT fps_numerator,fps_denominator FROM catalog_info_new_stream_breadth WHERE change_id=51 AND stream_index=0`).Scan(&fpsNumerator, &fpsDenominator); err != nil {
		t.Fatal(err)
	}
	if err := w.db.QueryRowContext(t.Context(), `SELECT detail_json FROM catalog_info_new_streams WHERE change_id=51 AND stream_index=0`).Scan(&detail); err != nil {
		t.Fatal(err)
	}
	if audioDescription != 1 || fpsNumerator != 24000 || fpsDenominator != 1001 || detail != `{"width":1920}` {
		t.Fatal(audioDescription, fpsNumerator, fpsDenominator, detail)
	}
}
func TestBasicMediaStreamCapsAndGlobalIndexes(t *testing.T) {
	source := basicStreamFactStage{libraryID: 7, assetToken: "token"}
	rows := make([]basicMediaRow, MaxStreamsPerClass+1)
	for n := range rows {
		rows[n] = basicStreamRow{index: int64(n), streamType: "audio"}
	}
	if _, err := prepareBasicMediaFamily(source, 2, 0, rows); !errors.Is(err, ErrCap) {
		t.Fatal("per-class cap", err)
	}
	rows = []basicMediaRow{basicStreamRow{index: 3, streamType: "video"}, basicStreamRow{index: 3, streamType: "audio"}}
	if _, err := prepareBasicMediaFamily(source, 2, 0, rows); !errors.Is(err, ErrInvalid) {
		t.Fatal("source-global index", err)
	}
	rows = []basicMediaRow{basicStreamRow{index: 3, streamType: "audio", detail: `{"truncated":`}}
	if _, err := prepareBasicMediaFamily(source, 2, 0, rows); !errors.Is(err, ErrInvalid) {
		t.Fatal("invalid detail payload", err)
	}
}
