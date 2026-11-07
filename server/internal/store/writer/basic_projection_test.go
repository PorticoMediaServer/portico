package writer

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

func TestBasicProjectionCompleteBytePreflight(t *testing.T) {
	in := basicAssetObservation{assetPath: "/media/Arrival.mkv", objectID: "object", sourceID: "source", incarnation: "inc", objectRevision: "rev", runID: "run"}
	n, err := basicProjectionRequiredBytes(in, [5]int64{5, 3, 4, 0, 16})
	want := uint64(16384 + 4*(len(in.assetPath)+6+6+3+3+3+5+3+4+16))
	if err != nil || n != want {
		t.Fatal(n, want, err)
	}
	for _, lengths := range [][5]int64{{-1}, {maxCanonicalRequestBytes}, {0, 0, 0, 0, 1 << 62}} {
		if n, err := basicProjectionRequiredBytes(in, lengths); n != 0 || !errors.Is(err, ErrCap) {
			t.Fatal(n, err)
		}
	}
	in.assetPath = strings.Repeat("x", maxCanonicalRequestBytes+1)
	if n, err := basicProjectionRequiredBytes(in, [5]int64{}); n != 0 || !errors.Is(err, ErrCap) {
		t.Fatal(n, err)
	}
	if n := testing.AllocsPerRun(10, func() { _, _ = basicProjectionRequiredBytes(basicAssetObservation{}, [5]int64{1, 2, 3, 4, 16}) }); n != 0 {
		t.Fatal("preflight allocates", n)
	}
}

func basicProjectionFixture(t *testing.T, mutate string) (*commandTurn, basicAssetObservation) {
	t.Helper()
	w, s, _, _ := basicControlFixture(t)
	inc := bytes.Repeat([]byte{0x41}, 16)
	if _, err := w.db.ExecContext(t.Context(), `UPDATE catalog_assets SET container='old',video_codec='vp9',audio_codec='',width=0,height=0,duration=0,playback_origin_incarnation=?,playback_origin_revision=101,prepared_source_revision=103,file_size_revision=113,info_invalidation_revision=109 WHERE id=41`, inc); err != nil {
		t.Fatal(err)
	}
	if mutate != "" {
		if _, err := w.db.ExecContext(t.Context(), mutate); err != nil {
			t.Fatal(err)
		}
	}
	conn, err := w.db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defs := basicProjectionDefinitions()
	golden := captureEffectiveFixture(t, conn, w.config, defs, "basic_projection_test.go")
	if err = conn.Close(); err != nil {
		t.Fatal(err)
	}
	registry, err := stmt.New(defs, golden.Receipt())
	if err != nil {
		t.Fatal(err)
	}
	var seq int64
	var name, path string
	if err = w.db.QueryRowContext(t.Context(), "PRAGMA database_list").Scan(&seq, &name, &path); err != nil {
		t.Fatal(err)
	}
	base, _ := basicResourceTestTurn(t, w, path, BasicStage, commandMemoryBudget{32768, 32768})
	vm := make(map[stmt.ID]uint64)
	for _, p := range golden.Plans {
		vm[p.ID] = max(vm[p.ID], 2*uint64(len(p.Explain.Values)))
	}
	var steps []statementStep
	for _, d := range defs {
		steps = append(steps, statementStep{d.ID, Work{Rows: 2, VMOps: vm[d.ID], Pages: 4, Hold: 100 * time.Microsecond}})
	}
	turn, err := newCommandTurn(base.transaction, registry, steps)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = turn.closeRows() })
	return turn, basicAssetObservation{s.assetID, s.libraryID, s.size, s.modifiedNS, "/media/Arrival.mkv", s.objectID, s.sourceID, s.incarnation, s.objectRevision, s.runID}
}

func TestBasicProjectionReadsIndependentNativeVector(t *testing.T) {
	turn, in := basicProjectionFixture(t, "")
	got, err := readBasicAssetBeforeProbe(t.Context(), turn, BasicPurposeStage, in)
	want := basicAssetProjection{observation: in, assetToken: "basic-asset", before: basicProbeScalars{"old", "vp9", "", 0, 0, 0}, playbackIncarnation: [16]byte(bytes.Repeat([]byte{0x41}, 16)), playbackRevision: 101, preparedRevision: 103, fileSizeRevision: 113, invalidationRevision: 109, expectedEpoch: 17}
	if err != nil || got != want {
		t.Fatal(got, err)
	}
}

func TestBasicProjectionFreshMismatchAndPurposeRefuse(t *testing.T) {
	for _, name := range []string{"object_revision", "asset_path", "asset_owner", "foreign_purpose", "invalid_utf8", "nul"} {
		t.Run(name, func(t *testing.T) {
			mutate := ""
			if name == "asset_owner" {
				mutate = `UPDATE catalog_assets SET info_owner_object='basic-object',info_owner_revision='object:019' WHERE id=41`
			}
			turn, in := basicProjectionFixture(t, mutate)
			purpose := BasicPurposeStage
			want := ErrConflict
			switch name {
			case "object_revision":
				in.objectRevision = "changed"
			case "asset_path":
				in.assetPath = "/media/other.mkv"
			case "foreign_purpose":
				purpose = BasicPurposeSeal
				want = ErrInvalid
			case "invalid_utf8":
				in.assetPath = "/media/\xff"
				want = ErrFilenameUnrepresentable
			case "nul":
				in.assetPath = "/media/\x00"
				want = ErrInvalid
			}
			got, err := readBasicAssetBeforeProbe(t.Context(), turn, purpose, in)
			if !errors.Is(err, want) || got != (basicAssetProjection{}) {
				t.Fatal(got, err, want)
			}
		})
	}
}
