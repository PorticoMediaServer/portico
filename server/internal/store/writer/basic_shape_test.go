package writer

import (
	"errors"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

func basicShapeFixture(t *testing.T) (*executor, basicProbeEffects) {
	t.Helper()
	w, in := basicProbeFixture(t)
	turn := basicProbeTestTurn(t, w, basicProbeTestDefinitions())
	if _, err := applyBasicProbeScalars(t.Context(), turn, in); err != nil {
		t.Fatal(err)
	}
	basicMediaCommit(t, turn)
	// A later command prepares from ACTUAL current native scalars/counters.
	if err := w.db.QueryRowContext(t.Context(), `SELECT container,video_codec,audio_codec,width,height,duration,playback_origin_revision,prepared_source_revision FROM catalog_assets WHERE id=41`).Scan(&in.before.container, &in.before.videoCodec, &in.before.audioCodec, &in.before.width, &in.before.height, &in.before.duration, &in.playbackRevision, &in.preparedRevision); err != nil {
		t.Fatal(err)
	}
	in.after = in.before
	return w, in
}
func basicShapeTestDefinitions() []stmt.Definition {
	return append(basicProbeTestDefinitions(), basicShapeDefinitions()...)
}
func TestBasicShapePinsNewAndExistingDictionary(t *testing.T) {
	for _, existing := range []bool{false, true} {
		t.Run(map[bool]string{false: "new", true: "existing"}[existing], func(t *testing.T) {
			w, in := basicShapeFixture(t)
			if existing {
				if _, err := w.db.ExecContext(t.Context(), `INSERT INTO catalog_info_shapes VALUES(61,'mkv','h264',0,2)`); err != nil {
					t.Fatal(err)
				}
			}
			turn := basicProbeTestTurn(t, w, basicShapeTestDefinitions())
			got, err := pinBasicProbeShape(t.Context(), turn, in)
			if err != nil || got.shapeID <= 0 || got.changeRevision != 2 || (existing && got.shapeID != 61) {
				t.Fatal(got, err)
			}
			basicMediaCommit(t, turn)
			var refs, debt, epoch, phase, ownerReady int64
			if err = w.db.QueryRowContext(t.Context(), `SELECT ref_count FROM catalog_info_shapes WHERE id=?`, got.shapeID).Scan(&refs); err != nil {
				t.Fatal(err)
			}
			if err = w.db.QueryRowContext(t.Context(), `SELECT (SELECT used_bytes FROM catalog_info_input_budget),(SELECT info_epoch FROM catalog_assets WHERE id=41),phase,copy_family FROM catalog_info_changes WHERE id=51 AND target_shape_id=? AND target_owner_object IS NULL AND target_owner_revision IS NULL`, got.shapeID).Scan(&debt, &epoch, &phase, &ownerReady); err != nil {
				t.Fatal(err)
			}
			wantRefs, wantDebt := int64(1), int64(2065+182)
			if existing {
				wantRefs, wantDebt = 3, 2065
			}
			if refs != wantRefs || debt != wantDebt || epoch != 17 || phase != 0 || ownerReady != 1 {
				t.Fatal("shape pin activated or mischarged", refs, debt, epoch, phase, ownerReady)
			}
		})
	}
}
func TestBasicShapeLateFailureRollsBackReferenceAndDebt(t *testing.T) {
	w, in := basicShapeFixture(t)
	turn := basicProbeTestTurn(t, w, basicShapeTestDefinitions())
	delete(turn.steps, basicShapeHeaderCAS)
	got, err := pinBasicProbeShape(t.Context(), turn, in)
	if err == nil || got != (basicShapeTarget{}) {
		t.Fatal(got, err)
	}
	var count, debt int64
	if e := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT (SELECT count(*) FROM catalog_info_shapes),(SELECT used_bytes FROM catalog_info_input_budget)`).Scan(&count, &debt); e != nil || count != 1 || debt != 2247 {
		t.Fatal("late boundary was not reached", count, debt, e)
	}
	_ = turn.closeRows()
	_, _ = turn.transaction.rollback(err)
	if err = w.db.QueryRowContext(t.Context(), `SELECT (SELECT count(*) FROM catalog_info_shapes),(SELECT used_bytes FROM catalog_info_input_budget)`).Scan(&count, &debt); err != nil || count != 0 || debt != 2065 {
		t.Fatal(count, debt, err)
	}
}
func TestBasicShapeRefusesChangedNativeTuple(t *testing.T) {
	w, in := basicShapeFixture(t)
	if _, err := w.db.ExecContext(t.Context(), `UPDATE catalog_assets SET height=720 WHERE id=41`); err != nil {
		t.Fatal(err)
	}
	turn := basicProbeTestTurn(t, w, basicShapeTestDefinitions())
	got, err := pinBasicProbeShape(t.Context(), turn, in)
	if !errors.Is(err, ErrConflict) || got != (basicShapeTarget{}) {
		t.Fatal(got, err)
	}
}
