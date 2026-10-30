package httpapi

import "github.com/PorticoMediaServer/portico/apikit/apierror"

func socialErrorDefinitions() []apierror.Definition {
	return []apierror.Definition{
		{Code: "receiver_required", Status: 403, Message: "Only the receiving device may do that.", Retry: apierror.Never},
		{Code: "capacity_reached", Status: 429, Message: "You already have several groups open.", Retry: apierror.Never},
		{Code: "cast_access_ended", Status: 403, Message: "This account can no longer play on this server. Pair again from a signed-in device.", Retry: apierror.Never},
		{Code: "cast_code_consumed", Status: 410, Message: "That pairing code has already been used.", Retry: apierror.Never},
		{Code: "cast_code_expired", Status: 410, Message: "That pairing code has expired.", Retry: apierror.Never},
		{Code: "cast_code_not_found", Status: 404, Message: "That pairing code is not valid.", Retry: apierror.Never},
		{Code: "cast_code_revoked", Status: 410, Message: "That pairing code was canceled.", Retry: apierror.Never},
		{Code: "cast_device_expired", Status: 410, Message: "That receiver pairing has expired; pair again.", Retry: apierror.Never},
		{Code: "cast_device_not_found", Status: 404, Message: "That receiver is not paired.", Retry: apierror.Never},
		{Code: "cast_device_revoked", Status: 403, Message: "That receiver pairing was revoked.", Retry: apierror.Never},
		{Code: "cast_unavailable", Status: 503, Message: "Cast pairing is not available.", Retry: apierror.Never},
		{Code: "device_required", Status: 409, Message: "Hosting a group needs a signed-in device.", Retry: apierror.Never},
		{Code: "entry_not_found", Status: 404, Message: "No such queue entry.", Retry: apierror.Never},
		{Code: "grant_not_accepted", Status: 403, Message: "That receiver has not authorized this controller.", Retry: apierror.Never},
		{Code: "grant_not_pending", Status: 409, Message: "That authorization is not awaiting a decision.", Retry: apierror.Never},
		{Code: "group_ended", Status: 409, Message: "The group has ended.", Retry: apierror.Never},
		{Code: "group_full", Status: 409, Message: "The group is full.", Retry: apierror.Never},
		{Code: "handoff_expired", Status: 410, Message: "The prepared handoff expired and was rolled back.", Retry: apierror.Never},
		{Code: "handoff_in_progress", Status: 409, Message: "That source already has a handoff in flight.", Retry: apierror.Never},
		{Code: "handoff_not_ready", Status: 409, Message: "The receiver is not playing that title.", Retry: apierror.Never},
		{Code: "handoff_source_conflict", Status: 409, Message: "The source is no longer playing.", Retry: apierror.Never},
		{Code: "handoff_source_revision_conflict", Status: 409, Message: "The source session changed.", Retry: apierror.Never},
		{Code: "handoff_state_conflict", Status: 409, Message: "The handoff is not open.", Retry: apierror.Never},
		{Code: "host_required", Status: 403, Message: "Only the group host may do that.", Retry: apierror.Never},
		{Code: "idempotency_key_reused", Status: 409, Message: "That request id was used for a different handoff.", Retry: apierror.Never},
		{Code: "invalid_request", Status: 400, Message: "The request could not be understood.", Retry: apierror.Never},
		{Code: "invite_consumed", Status: 410, Message: "That invitation has already been used.", Retry: apierror.Never},
		{Code: "invite_expired", Status: 410, Message: "That invitation has expired.", Retry: apierror.Never},
		{Code: "invite_not_found", Status: 404, Message: "No such invitation.", Retry: apierror.Never},
		{Code: "invite_revoked", Status: 410, Message: "That invitation was revoked.", Retry: apierror.Never},
		{Code: "invite_wrong_recipient", Status: 403, Message: "That invitation was issued to someone else.", Retry: apierror.Never},
		{Code: "media_no_longer_accessible", Status: 403, Message: "You cannot add media you cannot see.", Retry: apierror.Never},
		{Code: "member_not_found", Status: 404, Message: "No such member.", Retry: apierror.Never},
		{Code: "membership_revoked", Status: 403, Message: "You are not a member of this group.", Retry: apierror.Never},
		{Code: "not_found", Status: 404, Message: "No such resource.", Retry: apierror.Never},
		{Code: "participant_incompatible", Status: 409, Message: "That member is not ready to host.", Retry: apierror.Never},
		{Code: "playback_authority_unavailable", Status: 503, Message: "Playback authority is not available.", Retry: apierror.Never},
		{Code: "queue_full", Status: 409, Message: "The group queue is full.", Retry: apierror.Never},
		{Code: "rate_limited", Status: 429, Message: "Too many attempts. Try again shortly.", Retry: apierror.Never},
		{Code: "receiver_key_mismatch", Status: 403, Message: "That receiver key does not match the registration.", Retry: apierror.Never},
		{Code: "revision_conflict", Status: 409, Message: "The group changed.", Retry: apierror.Never},
		{Code: "target_unavailable", Status: 409, Message: "That receiver is retired.", Retry: apierror.Never},
	}
}

func socialErrorCodes() []string {
	out := []string{"unauthorized", "not_permitted", "internal"}
	for _, d := range socialErrorDefinitions() {
		out = append(out, d.Code)
	}
	return out
}
