package writer

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/stmttest"
)

func mgRecordingFixture(t *testing.T) (*executor, mgRecordingOwner, mgRecordingScope, mgRecordingFrozen, mgRecordingBirth) {
	t.Helper()
	w, _, clock := testExecutor(t)
	now := clock.UnixMilli()
	mgRecordingIntentDraft(t, w)
	o := mgRecordingOwner{"recording-account", "recording-profile", strings.Repeat("a", 32), strings.Repeat("b", 32), 7, 11, 13}
	scope := mgRecordingScope{strings.Repeat("1", 48), strings.Repeat("2", 48), strings.Repeat("2", 48), strings.Repeat("3", 64), strings.Repeat("4", 64), 3}
	present := func(s string) mgCaptureNullableText { return mgCaptureNullableText{text: s, valid: true} }
	p := mgRecordingProgramme{id: scope.programme, channel: scope.channel, provider: "provider-key", title: "Native full programme", start: time.UnixMilli(now + 600000).UTC().Format(time.RFC3339Nano), end: time.UnixMilli(now + 4200000).UTC().Format(time.RFC3339Nano), lineage: "provider-id", metadata: 1, series: present("series-one"), episode: present("episode-seven"), newEvidence: present("new"), description: present("Frozen description with quotes \"and\" Unicode é"), facts: present(`{"subtitle":"The whole programme","episode":{"season":2,"number":7,"display":"Part seven"},"categories":["Drama","Documentary"],"rating":{"system":"VCHIP","value":"TV-14"},"year":2026,"starRating":"4/5","live":true,"premiere":true}`)}
	f, err := freezeMGRecordingIntent(p, mgRecordingOptions{60000, 180000, 9, 30, 5})
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO accounts(id,username,password_hash,profile_id,epoch) VALUES(?,?,zeroblob(0),?,?)`, []any{o.account, o.account, o.profile, o.accountEpoch}},
		{`INSERT INTO direct_memberships(account_id,incarnation,role,revision) VALUES(?,?,'owner',?)`, []any{o.account, o.memberIncarnation, o.memberRevision}},
		{`INSERT INTO direct_profiles(id,account_id,name,lifetime,revision) VALUES(?,?,'DVR owner',?,?)`, []any{o.profile, o.account, o.profileLifetime, o.profileRevision}},
		{`INSERT INTO live_source_identities(id) VALUES(?)`, []any{scope.source}},
		{`INSERT INTO live_generations(id,source_id,name,tuner_count,channel_count,programme_count,created_at) VALUES(?,?,'Guide',2,1,1,'2026-11-07T00:00:00Z')`, []any{scope.generation, scope.source}},
		{`INSERT INTO live_sources(id,name,revision,state,tuner_count,active_generation,published_at) VALUES(?,'Guide',?,'active',2,?,'2026-11-07T00:00:00Z')`, []any{scope.source, scope.revision, scope.head}},
		{`INSERT INTO live_channel_versions(generation_id,channel_id,provider_key,name,number,group_name,position,locator_sealed,available_start,available_end) VALUES(?,?,'channel-provider','Channel','7','Group',0,zeroblob(16),?,?)`, []any{scope.generation, scope.channel, p.start, p.end}},
		{`INSERT INTO live_programmes(generation_id,id,channel_id,provider_key,title,start_utc,end_utc,lineage,has_live_programme_metadata,metadata_series_id,metadata_episode_id,metadata_new_evidence,metadata_description,metadata_facts,has_live_programme_icons,icon_url) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, []any{scope.generation, p.id, p.channel, p.provider, p.title, p.start, p.end, p.lineage, p.metadata, p.series.value(), p.episode.value(), p.newEvidence.value(), p.description.value(), p.facts.value(), p.hasIcon, p.icon.value()}},
	} {
		if _, err := w.db.ExecContext(t.Context(), q.sql, q.args...); err != nil {
			t.Fatal(q.sql, err)
		}
	}
	return w, o, scope, f, mgRecordingBirth{mgCaptureID{1}, mgCaptureID{2}, mgCaptureDigest{3}}
}
func mgRecordingTestTurn(t *testing.T, w *executor, defs ...[]stmt.Definition) *commandTurn {
	t.Helper()
	all := mgRecordingIntentDefinitions()
	if len(defs) > 0 {
		all = defs[0]
	}
	registry, err := stmttest.New(t, all)
	if err != nil {
		t.Fatal(err)
	}
	// Synthetic component budget only: several intent operations on one fixture
	// frame. Parent owns actual admission/profile and measured complete hold.
	recipe := turnRecipe{limit: Work{4096, 1000000, 4096, 32 << 20, 25 * time.Millisecond}, setup: Work{8, 100, 8, 4096, time.Millisecond}, terminal: Work{8, 100, 8, 4096, time.Millisecond}}
	tx, err := w.begin(t.Context(), InventoryAccept, recipe, &testCustody{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = tx.rollback(nil) })
	var steps []statementStep
	for _, d := range all {
		steps = append(steps, statementStep{d.ID, Work{16, 10000, 32, 65536, time.Microsecond}})
	}
	turn, err := newCommandTurn(tx, registry, steps)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = turn.closeRows() })
	return turn
}
func mgRecordingZero(t *testing.T, out mgRecordingIntentFacts, err error) {
	t.Helper()
	if err == nil || out != (mgRecordingIntentFacts{}) {
		t.Fatal("failure leaked intent facts", out, err)
	}
}
func TestMGRecordingIntentNativeScheduleUpdateKeep(t *testing.T) {
	w, o, scope, f, birth := mgRecordingFixture(t)
	turn := mgRecordingTestTurn(t, w)
	got, err := mgScheduleRecording(t.Context(), turn, o, scope, f, birth)
	if err != nil {
		t.Fatal(err)
	}
	n := got.recording.capture
	if n.id != mgRecordingID(o, scope.source, scope.programme) || n.revision != 1 || n.state != "scheduled" || got.ownerRevision != 1 || !got.reservation.present || got.reservation.enabled != 1 || n.incarnation != birth.incarnation || n.createOperation != birth.operation || n.birthDigest != birth.digest || n.lastSequence != 0 || n.ackSequence != 0 || n.memberIncarnation != o.memberIncarnation || n.profileLifetime != o.profileLifetime || !n.guidePin.valid || n.guidePin.text != scope.generation {
		t.Fatal("birth facts drift", got)
	}
	var copyProgramme mgRecordingProgrammeCopy
	if err := json.Unmarshal([]byte(got.recording.programmeJSON), &copyProgramme); err != nil || copyProgramme.Description != f.programme.description.text || copyProgramme.Subtitle != "The whole programme" || copyProgramme.Episode.Number != 7 || copyProgramme.Rating.Value != "TV-14" || len(copyProgramme.Categories) != 2 || !copyProgramme.Flags.New || !copyProgramme.Flags.Live || !copyProgramme.Flags.Premiere {
		t.Fatal("programme truncated", copyProgramme, err)
	}
	revised, err := freezeMGRecordingIntent(f.programme, mgRecordingOptions{120000, 240000, -9, 60, 10})
	if err != nil {
		t.Fatal(err)
	}
	updated, err := mgUpdateRecording(t.Context(), turn, o, got.recording, scope, revised)
	if err != nil {
		t.Fatal(err)
	}
	if updated.recording.capture.revision != 2 || updated.ownerRevision != 2 || updated.recording.programmeJSON != got.recording.programmeJSON || updated.recording.optionsJSON != revised.optionsJSON || updated.reservation.startMS != revised.startMS || updated.reservation.endMS != revised.endMS || updated.reservation.priority != -9 || updated.recording.capture.incarnation != n.incarnation || updated.recording.capture.lastSequence != 0 || updated.recording.capture.guidePin != n.guidePin {
		t.Fatal("update custody/copy drift", updated)
	}
	kept, err := mgKeepRecording(t.Context(), turn, o, updated.recording, true)
	if err != nil {
		t.Fatal(err)
	}
	if kept.recording.keep != 1 || kept.recording.capture.revision != 3 || kept.ownerRevision != 3 || kept.reservation != updated.reservation {
		t.Fatal("Keep mutated reservation", kept)
	}
	stale, err := mgKeepRecording(t.Context(), turn, o, updated.recording, false)
	mgRecordingZero(t, stale, err)
}
func TestMGRecordingIntentDuplicateAndManualAdoption(t *testing.T) {
	w, o, scope, f, birth := mgRecordingFixture(t)
	t.Run("existing occurrence does not replace namespace", func(t *testing.T) {
		turn := mgRecordingTestTurn(t, w)
		first, err := mgScheduleRecording(t.Context(), turn, o, scope, f, birth)
		if err != nil {
			t.Fatal(err)
		}
		var changes int64
		if err := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT total_changes()`).Scan(&changes); err != nil {
			t.Fatal(err)
		}
		another := birth
		another.incarnation[0]++
		again, err := mgScheduleRecording(t.Context(), turn, o, scope, f, another)
		if err != nil || again != first {
			t.Fatal("duplicate occurrence replaced immutable birth", again, err)
		}
		var after int64
		if err := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT total_changes()`).Scan(&after); err != nil || after != changes {
			t.Fatal("duplicate occurrence had effects", changes, after, err)
		}
	})
	t.Run("explicit adoption retains original options", func(t *testing.T) {
		turn := mgRecordingTestTurn(t, w)
		first, err := mgScheduleRecording(t.Context(), turn, o, scope, f, birth)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := turn.transaction.conn.ExecContext(t.Context(), `UPDATE dvr_recordings SET manual=0,rule_id='existing-rule',rule_revision=7 WHERE id=?`, first.recording.capture.id); err != nil {
			t.Fatal(err)
		}
		other, err := freezeMGRecordingIntent(f.programme, mgRecordingOptions{1, 2, 3, 4, 5})
		if err != nil {
			t.Fatal(err)
		}
		adopted, err := mgScheduleRecording(t.Context(), turn, o, scope, other, birth)
		if err != nil {
			t.Fatal(err)
		}
		if adopted.recording.manual != 1 || adopted.recording.capture.ruleID != "" || adopted.recording.capture.ruleRevision != 0 || adopted.recording.capture.revision != 2 || adopted.recording.optionsJSON != first.recording.optionsJSON || adopted.reservation != first.reservation || adopted.ownerRevision != 2 {
			t.Fatal("manual adoption replaced intent", adopted)
		}
	})
}
func TestMGRecordingIntentNativeFences(t *testing.T) {
	w, o, scope, f, birth := mgRecordingFixture(t)
	for _, name := range []string{"head", "source revision", "source removed", "source disabled", "channel", "programme metadata", "member incarnation", "profile lifetime", "account epoch", "member revision", "profile revision", "member disabled", "profile deleted", "copied programme"} {
		t.Run(name, func(t *testing.T) {
			turn := mgRecordingTestTurn(t, w)
			owner, expected, frozen := o, scope, f
			switch name {
			case "head":
				expected.head = strings.Repeat("9", 48)
			case "source revision":
				expected.revision++
			case "source removed":
				if _, err := turn.transaction.conn.ExecContext(t.Context(), `UPDATE live_source_identities SET has_live_source_removals=1,removal_request_id='removed',removal_revision=4,removal_removed_at='now'`); err != nil {
					t.Fatal(err)
				}
			case "source disabled":
				if _, err := turn.transaction.conn.ExecContext(t.Context(), `UPDATE live_sources SET state='disabled'`); err != nil {
					t.Fatal(err)
				}
			case "channel":
				expected.channel = strings.Repeat("9", 64)
			case "programme metadata":
				if _, err := turn.transaction.conn.ExecContext(t.Context(), `UPDATE live_programmes SET metadata_description='changed'`); err != nil {
					t.Fatal(err)
				}
			case "member incarnation":
				owner.memberIncarnation = strings.Repeat("c", 32)
			case "profile lifetime":
				owner.profileLifetime = strings.Repeat("c", 32)
			case "account epoch":
				owner.accountEpoch++
			case "member revision":
				owner.memberRevision++
			case "profile revision":
				owner.profileRevision++
			case "member disabled":
				if _, err := turn.transaction.conn.ExecContext(t.Context(), `UPDATE direct_memberships SET disabled=1`); err != nil {
					t.Fatal(err)
				}
			case "profile deleted":
				if _, err := turn.transaction.conn.ExecContext(t.Context(), `UPDATE direct_profiles SET deleted=1`); err != nil {
					t.Fatal(err)
				}
			case "copied programme":
				frozen.programmeJSON = strings.Replace(frozen.programmeJSON, "Native full programme", "Fabricated title", 1)
			}
			out, err := mgScheduleRecording(t.Context(), turn, owner, expected, frozen, birth)
			mgRecordingZero(t, out, err)
			var n int64
			if err := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT count(*) FROM dvr_recordings`).Scan(&n); err != nil || n != 0 {
				t.Fatal("rejected schedule wrote recording", n, err)
			}
		})
	}
}
func TestMGRecordingIntentCaptureDebtPreserved(t *testing.T) {
	w, o, scope, f, birth := mgRecordingFixture(t)
	turn := mgRecordingTestTurn(t, w)
	first, err := mgScheduleRecording(t.Context(), turn, o, scope, f, birth)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := turn.transaction.conn.ExecContext(t.Context(), `UPDATE dvr_recordings SET state='recording',claim_generation=1,claim_token='capture-token',owner_instance='capture-owner',lease_until_ms=?,allocation_id='physical-allocation',artifact_id='capture-artifact',capture_launch_id=zeroblob(16),capture_allocation_generation=9,capture_allocation_token='allocation-token',capture_source_revision=3,capture_source_fence='source-fence' WHERE id=?`, first.recording.capture.updatedMS-1, first.recording.capture.id); err != nil {
		t.Fatal(err)
	}
	if _, err := turn.transaction.conn.ExecContext(t.Context(), `INSERT INTO live_allocations(id,source_id,channel_id,resource_id,kind,authority,account_id,profile_id,token,generation,state,lease_until_ms,created_ms) VALUES('physical-allocation',?,?,?,'recording','local',?,?,'allocation-token',9,'active',?,?)`, scope.source, scope.channel, first.recording.capture.id, o.account, o.profile, first.recording.capture.updatedMS-1, first.recording.capture.updatedMS); err != nil {
		t.Fatal(err)
	}
	before, found, err := mgReadRecordingIntent(t.Context(), turn, first.recording.capture.id)
	if err != nil || !found {
		t.Fatal(before, found, err)
	}
	out, err := mgUpdateRecording(t.Context(), turn, o, before, scope, f)
	mgRecordingZero(t, out, err)
	// Keep stays available after source removal, without renewing or freeing any
	// active/expired physical debt and without touching worker command sequence.
	if _, err := turn.transaction.conn.ExecContext(t.Context(), `DELETE FROM live_sources`); err != nil {
		t.Fatal(err)
	}
	kept, err := mgKeepRecording(t.Context(), turn, o, before, true)
	if err != nil {
		t.Fatal(err)
	}
	want := before
	want.keep = 1
	want.capture.revision++
	want.capture.updatedMS = kept.recording.capture.updatedMS
	if kept.recording != want || kept.reservation != first.reservation || kept.recording.capture.guidePin != before.capture.guidePin {
		t.Fatal("Keep released capture debt", kept)
	}
	var state string
	var lease int64
	if err := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT state,lease_until_ms FROM live_allocations WHERE id='physical-allocation'`).Scan(&state, &lease); err != nil || state != "active" || lease != before.capture.leaseMS {
		t.Fatal("Keep freed allocation", state, lease, err)
	}

}
func TestMGRecordingIntentStatementRollback(t *testing.T) {
	w, o, scope, f, birth := mgRecordingFixture(t)
	for _, name := range []string{"reservation", "owner revision"} {
		t.Run(name, func(t *testing.T) {
			defs := mgRecordingIntentDefinitions()
			for i := range defs {
				if name == "reservation" && defs[i].ID == mgRecordingIntentReserveInsert {
					defs[i].SQL = `INSERT INTO live_reservations(id,source_id,start_ms,end_ms,priority,enabled) VALUES(?,?,?,?,?,?+1)`
				}
				if name == "owner revision" && defs[i].ID == mgRecordingIntentOwnerRevisionInsert {
					defs[i].SQL = `INSERT INTO dvr_owner_revisions(owner_key,revision) VALUES(?,?*0+NULL)`
				}
			}
			turn := mgRecordingTestTurn(t, w, defs)
			out, err := mgScheduleRecording(t.Context(), turn, o, scope, f, birth)
			mgRecordingZero(t, out, err)
			if _, err := turn.transaction.rollback(nil); err != nil {
				t.Fatal(err)
			}
			var recordings, reservations, owners, dependencies int64
			if err := w.db.QueryRowContext(t.Context(), `SELECT (SELECT count(*) FROM dvr_recordings),(SELECT count(*) FROM live_reservations),(SELECT count(*) FROM dvr_owner_revisions),(SELECT count(*) FROM live_source_dependencies)`).Scan(&recordings, &reservations, &owners, &dependencies); err != nil || recordings != 0 || reservations != 0 || owners != 0 || dependencies != 0 {
				t.Fatal("partial intent escaped rollback", recordings, reservations, owners, dependencies, err)
			}
		})
	}
}
func TestMGRecordingIntentCancelledLifetimeAndValues(t *testing.T) {
	w, o, scope, f, birth := mgRecordingFixture(t)
	turn := mgRecordingTestTurn(t, w)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	out, err := mgScheduleRecording(ctx, turn, o, scope, f, birth)
	mgRecordingZero(t, out, err)
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	bad := f.options
	bad.RetentionDays = 3651
	if _, err := freezeMGRecordingIntent(f.programme, bad); err == nil {
		t.Fatal("invalid options accepted")
	}
	p := f.programme
	p.facts.text = `{"arbitraryAuthority":true}`
	if _, err := freezeMGRecordingIntent(p, f.options); err == nil {
		t.Fatal("unknown JSON field treated as programme facts")
	}
	descriptors, err := buildCommandDescriptors(commandRegistration{owner: "MG", descriptors: mgRecordingIntentDescriptors()})
	if err != nil || len(descriptors) != 3 {
		t.Fatal(descriptors, err)
	}
}

func TestMGRecordingIntentRetirementAndHistoricalPin(t *testing.T) {
	w, o, scope, f, birth := mgRecordingFixture(t)
	t.Run("retained floor prevents namespace rebirth", func(t *testing.T) {
		turn := mgRecordingTestTurn(t, w)
		id := mgRecordingID(o, scope.source, scope.programme)
		if _, err := turn.transaction.conn.ExecContext(t.Context(), `INSERT INTO dvr_capture_retirements(recording_id,command_incarnation,create_operation_id,birth_request_digest,source_id,final_sequence,final_result_digest,retired_ms,owner_key,authority,account_id,profile_id,member_incarnation,profile_lifetime) VALUES(?,?,?,?,?,1,?,0,?,'local',?,?,?,?)`, id, birth.incarnation[:], birth.operation[:], birth.digest[:], scope.source, birth.digest[:], o.key(), o.account, o.profile, o.memberIncarnation, o.profileLifetime); err != nil {
			t.Fatal(err)
		}
		changedBirth := birth
		changedBirth.incarnation[0]++
		out, err := mgScheduleRecording(t.Context(), turn, o, scope, f, changedBirth)
		mgRecordingZero(t, out, err)
	})
	t.Run("option edit retains historical pin after head refresh", func(t *testing.T) {
		turn := mgRecordingTestTurn(t, w)
		first, err := mgScheduleRecording(t.Context(), turn, o, scope, f, birth)
		if err != nil {
			t.Fatal(err)
		}
		head := strings.Repeat("5", 48)
		if _, err := turn.transaction.conn.ExecContext(t.Context(), `INSERT INTO live_generations(id,source_id,name,tuner_count,channel_count,programme_count,created_at) VALUES(?,?,'New head',2,0,0,'2026-11-07T00:00:00Z')`, head, scope.source); err != nil {
			t.Fatal(err)
		}
		if _, err := turn.transaction.conn.ExecContext(t.Context(), `UPDATE live_sources SET active_generation=? WHERE id=?`, head, scope.source); err != nil {
			t.Fatal(err)
		}
		expected := scope
		expected.head = head
		revised, err := freezeMGRecordingIntent(f.programme, mgRecordingOptions{50000, 200000, 7, 14, 3})
		if err != nil {
			t.Fatal(err)
		}
		changed, err := mgUpdateRecording(t.Context(), turn, o, first.recording, expected, revised)
		if err != nil {
			t.Fatal(err)
		}
		if changed.recording.capture.guideGeneration != scope.generation || changed.recording.capture.guidePin != first.recording.capture.guidePin || changed.recording.programmeJSON != first.recording.programmeJSON {
			t.Fatal("head refresh released historical copy/pin", changed)
		}
	})
}
