package writer

import (
	"bytes"
	"errors"
	"math"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

func basicProbeFixture(t *testing.T) (*executor, basicProbeEffects) {
	t.Helper()
	w, s := basicStreamFixture(t)
	incarnation := [16]byte(bytes.Repeat([]byte{0x41}, 16))
	if _, err := w.db.ExecContext(t.Context(), `UPDATE catalog_info_changes SET family_mask=511 WHERE id=51`); err != nil {
		t.Fatal(err)
	}
	if _, err := w.db.ExecContext(t.Context(), `UPDATE catalog_assets SET container='old',video_codec='vp9',audio_codec='aac',width=1920,height=1080,duration=90,playback_origin_incarnation=?,playback_origin_revision=101,prepared_source_revision=103,file_size_revision=113,info_invalidation_revision=109 WHERE id=41`, incarnation[:]); err != nil {
		t.Fatal(err)
	}
	return w, basicProbeEffects{source: s, changeRevision: 1, assetPath: "/media/Arrival.mkv", before: basicProbeScalars{"old", "vp9", "aac", 1920, 1080, 90}, after: basicProbeScalars{"mkv", "h264", "", 0, 0, 100}, playbackIncarnation: incarnation, playbackRevision: 101, preparedRevision: 103, fileSizeRevision: 113, invalidationRevision: 109}
}
func basicProbeTestDefinitions() []stmt.Definition {
	return append(basicStreamStageDefinitions(), basicProbeDefinitions()...)
}
func TestBasicProbeReplacesSixScalarsAndCapturesOld(t *testing.T) {
	w, in := basicProbeFixture(t)
	turn := basicBoundedComponentTurn(t, w, basicProbeTestDefinitions())
	got, err := applyBasicProbeScalars(t.Context(), turn, in)
	if err != nil || got != (basicProbeProgress{6, 102, 104}) {
		t.Fatal(got, err)
	}
	basicMediaCommit(t, turn)
	var exact bool
	err = w.db.QueryRowContext(t.Context(), `SELECT
 (SELECT container='mkv' AND video_codec='h264' AND audio_codec='' AND width=0 AND height=0 AND duration=100 AND playback_origin_revision=102 AND prepared_source_revision=104 AND file_size_revision=113 AND info_invalidation_revision=109 AND info_epoch=17 AND info_owner_object IS NULL AND info_owner_revision IS NULL FROM catalog_assets WHERE id=41)
 AND (SELECT count(*)=6 FROM catalog_info_scalar_old WHERE change_id=51)
 AND (SELECT old_text='aac' AND old_integer IS NULL AND old_real IS NULL AND kind=3 FROM catalog_info_scalar_old WHERE change_id=51 AND field=6)
 AND (SELECT old_integer=1920 AND old_real IS NULL AND old_text IS NULL AND kind=1 FROM catalog_info_scalar_old WHERE change_id=51 AND field=7)
 AND (SELECT old_real=90 AND old_integer IS NULL AND old_text IS NULL AND kind=2 FROM catalog_info_scalar_old WHERE change_id=51 AND field=9)
 AND (SELECT used_bytes=2065 FROM catalog_info_input_budget)
 AND (SELECT phase=0 AND expected_epoch=17 AND input_revision=7 AND job_revision=1 AND family_mask=511 FROM catalog_info_changes WHERE id=51)`).Scan(&exact)
	if err != nil || !exact {
		t.Fatal("incoherent OLD/NEW or epoch alias", exact, err)
	}
}
func TestBasicProbeNoopAndPreservedMaximumCounters(t *testing.T) {
	for _, same := range []bool{true, false} {
		t.Run(map[bool]string{true: "scalar_noop", false: "codec_only"}[same], func(t *testing.T) {
			w, in := basicProbeFixture(t)
			in.playbackRevision, in.preparedRevision = math.MaxInt64, math.MaxInt64
			if _, err := w.db.ExecContext(t.Context(), `UPDATE catalog_assets SET playback_origin_revision=9223372036854775807,prepared_source_revision=9223372036854775807 WHERE id=41`); err != nil {
				t.Fatal(err)
			}
			in.after.container = in.before.container
			if same {
				in.after = in.before
			}
			turn := basicBoundedComponentTurn(t, w, basicProbeTestDefinitions())
			got, err := applyBasicProbeScalars(t.Context(), turn, in)
			if err != nil || got.playbackRevision != math.MaxInt64 || got.preparedRevision != math.MaxInt64 || (same && got.changedFields != 0) || (!same && got.changedFields != 5) {
				t.Fatal(got, err)
			}
			basicMediaCommit(t, turn)
			var phase, epoch, count int64
			if err = w.db.QueryRowContext(t.Context(), `SELECT (SELECT phase FROM catalog_info_changes WHERE id=51),(SELECT info_epoch FROM catalog_assets WHERE id=41),(SELECT count(*) FROM catalog_info_scalar_old)`).Scan(&phase, &epoch, &count); err != nil || phase != 0 || epoch != 17 || (same && count != 0) {
				t.Fatal("scalar noop invented Basic publication", phase, epoch, count, err)
			}
		})
	}
}
func TestBasicProbeInvalidAndStaleVectorsRefuse(t *testing.T) {
	for _, name := range []string{"zero_duration", "nan_duration", "missing_video", "invalidation_drift", "container_overflow"} {
		t.Run(name, func(t *testing.T) {
			w, in := basicProbeFixture(t)
			switch name {
			case "zero_duration":
				in.after.duration = 0
			case "nan_duration":
				in.after.duration = math.NaN()
			case "missing_video":
				in.after.videoCodec = ""
			case "invalidation_drift":
				if _, err := w.db.ExecContext(t.Context(), `UPDATE catalog_assets SET info_invalidation_revision=110 WHERE id=41`); err != nil {
					t.Fatal(err)
				}
			case "container_overflow":
				in.playbackRevision = math.MaxInt64
				if _, err := w.db.ExecContext(t.Context(), `UPDATE catalog_assets SET playback_origin_revision=9223372036854775807 WHERE id=41`); err != nil {
					t.Fatal(err)
				}
			}
			turn := basicBoundedComponentTurn(t, w, basicProbeTestDefinitions())
			got, err := applyBasicProbeScalars(t.Context(), turn, in)
			if err == nil || got != (basicProbeProgress{}) {
				t.Fatal("bad probe accepted", got, err)
			}
			_ = turn.closeRows()
			_, _ = turn.transaction.rollback(err)
			var count, used int64
			if err = w.db.QueryRowContext(t.Context(), `SELECT (SELECT count(*) FROM catalog_info_scalar_old),(SELECT used_bytes FROM catalog_info_input_budget)`).Scan(&count, &used); err != nil || count != 0 || used != 1000 {
				t.Fatal(count, used, err)
			}
		})
	}
}
func TestBasicProbeLateFailureRollsBackOldAndDebt(t *testing.T) {
	w, in := basicProbeFixture(t)
	turn := basicBoundedComponentTurn(t, w, basicProbeTestDefinitions())
	delete(turn.steps, basicProbeAssetCAS)
	got, err := applyBasicProbeScalars(t.Context(), turn, in)
	if err == nil || got != (basicProbeProgress{}) {
		t.Fatal(got, err)
	}
	var count, used int64
	if e := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT (SELECT count(*) FROM catalog_info_scalar_old),(SELECT used_bytes FROM catalog_info_input_budget)`).Scan(&count, &used); e != nil || count != 6 || used != 2065 {
		t.Fatal("did not reach late boundary", count, used, e)
	}
	_ = turn.closeRows()
	_, _ = turn.transaction.rollback(err)
	if err = w.db.QueryRowContext(t.Context(), `SELECT (SELECT count(*) FROM catalog_info_scalar_old),(SELECT used_bytes FROM catalog_info_input_budget)`).Scan(&count, &used); err != nil || count != 0 || used != 1000 {
		t.Fatal(count, used, err)
	}
	var container string
	if err = w.db.QueryRowContext(t.Context(), `SELECT container FROM catalog_assets WHERE id=41`).Scan(&container); err != nil || container != "old" {
		t.Fatal(container, err)
	}
	if _, err = applyBasicProbeScalars(nil, nil, in); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
}
