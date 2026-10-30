package httpapi

import (
	"net/http"
	"strconv"

	"github.com/PorticoMediaServer/portico/apikit/apierror"
)

func policyErrorDefinitions() []apierror.Definition {
	return []apierror.Definition{
		{Code: "avatar_unsupported", Status: 415, Message: "Choose a JPEG or PNG picture smaller than 4 MB.", Retry: apierror.Never},
		{Code: "avatar_not_found", Status: 404, Message: "This profile picture is no longer available.", Retry: apierror.AfterRefresh},
		{Code: "profile_changed", Status: 409, Message: publicErrorMessage("profile_changed"), Retry: apierror.Never},
		{Code: "search_busy", Status: 429, Message: "Too many searches are already running. Try again in a moment.", Retry: apierror.SameRequest},
		{Code: "recording_root_request_expired", Status: 410, Message: publicErrorMessage("console_expired"), Retry: apierror.Never},
		{Code: "invalid_query", Status: 400, Message: publicErrorMessage("invalid_request"), Retry: apierror.Never},
		{Code: "home_layout_conflict", Status: 409, Message: publicErrorMessage("home_layout_conflict"), Retry: apierror.AfterRefresh},
		{Code: "stale_continuation", Status: 409, Message: "This list changed. Refresh it to continue.", Retry: apierror.AfterRefresh},
		{Code: "source_changed", Status: 409, Message: publicErrorMessage("source_changed"), Retry: apierror.AfterRefresh},
		{Code: "library_configuration_conflict", Status: 409, Message: publicErrorMessage("library_configuration_conflict"), Retry: apierror.AfterRefresh},
		{Code: "analysis_conflict", Status: 409, Message: publicErrorMessage("analysis_conflict"), Retry: apierror.AfterRefresh},
		{Code: "analysis_unavailable", Status: 503, Message: publicErrorMessage("analysis_unavailable"), Retry: apierror.SameRequest},
		{Code: "invalid_analysis", Status: 400, Message: publicErrorMessage("invalid_request"), Retry: apierror.Never},
		{Code: "operation_expired", Status: 409, Message: "The time to undo this change has passed.", Retry: apierror.Never},
		{Code: "personal_state_conflict", Status: 409, Message: publicErrorMessage("personal_state_conflict"), Retry: apierror.Never},
		{Code: "personal_needs_resolution", Status: 409, Message: publicErrorMessage("personal_needs_resolution"), Retry: apierror.Never},
		{Code: "personal_needs_review", Status: 409, Message: publicErrorMessage("personal_needs_review"), Retry: apierror.Never},
		{Code: "invalid_channel_input", Status: 400, Message: "The playlist or guide could not be used. Check it and try again.", Retry: apierror.Never},
		{Code: "channel_revision_conflict", Status: 409, Message: "The channel source changed. Refresh before saving.", Retry: apierror.AfterRefresh},
		{Code: "source_lan_confirmation_required", Status: 422, Message: "Confirm the source addresses on your local network to continue.", Retry: apierror.Never},
		{Code: "source_network_policy", Status: 422, Message: "This source address needs confirmation before it can be reached.", Retry: apierror.Never},
		{Code: "source_credentials_required", Status: 422, Message: "The provider refused these credentials. Enter them again.", Retry: apierror.Never},
		{Code: "metadata_unavailable", Status: 503, Message: "Metadata is temporarily unavailable.", Retry: apierror.SameRequest},
		{Code: "metadata_conflict", Status: 409, Message: "Metadata changed. Reload and review your decision.", Retry: apierror.Never},
		{Code: "artwork_pending", Status: 404, Message: "Artwork is not available yet.", Retry: apierror.SameRequest},
		{Code: "artwork_gone", Status: 410, Message: "This artwork version is no longer available.", Retry: apierror.AfterRefresh},
		{Code: "method_not_allowed", Status: 405, Message: "This action is not supported.", Retry: apierror.Never},
		{Code: "web_unavailable", Status: 503, Message: "The web app is temporarily unavailable.", Retry: apierror.SameRequest},
		{Code: "source_folders_unsupported", Status: 422, Message: "Folder browsing inside a mounted source is not available on this server.", Retry: apierror.Never},
	}
}

func policyCatalogue() *apierror.Registry {
	definitions := append(v1ErrorDefinitions(), policyErrorDefinitions()...)
	definitions = append(definitions, connectionErrorDefinitions()...)
	definitions = append(definitions, credentialErrorDefinitions()...)
	definitions = append(definitions, accountSecurityErrorDefinitions()...)
	definitions = append(definitions, webhookErrorDefinitions()...)
	definitions = append(definitions, subtitleErrorDefinitions()...)
	definitions = append(definitions, downloadErrorDefinitions()...)
	definitions = append(definitions, rcloneSourceErrorDefinitions()...)
	known := map[string]bool{}
	for _, d := range append(apierror.Defaults(), definitions...) {
		known[d.Code] = true
	}
	for _, d := range socialErrorDefinitions() {
		if !known[d.Code] {
			definitions = append(definitions, d)
			known[d.Code] = true
		}
	}
	registry, err := apierror.New(definitions...)
	if err != nil {
		panic(err)
	}
	registry.Translate(databaseRefusal)
	return registry
}

var policyErrors = policyCatalogue()

func policyError(w http.ResponseWriter, code string) {
	seconds, _ := strconv.Atoi(w.Header().Get("Retry-After"))
	if seconds == 0 && (code == "artwork_pending" || code == "metadata_unavailable" || code == "web_unavailable") {
		seconds = 2
	}
	policyErrors.Write(w, w.Header().Get("X-Request-Id"), &apierror.Error{Code: code, RetryAfterSeconds: seconds})
}
