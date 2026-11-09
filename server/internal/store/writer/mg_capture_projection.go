package writer

// Exact native capture-row projection. The full frozen row is read so the
// pre-effect fence can name every capture column; no current head, JSON
// checkpoint or caller assertion reconstructs an old result.
const mgCaptureColumns = `id,owner_key,authority,account_id,profile_id,source_id,channel_id,programme_id,guide_generation,state,reason,cancel_generation,claim_generation,claim_token,owner_instance,lease_until_ms,allocation_id,artifact_id,artifact_digest,capture_launch_id,capture_allocation_generation,capture_allocation_token,capture_source_revision,capture_source_fence,capture_writer_retired_sequence,capture_physical_exit_sequence,capture_exit_launch_id,capture_exit_claim_generation,capture_exit_cancel_generation,capture_shared_ready_sequence,captured_start_ms,captured_end_ms,capture_gap_count,capture_producer_succeeded,capture_boundary_reached,has_dvr_recording_shares,share_capture_recording_id,share_capture_claim_generation,share_capture_launch_id,guide_pin_generation,start_ms,end_ms,revision,next_attempt_ms,recovery_attempt,updated_ms,finished_ms,last_command_sequence,acknowledged_command_sequence,command_incarnation,create_operation_id,birth_request_digest,command_member_incarnation,command_profile_lifetime,rule_id,rule_revision`

func mgCaptureTargets(r *mgCaptureRow) []any {
	return []any{&r.id, &r.ownerKey, &r.authority, &r.account, &r.profile, &r.source, &r.channel,
		&r.programme, &r.generation, &r.state, &r.reason, &r.cancelGeneration, &r.claimGeneration,
		&r.token, &r.ownerInstance, &r.leaseMS, &r.allocationID, &r.artifactID, &r.artifactDigest,
		&r.launch, &r.allocationGeneration, &r.allocationToken, &r.sourceRevision, &r.sourceFence,
		&r.writerRetiredSequence, &r.physicalExitSequence, &r.exitLaunch, &r.exitClaimGeneration,
		&r.exitCancelGeneration, &r.sharedReadySequence, &r.capturedStartMS, &r.capturedEndMS,
		&r.gapCount, &r.producerSucceeded, &r.boundaryReached, &r.shares, &r.shareLeader,
		&r.shareClaimGeneration, &r.shareLaunch, &r.guidePin, &r.startMS, &r.endMS, &r.revision,
		&r.nextAttemptMS, &r.recoveryAttempt, &r.updatedMS, &r.finishedMS, &r.lastSequence,
		&r.ackSequence, &r.incarnation, &r.operation, &r.birth, &r.member, &r.lifetime, &r.ruleID,
		&r.ruleRevision}
}
