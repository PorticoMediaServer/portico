package writer

import (
	"errors"
	"testing"
)

func mpGuideNextGenerationChannels(t *testing.T, e *executor) {
	t.Helper()
	wc008NativeExec(t, e, `INSERT INTO live_channel_versions(generation_id,channel_id,provider_key,name,number,group_name,position,locator_sealed,available_start,available_end) VALUES('guide-new','channel','c','Channel','1','',0,x'01','','')`)
	wc008NativeExec(t, e, `INSERT INTO live_programmes(generation_id,id,channel_id,provider_key,title,start_utc,end_utc,lineage) VALUES('guide-new','programme','channel','p','Programme','2026-11-08T00:00:00Z','2026-11-08T01:00:00Z','p')`)
}

func TestMPGuidePinRequiresActiveGenerationWithOtherwiseValidGuide(t *testing.T) {
	for _, cell := range []string{"inactive", "replaced"} {
		t.Run(cell, func(t *testing.T) {
			e, _, _, q := mpGuidePinFixture(t)
			mpGuideNextGenerationChannels(t, e)
			if cell == "inactive" {
				q.GuideGeneration = "guide-new"
			} else {
				wc008NativeExec(t, e, `UPDATE live_sources SET active_generation='guide-new' WHERE id='guide-source'`)
			}
			// Both generations contain the selected channel AND programme. Neither
			// downstream point may mask removal of the independent active-head guard.
			turn := mpGuideTurn(t, e, 720)
			if got, err := installMPExternalGuidePin(t.Context(), turn, q); got != (mpGuidePinEffect{}) || !errors.Is(err, ErrConflict) {
				t.Fatal("inactive guide accepted", got, err)
			}
			_, _ = turn.transaction.rollback(nil)
			var count int64
			if err := e.db.QueryRow(`SELECT count(*) FROM mp_external_guide_pins`).Scan(&count); err != nil || count != 0 {
				t.Fatal(count, err)
			}
		})
	}
}

func TestMPGuidePinReplacementKeepsOriginalPhysicalCapture(t *testing.T) {
	e, _, s, q := mpGuidePinFixture(t)
	mpGuideNextGenerationChannels(t, e)
	mpGuideInstallForTest(t, e, q)
	wc008NativeExec(t, e, `UPDATE live_sources SET active_generation='guide-new' WHERE id='guide-source'`)
	var captured, current string
	if err := e.db.QueryRow(`SELECT captured_generation_id,current_generation_id FROM mp_external_guide_pins WHERE media_session_id='guide-media'`).Scan(&captured, &current); err != nil || captured != "guide-old" || current != "guide-old" {
		t.Fatal("head replacement rewrote custody", captured, current, err)
	}
	o, key := mpGuideJoinedForTest(t, e, s, q)
	turn := mpGuideTurn(t, e, 727)
	effect, err := releaseMPExternalGuidePin(t.Context(), turn, s, o, q.Occurrence, key)
	if err != nil || effect.Rows != 1 || effect.release == nil {
		t.Fatal("old physical occurrence could not release", effect, err)
	}
	if _, err = turn.transaction.commit(); err != nil {
		t.Fatal(err)
	}
	if err = releaseMPPhysicalAfterCommit(turn, effect.release); err != nil {
		t.Fatal(err)
	}
}

func TestMPGuidePinNineCleanWindowsRefuseThenSameProofRetries(t *testing.T) {
	e, _, s, q := mpGuidePinFixture(t)
	mpGuideInstallForTest(t, e, q)
	o, key := mpGuideJoinedForTest(t, e, s, q)
	for start := int64(0); start < 9; start++ {
		wc008NativeExec(t, e, `INSERT INTO playback_hls_windows(session_id,start_index,status,started_at_ms,mp_physical_generation,mp_execution_id,mp_source_request_id,mp_writes_retired,mp_exit_confirmed,mp_cleaned) VALUES('guide-media',?,'complete',1,2,'execution','source-request',1,1,1)`, start)
	}
	turn := mpGuideTurn(t, e, 727)
	if got, err := releaseMPExternalGuidePin(t.Context(), turn, s, o, q.Occurrence, key); got != (mpGuidePinEffect{}) || !errors.Is(err, ErrCap) {
		t.Fatal("nine-window lookahead accepted", got, err)
	}
	_, _ = turn.transaction.rollback(nil)
	var released, exited int64
	if err := e.db.QueryRow(`SELECT released_ms,(SELECT mp_exited_generation FROM playback_sessions WHERE id='guide-media') FROM mp_external_guide_pins`).Scan(&released, &exited); err != nil || released != 0 || exited != 0 {
		t.Fatal("capacity refusal leaked prefix", released, exited, err)
	}
	wc008NativeExec(t, e, `DELETE FROM playback_hls_windows WHERE session_id='guide-media' AND start_index=8 AND mp_exit_confirmed=1 AND mp_cleaned=1`)
	turn = mpGuideTurn(t, e, 727)
	effect, err := releaseMPExternalGuidePin(t.Context(), turn, s, o, q.Occurrence, key)
	if err != nil || effect.Rows != 1 || effect.release == nil {
		t.Fatal("same observation failed after bounded drain", effect, err)
	}
	if _, err = turn.transaction.commit(); err != nil {
		t.Fatal(err)
	}
	if err = releaseMPPhysicalAfterCommit(turn, effect.release); err != nil {
		t.Fatal(err)
	}
}

