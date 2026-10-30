package httpapi

// Legacy families retain their codes until registry migration. Error causes,
// including errors wrapping a public sentinel, are never presentation text.
func publicErrorMessage(code string) string {
	switch code {
	case "idempotency_key_reused":
		return "This request conflicts with an earlier request."
	case "not_found":
		return "This item is not available."
	case "unauthorized", "authentication_required":
		return "Authentication is required."
	case "access_limit", "administration_denied", "downloads_not_allowed":
		return "This action is not permitted."
	case "remote_sign_in_not_allowed":
		return "This server does not allow sign-in from outside its home network."
	case "invitation_for_another_account":
		return "This invitation is for a different account."
	case "outside_allowed_hours":
		return "This account cannot be used at this time of day."
	case "invitation_expired", "console_expired", "playback_operation_expired", "storage_operation_expired":
		return "This request expired. Start again."
	case "administration_conflict", "console_conflict", "playback_command_conflict", "library_configuration_conflict", "pin_order_conflict", "download_conflict", "manual_metadata_conflict", "library_channel_changed", "lyrics_conflict", "lyrics_source_changed", "analysis_conflict", "storage_command_conflict", "stale_continuation":
		return "This information changed. Refresh and try again."
	case "administration_capacity", "console_capacity", "playback_command_capacity", "download_capacity", "lyrics_capacity", "storage_command_capacity", "source_capacity_unavailable":
		return "There are too many requests. Try again shortly."
	case "home_layout_conflict":
		return "Home changed on another device. Reload the layout before saving."
	case "storage_executable_invalid":
		return "That isn't rclone. Choose the rclone program."
	case "storage_executable_too_old":
		return "This rclone is too old. Choose a newer rclone."
	case "invalid_administration_input", "invalid_pivot", "invalid_home_layout", "invalid_activity_request", "invalid_library_channel", "invalid_lyrics", "invalid_analysis", "storage_config_invalid", "invalid_request", "invalid_download_receipt":
		return "Check the request and try again."
	case "download_not_ready", "administration_busy":
		return "This request is still being processed."
	case "invalid_download_grant":
		return "Request a new download link."
	case "storage_full":
		return "There is not enough storage for this download."
	case "storage_allocation_recovery_required":
		return "Review the storage configuration before trying again."
	case "confirmation_mismatch":
		return "The confirmation does not match. Check it and try again."
	case "channel_in_use":
		return "This channel is in use."
	case "overlay_unavailable", "library_channel_no_libraries", "template_inapplicable":
		return "This channel configuration is not available."
	case "analysis_clock_unavailable", "analysis_budget_exceeded", "analysis_unavailable":
		return "Media analysis is not available for this request."
	case "lyrics_unavailable":
		return "Lyrics are temporarily unavailable."
	default:
		return "The request could not be completed."
	}
}
