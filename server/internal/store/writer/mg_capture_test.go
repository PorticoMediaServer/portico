package writer

import (
	"errors"
	"testing"
)

// The three accepting commands compose claim -> renewal -> checkpoint on one
// occurrence. Each fresh command persists one native custody/ progress revision
// and its original scalar; a retry replays that scalar with zero effects and an
// ACKed original is a complete zero result. No checkpoint JSON, writer
// retirement, physical exit, artifact or byte fact is minted here.
func TestMGCaptureClaimRenewCheckpointAcceptReplayAndRetired(t *testing.T) {
	w, _, v := mgCaptureFixture(t)
	fake := mgCaptureFake(v)

	// Claim.
	claim := v.claimRequest()
	claimReference := mgCaptureClaimReference(t, mgPersonalIdentity(2, 2), claim)
	turn := mgCaptureTurn(t, w, MGCaptureClaim)
	fresh, err := acceptMGCaptureClaim(t.Context(), turn, fake, v.proof, t.Context(), claimReference, claim)
	want := v.claimResult()
	if err != nil || fresh.decision != receiptFresh || fresh.result != want || fresh.digest == (ResultDigest{}) {
		t.Fatal(fresh, err)
	}
	mgPersonalCommit(t, turn)

	var state, token, instance, allocation, artifact, sourceFence, checkpointJSON string
	var claimGeneration, lease, allocationGeneration int64
	var launch, exitLaunch []byte
	var allocToken string
	var sourceRevision, revision, last, writerRetired, physicalExit, sharedReady, capturedStart, capturedEnd, gaps, producer, boundary int64
	if err := w.db.QueryRowContext(t.Context(), `SELECT state,claim_token,owner_instance,lease_until_ms,allocation_id,artifact_id,capture_source_fence,capture_allocation_token,capture_launch_id,capture_exit_launch_id,claim_generation,capture_allocation_generation,capture_source_revision,revision,last_command_sequence,capture_writer_retired_sequence,capture_physical_exit_sequence,capture_shared_ready_sequence,captured_start_ms,captured_end_ms,capture_gap_count,capture_producer_succeeded,capture_boundary_reached,checkpoint_json FROM dvr_recordings WHERE id=?`, want.id).Scan(
		&state, &token, &instance, &lease, &allocation, &artifact, &sourceFence, &allocToken, &launch, &exitLaunch,
		&claimGeneration, &allocationGeneration, &sourceRevision, &revision, &last, &writerRetired, &physicalExit,
		&sharedReady, &capturedStart, &capturedEnd, &gaps, &producer, &boundary, &checkpointJSON); err != nil {
		t.Fatal(err)
	}
	if state != "preparing" || token != v.token || instance != v.instance || lease != claim.leaseMS ||
		allocation != v.allocation.id || artifact != v.artifact || sourceFence != v.fence ||
		allocToken != v.allocation.token || string(launch) != string(v.launch[:]) || exitLaunch != nil ||
		claimGeneration != 1 || allocationGeneration != 1 || sourceRevision != v.scope.revision ||
		revision != 2 || last != 1 || writerRetired != 0 || physicalExit != 0 || sharedReady != 0 ||
		capturedStart != 0 || capturedEnd != 0 || gaps != 0 || producer != 0 || boundary != 0 ||
		checkpointJSON != "{}" {
		t.Fatal("claim did not record exactly the native custody tuple", state, token, instance, lease, allocation, artifact,
			sourceFence, launch, exitLaunch, claimGeneration, allocationGeneration, sourceRevision, revision, last,
			writerRetired, physicalExit, sharedReady, capturedStart, capturedEnd, gaps, producer, boundary, checkpointJSON)
	}

	// Identical retry replays the original saved scalar with zero effects.
	turn = mgCaptureTurn(t, w, MGCaptureClaim)
	replay, err := acceptMGCaptureClaim(t.Context(), turn, fake, v.proof, t.Context(), claimReference, claim)
	if err != nil || replay.decision != receiptReplay || replay.result != want || replay.digest != fresh.digest {
		t.Fatal(replay, err)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}

	// A tampered retained payload is never decoded as the original result.
	var payload []byte
	if err := w.db.QueryRowContext(t.Context(), `SELECT result_payload FROM user_mutation_command_receipts WHERE root_id=? AND command_sequence=2`, v.root).Scan(&payload); err != nil {
		t.Fatal(err)
	}
	if _, err := w.db.ExecContext(t.Context(), `UPDATE user_mutation_command_receipts SET result_payload=substr(result_payload,1,length(result_payload)-1) WHERE root_id=? AND command_sequence=2`, v.root); err != nil {
		t.Fatal(err)
	}
	turn = mgCaptureTurn(t, w, MGCaptureClaim)
	if got, err := acceptMGCaptureClaim(t.Context(), turn, fake, v.proof, t.Context(), claimReference, claim); !errors.Is(err, ErrResolveRequired) || got != (mgCaptureAcceptance{}) {
		t.Fatal("tampered payload replayed", got, err)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}
	if _, err := w.db.ExecContext(t.Context(), `UPDATE user_mutation_command_receipts SET result_payload=? WHERE root_id=? AND command_sequence=2`, payload, v.root); err != nil {
		t.Fatal(err)
	}

	// ACK retires the original: a complete zero result and no new effect.
	ackMGRecording(t, w, v.root, 2)
	turn = mgCaptureTurn(t, w, MGCaptureClaim)
	retired, err := acceptMGCaptureClaim(t.Context(), turn, fake, v.proof, t.Context(), claimReference, claim)
	if err != nil || retired.decision != receiptRetired || retired.result != (mgCaptureResult{}) || retired.digest != (ResultDigest{}) {
		t.Fatal(retired, err)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}

	// Renewal extends only the logical lease.
	ackMGCaptureRow(t, w, want.id, 1)
	renew := v.renewRequest()
	renewReference := mgCaptureRenewReference(t, mgPersonalIdentity(3, 3), renew)
	turn = mgCaptureTurn(t, w, MGRenewCapture)
	renewed, err := acceptMGCaptureRenew(t.Context(), turn, fake, v.proof, t.Context(), renewReference, renew)
	wantRenew := v.renewResult()
	if err != nil || renewed.decision != receiptFresh || renewed.result != wantRenew {
		t.Fatal(renewed, err)
	}
	mgPersonalCommit(t, turn)
	if err := w.db.QueryRowContext(t.Context(), `SELECT lease_until_ms,revision,last_command_sequence FROM dvr_recordings WHERE id=?`, want.id).Scan(&lease, &revision, &last); err != nil {
		t.Fatal(err)
	}
	if lease != renew.leaseMS || revision != 3 || last != 2 {
		t.Fatal("renewal changed more than the logical lease", lease, revision, last)
	}
	ackMGRecording(t, w, v.root, 3)

	// Checkpoint records native progress only.
	ackMGCaptureRow(t, w, want.id, 2)
	checkpoint := v.checkpointRequest()
	checkpointReference := mgCaptureCheckpointReference(t, mgPersonalIdentity(4, 4), checkpoint)
	turn = mgCaptureTurn(t, w, MGCheckpointCapture)
	checked, err := acceptMGCaptureCheckpoint(t.Context(), turn, fake, v.proof, t.Context(), checkpointReference, checkpoint)
	wantCheckpoint := v.checkpointResult(checkpoint)
	if err != nil || checked.decision != receiptFresh || checked.result != wantCheckpoint {
		t.Fatal(checked, err)
	}
	mgPersonalCommit(t, turn)
	if err := w.db.QueryRowContext(t.Context(), `SELECT captured_start_ms,captured_end_ms,capture_gap_count,capture_producer_succeeded,capture_boundary_reached,revision,last_command_sequence,capture_writer_retired_sequence,capture_physical_exit_sequence,capture_exit_launch_id,capture_shared_ready_sequence,checkpoint_json FROM dvr_recordings WHERE id=?`, want.id).Scan(
		&capturedStart, &capturedEnd, &gaps, &producer, &boundary, &revision, &last, &writerRetired, &physicalExit, &exitLaunch, &sharedReady, &checkpointJSON); err != nil {
		t.Fatal(err)
	}
	if capturedStart != checkpoint.capturedStartMS || capturedEnd != checkpoint.capturedEndMS || gaps != checkpoint.gapCount ||
		producer != 1 || boundary != 0 || revision != 4 || last != 3 || writerRetired != 0 || physicalExit != 0 ||
		exitLaunch != nil || sharedReady != 0 || checkpointJSON != "{}" {
		t.Fatal("checkpoint minted physical evidence or JSON authority", capturedStart, capturedEnd, gaps, producer, boundary,
			revision, last, writerRetired, physicalExit, exitLaunch, sharedReady, checkpointJSON)
	}
	if receipts, sequence := mgCaptureCounts(t, w, v.root); receipts != 4 || sequence != 4 {
		t.Fatal(receipts, sequence)
	}

	// Replay of the checkpoint returns the same native progress scalar.
	ackMGCaptureRow(t, w, want.id, 3)
	turn = mgCaptureTurn(t, w, MGCheckpointCapture)
	replay, err = acceptMGCaptureCheckpoint(t.Context(), turn, fake, v.proof, t.Context(), checkpointReference, checkpoint)
	if err != nil || replay.decision != receiptReplay || replay.result != wantCheckpoint || replay.digest != checked.digest {
		t.Fatal(replay, err)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}
}
