package writer

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/stmttest"
)

// Synthetic native facts only: no authority, receipt, physical launch/exit,
// allocation admission or production resource qualification.
func mgCaptureFixture(t *testing.T) (*executor, int64) {
	t.Helper()
	w, _, clock := testExecutor(t)
	now := clock.UnixMilli()
	mgCaptureDraftFixture(t, w)
	if _, err := w.db.ExecContext(t.Context(), `INSERT INTO live_source_identities(id) VALUES('capture-source')`); err != nil {
		t.Fatal(err)
	}
	if _, err := w.db.ExecContext(t.Context(), `INSERT INTO live_generations(id,source_id,name,tuner_count,channel_count,programme_count,created_at) VALUES('guide','capture-source','Capture',2,1,1,'2026-11-07T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if _, err := w.db.ExecContext(t.Context(), `INSERT INTO live_sources(id,name,revision,state,tuner_count,active_generation,published_at) VALUES('capture-source','Capture',3,'active',2,'guide','2026-11-07T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"leader", "fresh", "follower"} {
		if _, err := w.db.ExecContext(t.Context(), `INSERT INTO dvr_recordings(id,owner_key,authority,account_id,profile_id,source_id,channel_id,programme_id,guide_generation,programme_json,options_json,start_ms,end_ms,priority,revision,state,created_ms,updated_ms,command_incarnation,create_operation_id,birth_request_digest,command_member_incarnation,command_profile_lifetime,guide_pin_generation) VALUES(?,?,'local','account',?,'capture-source','channel','programme','guide','{}','{}',?,?,1,5,'scheduled',?,?,x'01010101010101010101010101010101',x'02020202020202020202020202020202',x'0303030303030303030303030303030303030303030303030303030303030303','11111111111111111111111111111111','22222222222222222222222222222222','guide')`, id, "owner:"+id, id, now-1000, now+600000, now, now); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := w.db.ExecContext(t.Context(), `UPDATE dvr_recordings SET state='recording',claim_token='leader-token',claim_generation=7,cancel_generation=2,owner_instance='instance',lease_until_ms=?,allocation_id='leader-allocation',artifact_id='leader-artifact',capture_launch_id=x'04040404040404040404040404040404',capture_allocation_generation=9,capture_allocation_token='leader-allocation-token',capture_source_revision=3,capture_source_fence='source-fence',last_command_sequence=1,acknowledged_command_sequence=1 WHERE id='leader'`, now+30000); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"leader", "fresh"} {
		if _, err := w.db.ExecContext(t.Context(), `INSERT INTO live_allocations(id,source_id,channel_id,resource_id,kind,authority,account_id,profile_id,token,generation,state,lease_until_ms,created_ms) VALUES(?,'capture-source','channel',?,'recording','local','account',?,?,9,'active',?,?)`, id+"-allocation", id, id, id+"-allocation-token", now+120000, now); err != nil {
			t.Fatal(err)
		}
	}
	return w, now
}
func mgCaptureTestTurn(t *testing.T, w *executor, defs []stmt.Definition) *commandTurn {
	t.Helper()
	registry, err := stmttest.New(t, defs)
	if err != nil {
		t.Fatal(err)
	}
	recipe := testRecipe()
	recipe.limit = Work{1000, 100000, 1000, 1000000, 20 * time.Millisecond}
	tx, err := w.begin(t.Context(), InventoryAccept, recipe, &testCustody{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = tx.rollback(nil) })
	var steps []statementStep
	for _, d := range defs {
		steps = append(steps, statementStep{d.ID, Work{1, 100, 1, 100, time.Microsecond}})
	}
	turn, err := newCommandTurn(tx, registry, steps)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = turn.closeRows() })
	return turn
}
func mgCaptureSnapshotFor(t *testing.T, turn *commandTurn, id string) mgCaptureSnapshot {
	t.Helper()
	s, err := mgReadCapture(t.Context(), turn, id)
	if err != nil {
		t.Fatal(err)
	}
	return s
}
func mgCaptureAllocation(id string, now int64) mgCaptureAllocationFence {
	return mgCaptureAllocationFence{id + "-allocation", id + "-allocation-token", 9, now + 120000}
}
func mgCaptureZero(t *testing.T, out mgCaptureFacts, err error) {
	t.Helper()
	if err == nil || out != (mgCaptureFacts{}) {
		t.Fatal("failure leaked output", out, err)
	}
}
func TestMGCaptureNativeEffects(t *testing.T) {
	w, now := mgCaptureFixture(t)
	t.Run("claim renew cancel retains custody", func(t *testing.T) {
		turn := mgCaptureTestTurn(t, w, mgCaptureDefinitions())
		fresh := mgCaptureSnapshotFor(t, turn, "fresh")
		a := mgCaptureAllocation("fresh", now)
		got, err := mgClaimCapture(t.Context(), turn, fresh, mgCaptureTestCommand(fresh, mgCaptureKindClaim), mgCaptureClaimInput{"fresh-token", "instance", "fresh-artifact", a, now + 30000, mgCaptureID{9}, 3, "source-fence"})
		if err != nil {
			t.Fatal(err)
		}
		if got.recording.claimGeneration != 1 || got.recording.state != "preparing" || got.recording.revision != 6 || got.retirement != mgCaptureRetirementPending {
			t.Fatal(got)
		}
		stale := got.recording
		got = mgCaptureTestAccept(t, turn, got)
		got, err = mgRenewCapture(t.Context(), turn, got.recording, mgCaptureTestCommand(got.recording, mgCaptureKindRenew), a, now+60000)
		if err != nil {
			t.Fatal(err)
		}
		if got.recording.revision != 7 || got.recording.leaseMS != now+60000 {
			t.Fatal(got)
		}
		got = mgCaptureTestAccept(t, turn, got)
		before := got.recording
		got, err = mgCancelCapture(t.Context(), turn, before, mgCaptureTestCommand(before, mgCaptureKindCancel), "owner-cancelled")
		if err != nil {
			t.Fatal(err)
		}
		want := before
		want.state, want.reason = "cancelled", "owner-cancelled"
		want.finishedMS = now
		want.lastSequence++
		want.cancelGeneration++
		want.revision++
		if got.recording != want || got.retirement != mgCaptureRetirementPending {
			t.Fatal("cancel released custody", got)
		}
		var state string
		var lease int64
		if err := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT state,lease_until_ms FROM live_allocations WHERE id=?`, a.id).Scan(&state, &lease); err != nil || state != "active" || lease != a.leaseMS {
			t.Fatal("cancel freed allocation", state, lease, err)
		}
		out, err := mgRenewCapture(t.Context(), turn, stale, mgCaptureTestCommand(stale, mgCaptureKindRenew), a, now+90000)
		mgCaptureZero(t, out, err)
		out, err = mgRenewCapture(t.Context(), turn, got.recording, mgCaptureTestCommand(got.recording, mgCaptureKindRenew), a, now+90000)
		mgCaptureZero(t, out, err)
	})
	t.Run("attach keeps waiting intent and cancel keeps link", func(t *testing.T) {
		turn := mgCaptureTestTurn(t, w, mgCaptureDefinitions())
		leader := mgCaptureSnapshotFor(t, turn, "leader")
		follower := mgCaptureSnapshotFor(t, turn, "follower")
		got, err := mgAttachCapture(t.Context(), turn, follower, leader, mgCaptureTestCommand(follower, mgCaptureKindAttach))
		if err != nil {
			t.Fatal(err)
		}
		if got.recording.state != "scheduled" || got.recording.itemID != 0 || got.recording.artifactID != "" || got.recording.hasShare != 1 || got.recording.leaderID.text != leader.id || got.recording.shareClaimGeneration != leader.claimGeneration || got.recording.shareLaunch != leader.launch || got.recording.revision != 6 || got.retirement != mgCaptureSharedHold {
			t.Fatal("attachment invented output", got)
		}
		out, err := mgAttachCapture(t.Context(), turn, follower, leader, mgCaptureTestCommand(follower, mgCaptureKindAttach))
		mgCaptureZero(t, out, err)
		got = mgCaptureTestAccept(t, turn, got)
		out, err = mgCancelCapture(t.Context(), turn, got.recording, mgCaptureTestCommand(got.recording, mgCaptureKindCancel), "owner-cancelled")
		if err != nil || out.recording.leaderID != got.recording.leaderID || out.recording.hasShare != 1 {
			t.Fatal("cancel dropped retained link", out, err)
		}
	})
}
func TestMGCaptureStaleFences(t *testing.T) {
	w, now := mgCaptureFixture(t)
	cases := []struct {
		name   string
		change func(*mgCaptureSnapshot)
	}{
		{"token", func(s *mgCaptureSnapshot) { s.token = "stale-token" }},
		{"claim", func(s *mgCaptureSnapshot) { s.claimGeneration++ }},
		{"cancel", func(s *mgCaptureSnapshot) { s.cancelGeneration++ }},
		{"instance", func(s *mgCaptureSnapshot) { s.ownerInstance = "stale-instance" }},
		{"lease", func(s *mgCaptureSnapshot) { s.leaseMS++ }},
		{"revision", func(s *mgCaptureSnapshot) { s.revision++ }},
		{"owner", func(s *mgCaptureSnapshot) { s.ownerKey = "other-owner" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			turn := mgCaptureTestTurn(t, w, mgCaptureDefinitions())
			original := mgCaptureSnapshotFor(t, turn, "leader")
			s := original
			tc.change(&s)
			out, err := mgRenewCapture(t.Context(), turn, s, mgCaptureTestCommand(s, mgCaptureKindRenew), mgCaptureAllocation("leader", now), now+60000)
			if !errors.Is(err, ErrConflict) {
				t.Fatal(err)
			}
			mgCaptureZero(t, out, err)
			out, err = mgCancelCapture(t.Context(), turn, s, mgCaptureTestCommand(s, mgCaptureKindCancel), "owner-cancelled")
			if !errors.Is(err, ErrConflict) {
				t.Fatal(err)
			}
			mgCaptureZero(t, out, err)
			if got := mgCaptureSnapshotFor(t, turn, "leader"); got != original {
				t.Fatal("stale mutation changed row", got)
			}
		})
	}
	t.Run("expired exact lease never reclaims", func(t *testing.T) {
		turn := mgCaptureTestTurn(t, w, mgCaptureDefinitions())
		if _, err := turn.transaction.conn.ExecContext(t.Context(), `UPDATE dvr_recordings SET lease_until_ms=? WHERE id='leader'`, now); err != nil {
			t.Fatal(err)
		}
		s := mgCaptureSnapshotFor(t, turn, "leader")
		out, err := mgRenewCapture(t.Context(), turn, s, mgCaptureTestCommand(s, mgCaptureKindRenew), mgCaptureAllocation("leader", now), now+60000)
		mgCaptureZero(t, out, err)
		out, err = mgClaimCapture(t.Context(), turn, s, mgCaptureTestCommand(s, mgCaptureKindClaim), mgCaptureClaimInput{"replacement", "other-instance", "replacement-artifact", mgCaptureAllocation("leader", now), now + 60000, mgCaptureID{9}, 3, "source-fence"})
		mgCaptureZero(t, out, err)
	})
}
func TestMGCaptureSharingGuards(t *testing.T) {
	w, now := mgCaptureFixture(t)
	for _, name := range []string{"self", "leader is follower", "follower has followers", "stale leader cancel", "different occurrence", "expired leader"} {
		t.Run(name, func(t *testing.T) {
			turn := mgCaptureTestTurn(t, w, mgCaptureDefinitions())
			switch name {
			case "leader is follower":
				_, err := turn.transaction.conn.ExecContext(t.Context(), `UPDATE dvr_recordings SET has_dvr_recording_shares=1,share_capture_recording_id='fresh',share_capture_claim_generation=7,share_capture_launch_id=zeroblob(16) WHERE id='leader'`)
				if err != nil {
					t.Fatal(err)
				}
			case "follower has followers":
				_, err := turn.transaction.conn.ExecContext(t.Context(), `UPDATE dvr_recordings SET has_dvr_recording_shares=1,share_capture_recording_id='follower',share_capture_claim_generation=7,share_capture_launch_id=zeroblob(16) WHERE id='fresh'`)
				if err != nil {
					t.Fatal(err)
				}
			case "expired leader":
				_, err := turn.transaction.conn.ExecContext(t.Context(), `UPDATE dvr_recordings SET lease_until_ms=? WHERE id='leader'`, now)
				if err != nil {
					t.Fatal(err)
				}
			}
			leader := mgCaptureSnapshotFor(t, turn, "leader")
			follower := mgCaptureSnapshotFor(t, turn, "follower")
			switch name {
			case "self":
				leader = follower
			case "stale leader cancel":
				leader.cancelGeneration++
			case "different occurrence":
				follower.channelID = "different"
			}
			out, err := mgAttachCapture(t.Context(), turn, follower, leader, mgCaptureTestCommand(follower, mgCaptureKindAttach))
			mgCaptureZero(t, out, err)
			if got := mgCaptureSnapshotFor(t, turn, "follower"); got.hasShare != 0 || got.revision != 5 {
				t.Fatal("invalid share changed follower", got)
			}
		})
	}
	t.Run("withdrawn leader needs concrete eligible follower", func(t *testing.T) {
		turn := mgCaptureTestTurn(t, w, mgCaptureDefinitions())
		leader := mgCaptureSnapshotFor(t, turn, "leader")
		follower := mgCaptureSnapshotFor(t, turn, "follower")
		attached, err := mgAttachCapture(t.Context(), turn, follower, leader, mgCaptureTestCommand(follower, mgCaptureKindAttach))
		if err != nil {
			t.Fatal(err)
		}
		attached = mgCaptureTestAccept(t, turn, attached)
		cancelled, err := mgCancelCapture(t.Context(), turn, leader, mgCaptureTestCommand(leader, mgCaptureKindCancel), "owner-cancelled")
		if err != nil {
			t.Fatal(err)
		}
		cancelled = mgCaptureTestAccept(t, turn, cancelled)
		renewed, err := mgRenewCaptureForFollower(t.Context(), turn, cancelled.recording, attached.recording, mgCaptureTestCommand(cancelled.recording, mgCaptureKindRenew), mgCaptureAllocation("leader", now), now+60000)
		if err != nil || renewed.recording.state != "cancelled" || renewed.recording.cancelGeneration != cancelled.recording.cancelGeneration {
			t.Fatal(renewed, err)
		}
		renewed = mgCaptureTestAccept(t, turn, renewed)
		ended, err := mgCancelCapture(t.Context(), turn, attached.recording, mgCaptureTestCommand(attached.recording, mgCaptureKindCancel), "permission-denied")
		if err != nil {
			t.Fatal(err)
		}
		out, err := mgRenewCaptureForFollower(t.Context(), turn, renewed.recording, attached.recording, mgCaptureTestCommand(renewed.recording, mgCaptureKindRenew), mgCaptureAllocation("leader", now), now+90000)
		mgCaptureZero(t, out, err)
		out, err = mgRenewCaptureForFollower(t.Context(), turn, renewed.recording, ended.recording, mgCaptureTestCommand(renewed.recording, mgCaptureKindRenew), mgCaptureAllocation("leader", now), now+90000)
		mgCaptureZero(t, out, err)
	})
}
func TestMGCaptureAllocationFences(t *testing.T) {
	w, now := mgCaptureFixture(t)
	for _, name := range []string{"token", "generation", "lease", "resource", "preempt"} {
		t.Run(name, func(t *testing.T) {
			turn := mgCaptureTestTurn(t, w, mgCaptureDefinitions())
			a := mgCaptureAllocation("leader", now)
			switch name {
			case "token":
				a.token = "stale"
			case "generation":
				a.generation++
			case "lease":
				a.leaseMS++
			case "resource":
				if _, err := turn.transaction.conn.ExecContext(t.Context(), `UPDATE live_allocations SET resource_id='wrong' WHERE id='leader-allocation'`); err != nil {
					t.Fatal(err)
				}
			case "preempt":
				if _, err := turn.transaction.conn.ExecContext(t.Context(), `UPDATE live_allocations SET preempt_at_ms=? WHERE id='leader-allocation'`, now+40000); err != nil {
					t.Fatal(err)
				}
			}
			original := mgCaptureSnapshotFor(t, turn, "leader")
			out, err := mgRenewCapture(t.Context(), turn, original, mgCaptureTestCommand(original, mgCaptureKindRenew), a, now+60000)
			mgCaptureZero(t, out, err)
			if got := mgCaptureSnapshotFor(t, turn, "leader"); got != original {
				t.Fatal("invalid allocation extended capture", got)
			}
		})
	}
}

func TestMGCaptureFailureAndRollback(t *testing.T) {
	w, now := mgCaptureFixture(t)
	t.Run("statement failure after claim rolls whole turn back", func(t *testing.T) {
		defs := mgCaptureDefinitions()
		for i := range defs {
			if defs[i].ID == mgCaptureCancel {
				defs[i].SQL = strings.Replace(defs[i].SQL, "SET state='cancelled'", "SET end_ms=start_ms,state='cancelled'", 1)
			}
		}
		turn := mgCaptureTestTurn(t, w, defs)
		s := mgCaptureSnapshotFor(t, turn, "fresh")
		claimed, err := mgClaimCapture(t.Context(), turn, s, mgCaptureTestCommand(s, mgCaptureKindClaim), mgCaptureClaimInput{"token", "instance", "artifact", mgCaptureAllocation("fresh", now), now + 30000, mgCaptureID{9}, 3, "source-fence"})
		if err != nil {
			t.Fatal(err)
		}
		claimed = mgCaptureTestAccept(t, turn, claimed)
		out, err := mgCancelCapture(t.Context(), turn, claimed.recording, mgCaptureTestCommand(claimed.recording, mgCaptureKindCancel), "owner-cancelled")
		mgCaptureZero(t, out, err)
		if _, err := turn.transaction.rollback(nil); err != nil {
			t.Fatal(err)
		}
		var state, token string
		var generation, revision int64
		if err := w.db.QueryRowContext(t.Context(), `SELECT state,claim_token,claim_generation,revision FROM dvr_recordings WHERE id='fresh'`).Scan(&state, &token, &generation, &revision); err != nil || state != "scheduled" || token != "" || generation != 0 || revision != 5 {
			t.Fatal("claim escaped rollback", state, token, generation, revision, err)
		}
	})
	t.Run("cancelled lifetime returns zero", func(t *testing.T) {
		turn := mgCaptureTestTurn(t, w, mgCaptureDefinitions())
		s := mgCaptureSnapshotFor(t, turn, "leader")
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		out, err := mgCancelCapture(ctx, turn, s, mgCaptureTestCommand(s, mgCaptureKindCancel), "owner-cancelled")
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		mgCaptureZero(t, out, err)
	})
	t.Run("readback drift returns zero", func(t *testing.T) {
		defs := mgCaptureDefinitions()
		for i := range defs {
			if defs[i].ID == mgCaptureCancel {
				defs[i].SQL = strings.Replace(defs[i].SQL, "revision=revision+1", "revision=revision+2", 1)
			}
		}
		turn := mgCaptureTestTurn(t, w, defs)
		s := mgCaptureSnapshotFor(t, turn, "leader")
		out, err := mgCancelCapture(t.Context(), turn, s, mgCaptureTestCommand(s, mgCaptureKindCancel), "owner-cancelled")
		mgCaptureZero(t, out, err)
	})
}
func TestMGCaptureDescriptors(t *testing.T) {
	got := mgCaptureDescriptors()
	table, err := buildCommandDescriptors(commandRegistration{owner: "MG", descriptors: got})
	if err != nil || len(table) != 3 || got[0].ID != 830 || got[1].ID != 831 || got[2].ID != 835 {
		t.Fatal(table, err)
	}
}

func mgCaptureTestCommand(s mgCaptureSnapshot, kind mgCaptureKind) mgCaptureCommand {
	sequence := s.lastSequence + 1
	return mgCaptureCommand{s.id, s.incarnation, sequence, mgCaptureID{byte(sequence), byte(kind[0])}, mgCaptureDigest{byte(sequence), byte(kind[0])}, kind}
}

// Test-only stand-in for the parent's receipt/ACK sequence; no runtime ACK API.
func mgCaptureTestAccept(t *testing.T, turn *commandTurn, f mgCaptureFacts) mgCaptureFacts {
	t.Helper()
	if _, err := mgSaveCaptureReceipt(t.Context(), turn, f); err != nil {
		t.Fatal(err)
	}
	if _, err := turn.transaction.conn.ExecContext(t.Context(), `UPDATE dvr_recordings SET acknowledged_command_sequence=last_command_sequence WHERE id=?`, f.recording.id); err != nil {
		t.Fatal(err)
	}
	f.recording = mgCaptureSnapshotFor(t, turn, f.recording.id)
	return f
}