func TestMPGuidePinReclaimRequiresOriginalSourceRequest(t *testing.T) {
	e, _, s, q := mpGuidePinFixture(t)
	mpGuideInstallForTest(t, e, q)
	o, key := mpGuideJoinedForTest(t, e, s, q)
	turn := mpGuideTurn(t, e, 727)
	effect, err := releaseMPExternalGuidePin(t.Context(), turn, s, o, q.Occurrence, key)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = turn.transaction.commit(); err != nil {
		t.Fatal(err)
	}
	if err = releaseMPPhysicalAfterCommit(turn, effect.release); err != nil {
		t.Fatal(err)
	}
	wrong := q.Occurrence
	wrong.SourceRequestID = "wrong-request"
	turn = mpGuideTurn(t, e, IARetireStep)
	if got, err := reclaimMPReleasedGuidePin(t.Context(), turn, wrong); got != (mpGuidePinReclaimEffect{}) || !errors.Is(err, ErrConflict) {
		t.Fatal("wrong source request deleted history", got, err)
	}
	_, _ = turn.transaction.rollback(nil)
	var bytes int64
	if err = e.db.QueryRow(`SELECT variable_bytes FROM mp_external_guide_pins WHERE media_session_id='guide-media'`).Scan(&bytes); err != nil || bytes != effect.AfterBytes {
		t.Fatal(bytes, err)
	}
	turn = mpGuideTurn(t, e, IARetireStep)
	got, err := reclaimMPReleasedGuidePin(t.Context(), turn, q.Occurrence)
	if err != nil || got.Rows != 1 || got.VariableBytes != effect.AfterBytes {
		t.Fatal(got, err)
	}
	if _, err = turn.transaction.commit(); err != nil {
		t.Fatal(err)
	}
}

func TestMPGuidePinReleaseRefusesSessionSourceRequestDrift(t *testing.T) {
	e, _, s, q := mpGuidePinFixture(t)
	mpGuideInstallForTest(t, e, q)
	o, key := mpGuideJoinedForTest(t, e, s, q)
	wc008NativeExec(t, e, `UPDATE playback_sessions SET request_id='drifted-request' WHERE id='guide-media'`)
	turn := mpGuideTurn(t, e, 727)
	if got, err := releaseMPExternalGuidePin(t.Context(), turn, s, o, q.Occurrence, key); got != (mpGuidePinEffect{}) || err == nil {
		t.Fatal("source request drift released pin", got, err)
	}
	_, _ = turn.transaction.rollback(nil)
	var released, exited int64
	if err := e.db.QueryRow(`SELECT released_ms,(SELECT mp_exited_generation FROM playback_sessions WHERE id='guide-media') FROM mp_external_guide_pins`).Scan(&released, &exited); err != nil || released != 0 || exited != 0 {
		t.Fatal(released, exited, err)
	}
	wc008NativeExec(t, e, `UPDATE playback_sessions SET request_id='source-request' WHERE id='guide-media'`)
	turn = mpGuideTurn(t, e, 727)
	effect, err := releaseMPExternalGuidePin(t.Context(), turn, s, o, q.Occurrence, key)
	if err != nil || effect.Rows != 1 || effect.release == nil {
		t.Fatal("original source request did not recover", effect, err)
	}
	if _, err = turn.transaction.commit(); err != nil {
		t.Fatal(err)
	}
	if err = releaseMPPhysicalAfterCommit(turn, effect.release); err != nil {
		t.Fatal(err)
	}
}
