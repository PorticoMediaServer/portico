package writer

import (
	"errors"
	"testing"
	"time"
)

// A claim is refused by every stale or forged native fact: expected revision,
// source revision/state/head/removal, window/backoff, allocation custody, launch
// uniqueness and owner tuple. None of them leaves a row effect.
func TestMGCaptureClaimRefusals(t *testing.T) {
	w, _, v := mgCaptureFixture(t)
	fake := mgCaptureFake(v)

	want := ErrConflict
	checkNoClaim := func(label string) {
		t.Helper()
		var state string
		var generation, launch int64
		if err := w.db.QueryRowContext(t.Context(), `SELECT state,claim_generation,(SELECT count(*) FROM dvr_recordings WHERE capture_launch_id IS NOT NULL) FROM dvr_recordings WHERE id=?`, v.saved.result.id).Scan(&state, &generation, &launch); err != nil {
			t.Fatal(err)
		}
		if state != "scheduled" || generation != 0 || launch != 0 {
			t.Fatal(label, "left a capture effect", state, generation, launch)
		}
	}
	run := func(request mgCaptureClaimRequest, identity CommandIdentity) error {
		turn := mgCaptureTurn(t, w, MGCaptureClaim)
		out, err := acceptMGCaptureClaim(t.Context(), turn, fake, v.proof, t.Context(), mgCaptureClaimReference(t, identity, request), request)
		if !errors.Is(err, want) || out != (mgCaptureAcceptance{}) {
			return errors.Join(errors.New("unexpected claim result"), err)
		}
		if _, err := turn.transaction.rollback(nil); err != nil {
			return err
		}
		return nil
	}

	stale := v.claimRequest()
	stale.expected = 2
	if err := run(stale, mgPersonalIdentity(2, 2)); err != nil {
		t.Fatal("stale revision", err)
	}
	checkNoClaim("stale revision")

	if _, err := w.db.ExecContext(t.Context(), `UPDATE live_sources SET revision=8 WHERE id=?`, v.scope.source); err != nil {
		t.Fatal(err)
	}
	if err := run(v.claimRequest(), mgPersonalIdentity(2, 2)); err != nil {
		t.Fatal("stale source revision", err)
	}
	checkNoClaim("stale source revision")
	if _, err := w.db.ExecContext(t.Context(), `UPDATE live_sources SET revision=7 WHERE id=?`, v.scope.source); err != nil {
		t.Fatal(err)
	}

	if _, err := w.db.ExecContext(t.Context(), `UPDATE live_source_identities SET has_live_source_removals=1,removal_request_id='removal-1',removal_revision=1,removal_removed_at='2026-11-09T00:00:00Z' WHERE id=?`, v.scope.source); err != nil {
		t.Fatal(err)
	}
	if err := run(v.claimRequest(), mgPersonalIdentity(2, 2)); err != nil {
		t.Fatal("removed source", err)
	}
	checkNoClaim("removed source")
	if _, err := w.db.ExecContext(t.Context(), `UPDATE live_source_identities SET has_live_source_removals=0,removal_request_id=NULL,removal_revision=NULL,removal_removed_at=NULL WHERE id=?`, v.scope.source); err != nil {
		t.Fatal(err)
	}
	if _, err := w.db.ExecContext(t.Context(), `UPDATE live_sources SET state='disabled' WHERE id=?`, v.scope.source); err != nil {
		t.Fatal(err)
	}
	if err := run(v.claimRequest(), mgPersonalIdentity(2, 2)); err != nil {
		t.Fatal("disabled source", err)
	}
	checkNoClaim("disabled source")
	if _, err := w.db.ExecContext(t.Context(), `UPDATE live_sources SET state='active' WHERE id=?`, v.scope.source); err != nil {
		t.Fatal(err)
	}

	if _, err := w.db.ExecContext(t.Context(), `UPDATE live_sources SET active_generation=? WHERE id=?`, v.replacement, v.scope.source); err != nil {
		t.Fatal(err)
	}
	if err := run(v.claimRequest(), mgPersonalIdentity(2, 2)); err != nil {
		t.Fatal("replaced head", err)
	}
	checkNoClaim("replaced head")
	if _, err := w.db.ExecContext(t.Context(), `UPDATE live_sources SET active_generation=? WHERE id=?`, v.scope.generation, v.scope.source); err != nil {
		t.Fatal(err)
	}

	// A window that has not started or has already ended refuses.
	originalNow := w.now
	w.now = func() time.Time { return v.now.Add(23 * time.Hour) }
	if err := run(v.claimRequest(), mgPersonalIdentity(2, 2)); err != nil {
		t.Fatal("early window", err)
	}
	w.now = func() time.Time { return v.now.Add(26 * time.Hour) }
	if err := run(v.claimRequest(), mgPersonalIdentity(2, 2)); err != nil {
		t.Fatal("ended window", err)
	}
	w.now = originalNow

	// Backoff and allocation custody refuse before any write.
	if _, err := w.db.ExecContext(t.Context(), `UPDATE dvr_recordings SET next_attempt_ms=? WHERE id=?`, w.now().UnixMilli()+int64(time.Minute/time.Millisecond), v.saved.result.id); err != nil {
		t.Fatal(err)
	}
	if err := run(v.claimRequest(), mgPersonalIdentity(2, 2)); err != nil {
		t.Fatal("backoff", err)
	}
	checkNoClaim("backoff")
	if _, err := w.db.ExecContext(t.Context(), `UPDATE dvr_recordings SET next_attempt_ms=0 WHERE id=?`, v.saved.result.id); err != nil {
		t.Fatal(err)
	}
	wrongToken := v.claimRequest()
	wrongToken.allocation.token = "forged-allocation-token"
	if err := run(wrongToken, mgPersonalIdentity(2, 2)); err != nil {
		t.Fatal("forged allocation token", err)
	}
	checkNoClaim("forged allocation token")
	shortLease := v.claimRequest()
	shortLease.leaseMS = shortLease.allocation.leaseMS + int64(time.Hour/time.Millisecond)
	if err := run(shortLease, mgPersonalIdentity(2, 2)); err != nil {
		t.Fatal("allocation lease shorter than claim", err)
	}
	checkNoClaim("allocation lease")
	if _, err := w.db.ExecContext(t.Context(), `UPDATE live_allocations SET state='released' WHERE id=?`, v.allocation.id); err != nil {
		t.Fatal(err)
	}
	if err := run(v.claimRequest(), mgPersonalIdentity(2, 2)); err != nil {
		t.Fatal("released allocation", err)
	}
	checkNoClaim("released allocation")
	if _, err := w.db.ExecContext(t.Context(), `UPDATE live_allocations SET state='active' WHERE id=?`, v.allocation.id); err != nil {
		t.Fatal(err)
	}

	// The claimed owner tuple is bound to the current session: another profile is
	// denied and no row effect survives.
	foreign := v.claimRequest()
	foreign.owner.profile = "another-profile"
	turn := mgCaptureTurn(t, w, MGCaptureClaim)
	if got, err := acceptMGCaptureClaim(t.Context(), turn, fake, v.proof, t.Context(), mgCaptureClaimReference(t, mgPersonalIdentity(2, 2), foreign), foreign); !errors.Is(err, ErrDisclosureDenied) || got != (mgCaptureAcceptance{}) {
		t.Fatal("foreign owner claimed", got, err)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}
	checkNoClaim("foreign owner")

	// The first worker wins; a second claim at the same revision is stale.
	claimMGCapture(t, w, v, fake, mgPersonalIdentity(2, 2))
	second := v.claimRequest()
	turn = mgCaptureTurn(t, w, MGCaptureClaim)
	if got, err := acceptMGCaptureClaim(t.Context(), turn, fake, v.proof, t.Context(), mgCaptureClaimReference(t, mgPersonalIdentity(3, 3), second), second); !errors.Is(err, ErrConflict) || got != (mgCaptureAcceptance{}) {
		t.Fatal("double worker re-claimed the occurrence", got, err)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}

	// A different occurrence cannot reuse the same launch identity.
	otherRequest := mgRecordingScheduleRequest{owner: v.owner, scope: v.other, programme: v.otherProgramme, options: v.options()}
	turn = mgRecordingTurn(t, w, MGScheduleRecording)
	otherSaved, err := acceptMGRecordingSchedule(t.Context(), turn, fake, v.proof, t.Context(), mgRecordingScheduleReference(t, mgPersonalIdentity(3, 3), otherRequest), otherRequest)
	if err != nil || otherSaved.decision != receiptFresh {
		t.Fatal(otherSaved, err)
	}
	mgPersonalCommit(t, turn)
	ackMGRecording(t, w, v.root, 3)
	otherAllocation := mgCaptureAllocationFence{
		id: "allocation-2", token: "allocation-token-2", generation: 1, leaseMS: v.allocation.leaseMS,
	}
	if _, err := w.db.ExecContext(t.Context(), `INSERT INTO live_allocations(id,source_id,channel_id,resource_id,kind,authority,account_id,profile_id,token,generation,state,lease_until_ms,created_ms,preempt_at_ms) VALUES(?,?,?,?,'recording','local',?,?,?,1,'active',?,?,0)`,
		otherAllocation.id, v.other.source, v.other.channel, otherSaved.result.id, v.owner.account,
		v.owner.profile, otherAllocation.token, otherAllocation.leaseMS, w.now().UnixMilli()); err != nil {
		t.Fatal(err)
	}
	sharedLaunch := v.claimRequest()
	sharedLaunch.id = otherSaved.result.id
	sharedLaunch.allocation = otherAllocation
	turn = mgCaptureTurn(t, w, MGCaptureClaim)
	if got, err := acceptMGCaptureClaim(t.Context(), turn, fake, v.proof, t.Context(), mgCaptureClaimReference(t, mgPersonalIdentity(4, 4), sharedLaunch), sharedLaunch); !errors.Is(err, ErrConflict) || got != (mgCaptureAcceptance{}) {
		t.Fatal("shared launch claimed a second occurrence", got, err)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}
}

