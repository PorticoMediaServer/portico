package writer

import (
	"context"
	"database/sql"
	"errors"
	"testing"
)

func mpGuidePinFixture(t *testing.T) (*executor, *Writer, *mpPhysicalSupervisor, mpGuidePinSelection) {
	t.Helper()
	e, _ := uoFlagsFixture(t)
	exec := func(q string, args ...any) { wc008NativeExec(t, e, q, args...) }
	exec(`INSERT INTO configuration(key,value) VALUES('id','server')`)
	exec(`INSERT INTO catalog_assets(id,token,path,size,modified_ns,container,video_codec,audio_codec,width,height,duration,library_id) VALUES(900,'guide-asset','guide.mkv',100,1,'matroska','h264','aac',1920,1080,1,7)`)
	exec(`INSERT INTO playback_sessions(id,session_hash,account_id,profile_id,item_id,asset_id,generation,state,grant_hash,grant_token,expires_at,request_id,duration,mp_execution_id) VALUES('guide-media','hash','account','profile',2111,'guide-asset',2,'playing','grant','opaque','2099-01-01T00:00:00Z','source-request',1,'execution')`)
	exec(`INSERT INTO live_source_identities(id) VALUES('guide-source')`)
	exec(`INSERT INTO live_generations(id,source_id,name,tuner_count,channel_count,programme_count,created_at) VALUES('guide-old','guide-source','Guide',1,2,1,'2026-11-08T00:00:00Z'),('guide-new','guide-source','Next',1,0,0,'2026-11-08T00:00:00Z')`)
	exec(`INSERT INTO live_sources(id,name,revision,state,tuner_count,active_generation,published_at) VALUES('guide-source','Guide',1,'active',1,'guide-old','2026-11-08T00:00:00Z')`)
	exec(`INSERT INTO live_channel_versions(generation_id,channel_id,provider_key,name,number,group_name,position,locator_sealed,available_start,available_end) VALUES('guide-old','channel','c','Channel','1','',0,x'01','',''),('guide-old','other-channel','o','Other','2','',1,x'01','','')`)
	exec(`INSERT INTO live_programmes(generation_id,id,channel_id,provider_key,title,start_utc,end_utc,lineage) VALUES('guide-old','programme','channel','p','Programme','2026-11-08T00:00:00Z','2026-11-08T01:00:00Z','p'),('guide-old','other-programme','other-channel','o','Other','2026-11-08T00:00:00Z','2026-11-08T01:00:00Z','o')`)
	w := &Writer{effective: &effectiveRuntime{executor: e}}
	s, err := newMPPhysicalSupervisor(w, t.Context(), "server")
	if err != nil {
		t.Fatal(err)
	}
	q := mpGuidePinSelection{Occurrence: mpGuidePinOccurrence{"guide-media", 2, "execution", "source-request"}, SourceID: "guide-source", GuideGeneration: "guide-old", ChannelID: "channel", ProgrammeID: sql.NullString{String: "programme", Valid: true}, CapturedMS: 100}
	return e, w, s, q
}
func mpGuideTurn(t *testing.T, e *executor, p CommandID) *commandTurn {
	t.Helper()
	return wc008NativeTurn(t, e, p, mpExternalGuidePinDefinitions())
}
func mpGuideInstallForTest(t *testing.T, e *executor, q mpGuidePinSelection) {
	t.Helper()
	turn := mpGuideTurn(t, e, 720)
	if got, err := installMPExternalGuidePin(t.Context(), turn, q); err != nil || got.Rows != 1 || got.AfterBytes <= 0 {
		t.Fatal(got, err)
	}
	if _, err := turn.transaction.commit(); err != nil {
		t.Fatal(err)
	}
}
func mpGuideStopForTest(t *testing.T, e *executor) {
	t.Helper()
	turn := wc008NativeTurn(t, e, 715, mpWorkerDefinitions())
	row, err := wc008DraftQuery(t.Context(), turn, mpRepeatStop, "stop", "guide-media", int64(1), int64(2), "execution", "account", "profile")
	if err != nil {
		t.Fatal(err)
	}
	var req string
	var exited int64
	if err = row.Scan(&req, &exited); err != nil || req != "stop" || exited != 0 {
		t.Fatal(req, exited, err)
	}
	if _, err = turn.transaction.commit(); err != nil {
		t.Fatal(err)
	}
}
func mpGuideJoinedForTest(t *testing.T, e *executor, s *mpPhysicalSupervisor, q mpGuidePinSelection) (*mpPhysicalObservation, mpPhysicalKey) {
	t.Helper()
	k := q.Occurrence
	execution, err := s.start(mpPhysicalRoot{k.MediaSessionID, k.ExecutionID, k.Generation}, &mpPhysicalTestSupervisor{}, mpPhysicalCommand(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	mpGuideStopForTest(t, e)
	o, err := execution.join(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return o, mpPhysicalKey{media: k.MediaSessionID, execution: k.ExecutionID, request: "stop", generation: k.Generation, revision: 2}
}
func TestMPGuidePinInstallExactNativeSelection(t *testing.T) {
	for _, cell := range []string{"accepted", "no-programme", "wrong-source", "wrong-generation", "wrong-channel", "wrong-programme-channel", "wrong-execution", "wrong-source-request", "retired", "rollback"} {
		t.Run(cell, func(t *testing.T) {
			e, _, _, q := mpGuidePinFixture(t)
			switch cell {
			case "no-programme":
				q.ProgrammeID = sql.NullString{}
			case "wrong-source":
				q.SourceID = "foreign"
			case "wrong-generation":
				q.GuideGeneration = "guide-new"
			case "wrong-channel":
				q.ChannelID = "foreign"
			case "wrong-programme-channel":
				q.ProgrammeID.String = "other-programme"
			case "wrong-execution":
				q.Occurrence.ExecutionID = "foreign"
			case "wrong-source-request":
				q.Occurrence.SourceRequestID = "foreign"
			case "retired":
				mpGuideStopForTest(t, e)
			}
			turn := mpGuideTurn(t, e, 720)
			got, err := installMPExternalGuidePin(t.Context(), turn, q)
			success := cell == "accepted" || cell == "no-programme" || cell == "rollback"
			if success {
				if err != nil || got.Rows != 1 || got.AfterBytes <= 0 {
					t.Fatal(got, err)
				}
			} else if err == nil || got != (mpGuidePinEffect{}) {
				t.Fatal("inexact guide accepted", got, err)
			}
			_, _ = turn.transaction.rollback(nil)
			var count int
			if err = e.db.QueryRow(`SELECT count(*) FROM mp_external_guide_pins`).Scan(&count); err != nil || count != 0 {
				t.Fatal("prefix leaked", count, err)
			}
		})
	}
}
func TestMPGuidePinReleaseWitnessesNoopsAndAccounting(t *testing.T) {
	for _, cell := range []string{"accepted", "no-proof", "foreign", "read-pin", "unclean-window", "clean-window", "rollback"} {
		t.Run(cell, func(t *testing.T) {
			e, _, s, q := mpGuidePinFixture(t)
			mpGuideInstallForTest(t, e, q)
			o, key := mpGuideJoinedForTest(t, e, s, q)
			switch cell {
			case "no-proof":
				o = nil
			case "foreign":
				key.execution = "foreign"
			case "read-pin":
				wc008NativeExec(t, e, `INSERT INTO mp_media_read_pins(pin_id,media_session_id,execution_id,occurrence_generation,source_version,from_byte,size) VALUES('reader','guide-media','execution',2,'source',0,1)`)
			case "unclean-window", "clean-window":
				wc008NativeExec(t, e, `INSERT INTO playback_hls_windows(session_id,start_index,status,started_at_ms,mp_physical_generation,mp_execution_id,mp_source_request_id,mp_writes_retired,mp_exit_confirmed,mp_cleaned) VALUES('guide-media',0,'complete',1,2,'execution','source-request',1,1,?)`, map[bool]int64{true: 1, false: 0}[cell == "clean-window"])
			}
			turn := mpGuideTurn(t, e, 727)
			got, err := releaseMPExternalGuidePin(t.Context(), turn, s, o, q.Occurrence, key)
			success := cell == "accepted" || cell == "clean-window" || cell == "rollback"
			if success {
				if err != nil || got.Rows != 1 || got.release == nil || got.BeforeBytes <= got.AfterBytes || got.PhysicalAfterBytes <= got.PhysicalBeforeBytes {
					t.Fatal(got, err)
				}
			} else if err == nil || got != (mpGuidePinEffect{}) {
				t.Fatal("unproved release", got, err)
			}
			if !success || cell == "rollback" {
				_, _ = turn.transaction.rollback(nil)
				var released, exited int64
				if err = e.db.QueryRow(`SELECT released_ms,(SELECT mp_exited_generation FROM playback_sessions WHERE id='guide-media') FROM mp_external_guide_pins`).Scan(&released, &exited); err != nil || released != 0 || exited != 0 {
					t.Fatal("prefix leaked", released, exited, err)
				}
				return
			}
			if _, err = turn.transaction.commit(); err != nil {
				t.Fatal(err)
			}
			if err = releaseMPPhysicalAfterCommit(turn, got.release); err != nil {
				t.Fatal(err)
			}
			turn = mpGuideTurn(t, e, 727)
			noop, err := releaseMPExternalGuidePin(t.Context(), turn, nil, nil, q.Occurrence, key)
			if err != nil || noop.Rows != 0 || noop.Noop != "already_released" || noop.release != nil || noop.BeforeBytes != noop.AfterBytes {
				t.Fatal(noop, err)
			}
			missing := q.Occurrence
			missing.ExecutionID = "absent"
			noop, err = releaseMPExternalGuidePin(t.Context(), turn, nil, nil, missing, key)
			if err != nil || noop.Noop != "absent" || noop.release != nil {
				t.Fatal(noop, err)
			}
		})
	}
}
func TestMPGuidePinReleasedOldOccurrenceReclaimAndRollback(t *testing.T) {
	e, _, s, q := mpGuidePinFixture(t)
	mpGuideInstallForTest(t, e, q)
	turn := mpGuideTurn(t, e, IARetireStep)
	if got, err := reclaimMPReleasedGuidePin(t.Context(), turn, q.Occurrence); !errors.Is(err, ErrConflict) || got != (mpGuidePinReclaimEffect{}) {
		t.Fatal("unreleased deletion", got, err)
	}
	_, _ = turn.transaction.rollback(nil)
	o, key := mpGuideJoinedForTest(t, e, s, q)
	turn = mpGuideTurn(t, e, 727)
	released, err := releaseMPExternalGuidePin(t.Context(), turn, s, o, q.Occurrence, key)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = turn.transaction.commit(); err != nil {
		t.Fatal(err)
	}
	if err = releaseMPPhysicalAfterCommit(turn, released.release); err != nil {
		t.Fatal(err)
	}
	wc008NativeExec(t, e, `UPDATE live_sources SET active_generation='guide-new' WHERE id='guide-source'`)
	wc008NativeExec(t, e, `DELETE FROM live_programmes WHERE generation_id='guide-old'`)
	wc008NativeExec(t, e, `DELETE FROM live_channel_versions WHERE generation_id='guide-old'`)
	wc008NativeExec(t, e, `DELETE FROM live_generations WHERE id='guide-old'`)
	wc008NativeExec(t, e, `UPDATE playback_sessions SET generation=3,mp_execution_id='successor',mp_cleanup_complete=0 WHERE id='guide-media'`)
	turn = mpGuideTurn(t, e, IARetireStep)
	candidate, err := selectMPGuidePinOccurrence(t.Context(), turn, "guide-media", mpGuidePinOccurrence{})
	if err != nil || candidate != q.Occurrence {
		t.Fatal(candidate, err)
	}
	removed, err := reclaimMPReleasedGuidePin(t.Context(), turn, candidate)
	if err != nil || removed.Rows != 1 || removed.VariableBytes != released.AfterBytes {
		t.Fatal(removed, err)
	}
	_, _ = turn.transaction.rollback(nil)
	var count int
	if err = e.db.QueryRow(`SELECT count(*) FROM mp_external_guide_pins`).Scan(&count); err != nil || count != 1 {
		t.Fatal("rollback lost history", count, err)
	}
	turn = mpGuideTurn(t, e, IARetireStep)
	removed, err = reclaimMPReleasedGuidePin(t.Context(), turn, q.Occurrence)
	if err != nil || removed.Rows != 1 {
		t.Fatal(removed, err)
	}
	if _, err = turn.transaction.commit(); err != nil {
		t.Fatal(err)
	}
	turn = mpGuideTurn(t, e, IARetireStep)
	if _, err = selectMPGuidePinOccurrence(t.Context(), turn, "guide-media", mpGuidePinOccurrence{}); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal(err)
	}
}
func TestMPGuidePinCancelledJoinCannotRelease(t *testing.T) {
	e, _, s, q := mpGuidePinFixture(t)
	mpGuideInstallForTest(t, e, q)
	cmd, ready, release := mpPhysicalBlockedCommand(t)
	execution, err := s.start(mpPhysicalRoot{q.Occurrence.MediaSessionID, q.Occurrence.ExecutionID, 2}, &mpPhysicalTestSupervisor{}, cmd, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	mpPhysicalAwaitFile(t, ready)
	mpGuideStopForTest(t, e)
	ctx, cancel := context.WithCancel(t.Context())
	done := mpPhysicalJoinAsync(ctx, execution)
	mpPhysicalAssertHeld(t, execution, done)
	cancel()
	got := <-done
	if got.o != nil || !errors.Is(got.err, context.Canceled) {
		t.Fatal(got)
	}
	turn := mpGuideTurn(t, e, 727)
	if out, err := releaseMPExternalGuidePin(t.Context(), turn, s, got.o, q.Occurrence, mpPhysicalKey{media: "guide-media", execution: "execution", generation: 2, revision: 2, request: "stop"}); out != (mpGuidePinEffect{}) || err == nil {
		t.Fatal(out, err)
	}
	_, _ = turn.transaction.rollback(nil)
	release()
	if _, err = execution.join(t.Context()); err != nil {
		t.Fatal(err)
	}
}
