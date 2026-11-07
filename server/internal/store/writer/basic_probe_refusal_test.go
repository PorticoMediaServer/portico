package writer

import (
	"errors"
	"strings"
	"testing"
)

func TestBasicProbeRefusesPreviouslyCapturedOld(t *testing.T) {
	w, in := basicProbeFixture(t)
	in.after = in.before
	in.after.videoCodec = "hevc"
	// Field 4 is deliberately disjoint from the changed field 5: a UNIQUE
	// collision must not substitute for the fresh-input presence guard.
	if _, err := w.db.ExecContext(t.Context(), `INSERT INTO catalog_info_scalar_old(change_id,field,kind,old_integer,old_real,old_text) VALUES(51,4,3,NULL,NULL,'stale')`); err != nil {
		t.Fatal(err)
	}
	if _, err := w.db.ExecContext(t.Context(), `UPDATE catalog_info_input_budget SET used_bytes=1181 WHERE singleton=1`); err != nil {
		t.Fatal(err)
	}
	turn := basicProbeTestTurn(t, w, basicProbeTestDefinitions())
	var before int64
	if err := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT total_changes()`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	got, err := applyBasicProbeScalars(t.Context(), turn, in)
	if !errors.Is(err, ErrConflict) || got != (basicProbeProgress{}) {
		t.Fatalf("previously captured OLD must refuse: got=%+v err=%v", got, err)
	}
	// Inspect before rollback, on the very same native transaction. Rollback
	// must not hide a debt charge, OLD insert, raw scalar write or epoch change.
	var after int64
	var exact bool
	err = turn.transaction.conn.QueryRowContext(t.Context(), `SELECT total_changes(),
 (SELECT used_bytes=1181 FROM catalog_info_input_budget WHERE singleton=1)
 AND (SELECT count(*)=1 FROM catalog_info_scalar_old)
 AND (SELECT kind=3 AND old_text='stale' AND old_integer IS NULL AND old_real IS NULL FROM catalog_info_scalar_old WHERE change_id=51 AND field=4)
 AND (SELECT container='old' AND video_codec='vp9' AND audio_codec='aac' AND width=1920 AND height=1080 AND duration=90 AND playback_origin_incarnation=? AND playback_origin_revision=101 AND prepared_source_revision=103 AND file_size_revision=113 AND info_invalidation_revision=109 AND info_epoch=17 AND info_owner_object IS NULL AND info_owner_revision IS NULL FROM catalog_assets WHERE id=41)
 AND (SELECT phase=0 AND expected_epoch=17 AND input_revision=7 AND job_revision=1 AND family_mask=511 FROM catalog_info_changes WHERE id=51)`, in.playbackIncarnation[:]).Scan(&after, &exact)
	if err != nil || !exact || after != before {
		t.Fatalf("refusal changed native state before rollback: changes=%d->%d exact=%v err=%v", before, after, exact, err)
	}
}

func TestBasicProbePayloadCapPrecedesNativeLookup(t *testing.T) {
	w, in := basicProbeFixture(t)
	in.after.audioCodec = strings.Repeat("a", maxCanonicalRequestBytes+1)
	turn := basicProbeTestTurn(t, w, basicProbeTestDefinitions())
	// A missing first native step makes cap removal observable as an
	// unregistered-step refusal, before a binder can impose its own byte cap.
	delete(turn.steps, basicStageHeader)
	got, err := applyBasicProbeScalars(t.Context(), turn, in)
	if !errors.Is(err, ErrCap) || got != (basicProbeProgress{}) {
		t.Fatalf("payload cap must precede native lookup: got=%+v err=%v", got, err)
	}
	if err := validateBasicProbeEffects(in); !errors.Is(err, ErrCap) {
		t.Fatalf("pure payload validation must refuse oversized input: %v", err)
	}
}

// Every individual field is legal-sized. The shared running allowance, rather
// than an independent per-field cap or a later binder, must govern the tuple.
func TestBasicProbePayloadCapUsesWholeScalarTuple(t *testing.T) {
	in := basicProbeEffects{changeRevision: 1, assetPath: "/a", playbackIncarnation: [16]byte{1},
		playbackRevision: 1, preparedRevision: 1, fileSizeRevision: 1, invalidationRevision: 1,
		after: basicProbeScalars{videoCodec: "h264", duration: 1}}
	in.before.container = strings.Repeat("m", maxCanonicalRequestBytes/2)
	remaining := maxCanonicalRequestBytes - len(in.assetPath) - len(in.before.container) - len(in.after.videoCodec)
	in.after.audioCodec = strings.Repeat("a", remaining+1)
	if len(in.before.container) >= maxCanonicalRequestBytes || len(in.after.audioCodec) >= maxCanonicalRequestBytes {
		t.Fatal("fixture must exercise the aggregate cap, not a per-field cap")
	}
	if err := validateBasicProbeEffects(in); !errors.Is(err, ErrCap) {
		t.Fatalf("complete scalar tuple cap+1 must refuse: %v", err)
	}
	in.after.audioCodec = in.after.audioCodec[:remaining]
	if err := validateBasicProbeEffects(in); err != nil {
		t.Fatalf("exact-cap complete tuple is valid: %v", err)
	}
}