// Renewal and checkpoint refuse stale claim/cancel/allocation and regressing
// progress. Cancellation is a logical intent fact; it never releases the claim
// and a checkpoint can never mint writer-retired or physical-exit evidence.
func TestMGCaptureRenewCheckpointStaleAndProgress(t *testing.T) {
	w, _, v := mgCaptureFixture(t)
	fake := mgCaptureFake(v)
	claimMGCapture(t, w, v, fake, mgPersonalIdentity(2, 2))

	runRenew := func(request mgCaptureRenewRequest, identity CommandIdentity) error {
		turn := mgCaptureTurn(t, w, MGRenewCapture)
		out, err := acceptMGCaptureRenew(t.Context(), turn, fake, v.proof, t.Context(), mgCaptureRenewReference(t, identity, request), request)
		if !errors.Is(err, ErrConflict) || out != (mgCaptureAcceptance{}) {
			return errors.Join(errors.New("unexpected renewal result"), err)
		}
		if _, err := turn.transaction.rollback(nil); err != nil {
			return err
		}
		return nil
	}

	staleRenew := v.renewRequest()
	staleRenew.expected = 1
	if err := runRenew(staleRenew, mgPersonalIdentity(3, 3)); err != nil {
		t.Fatal("stale renewal revision", err)
	}
	badGeneration := v.renewRequest()
	badGeneration.claimGeneration = 2
	if err := runRenew(badGeneration, mgPersonalIdentity(3, 3)); err != nil {
		t.Fatal("stale claim generation", err)
	}
	badToken := v.renewRequest()
	badToken.token = "another-claim-token"
	if err := runRenew(badToken, mgPersonalIdentity(3, 3)); err != nil {
		t.Fatal("stale claim token", err)
	}
	badLaunch := v.renewRequest()
	badLaunch.launch = mgCaptureLaunch{0xff}
	if err := runRenew(badLaunch, mgPersonalIdentity(3, 3)); err != nil {
		t.Fatal("stale launch", err)
	}
	badAllocation := v.renewRequest()
	badAllocation.allocation.token = "another-allocation-token"
	if err := runRenew(badAllocation, mgPersonalIdentity(3, 3)); err != nil {
		t.Fatal("stale allocation", err)
	}
	shortRenew := v.renewRequest()
	shortRenew.leaseMS = v.claimLease()
	if err := runRenew(shortRenew, mgPersonalIdentity(3, 3)); err != nil {
		t.Fatal("non-extending renewal", err)
	}
	overAllocation := v.renewRequest()
	overAllocation.leaseMS = v.allocation.leaseMS + int64(time.Hour/time.Millisecond)
	overAllocation.allocation.leaseMS = overAllocation.leaseMS
	if err := runRenew(overAllocation, mgPersonalIdentity(3, 3)); err != nil {
		t.Fatal("renewal beyond allocation", err)
	}
	expired := v.renewRequest()
	if _, err := w.db.ExecContext(t.Context(), `UPDATE dvr_recordings SET lease_until_ms=? WHERE id=?`, w.now().UnixMilli()-1, v.saved.result.id); err != nil {
		t.Fatal(err)
	}
	if err := runRenew(expired, mgPersonalIdentity(3, 3)); err != nil {
		t.Fatal("expired lease renewed", err)
	}
	if _, err := w.db.ExecContext(t.Context(), `UPDATE dvr_recordings SET lease_until_ms=? WHERE id=?`, v.claimLease(), v.saved.result.id); err != nil {
		t.Fatal(err)
	}

	if _, err := w.db.ExecContext(t.Context(), `UPDATE live_sources SET revision=8 WHERE id=?`, v.scope.source); err != nil {
		t.Fatal(err)
	}
	if err := runRenew(v.renewRequest(), mgPersonalIdentity(3, 3)); err != nil {
		t.Fatal("stale source on renewal", err)
	}
	if _, err := w.db.ExecContext(t.Context(), `UPDATE live_sources SET revision=7 WHERE id=?`, v.scope.source); err != nil {
		t.Fatal(err)
	}

	renewMGCapture(t, w, v, fake, mgPersonalIdentity(3, 3))

	runCheckpoint := func(request mgCaptureCheckpointRequest, identity CommandIdentity) error {
		turn := mgCaptureTurn(t, w, MGCheckpointCapture)
		out, err := acceptMGCaptureCheckpoint(t.Context(), turn, fake, v.proof, t.Context(), mgCaptureCheckpointReference(t, identity, request), request)
		if !errors.Is(err, ErrConflict) || out != (mgCaptureAcceptance{}) {
			return errors.Join(errors.New("unexpected checkpoint result"), err)
		}
		if _, err := turn.transaction.rollback(nil); err != nil {
			return err
		}
		return nil
	}
	staleCheckpoint := v.checkpointRequest()
	staleCheckpoint.expected = 2
	if err := runCheckpoint(staleCheckpoint, mgPersonalIdentity(4, 4)); err != nil {
		t.Fatal("stale checkpoint revision", err)
	}
	badCheckpointClaim := v.checkpointRequest()
	badCheckpointClaim.claimGeneration = 2
	if err := runCheckpoint(badCheckpointClaim, mgPersonalIdentity(4, 4)); err != nil {
		t.Fatal("stale checkpoint claim", err)
	}
	badCheckpointAllocation := v.checkpointRequest()
	badCheckpointAllocation.allocation.id = "another-allocation"
	if err := runCheckpoint(badCheckpointAllocation, mgPersonalIdentity(4, 4)); err != nil {
		t.Fatal("stale checkpoint allocation", err)
	}

	checkpoint := v.checkpointRequest()
	checkpoint.boundaryReached = true
	turn := mgCaptureTurn(t, w, MGCheckpointCapture)
	saved, err := acceptMGCaptureCheckpoint(t.Context(), turn, fake, v.proof, t.Context(), mgCaptureCheckpointReference(t, mgPersonalIdentity(4, 4), checkpoint), checkpoint)
	if err != nil || saved.decision != receiptFresh || saved.result.boundaryReached != 1 {
		t.Fatal(saved, err)
	}
	mgPersonalCommit(t, turn)
	ackMGCaptureRow(t, w, v.saved.result.id, 3)
	ackMGRecording(t, w, v.root, 4)
	afterCheckpoint := v.checkpointRequest()
	afterCheckpoint.expected, afterCheckpoint.claimGeneration = 4, 1
	afterCheckpoint.capturedStartMS++
	if err := runCheckpoint(afterCheckpoint, mgPersonalIdentity(5, 5)); err != nil {
		t.Fatal("changed first captured instant", err)
	}
	regressEnd := v.checkpointRequest()
	regressEnd.expected = 4
	regressEnd.capturedEndMS--
	if err := runCheckpoint(regressEnd, mgPersonalIdentity(5, 5)); err != nil {
		t.Fatal("regressed captured end", err)
	}
	regressGaps := v.checkpointRequest()
	regressGaps.expected = 4
	regressGaps.gapCount = 0
	if err := runCheckpoint(regressGaps, mgPersonalIdentity(5, 5)); err != nil {
		t.Fatal("regressed gap count", err)
	}
	clearedProducer := v.checkpointRequest()
	clearedProducer.expected = 4
	clearedProducer.producerSucceeded = false
	if err := runCheckpoint(clearedProducer, mgPersonalIdentity(5, 5)); err != nil {
		t.Fatal("cleared producer success", err)
	}
	clearedBoundary := v.checkpointRequest()
	clearedBoundary.expected = 4
	clearedBoundary.boundaryReached = false
	if err := runCheckpoint(clearedBoundary, mgPersonalIdentity(5, 5)); err != nil {
		t.Fatal("cleared boundary", err)
	}

	// Physical exit evidence is not a capture-command fact: a checkpointed row
	// still has none, and a row that has one refuses further checkpoints.
	var writerRetired, physicalExit int64
	var exitLaunch []byte
	if err := w.db.QueryRowContext(t.Context(), `SELECT capture_writer_retired_sequence,capture_physical_exit_sequence,capture_exit_launch_id FROM dvr_recordings WHERE id=?`, v.saved.result.id).Scan(&writerRetired, &physicalExit, &exitLaunch); err != nil {
		t.Fatal(err)
	}
	if writerRetired != 0 || physicalExit != 0 || exitLaunch != nil {
		t.Fatal("checkpoint minted physical exit", writerRetired, physicalExit, exitLaunch)
	}
	ackMGRecording(t, w, v.root, 4)
	if _, err := w.db.ExecContext(t.Context(), `UPDATE dvr_recordings SET capture_writer_retired_sequence=3,capture_physical_exit_sequence=3,capture_exit_launch_id=capture_launch_id,capture_exit_claim_generation=claim_generation WHERE id=?`, v.saved.result.id); err != nil {
		t.Fatal(err)
	}
	exited := v.checkpointRequest()
	exited.expected = 4
	if err := runCheckpoint(exited, mgPersonalIdentity(5, 5)); err != nil {
		t.Fatal("checkpoint after physical exit", err)
	}

	// Cancellation advances the cancel generation and refuses renewal and
	// checkpoint; the claim tuple survives untouched.
	if _, err := w.db.ExecContext(t.Context(), `UPDATE dvr_recordings SET state='cancelled',reason='owner-cancelled',cancel_generation=cancel_generation+1,revision=revision+1 WHERE id=?`, v.saved.result.id); err != nil {
		t.Fatal(err)
	}
	cancelledRenew := v.renewRequest()
	cancelledRenew.expected = 5
	if err := runRenew(cancelledRenew, mgPersonalIdentity(6, 6)); err != nil {
		t.Fatal("cancelled occurrence renewed", err)
	}
	cancelledCheckpoint := v.checkpointRequest()
	cancelledCheckpoint.expected = 5
	if err := runCheckpoint(cancelledCheckpoint, mgPersonalIdentity(6, 6)); err != nil {
		t.Fatal("cancelled occurrence checkpointed", err)
	}
	var cancelGeneration int64
	var claimToken, allocation string
	if err := w.db.QueryRowContext(t.Context(), `SELECT cancel_generation,cancel_generation>0,claim_token,allocation_id FROM dvr_recordings WHERE id=?`, v.saved.result.id).Scan(&cancelGeneration, &cancelGeneration, &claimToken, &allocation); err != nil {
		t.Fatal(err)
	}
	if claimToken != v.token || allocation != v.allocation.id {
		t.Fatal("cancellation released claim custody", claimToken, allocation)
	}
}

// A guide replacement is not a source revocation: an in-progress capture may
// renew its lease while the pinned occurrence is unchanged. The claim itself
// still requires the pinned generation to be the current head.
func TestMGCaptureRenewAcrossGuideHeadReplacement(t *testing.T) {
	w, _, v := mgCaptureFixture(t)
	fake := mgCaptureFake(v)
	claimMGCapture(t, w, v, fake, mgPersonalIdentity(2, 2))
	if _, err := w.db.ExecContext(t.Context(), `UPDATE live_sources SET active_generation=? WHERE id=?`, v.replacement, v.scope.source); err != nil {
		t.Fatal(err)
	}
	request := v.renewRequest()
	request.leaseMS = v.claimLease() + int64(15*time.Minute/time.Millisecond)
	turn := mgCaptureTurn(t, w, MGRenewCapture)
	out, err := acceptMGCaptureRenew(t.Context(), turn, fake, v.proof, t.Context(), mgCaptureRenewReference(t, mgPersonalIdentity(3, 3), request), request)
	if err != nil || out.decision != receiptFresh || out.result.leaseMS != request.leaseMS {
		t.Fatal("renewal across a guide replacement", out, err)
	}
	mgPersonalCommit(t, turn)
}
