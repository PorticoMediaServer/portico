package httpapi

import "github.com/PorticoMediaServer/portico/apikit/apierror"

func subtitleErrorDefinitions() []apierror.Definition {
	return []apierror.Definition{
		{Code: "subtitle_unavailable", Status: 503, Message: "Subtitles are temporarily unavailable. Try again.", Retry: apierror.SameRequest},
		{Code: "subtitle_not_found", Status: 404, Message: "This subtitle is not available.", Retry: apierror.Never},
		{Code: "subtitle_renderer_platform_unsupported", Status: 422, Message: "This server cannot render this subtitle. Choose a text track or a player that supports it.", Retry: apierror.Never},
		{Code: "subtitle_renderer_configuration", Status: 422, Message: "Ask the owner to finish setting up subtitle rendering.", Retry: apierror.Never},
		{Code: "subtitle_conflict", Status: 409, Message: "Subtitles changed. Refresh before applying this edit.", Retry: apierror.AfterRefresh},
		{Code: "subtitle_operation_conflict", Status: 409, Message: "This request was already used for a different subtitle edit.", Retry: apierror.Never},
		{Code: "subtitle_capacity", Status: 422, Message: "This subtitle exceeds the supported size, track or cue limit.", Retry: apierror.Never},
		{Code: "subtitle_unsupported", Status: 422, Message: "This subtitle format or timing cannot be used by this player.", Retry: apierror.Never},
		{Code: "subtitle_invalid_input", Status: 400, Message: "Check the subtitle request and try again.", Retry: apierror.Never},
		// OpenSubtitles, with the owner's own key: each refusal says who can fix it.
		{Code: "subtitle_provider_not_configured", Status: 503, Message: "Online subtitle search is not set up on this server.", Retry: apierror.Never},
		{Code: "subtitle_provider_key_rejected", Status: 503, Message: "OpenSubtitles did not accept this server's API key.", Retry: apierror.Never},
		{Code: "subtitle_provider_sign_in_failed", Status: 503, Message: "OpenSubtitles did not accept this server's account.", Retry: apierror.Never},
		{Code: "subtitle_provider_sign_in_required", Status: 503, Message: "OpenSubtitles downloads need an account on this server.", Retry: apierror.Never},
		{Code: "subtitle_provider_quota", Status: 429, Message: "This server has used its OpenSubtitles downloads for today.", Retry: apierror.Never},
		{Code: "subtitle_provider_busy", Status: 429, Message: "OpenSubtitles asked for a pause. Try again shortly.", Retry: apierror.SameRequest},
	}
}

func subtitleRouteErrors() []string {
	codes := []string{"unauthorized", "not_permitted", "not_found", "invalid_request", "internal"}
	for _, d := range subtitleErrorDefinitions() {
		codes = append(codes, d.Code)
	}
	return codes
}
