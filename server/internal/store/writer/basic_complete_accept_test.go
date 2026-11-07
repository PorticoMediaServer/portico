package writer

import (
	"errors"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

func basicCompleteAcceptFixture(t *testing.T) (*executor, basicOrdinaryInputAccept) {
	t.Helper()
	w, stream := basicOrdinaryInputAcceptFixture(t)
	if _, err := w.db.ExecContext(t.Context(), `UPDATE catalog_info_changes SET family_mask=511 WHERE id=51`); err != nil {
		t.Fatal(err)
	}
	in := basicOrdinaryInputAccept{stream: stream, counts: [9]int64{1, 2, 2, 1, 2, 1, 1, 2, 1}}
	in.stream.rawShape.RawChapterCount = 2
	if _, err := w.db.ExecContext(t.Context(), `UPDATE catalog_assets SET duration=9 WHERE id=41`); err != nil {
		t.Fatal(err)
	}
	in.snapshot = basicProbeEffects{source: stream.source, changeRevision: stream.changeRevision}
	var incarnation []byte
	if err := w.db.QueryRowContext(t.Context(), `SELECT path,container,video_codec,audio_codec,width,height,duration,playback_origin_incarnation,playback_origin_revision,prepared_source_revision,file_size_revision,info_invalidation_revision FROM catalog_assets WHERE id=41`).Scan(&in.snapshot.assetPath, &in.snapshot.before.container, &in.snapshot.before.videoCodec, &in.snapshot.before.audioCodec, &in.snapshot.before.width, &in.snapshot.before.height, &in.snapshot.before.duration, &incarnation, &in.snapshot.playbackRevision, &in.snapshot.preparedRevision, &in.snapshot.fileSizeRevision, &in.snapshot.invalidationRevision); err != nil {
		t.Fatal(err)
	}
	copy(in.snapshot.playbackIncarnation[:], incarnation)
	in.snapshot.after = in.snapshot.before

	definitions := map[stmt.ID]stmt.Definition{}
	for _, d := range basicMediaDefinitions() {
		definitions[d.ID] = d
	}
	// Exact native fixture rows for the six additional sealed families. This is
	// SQL-fact setup; actual producer stage/seal controls live in their own suite.
	for _, family := range []struct {
		number int64
		rows   []basicMediaRow
	}{
		{4, []basicMediaRow{basicChapterFact{37, "known", "chapters:37"}}},
		{5, []basicMediaRow{basicChapter{1, "Opening", 0, 3}, basicChapter{2, "Closing", 3, 9}}},
		{6, []basicMediaRow{basicSubtitleFact{41, 0, true, "known", "subtitles:41"}}},
		{7, []basicMediaRow{basicSubtitleSidecar{"sidecar-1", "/media/Arrival.srt", "sidecar:30", 30, -7}}},
		{8, []basicMediaRow{basicSubtitle{id: "subtitle-1", origin: "embedded", locator: "2", streamIndex: 2, format: "subrip", language: "en"}, basicSubtitle{id: "subtitle-2", origin: "sidecar", locator: "/media/Arrival.srt", sidecarID: "sidecar-1", streamIndex: -1, format: "subrip", language: "fr"}}},
		{9, []basicMediaRow{basicSubtitleEvidence{41, "inventory:41"}}},
	} {
		for n, value := range family.rows {
			_, id, args, _, err := basicMediaValues(stream.source, int64(n), value)
			if err != nil {
				t.Fatal(err)
			}
			rows, err := w.db.QueryContext(t.Context(), definitions[id].SQL, append([]any{int64(51)}, args...)...)
			if err != nil {
				t.Fatal(err)
			}
			for rows.Next() {
			}
			if err = rows.Err(); err != nil {
				t.Fatal(err)
			}
			if err = rows.Close(); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := w.db.ExecContext(t.Context(), `INSERT INTO catalog_info_input_families VALUES(51,?,0,?,?,1)`, family.number, len(family.rows), len(family.rows)); err != nil {
			t.Fatal(err)
		}
	}
	return w, in
}
func TestBasicCompleteNineFamilyFreezeIsPendingOnly(t *testing.T) {
	w, in := basicCompleteAcceptFixture(t)
	turn := basicBoundedComponentTurn(t, w, basicCompleteTestDefinitions())
	got, err := acceptBasicOrdinaryInput(t.Context(), turn, in, 2000)
	if err != nil || got != (basicAcceptedPending{51, 7, 17, 14, 31, 1, 61}) {
		t.Fatal(got, err)
	}
	basicMediaCommit(t, turn)
	var exact bool
	err = w.db.QueryRowContext(t.Context(), `SELECT
 (SELECT phase=1 AND family_mask=511 AND job_revision=14 AND target_available=1 FROM catalog_info_changes WHERE id=51)
 AND (SELECT info_epoch=17 AND info_publication_pending=0 FROM catalog_assets WHERE id=41)
 AND (SELECT count(*)=0 FROM asset_chapters) AND (SELECT count(*)=0 FROM asset_streams)
 AND (SELECT count(*)=0 FROM scan_command_receipts)`).Scan(&exact)
	if err != nil || !exact {
		t.Fatal("freeze invented canonical activation/receipt", exact, err)
	}
}
func TestBasicCompleteNineFamilyFreezeRefusesIncompleteManifest(t *testing.T) {
	for _, scenario := range []string{"missing_family", "unsealed_subtitles", "wrong_expected", "extra_family", "raw_chapters", "invalidation_drift"} {
		t.Run(scenario, func(t *testing.T) {
			w, in := basicCompleteAcceptFixture(t)
			var q string
			switch scenario {
			case "missing_family":
				q = `DELETE FROM catalog_info_input_families WHERE family=9`
			case "unsealed_subtitles":
				q = `UPDATE catalog_info_input_families SET sealed=0 WHERE family=8`
			case "wrong_expected":
				in.counts[7]++
			case "extra_family":
				q = `INSERT INTO catalog_info_input_families VALUES(51,10,0,0,0,1)`
			case "invalidation_drift":
				q = `UPDATE catalog_assets SET info_invalidation_revision=2 WHERE id=41`
			case "raw_chapters":
				in.stream.rawShape.RawChapterCount = MaxRawChapters + 1
			}
			if q != "" {
				if _, err := w.db.ExecContext(t.Context(), q); err != nil {
					t.Fatal(err)
				}
			}
			turn := basicBoundedComponentTurn(t, w, basicCompleteTestDefinitions())
			got, err := acceptBasicOrdinaryInput(t.Context(), turn, in, 2000)
			if err == nil || got != (basicAcceptedPending{}) {
				t.Fatal("incomplete manifest froze", got, err)
			}
			if scenario == "raw_chapters" && !errors.Is(err, ErrCap) {
				t.Fatal(err)
			}
			if closeErr := turn.closeRows(); closeErr != nil {
				t.Fatal(closeErr)
			}
			if _, rollbackErr := turn.transaction.rollback(err); !errors.Is(rollbackErr, err) {
				t.Fatal(rollbackErr)
			}
			var phase, revision int64
			if err = w.db.QueryRowContext(t.Context(), `SELECT phase,job_revision FROM catalog_info_changes WHERE id=51`).Scan(&phase, &revision); err != nil || phase != 0 || revision != 13 {
				t.Fatal(phase, revision, err)
			}
		})
	}
}

func basicCompleteTestDefinitions() []stmt.Definition {
	defs := basicAcceptDefinitions()
	for _, d := range basicProbeDefinitions() {
		if d.ID == basicProbeAsset {
			defs = append(defs, d)
		}
	}
	return defs
}
