package httpapi

import (
	"github.com/PorticoMediaServer/portico/apikit"
	access "github.com/PorticoMediaServer/portico/server/internal/access"
	administration "github.com/PorticoMediaServer/portico/server/internal/administration"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	diagnostics "github.com/PorticoMediaServer/portico/server/internal/diagnostics"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/mediaanalysis"
	operations "github.com/PorticoMediaServer/portico/server/internal/operations"
	persistence "github.com/PorticoMediaServer/portico/server/internal/persistence"
	servicelog "github.com/PorticoMediaServer/portico/server/internal/servicelog"
	telemetry "github.com/PorticoMediaServer/portico/server/internal/telemetry"
)

func registerAdministrationHTTPContracts(registry *apikit.Registry) error {
	if err := apikit.RegisterHTTP[struct{}, operations.RecordingRootStatus](registry, apikit.Metadata{Audience: apikit.Clients, ID: "get_admin_dvr_recording_root", Method: "GET", Path: "/v1/admin/dvr/recording-root", Summary: "Read requested and effective recording folder", Access: apikit.Owner, Requires: apikit.ScopeAdmin, Lane: apikit.Default, Admission: laneAdmin, Cost: apikit.Constant, Status: 200, Errors: []string{"unauthorized", "not_permitted", "not_found", "sign_in_kind_local", "sign_in_kind_portico", "factor_setup_required", "password_change_required", "revision_mismatch", "administration_unavailable", "internal"}}); err != nil {
		return err
	}
	if err := apikit.RegisterHTTP[RecordingRootInput, operations.RecordingRootStatus](registry, apikit.Metadata{Audience: apikit.Clients, ID: "patch_admin_dvr_recording_root", Method: "PATCH", Path: "/v1/admin/dvr/recording-root", Summary: "Request a recording folder for the next restart", Access: apikit.Owner, Requires: apikit.ScopeAdmin, Lane: apikit.Default, Admission: laneAdmin, Cost: apikit.Constant, BodyLimit: 16384, Status: 200, Errors: []string{"invalid_request", "revision_required", "unauthorized", "not_permitted", "not_found", "sign_in_kind_local", "sign_in_kind_portico", "factor_setup_required", "password_change_required", "idempotency_key_reused", "recording_root_request_expired", "revision_mismatch", "rate_limited", "administration_unavailable", "internal"}}); err != nil {
		return err
	}

	sonicErrors := []string{"invalid_request", "invalid_analysis", "unauthorized", "not_permitted", "not_found", "revision_required", "library_configuration_conflict", "analysis_conflict", "analysis_unavailable", "internal"}
	if err := apikit.RegisterHTTP[struct{}, mediaanalysis.SonicStatusView](registry, apikit.Metadata{Audience: apikit.Clients, ID: "get_admin_libraries_id_sonic_analysis", Method: "GET", Path: "/v1/admin/libraries/{id}/sonic-analysis", Summary: "Read current library sound analysis status", Access: apikit.Owner, Requires: apikit.ScopeAdmin, Lane: apikit.Default, Admission: laneAdminHeavy, Cost: apikit.PageSized, Status: 200, Errors: sonicErrors}); err != nil {
		return err
	}
	if err := apikit.RegisterHTTP[SonicSettingsChange, mediaanalysis.SonicStatusView](registry, apikit.Metadata{Audience: apikit.Clients, ID: "put_admin_libraries_id_sonic_analysis", Method: "PUT", Path: "/v1/admin/libraries/{id}/sonic-analysis", Summary: "Save library sound analysis with its current revision", Access: apikit.Owner, Requires: apikit.ScopeAdmin, Lane: apikit.Default, Admission: laneAdminHeavy, Cost: apikit.PageSized, BodyLimit: 4096, Status: 200, Errors: sonicErrors}); err != nil {
		return err
	}
	if err := apikit.RegisterHTTP[struct{}, mediaanalysis.SonicStatusView](registry, apikit.Metadata{Audience: apikit.Clients, ID: "post_admin_libraries_id_sonic_analysis_retry", Method: "POST", Path: "/v1/admin/libraries/{id}/sonic-analysis/retry", Summary: "Retry the installed sound analysis engine", Access: apikit.Owner, Requires: apikit.ScopeAdmin, Lane: apikit.Default, Admission: laneAdminHeavy, Cost: apikit.Constant, BodyLimit: 1024, Status: 200, Errors: sonicErrors}); err != nil {
		return err
	}

	if err := apikit.RegisterHTTP[access.MemberBulkRequest, access.MemberBulkResult](registry, apikit.Metadata{Audience: apikit.Clients, ID: "post_admin_access_members_bulk", Method: "POST", Path: "/v1/admin/access/members/bulk", Summary: "Change selected people in one transaction", Access: apikit.Admin, Requires: apikit.ScopeAdmin, AdminScope: "members", Lane: apikit.Security, Admission: laneSecurityFence, Cost: apikit.PageSized, BodyLimit: 65536, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "revision_mismatch", "internal"}}); err != nil {
		return err
	}

	// internal/httpapi/admin_access.go:27
	if err := apikit.RegisterHTTP[struct{}, access.MemberPage](registry, apikit.Metadata{Audience: apikit.Clients, ID: "get_admin_access_members", Method: "GET", Path: "/v1/admin/access/members", Summary: "Read server access members", Access: apikit.Admin, Requires: apikit.ScopeAdmin, Lane: apikit.Default, Admission: laneAdmin, Cost: apikit.PageSized, BodyLimit: 0, Status: 200, Query: []string{"cursor", "limit", "q", "role", "status", "sort", "direction"}, QueryIntegers: emptyLimitQuery(25, 100), AdminScope: "access", Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/admin_access.go: one member
	if err := apikit.RegisterHTTP[struct{}, access.Member](registry, apikit.Metadata{Audience: apikit.Clients, ID: "get_admin_access_member", Method: "GET", Path: "/v1/admin/access/members/{id}", Summary: "Read one member as the members list shows it", Access: apikit.Admin, Requires: apikit.ScopeAdmin, Lane: apikit.Default, Admission: laneAdmin, Cost: apikit.Constant, BodyLimit: 0, Status: 200, AdminScope: "access", Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/admin_access.go: one invitation
	if err := apikit.RegisterHTTP[struct{}, access.Invitation](registry, apikit.Metadata{Audience: apikit.Clients, ID: "get_admin_access_invitation", Method: "GET", Path: "/v1/admin/access/invitations/{id}", Summary: "Read one invitation as the invitations list shows it", Access: apikit.Admin, Requires: apikit.ScopeAdmin, Lane: apikit.Default, Admission: laneAdmin, Cost: apikit.Constant, BodyLimit: 0, Status: 200, AdminScope: "access", Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/admin_access.go:40
	if err := apikit.RegisterHTTP[access.RoleChange, access.Member](registry, apikit.Metadata{Audience: apikit.Clients, ID: "put_admin_access_members_id_role", Method: "PUT", Path: "/v1/admin/access/members/{id}/role", Summary: "Save server access members role", Access: apikit.Admin, Requires: apikit.ScopeAdmin, Lane: apikit.Default, Admission: laneAdmin, Cost: apikit.Constant, BodyLimit: 65536, Status: 200, AdminScope: "access", Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/admin_access.go:58
	if err := apikit.RegisterHTTP[struct{}, access.LimitsDocument](registry, apikit.Metadata{Audience: apikit.Clients, ID: "get_admin_access_members_id_limits", Method: "GET", Path: "/v1/admin/access/members/{id}/limits", Summary: "Read server access members limits", Access: apikit.Admin, Requires: apikit.ScopeAdmin, Lane: apikit.Default, Admission: laneAdmin, Cost: apikit.PageSized, BodyLimit: 0, Status: 200, AdminScope: "access", Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/admin_access.go:71
	if err := apikit.RegisterHTTP[access.LimitsChange, access.LimitsDocument](registry, apikit.Metadata{Audience: apikit.Clients, ID: "put_admin_access_members_id_limits", Method: "PUT", Path: "/v1/admin/access/members/{id}/limits", Summary: "Save server access members limits", Access: apikit.Admin, Requires: apikit.ScopeAdmin, Lane: apikit.Default, Admission: laneAdmin, Cost: apikit.Constant, BodyLimit: 65536, Status: 200, AdminScope: "access", Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	if err := apikit.RegisterHTTP[struct{}, identity.MemberProfiles](registry, apikit.Metadata{Audience: apikit.Clients, ID: "get_admin_access_members_id_profiles", Method: "GET", Path: "/v1/admin/access/members/{id}/profiles", Summary: "Read another person's profiles to manage them", Access: apikit.Admin, Requires: apikit.ScopeAdmin, Lane: apikit.Default, Admission: laneAdmin, Cost: apikit.Constant, BodyLimit: 0, Status: 200, AdminScope: "access", Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	if err := apikit.RegisterHTTP[struct {
		Image SubtitleBytesContract `json:"image"`
	}, identity.MemberProfile](registry, apikit.Metadata{Audience: apikit.Clients, ID: "post_admin_access_members_id_profiles_profile_avatar", Method: "POST", Path: "/v1/admin/access/members/{id}/profiles/{profileId}/avatar", Summary: "Post another person's profile picture", Access: apikit.Admin, Requires: apikit.ScopeAdmin, Lane: apikit.Default, Admission: laneAdmin, Cost: apikit.Constant, BodyLimit: identity.AvatarUploadBytes + (1 << 20), Status: 200, Query: []string{"expectedRevision"}, RequestContentTypes: []string{"image/jpeg", "image/png", "application/octet-stream", "multipart/form-data"}, AdminScope: "access", Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "revision_mismatch", "avatar_unsupported", "avatar_not_found", "internal"}}); err != nil {
		return err
	}
	if err := apikit.RegisterHTTP[identity.MemberProfilePINReset, identity.MemberProfile](registry, apikit.Metadata{Audience: apikit.Clients, ID: "delete_admin_access_members_id_profiles_profile_avatar", Method: "DELETE", Path: "/v1/admin/access/members/{id}/profiles/{profileId}/avatar", Summary: "Delete another person's profile picture", Access: apikit.Admin, Requires: apikit.ScopeAdmin, Lane: apikit.Default, Admission: laneAdmin, Cost: apikit.Constant, BodyLimit: 4096, Status: 200, Query: nil, RequestContentTypes: nil, AdminScope: "access", Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "revision_mismatch", "avatar_unsupported", "avatar_not_found", "internal"}}); err != nil {
		return err
	}
	if err := apikit.RegisterHTTP[struct{}, SubtitleBytesContract](registry, apikit.Metadata{Audience: apikit.Clients, ID: "get_admin_access_members_id_profiles_profile_avatar", Method: "GET", Path: "/v1/admin/access/members/{id}/profiles/{profileId}/avatar", Summary: "Get another person's profile picture", Access: apikit.Admin, Requires: apikit.ScopeAdmin, Lane: apikit.Default, Admission: laneAdmin, Cost: apikit.Constant, BodyLimit: 0, Status: 200, Query: []string{"v", "size"}, RequestContentTypes: nil, ResponseContentType: "application/octet-stream", AdminScope: "access", Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "revision_mismatch", "avatar_unsupported", "avatar_not_found", "internal"}}); err != nil {
		return err
	}
	if err := apikit.RegisterHTTP[identity.MemberProfileEdit, identity.MemberProfile](registry, apikit.Metadata{Audience: apikit.Clients, ID: "patch_admin_access_members_id_profiles_profile", Method: "PATCH", Path: "/v1/admin/access/members/{id}/profiles/{profileId}", Summary: "Rename or recolour another person's profile", Access: apikit.Admin, Requires: apikit.ScopeAdmin, Lane: apikit.Security, Admission: laneAdmin, Cost: apikit.Constant, BodyLimit: 4096, Status: 200, AdminScope: "access", Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "revision_mismatch", "internal"}}); err != nil {
		return err
	}
	if err := apikit.RegisterHTTP[identity.ProfileRestrictionEdit, identity.MemberProfile](registry, apikit.Metadata{Audience: apikit.Clients, ID: "put_admin_access_members_id_profiles_profile_restrictions", Method: "PUT", Path: "/v1/admin/access/members/{id}/profiles/{profileId}/restrictions", Summary: "Replace another person's profile restrictions", Access: apikit.Admin, Requires: apikit.ScopeAdmin, Lane: apikit.Security, Admission: laneAdmin, Cost: apikit.Constant, BodyLimit: 32768, Status: 200, AdminScope: "access", Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "revision_mismatch", "internal"}}); err != nil {
		return err
	}
	if err := apikit.RegisterHTTP[identity.MemberProfilePINReset, identity.MemberProfile](registry, apikit.Metadata{Audience: apikit.Clients, ID: "post_admin_access_members_id_profiles_profile_pin_reset", Method: "POST", Path: "/v1/admin/access/members/{id}/profiles/{profileId}/pin-reset", Summary: "Clear the PIN on another person's profile", Access: apikit.Admin, Requires: apikit.ScopeAdmin, Lane: apikit.Security, Admission: laneAdmin, Cost: apikit.Constant, BodyLimit: 4096, Status: 200, AdminScope: "access", Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "revision_mismatch", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/admin_access.go:89
	if err := apikit.RegisterHTTP[struct{}, access.InvitationTablePage](registry, apikit.Metadata{Audience: apikit.Clients, ID: "get_admin_access_invitations", Method: "GET", Path: "/v1/admin/access/invitations", Summary: "Read server access invitations", Access: apikit.Admin, Requires: apikit.ScopeAdmin, Lane: apikit.Default, Admission: laneAdmin, Cost: apikit.PageSized, BodyLimit: 0, Status: 200, Query: []string{"cursor", "limit", "state", "q", "sort", "direction"}, QueryIntegers: emptyLimitQuery(25, 100), AdminScope: "access", Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/admin_access.go:103
	if err := apikit.RegisterHTTP[access.InvitationRequest, access.Invitation](registry, apikit.Metadata{Audience: apikit.Clients, ID: "post_admin_access_invitations", Method: "POST", Path: "/v1/admin/access/invitations", Summary: "Run server access invitations", Access: apikit.Admin, Requires: apikit.ScopeAdmin, Lane: apikit.Default, Admission: laneAdmin, Cost: apikit.Constant, BodyLimit: 65536, Status: 201, AdminScope: "access", Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/admin_access.go:121
	if err := apikit.RegisterHTTP[access.InvitationRevoke, access.Invitation](registry, apikit.Metadata{Audience: apikit.Clients, ID: "post_admin_access_invitations_id_revoke", Method: "POST", Path: "/v1/admin/access/invitations/{id}/revoke", Summary: "Run server access invitations revoke", Access: apikit.Admin, Requires: apikit.ScopeAdmin, Lane: apikit.Security, Admission: laneSecurityFence, Cost: apikit.Constant, BodyLimit: 65536, Status: 200, AdminScope: "access", Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	if err := apikit.RegisterHTTP[access.MemberNotesChange, access.Member](registry, apikit.Metadata{Audience: apikit.Clients, ID: "put_admin_access_member_notes", Method: "PUT", Path: "/v1/admin/access/members/{id}/notes", Summary: "Manage invitation or private person notes", Access: apikit.Admin, Requires: apikit.ScopeAdmin, Lane: apikit.Security, Admission: laneSecurityFence, Cost: apikit.Constant, BodyLimit: 8192, Status: 200, AdminScope: "access", Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	if err := apikit.RegisterHTTP[access.InvitationChange, access.Invitation](registry, apikit.Metadata{Audience: apikit.Clients, ID: "put_admin_access_invitation", Method: "PUT", Path: "/v1/admin/access/invitations/{id}", Summary: "Manage invitation or private person notes", Access: apikit.Admin, Requires: apikit.ScopeAdmin, Lane: apikit.Security, Admission: laneSecurityFence, Cost: apikit.Constant, BodyLimit: 8192, Status: 200, AdminScope: "access", Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	if err := apikit.RegisterHTTP[access.InvitationLinkRequest, access.InvitationLink](registry, apikit.Metadata{Audience: apikit.Clients, ID: "post_admin_access_invitation_link", Method: "POST", Path: "/v1/admin/access/invitations/{id}/link", Summary: "Manage invitation or private person notes", Access: apikit.Admin, Requires: apikit.ScopeAdmin, Lane: apikit.Security, Admission: laneSecurityFence, Cost: apikit.Constant, BodyLimit: 8192, Status: 200, AdminScope: "access", Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	if err := apikit.RegisterHTTP[access.InvitationShortRequest, access.InvitationShortResponse](registry, apikit.Metadata{Audience: apikit.Clients, ID: "post_admin_access_invitation_short_code", Method: "POST", Path: "/v1/admin/access/invitations/{id}/short-code", Summary: "Issue a one-time display short invitation code", Access: apikit.Admin, Requires: apikit.ScopeAdmin, Lane: apikit.Security, Admission: laneSecurityFence, Cost: apikit.Constant, BodyLimit: 4096, Status: 200, AdminScope: "access", Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "revision_mismatch", "invitation_not_found", "idempotency_key_reused", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/admin_access.go:139
	if err := apikit.RegisterHTTP[InvitationAcceptRequest, InvitationAcceptanceResponse](registry, apikit.Metadata{Audience: apikit.Clients, ID: "post_access_invitations_accept", Method: "POST", Path: "/v1/access/invitations/accept", Summary: "Run access invitations accept", Access: apikit.Public, Requires: apikit.ScopeSession, Lane: apikit.Default, Admission: laneDefault, Cost: apikit.Constant, BodyLimit: 65536, Status: 201, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "sign_in_kind_local", "sign_in_kind_portico", "factor_setup_required", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/admin_concurrency.go:182
	if err := apikit.RegisterHTTP[struct{}, ConcurrencyDiagnostics](registry, apikit.Metadata{Audience: apikit.Clients, ID: "get_admin_diagnostics_concurrency", Method: "GET", Path: "/v1/admin/diagnostics/concurrency", Summary: "Read server diagnostics concurrency", Access: apikit.Owner, Requires: apikit.ScopeAdmin, Lane: apikit.Default, Admission: laneAdmin, Cost: apikit.Constant, BodyLimit: 0, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/admin_concurrency.go:196
	if err := apikit.RegisterHTTP[struct{}, IntegrityDiagnostics](registry, apikit.Metadata{Audience: apikit.Clients, ID: "post_admin_diagnostics_integrity", Method: "POST", Path: "/v1/admin/diagnostics/integrity", Summary: "Run server diagnostics integrity", Access: apikit.Owner, Requires: apikit.ScopeAdmin, Lane: apikit.Default, Admission: laneAdminLong, Cost: apikit.Constant, BodyLimit: 0, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/admin_concurrency.go:208
	if err := apikit.RegisterHTTP[struct{}, struct {
		Repair     persistence.OrphanRepairReport `json:"repair"`
		Incomplete bool                           `json:"incomplete"`
	}](registry, apikit.Metadata{Audience: apikit.Clients, ID: "post_admin_diagnostics_integrity_repair", Method: "POST", Path: "/v1/admin/diagnostics/integrity/repair", Summary: "Run server diagnostics integrity repair", Access: apikit.Owner, Requires: apikit.ScopeAdmin, Lane: apikit.Default, Admission: laneAdminLong, Cost: apikit.Constant, BodyLimit: 0, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/admin_concurrency.go:226
	if err := apikit.RegisterHTTP[struct{}, struct {
		Ready  bool   `json:"ready"`
		Status string `json:"status"`
	}](registry, apikit.Metadata{Audience: apikit.Clients, ID: "get_readiness", Method: "GET", Path: "/v1/readiness", Summary: "Read readiness", Access: apikit.Public, Requires: apikit.ScopeOpen, Lane: apikit.Default, Admission: laneDefault, Cost: apikit.Constant, BodyLimit: 0, Status: 200, ResponseStatuses: []int{503}, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/admin_diagnostics.go:44
	if err := apikit.RegisterHTTP[struct{}, servicelog.Result](registry, apikit.Metadata{Audience: apikit.Clients, ID: "get_admin_logs", Method: "GET", Path: "/v1/admin/logs", Summary: "Read server logs", Access: apikit.Owner, Requires: apikit.ScopeAdmin, Lane: apikit.Default, Admission: laneAdminHeavy, Cost: apikit.PageSized, BodyLimit: 0, Status: 200, Query: []string{"category", "cursor", "level", "limit"}, QueryIntegers: []apikit.QueryInteger{{Name: "limit", Minimum: 1, Default: 100, Maximum: 500, DefaultBelowMinimum: true, EmptyIsDefault: true}}, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/admin_diagnostics.go:66
	if err := apikit.RegisterHTTP[struct{}, SubtitleBytesContract](registry, apikit.Metadata{Audience: apikit.Clients, ID: "get_admin_logs_events", Method: "GET", Path: "/v1/admin/logs/events", Summary: "Read server logs events", Access: apikit.Owner, Requires: apikit.ScopeAdmin, Lane: apikit.Realtime, Admission: laneRealtime, Cost: apikit.Constant, BodyLimit: 0, Status: 200, Query: []string{"level"}, ResponseContentType: "text/event-stream", Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/admin_diagnostics.go:67
	if err := apikit.RegisterHTTP[struct{}, LogSettings](registry, apikit.Metadata{Audience: apikit.Clients, ID: "get_admin_logs_settings", Method: "GET", Path: "/v1/admin/logs/settings", Summary: "Read server logs settings", Access: apikit.Owner, Requires: apikit.ScopeAdmin, Lane: apikit.Default, Admission: laneAdmin, Cost: apikit.Constant, BodyLimit: 0, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/admin_diagnostics.go:79
	if err := apikit.RegisterHTTP[struct {
		ExpectedRevision int64                       "json:\"expectedRevision\""
		OperationID      string                      "json:\"operationId\""
		Level            string                      "json:\"logLevel\""
		DetailMinutes    operations.LogDetailMinutes `json:"logDetailMinutes,omitempty"`
		Retention        []operations.LogRetention   "json:\"retention\""
	}, LogSettings](registry, apikit.Metadata{Audience: apikit.Clients, ID: "patch_admin_logs_settings", Method: "PATCH", Path: "/v1/admin/logs/settings", Summary: "Change server logs settings", Access: apikit.Owner, Requires: apikit.ScopeAdmin, Lane: apikit.Default, Admission: laneAdmin, Cost: apikit.Constant, BodyLimit: 65536, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/admin_diagnostics.go:110
	if err := apikit.RegisterHTTP[struct {
		Minutes     int    "json:\"minutes\""
		OperationID string "json:\"operationId\""
	}, LogSettings](registry, apikit.Metadata{Audience: apikit.Clients, ID: "post_admin_logs_debug_window", Method: "POST", Path: "/v1/admin/logs/debug-window", Summary: "Run server logs debug window", Access: apikit.Owner, Requires: apikit.ScopeAdmin, Lane: apikit.Default, Admission: laneAdmin, Cost: apikit.Constant, BodyLimit: 65536, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/admin_diagnostics.go:148
	if err := apikit.RegisterHTTP[struct{}, SubtitleBytesContract](registry, apikit.Metadata{Audience: apikit.Clients, ID: "get_admin_diagnostics_bundle", Method: "GET", Path: "/v1/admin/diagnostics/bundle", Summary: "Read server diagnostics bundle", Access: apikit.Owner, Requires: apikit.ScopeAdmin, Lane: apikit.Default, Admission: laneAdminHeavy, Cost: apikit.Constant, BodyLimit: 0, Status: 200, ResponseContentType: "application/zip", Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/admin_pprof.go:28
	if err := apikit.RegisterHTTP[struct{}, SubtitleBytesContract](registry, apikit.Metadata{Audience: apikit.Clients, ID: "get_admin_diagnostics_pprof_profile", Method: "GET", Path: "/v1/admin/diagnostics/pprof/{profile}", Summary: "Read server diagnostics pprof", Access: apikit.Owner, Requires: apikit.ScopeAdmin, Lane: apikit.Default, Admission: laneAdminHeavy, Cost: apikit.Constant, BodyLimit: 0, Status: 200, ResponseContentType: "application/octet-stream", Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/administration.go:179
	if err := apikit.RegisterHTTP[struct{}, LiveOwnerResult[administration.DeletePreview]](registry, apikit.Metadata{Audience: apikit.Clients, ID: "post_items_id_delete_preview", Method: "POST", Path: "/v1/items/{id}/delete/preview", Summary: "Preview deleting a title’s files", Access: apikit.Device, Requires: apikit.ScopeLibrary, Lane: apikit.Browsing, Admission: laneBrowsing, Cost: apikit.Constant, BodyLimit: 0, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/administration.go:185
	if err := apikit.RegisterHTTP[struct {
		ItemIDs []string "json:\"itemIds\""
	}, LiveOwnerResult[administration.DeletePreview]](registry, apikit.Metadata{Audience: apikit.Clients, ID: "post_admin_media_delete_preview", Method: "POST", Path: "/v1/admin/media/delete/preview", Summary: "Preview deleting selected media files", Access: apikit.Owner, Requires: apikit.ScopeLibrary, Lane: apikit.Default, Admission: laneAdminHeavy, Cost: apikit.Constant, BodyLimit: 65536, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/administration.go:194
	if err := apikit.RegisterHTTP[administration.DeleteRequest, LiveOwnerResult[administration.DeleteResult]](registry, apikit.Metadata{Audience: apikit.Clients, ID: "post_items_id_delete", Method: "POST", Path: "/v1/items/{id}/delete", Summary: "Delete a title’s files", Access: apikit.Device, Requires: apikit.ScopeLibrary, Lane: apikit.Browsing, Admission: laneBrowsing, Cost: apikit.Constant, BodyLimit: 65536, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/administration.go:206
	if err := apikit.RegisterHTTP[administration.DeleteRequest, LiveOwnerResult[administration.DeleteResult]](registry, apikit.Metadata{Audience: apikit.Clients, ID: "post_admin_media_delete", Method: "POST", Path: "/v1/admin/media/delete", Summary: "Delete selected media files", Access: apikit.Owner, Requires: apikit.ScopeLibrary, Lane: apikit.Default, Admission: laneAdminHeavy, Cost: apikit.Constant, BodyLimit: 65536, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/administration.go:218
	if err := apikit.RegisterHTTP[struct{}, LiveOwnerResult[administration.FilesystemPage]](registry, apikit.Metadata{Audience: apikit.Clients, ID: "get_admin_filesystem", Method: "GET", Path: "/v1/admin/filesystem", Summary: "Read server filesystem", Query: []string{"path", "cursor", "limit", "includeFiles", "showHidden", "prefix", "checkWritable"}, QueryIntegers: emptyLimitQuery(40, 200), Access: apikit.Admin, AdminScope: "libraries", Requires: apikit.ScopeAdmin, Lane: apikit.Default, Admission: laneAdmin, Cost: apikit.PageSized, BodyLimit: 0, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/administration.go:232
	if err := apikit.RegisterHTTP[struct{}, LiveOwnerResult[administration.LibrarySettingsIndex]](registry, apikit.Metadata{Audience: apikit.Clients, ID: "get_admin_library_settings", Method: "GET", Path: "/v1/admin/library-settings", Summary: "Read server library settings", Query: []string{"cursor", "limit"}, QueryIntegers: emptyLimitQuery(40, 200), Access: apikit.Admin, AdminScope: "libraries", Requires: apikit.ScopeLibrary, Lane: apikit.Default, Admission: laneAdmin, Cost: apikit.PageSized, BodyLimit: 0, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/administration.go:243
	if err := apikit.RegisterHTTP[struct{}, LiveOwnerResult[administration.AnalysisMatrix]](registry, apikit.Metadata{Audience: apikit.Clients, ID: "get_admin_analysis_operations", Method: "GET", Path: "/v1/admin/analysis-operations", Summary: "Read server analysis operations", Access: apikit.Admin, AdminScope: "libraries", Requires: apikit.ScopeLibrary, Lane: apikit.Default, Admission: laneAdminHeavy, Cost: apikit.Constant, BodyLimit: 0, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/administration.go:249
	if err := apikit.RegisterHTTP[struct{}, LiveOwnerResult[struct {
		Providers []administration.MetadataProvider `json:"providers"`
	}]](registry, apikit.Metadata{Audience: apikit.Clients, ID: "get_admin_metadata_providers", Method: "GET", Path: "/v1/admin/metadata-providers", Summary: "Read server metadata providers", Access: apikit.Admin, AdminScope: "libraries", Requires: apikit.ScopeLibrary, Lane: apikit.Default, Admission: laneAdmin, Cost: apikit.Constant, BodyLimit: 0, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/administration.go:262
	if err := apikit.RegisterHTTP[struct{}, LiveOwnerResult[administration.ChannelMapPage]](registry, apikit.Metadata{Audience: apikit.Clients, ID: "get_admin_live_sources_id_channel_map", Method: "GET", Path: "/v1/admin/live-sources/{id}/channel-map", Summary: "Read server live sources channel map", Query: []string{"channelId", "cursor", "limit"}, QueryIntegers: emptyLimitQuery(40, 200), Access: apikit.Owner, Requires: apikit.ScopeAdmin, Lane: apikit.Default, Admission: laneAdmin, Cost: apikit.PageSized, BodyLimit: 0, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/administration.go:273
	if err := apikit.RegisterHTTP[administration.ChannelMapChange, LiveOwnerResult[administration.ChannelMapPage]](registry, apikit.Metadata{Audience: apikit.Clients, ID: "put_admin_live_sources_id_channel_map", Method: "PUT", Path: "/v1/admin/live-sources/{id}/channel-map", Summary: "Save server live sources channel map", Access: apikit.Owner, Requires: apikit.ScopeAdmin, Lane: apikit.Default, Admission: laneAdmin, Cost: apikit.Constant, BodyLimit: 65536, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/administration.go:280
	if err := apikit.RegisterHTTP[struct {
		OperationID string "json:\"operationId\""
	}, LiveOwnerResult[administration.LogoImportResult]](registry, apikit.Metadata{Audience: apikit.Clients, ID: "post_admin_live_sources_id_logos", Method: "POST", Path: "/v1/admin/live-sources/{id}/logos", Summary: "Run server live sources logos", Access: apikit.Owner, Requires: apikit.ScopeAdmin, Lane: apikit.Default, Admission: laneAdmin, Cost: apikit.Constant, BodyLimit: 65536, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/administration.go:289
	if err := apikit.RegisterHTTP[struct {
		WindowMs int "json:\"windowMs\""
	}, LiveOwnerResult[administration.TunerDiscovery]](registry, apikit.Metadata{Audience: apikit.Clients, ID: "post_admin_live_sources_discover", Method: "POST", Path: "/v1/admin/live-sources/discover", Summary: "Run server live sources discover", Access: apikit.Owner, Requires: apikit.ScopeAdmin, Lane: apikit.Default, Admission: laneAdmin, Cost: apikit.Constant, BodyLimit: 65536, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/administration.go:308
	if err := apikit.RegisterHTTP[struct{}, LiveOwnerResult[administration.DVRDocument]](registry, apikit.Metadata{Audience: apikit.Clients, ID: "get_admin_dvr_settings", Method: "GET", Path: "/v1/admin/dvr/settings", Summary: "Read server dvr settings", Access: apikit.Owner, Requires: apikit.ScopeAdmin, Lane: apikit.Default, Admission: laneAdmin, Cost: apikit.Constant, BodyLimit: 0, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/administration.go:314
	if err := apikit.RegisterHTTP[administration.Change[administration.DVRDefaults], LiveOwnerResult[administration.DVRDocument]](registry, apikit.Metadata{Audience: apikit.Clients, ID: "put_admin_dvr_settings", Method: "PUT", Path: "/v1/admin/dvr/settings", Summary: "Save server dvr settings", Access: apikit.Owner, Requires: apikit.ScopeAdmin, Lane: apikit.Default, Admission: laneAdmin, Cost: apikit.Constant, BodyLimit: 65536, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/administration.go:322
	if err := apikit.RegisterHTTP[struct{}, LiveOwnerResult[administration.RecordingGroupPage]](registry, apikit.Metadata{Audience: apikit.Clients, ID: "get_admin_dvr_recording_groups", Method: "GET", Path: "/v1/admin/dvr/recording-groups", Summary: "Read server dvr recording groups", Query: []string{"cursor", "limit"}, QueryIntegers: emptyLimitQuery(40, 200), Access: apikit.Owner, Requires: apikit.ScopeAdmin, Lane: apikit.Default, Admission: laneAdmin, Cost: apikit.PageSized, BodyLimit: 0, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/administration.go:333
	if err := apikit.RegisterHTTP[administration.RecordingGroupChange, LiveOwnerResult[administration.RecordingGroup]](registry, apikit.Metadata{Audience: apikit.Clients, ID: "post_admin_dvr_recording_groups", Method: "POST", Path: "/v1/admin/dvr/recording-groups", Summary: "Run server dvr recording groups", Access: apikit.Owner, Requires: apikit.ScopeAdmin, Lane: apikit.Default, Admission: laneAdmin, Cost: apikit.Constant, BodyLimit: 65536, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/administration.go:340
	if err := apikit.RegisterHTTP[administration.RecordingGroupChange, LiveOwnerResult[administration.RecordingGroup]](registry, apikit.Metadata{Audience: apikit.Clients, ID: "put_admin_dvr_recording_groups_id", Method: "PUT", Path: "/v1/admin/dvr/recording-groups/{id}", Summary: "Save server dvr recording groups", Access: apikit.Owner, Requires: apikit.ScopeAdmin, Lane: apikit.Default, Admission: laneAdmin, Cost: apikit.Constant, BodyLimit: 65536, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/administration.go:347
	if err := apikit.RegisterHTTP[struct{}, LiveOwnerResult[struct {
		Deleted bool   `json:"deleted"`
		ID      string `json:"id"`
	}]](registry, apikit.Metadata{Audience: apikit.Clients, ID: "delete_admin_dvr_recording_groups_id", Method: "DELETE", Path: "/v1/admin/dvr/recording-groups/{id}", Summary: "Remove server dvr recording groups", Access: apikit.Owner, Requires: apikit.ScopeAdmin, Lane: apikit.Default, Admission: laneAdmin, Cost: apikit.Constant, BodyLimit: 0, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/administration.go:369
	if err := apikit.RegisterHTTP[struct{}, LiveOwnerResult[administration.CriteriaPage]](registry, apikit.Metadata{Audience: apikit.Clients, ID: "get_admin_library_channels_criteria", Method: "GET", Path: "/v1/admin/library-channels/criteria", Summary: "Read server library channels criteria", Query: []string{"libraryId", "field", "cursor", "limit"}, QueryIntegers: emptyLimitQuery(40, 200), Access: apikit.Owner, Requires: apikit.ScopeAdmin, Lane: apikit.Default, Admission: laneAdmin, Cost: apikit.PageSized, BodyLimit: 0, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/administration.go:380
	if err := apikit.RegisterHTTP[struct{}, LiveOwnerResult[administration.BlockPresetPage]](registry, apikit.Metadata{Audience: apikit.Clients, ID: "get_admin_library_channels_block_presets", Method: "GET", Path: "/v1/admin/library-channels/block-presets", Summary: "Read server library channels block presets", Query: []string{"cursor", "limit"}, QueryIntegers: emptyLimitQuery(40, 200), Access: apikit.Owner, Requires: apikit.ScopeAdmin, Lane: apikit.Default, Admission: laneAdmin, Cost: apikit.PageSized, BodyLimit: 0, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/administration.go:391
	if err := apikit.RegisterHTTP[administration.BlockPresetChange, LiveOwnerResult[administration.BlockPreset]](registry, apikit.Metadata{Audience: apikit.Clients, ID: "post_admin_library_channels_block_presets", Method: "POST", Path: "/v1/admin/library-channels/block-presets", Summary: "Run server library channels block presets", Access: apikit.Owner, Requires: apikit.ScopeAdmin, Lane: apikit.Default, Admission: laneAdmin, Cost: apikit.Constant, BodyLimit: 65536, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/administration.go:398
	if err := apikit.RegisterHTTP[administration.BlockPresetChange, LiveOwnerResult[administration.BlockPreset]](registry, apikit.Metadata{Audience: apikit.Clients, ID: "put_admin_library_channels_block_presets_id", Method: "PUT", Path: "/v1/admin/library-channels/block-presets/{id}", Summary: "Save server library channels block presets", Access: apikit.Owner, Requires: apikit.ScopeAdmin, Lane: apikit.Default, Admission: laneAdmin, Cost: apikit.Constant, BodyLimit: 65536, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/administration.go:405
	if err := apikit.RegisterHTTP[struct{}, LiveOwnerResult[struct {
		Deleted bool   `json:"deleted"`
		ID      string `json:"id"`
	}]](registry, apikit.Metadata{Audience: apikit.Clients, ID: "delete_admin_library_channels_block_presets_id", Method: "DELETE", Path: "/v1/admin/library-channels/block-presets/{id}", Summary: "Remove server library channels block presets", Access: apikit.Owner, Requires: apikit.ScopeAdmin, Lane: apikit.Default, Admission: laneAdmin, Cost: apikit.Constant, BodyLimit: 0, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/administration.go:419
	if err := apikit.RegisterHTTP[struct{}, LiveOwnerResult[administration.ChannelHealth]](registry, apikit.Metadata{Audience: apikit.Clients, ID: "get_admin_library_channels_id_health", Method: "GET", Path: "/v1/admin/library-channels/{id}/health", Summary: "Read server library channels health", Access: apikit.Owner, Requires: apikit.ScopeAdmin, Lane: apikit.Default, Admission: laneAdmin, Cost: apikit.Constant, BodyLimit: 0, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/administration.go:425
	if err := apikit.RegisterHTTP[struct {
		File             SubtitleBytesContract `json:"file"`
		ExpectedRevision string                `json:"expectedRevision"`
		OperationID      string                `json:"operationId"`
	}, LiveOwnerResult[administration.ChannelLogo]](registry, apikit.Metadata{Audience: apikit.Clients, ID: "post_admin_library_channels_id_logo", Method: "POST", Path: "/v1/admin/library-channels/{id}/logo", RequestContentTypes: []string{"multipart/form-data"}, Summary: "Run server library channels logo", Access: apikit.Owner, Requires: apikit.ScopeAdmin, Lane: apikit.Default, Admission: laneAdmin, Cost: apikit.Constant, BodyLimit: 11534336, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/administration.go:439
	if err := apikit.RegisterHTTP[struct{}, SubtitleBytesContract](registry, apikit.Metadata{Audience: apikit.Clients, ID: "get_library_channels_id_logo", Method: "GET", Path: "/v1/library-channels/{id}/logo", Summary: "Read library channels logo", Access: apikit.Device, Requires: apikit.ScopeRead, Lane: apikit.Default, Admission: laneDefault, Cost: apikit.Constant, BodyLimit: 0, Status: 200, ResponseContentType: "application/octet-stream", Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/administration.go:504
	if err := apikit.RegisterHTTP[struct{}, LiveOwnerResult[administration.StorageReport]](registry, apikit.Metadata{Audience: apikit.Clients, ID: "get_admin_storage_usage", Method: "GET", Path: "/v1/admin/storage-usage", Summary: "Read server storage usage", Access: apikit.Owner, Requires: apikit.ScopeAdmin, Lane: apikit.Default, Admission: laneAdmin, Cost: apikit.Constant, BodyLimit: 0, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/administration.go:510
	if err := apikit.RegisterHTTP[administration.CleanupRequest, LiveOwnerResult[administration.CleanupResult]](registry, apikit.Metadata{Audience: apikit.Clients, ID: "post_admin_storage_usage_category_cleanup", Method: "POST", Path: "/v1/admin/storage-usage/{category}/cleanup", Summary: "Run server storage usage cleanup", Access: apikit.Owner, Requires: apikit.ScopeAdmin, Lane: apikit.Default, Admission: laneAdmin, Cost: apikit.Constant, BodyLimit: 65536, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/console.go:204
	if err := apikit.RegisterHTTP[struct{}, projectedConsoleEnvelope[operations.PreferenceSnapshot]](registry, apikit.Metadata{Audience: apikit.Clients, ID: "get_preferences", Method: "GET", Path: "/v1/preferences", Summary: "Read preferences", Access: apikit.Device, Requires: apikit.ScopeRead, Lane: apikit.Default, Admission: laneDefault, Cost: apikit.PageSized, BodyLimit: 0, Status: 200, Query: []string{"deviceClass"}, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/console.go:207
	if err := apikit.RegisterHTTP[operations.PreferenceChange, projectedConsoleEnvelope[operations.PreferenceSnapshot]](registry, apikit.Metadata{Audience: apikit.Clients, ID: "patch_preferences", Method: "PATCH", Path: "/v1/preferences", Summary: "Change preferences", Access: apikit.Device, Requires: apikit.ScopePlayback, Lane: apikit.Default, Admission: laneDefault, Cost: apikit.Constant, BodyLimit: 65536, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/console.go:251
	if err := apikit.RegisterHTTP[struct{}, projectedConsoleEnvelope[map[string]bool]](registry, apikit.Metadata{Audience: apikit.Clients, ID: "post_notifications_id_read", Method: "POST", Path: "/v1/notifications/{id}/read", Summary: "Run notifications read", Access: apikit.Device, Requires: apikit.ScopePlayback, Lane: apikit.Realtime, Admission: laneRealtime, Cost: apikit.Constant, BodyLimit: 0, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/console.go:263
	if err := apikit.RegisterHTTP[struct{}, projectedConsoleEnvelope[operations.StreamOptions]](registry, apikit.Metadata{Audience: apikit.Clients, ID: "get_admin_console_stream_options", Method: "GET", Path: "/v1/admin/console/stream-options", Summary: "Read server console stream options", Access: apikit.Owner, Requires: apikit.ScopeAdmin, Lane: apikit.Default, Admission: laneAdmin, Cost: apikit.Constant, BodyLimit: 0, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/console.go:266
	if err := apikit.RegisterHTTP[struct{}, projectedConsoleEnvelope[[]operations.StreamOption]](registry, apikit.Metadata{Audience: apikit.Clients, ID: "get_admin_console_accounts", Method: "GET", Path: "/v1/admin/console/accounts", Summary: "Read server console accounts", Access: apikit.Owner, Requires: apikit.ScopeAdmin, Lane: apikit.Default, Admission: laneAdmin, Cost: apikit.Constant, BodyLimit: 0, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/console.go:272
	if err := apikit.RegisterHTTP[operations.JobCommand, projectedConsoleEnvelope[map[string]bool]](registry, apikit.Metadata{Audience: apikit.Clients, ID: "post_admin_console_alerts_id_acknowledge", Method: "POST", Path: "/v1/admin/console/alerts/{id}/acknowledge", Summary: "Run server console alerts acknowledge", Access: apikit.Owner, Requires: apikit.ScopeAdmin, Lane: apikit.Default, Admission: laneAdminHeavy, Cost: apikit.Constant, BodyLimit: 65536, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/console.go:302
	if err := apikit.RegisterHTTP[struct{}, projectedConsoleEnvelope[[]operations.JobKind]](registry, apikit.Metadata{Audience: apikit.Clients, ID: "get_admin_console_job_kinds", Method: "GET", Path: "/v1/admin/console/job-kinds", Summary: "Read server console job kinds", Access: apikit.Owner, Requires: apikit.ScopeAdmin, Lane: apikit.Default, Admission: laneAdmin, Cost: apikit.Constant, BodyLimit: 0, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/notifications_routes.go:103
	if err := apikit.RegisterHTTP[struct{}, projectedConsoleEnvelope[operations.NoticeVocabulary]](registry, apikit.Metadata{Audience: apikit.Clients, ID: "get_notifications_capabilities", Method: "GET", Path: "/v1/notifications/capabilities", Summary: "Read notifications capabilities", Access: apikit.Device, Requires: apikit.ScopeRead, Lane: apikit.Realtime, Admission: laneRealtime, Cost: apikit.Constant, BodyLimit: 0, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/notifications_routes.go:119
	if err := apikit.RegisterHTTP[operations.NoticeBatch, projectedConsoleEnvelope[operations.NoticeBatchResult]](registry, apikit.Metadata{Audience: apikit.Clients, ID: "post_notifications_inbox_actions", Method: "POST", Path: "/v1/notifications/inbox/actions", Summary: "Run notifications inbox actions", Access: apikit.Device, Requires: apikit.ScopePlayback, Lane: apikit.Realtime, Admission: laneRealtime, Cost: apikit.Constant, BodyLimit: 65536, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/notifications_routes.go:126
	if err := apikit.RegisterHTTP[operations.NoticeBroadcast, projectedConsoleEnvelope[operations.NoticeBroadcastResult]](registry, apikit.Metadata{Audience: apikit.Integrations, ID: "post_admin_notifications_broadcast", Method: "POST", Path: "/v1/admin/notifications/broadcast", Summary: "Run server notifications broadcast", Access: apikit.Owner, Requires: apikit.ScopeAdmin, Lane: apikit.Default, Admission: laneAdminHeavy, Cost: apikit.Constant, BodyLimit: 65536, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/notifications_routes.go:133
	if err := apikit.RegisterHTTP[struct{}, projectedConsoleEnvelope[operations.NotificationSettings]](registry, apikit.Metadata{Audience: apikit.Clients, ID: "get_admin_notifications_settings", Method: "GET", Path: "/v1/admin/notifications/settings", Summary: "Read server notifications settings", Access: apikit.Owner, Requires: apikit.ScopeAdmin, Lane: apikit.Default, Admission: laneAdmin, Cost: apikit.Constant, BodyLimit: 0, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/notifications_routes.go:136
	if err := apikit.RegisterHTTP[operations.NotificationSettingsChange, projectedConsoleEnvelope[operations.NotificationSettings]](registry, apikit.Metadata{Audience: apikit.Clients, ID: "patch_admin_notifications_settings", Method: "PATCH", Path: "/v1/admin/notifications/settings", Summary: "Change server notifications settings", Access: apikit.Owner, Requires: apikit.ScopeAdmin, Lane: apikit.Default, Admission: laneAdminHeavy, Cost: apikit.Constant, BodyLimit: 65536, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/notifications_routes.go:145
	if err := apikit.RegisterHTTP[struct{}, projectedConsoleEnvelope[operations.FeedbackCapabilities]](registry, apikit.Metadata{Audience: apikit.Clients, ID: "get_feedback_capabilities", Method: "GET", Path: "/v1/feedback/capabilities", Summary: "Read feedback capabilities", Access: apikit.Device, Requires: apikit.ScopeRead, Lane: apikit.Default, Admission: laneDefault, Cost: apikit.Constant, BodyLimit: 0, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/operations.go:16
	if err := apikit.RegisterHTTP[struct{}, struct {
		Scope map[string]string `json:"scope"`
		Panel operations.Panel  `json:"panel"`
	}](registry, apikit.Metadata{Audience: apikit.Clients, ID: "get_admin_operations_panel", Method: "GET", Path: "/v1/admin/operations/{panel}", Summary: "Read server operations", Access: apikit.Owner, Requires: apikit.ScopeAdmin, Lane: apikit.Default, Admission: laneAdmin, Cost: apikit.Constant, BodyLimit: 0, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/support.go:20
	if err := apikit.RegisterHTTP[struct{}, struct {
		Scope  projectedScope     `json:"scope"`
		Report diagnostics.Report `json:"report"`
	}](registry, apikit.Metadata{Audience: apikit.Clients, ID: "get_admin_support_report", Method: "GET", Path: "/v1/admin/support-report", Summary: "Read server support report", Access: apikit.Owner, Requires: apikit.ScopeAdmin, Lane: apikit.Default, Admission: laneAdmin, Cost: apikit.Constant, BodyLimit: 0, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/telemetry_routes.go:42
	if err := apikit.RegisterHTTP[struct{}, projectedConsoleEnvelope[telemetry.Report]](registry, apikit.Metadata{Audience: apikit.Clients, ID: "test_admin_transcode_hardware", Method: "POST", Path: "/v1/admin/transcode/capacity/test", Summary: "Probe every graphics adapter and backend again now", Access: apikit.Owner, Requires: apikit.ScopeAdmin, Lane: apikit.Default, Admission: laneAdminHeavy, Cost: apikit.Constant, BodyLimit: 4096, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/telemetry_routes.go:42
	if err := apikit.RegisterHTTP[struct{}, projectedConsoleEnvelope[telemetry.Report]](registry, apikit.Metadata{Audience: apikit.Clients, ID: "get_admin_transcode_capacity", Method: "GET", Path: "/v1/admin/transcode/capacity", Summary: "Read server transcode capacity", Access: apikit.Owner, Requires: apikit.ScopeAdmin, Lane: apikit.Default, Admission: laneAdmin, Cost: apikit.Constant, BodyLimit: 0, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/telemetry_routes.go:42
	if err := apikit.RegisterHTTP[struct{}, projectedConsoleEnvelope[telemetry.Reading]](registry, apikit.Metadata{Audience: apikit.Clients, ID: "get_admin_telemetry", Method: "GET", Path: "/v1/admin/telemetry", Summary: "Read server telemetry", Access: apikit.Owner, Requires: apikit.ScopeAdmin, Lane: apikit.Default, Admission: laneAdmin, Cost: apikit.Constant, BodyLimit: 0, Status: 200, Query: []string{"window", "timeZone", "until"}, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/telemetry_routes.go:42
	if err := apikit.RegisterHTTP[struct{}, projectedConsoleEnvelope[telemetry.Sample]](registry, apikit.Metadata{Audience: apikit.Clients, ID: "get_admin_telemetry_now", Method: "GET", Path: "/v1/admin/telemetry/now", Summary: "Read server telemetry now", Access: apikit.Owner, Requires: apikit.ScopeAdmin, Lane: apikit.Default, Admission: laneAdmin, Cost: apikit.Constant, BodyLimit: 0, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/telemetry_routes.go:42
	if err := apikit.RegisterHTTP[struct{}, projectedConsoleEnvelope[struct {
		Items []telemetry.Item `json:"items"`
	}]](registry, apikit.Metadata{Audience: apikit.Clients, ID: "get_admin_attention", Method: "GET", Path: "/v1/admin/attention", Summary: "Read server attention", Access: apikit.Owner, Requires: apikit.ScopeAdmin, Lane: apikit.Default, Admission: laneAdmin, Cost: apikit.Constant, BodyLimit: 0, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/telemetry_routes.go:73
	if err := apikit.RegisterHTTP[struct{}, SubtitleBytesContract](registry, apikit.Metadata{Audience: apikit.Clients, ID: "get_admin_playback_history_csv", Method: "GET", Path: "/v1/admin/playback/history.csv", Summary: "Read server playback history.csv", Access: apikit.Owner, Requires: apikit.ScopeAdmin, Lane: apikit.Media, Admission: lanePlayback, Cost: apikit.PageSized, BodyLimit: 0, Status: 200, ResponseContentType: "text/csv", Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}

	for _, spec := range []struct {
		id, path string
		people   bool
	}{
		{"get_admin_play_history_dashboard", "/v1/admin/play-history/dashboard", false},
		{"get_admin_play_history_people", "/v1/admin/play-history/people", true},
	} {
		meta := apikit.Metadata{Audience: apikit.Clients, ID: spec.id, Method: "GET", Path: spec.path, Summary: "Read owner Dashboard analytics", Access: apikit.Owner, Requires: apikit.ScopeAdmin, Lane: apikit.Default, Admission: laneAdmin, Cost: apikit.PageSized, Status: 200, Query: []string{"period", "accountId", "libraryId", "media", "q", "cursor", "limit", "topKind", "timeZone", "until"}, QueryIntegers: emptyLimitQuery(12, 100), Errors: []string{"invalid_request", "unauthorized", "not_permitted", "internal"}}
		if spec.people {
			if err := apikit.RegisterHTTP[struct{}, catalog.DashboardPeople](registry, meta); err != nil {
				return err
			}
		} else {
			if err := apikit.RegisterHTTP[struct{}, catalog.DashboardHistory](registry, meta); err != nil {
				return err
			}
		}
	}

	return nil
}
