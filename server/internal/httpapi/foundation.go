package httpapi

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"sort"

	"github.com/PorticoMediaServer/portico/apikit"
	"github.com/PorticoMediaServer/portico/apikit/apierror"
	"github.com/PorticoMediaServer/portico/server/internal/access"
	"github.com/PorticoMediaServer/portico/server/internal/administration"
	"github.com/PorticoMediaServer/portico/server/internal/buildinfo"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/downloads"
	"github.com/PorticoMediaServer/portico/server/internal/eventfeed"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/operations"
	"github.com/PorticoMediaServer/portico/server/internal/playbackv1"
)

type APIVersion struct {
	Major int `json:"major"`
	Level int `json:"level"`
}
type ServerDocument struct {
	ID            string     `json:"id"`
	Name          string     `json:"name"`
	API           APIVersion `json:"api"`
	SetupRequired bool       `json:"setupRequired"`
}
type CapabilitiesDocument struct {
	API      APIVersion        `json:"api"`
	Features map[string]string `json:"features"`
	Limits   map[string]int    `json:"limits"`
}
type BootstrapViewer struct {
	Viewer identity.Viewer `json:"viewer"`
}
type BootstrapDocument struct {
	Server       ServerDocument                `json:"server"`
	Capabilities CapabilitiesDocument          `json:"capabilities"`
	Me           BootstrapViewer               `json:"me"`
	Libraries    []catalog.Library             `json:"libraries"`
	Preferences  operations.PreferenceSnapshot `json:"preferences"`
	HomeLayout   operations.HomeLayoutDocument `json:"homeLayout"`
}
type RatingSystemsDocument struct {
	Items []identity.RatingSystem `json:"items"`
}
type DirectSessionsDocument struct {
	Items []identity.DirectSession `json:"items"`
}
type ProfileAvatarsDocument struct {
	Items []identity.ProfileAvatar `json:"items"`
}
type DirectPINResetRequest struct {
	PIN string `json:"pin"`
}

type foundationPrincipalKey struct{}

func (d Dependencies) foundationCaller(r *http.Request) (identity.Principal, error) {
	if p, ok := r.Context().Value(foundationPrincipalKey{}).(identity.Principal); ok {
		return p, nil
	}
	// Integration merge: the registry's contextual v1 authorizer (BE-playback)
	// already admitted this caller and put it on the request context.
	if c := callerFrom(r.Context()); c.Principal.Hash != "" {
		return c.Principal, nil
	}
	return d.principal(r)
}

func foundationAuthError(err error) error {
	if errors.Is(err, identity.ErrProfilePINReentry) {
		return &apierror.Error{Code: "profile_pin_reentry", Cause: err}
	}
	if errors.Is(err, identity.ErrProfilePINManage) {
		return &apierror.Error{Code: "profile_pin_manage", Cause: err}
	}
	if errors.Is(err, identity.ErrNotVisible) {
		return &apierror.Error{Code: "not_found", Cause: err}
	}
	if errors.Is(err, identity.ErrForbidden) {
		return &apierror.Error{Code: "not_permitted", Cause: err}
	}
	if errors.Is(err, identity.ErrUnauthorized) {
		return &apierror.Error{Code: "unauthorized", Cause: err}
	}
	if errors.Is(err, identity.ErrPasswordChangeRequired) {
		return &apierror.Error{Code: "password_change_required", Cause: err}
	}
	if errors.Is(err, identity.ErrDirectInput) {
		return &apierror.Error{Code: "invalid_request", Cause: err}
	}
	if errors.Is(err, identity.ErrDeviceInput) {
		return &apierror.Error{Code: "invalid_request", Cause: err}
	}
	if errors.Is(err, identity.ErrDeviceUnknown) {
		return &apierror.Error{Code: "not_found", Cause: err}
	}
	if errors.Is(err, identity.ErrRestrictionInput) {
		return &apierror.Error{Code: "invalid_request", Cause: err}
	}
	if errors.Is(err, identity.ErrRestrictionChanged) || errors.Is(err, identity.ErrProfileChanged) {
		return &apierror.Error{Code: "revision_mismatch", Cause: err}
	}
	if errors.Is(err, identity.ErrBusy) {
		return &apierror.Error{Code: "request_in_progress", RetryAfterSeconds: 2, Cause: err}
	}
	// Keep transient database errors available to the v1 timeout mapping and
	// catalogue's database-refusal translator instead of sealing them as internal.
	return v1Error(err)
}

func foundationAdministrationError(err error) error {
	if mutation := userMutationError(err); mutation != nil {
		return mutation
	}

	var validation *administration.ValidationError
	if errors.As(err, &validation) {
		fields := make([]apierror.Field, 0, len(validation.Fields))
		for _, path := range validation.Fields {
			fields = append(fields, apierror.Field{Path: path, Code: "invalid_value", Message: "Check this field and try again."})
		}
		return &apierror.Error{Code: "invalid_request", Fields: fields}
	}
	switch {
	case errors.Is(err, identity.ErrNotVisible):
		return &apierror.Error{Code: "not_found"}
	case errors.Is(err, identity.ErrForbidden):
		return &apierror.Error{Code: "not_permitted"}
	case errors.Is(err, identity.ErrUnauthorized):
		return &apierror.Error{Code: "unauthorized"}
	case errors.Is(err, administration.ErrInput):
		return &apierror.Error{Code: "invalid_request"}
	case errors.Is(err, administration.ErrConflict):
		return &apierror.Error{Code: "revision_mismatch"}
	case errors.Is(err, administration.ErrDenied):
		return &apierror.Error{Code: "not_permitted"}
	case errors.Is(err, administration.ErrNotFound):
		return &apierror.Error{Code: "not_found"}
	case errors.Is(err, administration.ErrBusy):
		return &apierror.Error{Code: "request_in_progress", RetryAfterSeconds: 2}
	case errors.Is(err, administration.ErrUnavailable):
		return &apierror.Error{Code: "administration_unavailable", RetryAfterSeconds: 2}
	default:
		return &apierror.Error{Code: "internal", Cause: err}
	}
}

func (d Dependencies) viewerCapabilities(r *http.Request) (CapabilitiesDocument, error) {
	p, err := d.foundationCaller(r)
	if err != nil {
		return CapabilitiesDocument{}, foundationAuthError(err)
	}
	out := CapabilitiesDocument{API: APIVersion{Major: 1, Level: 1}, Features: map[string]string{}, Limits: map[string]int{
		"pageSizeMax": catalog.BrowseMaximumLimit, "selectorIdsMax": 500, "longPollMaxMs": 25000,
		"queueKeysMax": playbackv1.MaxQueueKeys, "playlistWriteMax": catalog.MaxPlaylistEntries,
		"bulkJobsActiveMax": catalog.MaxActiveBulkJobs, "downloadBatchMax": downloads.MaxBatchTargets,
	}}
	// selectorIdsMax is the playbackv1 explicit-items cap (selectors.go: 500).
	available := map[string]bool{
		"downloads": d.Downloads != nil, "live_tv": d.LiveChannels != nil,
		"dvr": d.DVR != nil, "watch_together": true, "feedback": true,
	}
	// Owner switches: only transcoding has one (playback_owner_policy). No
	// owner switch exists for downloads, Live TV/DVR or Watch Together, so
	// those never report disabled_by_owner.
	transcodingEnabled := true
	if d.DB != nil {
		var enabled int
		if e := d.DB.QueryRowContext(r.Context(), `SELECT transcoding_enabled FROM playback_owner_policy WHERE singleton=1`).Scan(&enabled); e == nil {
			transcodingEnabled = enabled != 0
		} else if !errors.Is(e, sql.ErrNoRows) {
			return out, e
		}
	}
	// Precedence: unavailable > disabled_by_owner > not_permitted > enabled.
	if transcodingEnabled {
		out.Features["transcoding"] = "enabled"
		out.Features["subtitle_burn_in"] = "enabled"
	} else {
		out.Features["transcoding"] = "disabled_by_owner"
		out.Features["subtitle_burn_in"] = "disabled_by_owner"
	}
	// Playback Protocol v1 (sessions, queues, events) is a property of the server, not a
	// permission: clients switch their playback path on it, per media kind.
	out.Features["playback_v1"] = "enabled"
	// Protocol support for the opt-in server-composed Reading series row.
	// This does not grant access to any library or establish projection readiness.
	out.Features["reading_discover_series"] = "enabled"
	if d.LiveChannels != nil {
		out.Features["guide_windowed_directory"] = "enabled"
	}
	// The media kinds v1 sessions and queues play (F-title S20: music included, so an
	// artist, album or playlist plays in one request), comma-separated.
	out.Features["playback_v1_kinds"] = "movie,episode,video,audiobook,audiobook_file,book,song,track"
	// Queue transitions for rendered audio (spec §18): gapless and crossfade on v1.
	out.Features["queueTransitions"] = "enabled"
	// Live TV and Library Channels as v1 sessions (spec §18.6), where the channel
	// runtime runs: clients tune with POST /v1/playback/sessions {channelId}.
	if rt := d.PlaybackRuntime; rt != nil && rt.Channels != nil && rt.Channels.Configured() {
		out.Features["playback_v1_channels"] = "enabled"
	}
	if p.Role == "account" {
		for feature := range available {
			if !available[feature] {
				out.Features[feature] = "unavailable"
			} else {
				out.Features[feature] = "not_permitted"
			}
		}
		return out, nil
	}
	err = d.Identity.WithProfileRestrictions(r.Context(), p, func(policy identity.ProfileRestrictions) error {
		permitted := map[string]bool{
			"downloads": policy.AllowDownloads, "live_tv": policy.AllowLiveTV,
			"dvr": policy.AllowDVR, "watch_together": policy.AllowWatchTogether,
			"feedback": true, // not a profile permission: every profile may send feedback
		}
		for feature, allowed := range permitted {
			switch {
			case !available[feature]:
				out.Features[feature] = "unavailable"
			case !allowed:
				out.Features[feature] = "not_permitted"
			default:
				out.Features[feature] = "enabled"
			}
		}
		return nil
	})
	return out, err
}

func (d Dependencies) visibleLibraries(r *http.Request, p identity.Principal) ([]catalog.Library, error) {
	items, err := d.Catalog.WithContext(r.Context()).LibrariesForViewer(resourceActor(p))
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.ID)
	}
	allowed, err := d.allowedLibrarySet(r.Context(), p, ids)
	if err != nil {
		return nil, err
	}
	visible := make([]catalog.Library, 0, len(items))
	for _, item := range items {
		if allowed[item.ID] {
			visible = append(visible, item)
		}
	}
	return visible, nil
}

func (d Dependencies) bootstrap(r *http.Request) (BootstrapDocument, error) {
	p, err := d.foundationCaller(r)
	if err != nil {
		return BootstrapDocument{}, foundationAuthError(err)
	}
	if p.Role == "account" {
		return BootstrapDocument{}, &apierror.Error{Code: "not_permitted"}
	}
	deviceClass := r.URL.Query().Get("deviceClass")
	if deviceClass == "" {
		deviceClass = "web"
	}
	if deviceClass != "web" && deviceClass != "mobile" && deviceClass != "television" {
		return BootstrapDocument{}, &apierror.Error{Code: "invalid_request"}
	}
	capabilities, err := d.viewerCapabilities(r)
	if err != nil {
		return BootstrapDocument{}, &apierror.Error{Code: "internal", Cause: err}
	}
	libraries, err := d.visibleLibraries(r, p)
	if err != nil {
		return BootstrapDocument{}, &apierror.Error{Code: "internal", Cause: err}
	}
	authorize := d.consoleAuthority(p, false)
	preferences, err := d.Console.Preferences(r.Context(), p, deviceClass, authorize)
	if err != nil {
		return BootstrapDocument{}, &apierror.Error{Code: "internal", Cause: err}
	}
	homeLayout, err := d.Console.HomeLayout(r.Context(), p, authorize)
	if err != nil {
		return BootstrapDocument{}, &apierror.Error{Code: "internal", Cause: err}
	}
	return BootstrapDocument{
		Server:       ServerDocument{d.Identity.ID(), d.Identity.Name(), APIVersion{Major: 1, Level: 1}, d.Identity.SetupRequired()},
		Capabilities: capabilities, Me: BootstrapViewer{Viewer: p.Viewer}, Libraries: libraries,
		Preferences: preferences, HomeLayout: homeLayout,
	}, nil
}

// FoundationRegistry is used both by the live router and apigen. Contract
// generation never opens a database or invokes a handler.
func FoundationRegistry(d Dependencies) (*apikit.Registry, error) {
	// Integration merge: BE-playback's contextual v1 authorizer and error
	// catalogue (bf54a83) plus lane C's /v1/capabilities route (2a92fa3).
	registry := apikit.NewContextual(d.v1Authorize, policyCatalogue())
	registry.MetadataPolicy(userMutationMetadata)
	// Any change can meet a full writer queue; its typed answer is part of
	// every write route's contract.
	registry.WriteErrors("writer_capacity")
	// Any route can meet a WAL reset's brief reader pause.
	registry.RouteErrors("database_busy")
	registry.GateScopes(registryScopeGate)
	registry.RewritePathValues(d.mergedTitlePath)
	registry.RewriteRefs(apikit.Refs{Names: titleRefNames, Resolve: d.mergedTitleRefs})
	err := apikit.Register(registry, apikit.Route[struct{}, ServerDocument]{Metadata: apikit.Metadata{Audience: apikit.Integrations, Admission: laneDefault,
		ID: "get_server", Method: "GET", Path: "/v1/server", Summary: "Read server identity and API level", Access: apikit.Public, Requires: apikit.ScopeOpen, Lane: apikit.Default, Cost: apikit.Constant, Status: 200, Errors: []string{"internal"},
	}, Handler: func(context.Context, *http.Request, struct{}) (ServerDocument, error) {
		return ServerDocument{d.Identity.ID(), d.Identity.Name(), APIVersion{Major: 1, Level: 1}, d.Identity.SetupRequired()}, nil
	}})
	if err != nil {
		return nil, err
	}
	err = apikit.Register(registry, apikit.Route[access.InvitationPreviewRequest, access.InvitationPreview]{Metadata: apikit.Metadata{Audience: apikit.Clients, Admission: laneDefault,
		ID: "preview_invitation", Method: "POST", Path: "/v1/access/invitations/preview", Summary: "Preview an invitation without consuming its code", Access: apikit.Public, Requires: apikit.ScopeSession, Lane: apikit.Security, Cost: apikit.Constant, BodyLimit: 4096, Status: 200, Errors: []string{"invitation_not_found", "rate_limited", "invalid_request", "internal"},
	}, Handler: func(ctx context.Context, r *http.Request, body access.InvitationPreviewRequest) (access.InvitationPreview, error) {
		var empty access.InvitationPreview
		if d.Identity == nil || d.Access.Access == nil {
			return empty, &apierror.Error{Code: "internal"}
		}
		if err := d.Identity.SetupLimit(ctx, d.setupPeerSubject(r), "invitation", 10); err != nil {
			var rate *identity.SetupProtocolError
			if errors.As(err, &rate) && rate.Code == "rate_limited" {
				return empty, &apierror.Error{Code: "rate_limited", RetryAfterSeconds: rate.Interval}
			}
			return empty, &apierror.Error{Code: "internal", Cause: err}
		}
		preview, err := d.Access.Access.PreviewCapability(ctx, body)
		if errors.Is(err, access.ErrInvitationNotFound) {
			return empty, &apierror.Error{Code: "invitation_not_found"}
		}
		if err != nil {
			return empty, &apierror.Error{Code: "internal", Cause: err}
		}
		preview.ServerName = d.Identity.Name()
		return preview, nil
	}})
	if err != nil {
		return nil, err
	}
	err = apikit.Register(registry, apikit.Route[struct{}, administration.MaintenanceDocument]{Metadata: apikit.Metadata{Audience: apikit.Clients, Admission: laneAdmin,
		ID: "get_admin_maintenance_settings", Method: "GET", Path: "/v1/admin/maintenance/settings", Summary: "Read owner maintenance settings and choices", Access: apikit.Owner, Requires: apikit.ScopeAdmin, Lane: apikit.Default, Cost: apikit.Constant, Status: 200, Errors: []string{"unauthorized", "administration_unavailable", "internal"},
	}, Handler: func(ctx context.Context, r *http.Request, _ struct{}) (administration.MaintenanceDocument, error) {
		if d.Administration == nil {
			return administration.MaintenanceDocument{}, &apierror.Error{Code: "internal"}
		}
		p, _ := d.foundationCaller(r)
		out, err := d.Administration.MaintenanceSettings(ctx, d.administrationAuthority(p))
		if err != nil {
			return out, foundationAdministrationError(err)
		}
		if _, err = d.ownerContext(ctx, r); err != nil {
			return out, foundationAuthError(err)
		}
		return out, nil
	}})
	if err != nil {
		return nil, err
	}
	err = apikit.Register(registry, apikit.Route[administration.Change[administration.MaintenanceSettingsDocument], administration.MaintenanceDocument]{Metadata: apikit.Metadata{Audience: apikit.Clients, Admission: laneAdmin,
		ID: "put_admin_maintenance_settings", Method: "PUT", Path: "/v1/admin/maintenance/settings", Summary: "Save owner maintenance settings", Access: apikit.Owner, Requires: apikit.ScopeAdmin, Lane: apikit.Default, Cost: apikit.Constant, BodyLimit: 65536, Status: 200, Errors: []string{"unauthorized", "invalid_request", "revision_mismatch", "not_permitted", "not_found", "request_in_progress", "administration_unavailable", "internal"},
	}, Handler: func(ctx context.Context, r *http.Request, body administration.Change[administration.MaintenanceSettingsDocument]) (administration.MaintenanceDocument, error) {
		if d.Administration == nil {
			return administration.MaintenanceDocument{}, &apierror.Error{Code: "internal"}
		}
		p, _ := d.foundationCaller(r)
		out, err := d.Administration.SaveMaintenanceSettings(ctx, d.administrationAuthority(p), body)
		if err != nil {
			return out, foundationAdministrationError(err)
		}
		// Integration merge (edit 14): a saved background priority applies to
		// the running supervisor at once, as the legacy route did.
		if d.Storage != nil && d.Storage.Supervisor != nil {
			if err = d.Storage.Supervisor.SetBackgroundTaskPriority(dbwork.BackgroundPriorityForSpeed(out.Settings.BackgroundSpeed)); err != nil {
				return out, &apierror.Error{Code: "internal", Cause: err}
			}
		}
		if _, err = d.ownerContext(ctx, r); err != nil {
			return out, foundationAuthError(err)
		}
		return out, nil
	}})
	if err != nil {
		return nil, err
	}
	if err = registerResourceUsage(registry, d); err != nil {
		return nil, err
	}
	if err = registerActivitySummary(registry, d); err != nil {
		return nil, err
	}
	if err = registerBackgroundPause(registry, d); err != nil {
		return nil, err
	}
	err = apikit.Register(registry, apikit.Route[struct{}, administration.UpdateReport]{Metadata: apikit.Metadata{Audience: apikit.Clients, Admission: laneAdmin,
		ID: "get_admin_updates", Method: "GET", Path: "/v1/admin/updates", Summary: "Read the owner's cached release check", Access: apikit.Owner, Requires: apikit.ScopeAdmin, Lane: apikit.Default, Cost: apikit.Constant, Status: 200, Errors: []string{"unauthorized", "internal"},
	}, Handler: func(ctx context.Context, r *http.Request, _ struct{}) (administration.UpdateReport, error) {
		if d.Administration == nil {
			return administration.UpdateReport{}, &apierror.Error{Code: "internal"}
		}
		p, _ := d.foundationCaller(r)
		channel, feed := d.updatePolicy(ctx, p)
		info := buildinfo.Info()
		current := administration.UpdateBuild{Version: info["version"], BuildID: info["buildId"], SourceDigest: info["sourceDigest"], BuiltAt: info["builtAt"]}
		result := d.Administration.CachedUpdates(ctx, current, channel, feed)
		if _, err := d.ownerContext(ctx, r); err != nil {
			return administration.UpdateReport{}, foundationAuthError(err)
		}
		return result, nil
	}})
	if err != nil {
		return nil, err
	}
	// Check now, as the owner asks. The read path never performs network I/O;
	// this one does, inside the feed's own bounded fetch and retry rules, and
	// stores what it learns where the read reports look.
	err = apikit.Register(registry, apikit.Route[struct{}, administration.UpdateReport]{Metadata: apikit.Metadata{Audience: apikit.Clients, Admission: laneAdminHeavy,
		ID: "post_admin_updates_check", Method: "POST", Path: "/v1/admin/updates/check", Summary: "Check the release feed now and answer with what it lists", Access: apikit.Owner, Requires: apikit.ScopeAdmin, Lane: apikit.Default, Cost: apikit.Constant, OptionalBody: true, BodyLimit: 4096, Status: 200, Errors: []string{"unauthorized", "invalid_request", "internal"},
	}, Handler: func(ctx context.Context, r *http.Request, _ struct{}) (administration.UpdateReport, error) {
		if d.Administration == nil {
			return administration.UpdateReport{}, &apierror.Error{Code: "internal"}
		}
		p, _ := d.foundationCaller(r)
		channel, feed := d.updatePolicy(ctx, p)
		info := buildinfo.Info()
		current := administration.UpdateBuild{Version: info["version"], BuildID: info["buildId"], SourceDigest: info["sourceDigest"], BuiltAt: info["builtAt"]}
		result := d.Administration.CheckUpdates(ctx, current, channel, feed)
		if _, err := d.ownerContext(ctx, r); err != nil {
			return administration.UpdateReport{}, foundationAuthError(err)
		}
		return result, nil
	}})
	if err != nil {
		return nil, err
	}
	err = apikit.Register(registry, apikit.Route[struct{}, RatingSystemsDocument]{Metadata: apikit.Metadata{Audience: apikit.Clients, Admission: laneDefault,
		ID: "get_rating_systems", Method: "GET", Path: "/v1/rating-systems", Summary: "List supported content-rating systems", Access: apikit.Device, Requires: apikit.ScopeRead, Lane: apikit.Default, Cost: apikit.Constant, Status: 200, Errors: []string{"unauthorized", "internal"},
	}, Handler: func(ctx context.Context, _ *http.Request, _ struct{}) (RatingSystemsDocument, error) {
		// The list is part of the build: its digest is the validator.
		if apikit.Conditional(ctx, ratingSystemsTag) {
			return RatingSystemsDocument{}, nil
		}
		return RatingSystemsDocument{Items: identity.RatingSystems()}, nil
	}})
	if err != nil {
		return nil, err
	}
	err = apikit.Register(registry, apikit.Route[struct{}, identity.ProfileRestrictions]{Metadata: apikit.Metadata{Audience: apikit.Clients, Admission: laneSecurityFence,
		ID: "get_direct_profile_restrictions", Method: "GET", Path: "/v1/direct/profiles/{id}/restrictions", Summary: "Read a direct profile's content and feature restrictions", Access: apikit.Device, Requires: apikit.ScopeRead, Lane: apikit.Security, Cost: apikit.Constant, Status: 200, Errors: []string{"unauthorized", "password_change_required", "invalid_request", "internal"},
	}, Handler: func(ctx context.Context, r *http.Request, _ struct{}) (identity.ProfileRestrictions, error) {
		if d.Identity == nil {
			return identity.ProfileRestrictions{}, &apierror.Error{Code: "internal"}
		}
		out, err := d.Identity.ProfileRestrictions(ctx, directBearer(r), r.PathValue("id"))
		if err != nil {
			return out, foundationAuthError(err)
		}
		return out, nil
	}})
	if err != nil {
		return nil, err
	}
	err = apikit.Register(registry, apikit.Route[identity.ProfileRestrictionEdit, identity.ProfileRestrictions]{Metadata: apikit.Metadata{Audience: apikit.Clients, Admission: laneSecurityFence,
		ID: "put_direct_profile_restrictions", Method: "PUT", Path: "/v1/direct/profiles/{id}/restrictions", Summary: "Replace a direct profile's restrictions", Access: apikit.Admin, Requires: apikit.ScopeAdmin, AdminScope: "direct_account", Lane: apikit.Security, Cost: apikit.Constant, BodyLimit: 32768, Status: 200, Errors: []string{"unauthorized", "password_change_required", "not_permitted", "invalid_request", "revision_mismatch", "internal"},
	}, Handler: func(ctx context.Context, r *http.Request, edit identity.ProfileRestrictionEdit) (identity.ProfileRestrictions, error) {
		if d.Identity == nil {
			return identity.ProfileRestrictions{}, &apierror.Error{Code: "internal"}
		}
		out, err := d.Identity.SetProfileRestrictions(ctx, directBearer(r), r.PathValue("id"), edit)
		if err != nil {
			return out, foundationAuthError(err)
		}
		return out, nil
	}})
	if err != nil {
		return nil, err
	}
	err = apikit.Register(registry, apikit.Route[struct{}, identity.PINRecoveryMethods]{Metadata: apikit.Metadata{Audience: apikit.Clients, Admission: laneSecurityFence,
		ID: "get_direct_pin_recovery", Method: "GET", Path: "/v1/direct/pin-recovery", Summary: "Read available PIN reset confirmations", Access: apikit.Admin, Requires: apikit.ScopeSession, AdminScope: "direct_account", Lane: apikit.Security, Cost: apikit.Constant, Status: 200, Errors: []string{"unauthorized", "password_change_required", "factor_setup_required", "not_permitted", "internal"},
	}, Handler: func(ctx context.Context, r *http.Request, _ struct{}) (identity.PINRecoveryMethods, error) {
		if d.Identity == nil {
			return identity.PINRecoveryMethods{}, &apierror.Error{Code: "internal"}
		}
		out, err := d.Identity.PINRecovery(ctx, directBearer(r))
		if err != nil {
			return out, foundationAuthError(err)
		}
		return out, nil
	}})
	if err != nil {
		return nil, err
	}
	err = apikit.Register(registry, apikit.Route[struct{}, ProfileAvatarsDocument]{Metadata: apikit.Metadata{Audience: apikit.Clients, Admission: laneAuth,
		ID: "get_direct_profile_avatars", Method: "GET", Path: "/v1/direct/profiles/avatars", Summary: "List this direct account's profile pictures", Access: apikit.Device, Requires: apikit.ScopeRead, Lane: apikit.Default, Cost: apikit.Constant, Status: 200, Errors: []string{"unauthorized", "password_change_required", "internal"},
	}, Handler: func(ctx context.Context, r *http.Request, _ struct{}) (ProfileAvatarsDocument, error) {
		if d.Identity == nil {
			return ProfileAvatarsDocument{}, &apierror.Error{Code: "internal"}
		}
		records, err := d.Identity.ProfileAvatars(ctx, directBearer(r))
		if err != nil {
			return ProfileAvatarsDocument{}, foundationAuthError(err)
		}
		out := ProfileAvatarsDocument{Items: make([]identity.ProfileAvatar, 0, len(records))}
		for _, record := range records {
			out.Items = append(out.Items, record)
		}
		sort.Slice(out.Items, func(i, j int) bool { return out.Items[i].ProfileID < out.Items[j].ProfileID })
		return out, nil
	}})
	if err != nil {
		return nil, err
	}
	err = apikit.Register(registry, apikit.Route[DirectPINResetRequest, identity.DirectSnapshot]{Metadata: apikit.Metadata{Audience: apikit.Clients, Admission: laneSecurityFence,
		ID: "post_direct_profile_pin_reset", Method: "POST", Path: "/v1/direct/profiles/{id}/pin-reset", Summary: "Clear or replace a direct profile PIN", Access: apikit.Admin, Requires: apikit.ScopeAdmin, AdminScope: "direct_account", Lane: apikit.Security, Cost: apikit.Constant, BodyLimit: 4096, Status: 200, Errors: []string{"unauthorized", "password_change_required", "factor_setup_required", "not_permitted", "invalid_request", "request_in_progress", "internal"},
	}, Handler: func(ctx context.Context, r *http.Request, body DirectPINResetRequest) (identity.DirectSnapshot, error) {
		if d.Identity == nil {
			return identity.DirectSnapshot{}, &apierror.Error{Code: "internal"}
		}
		out, err := d.Identity.PINReset(ctx, directBearer(r), r.PathValue("id"), body.PIN)
		if err != nil {
			return out, foundationAuthError(err)
		}
		return out, nil
	}})
	if err != nil {
		return nil, err
	}
	err = apikit.Register(registry, apikit.Route[struct{}, DirectSessionsDocument]{Metadata: apikit.Metadata{Audience: apikit.Clients, Admission: laneAuth,
		ID: "get_direct_sessions", Method: "GET", Path: "/v1/direct/sessions", Summary: "Read this direct account's active and recently signed-out sessions", Access: apikit.Admin, Requires: apikit.ScopeAdmin, AdminScope: "direct_account", Lane: apikit.Security, Cost: apikit.PageSized, Status: 200, Errors: []string{"unauthorized", "password_change_required", "not_permitted", "internal"},
	}, Handler: func(ctx context.Context, r *http.Request, _ struct{}) (DirectSessionsDocument, error) {
		if d.Identity == nil {
			return DirectSessionsDocument{}, &apierror.Error{Code: "internal"}
		}
		items, err := d.Identity.DirectSessions(ctx, directBearer(r))
		if err != nil {
			return DirectSessionsDocument{}, foundationAuthError(err)
		}
		return DirectSessionsDocument{Items: items}, nil
	}})
	if err != nil {
		return nil, err
	}
	err = apikit.Register(registry, apikit.Route[struct{}, struct{}]{Metadata: apikit.Metadata{Audience: apikit.Clients, Admission: laneSecurityFence,
		ID: "delete_direct_session", Method: "DELETE", Path: "/v1/direct/sessions/{id}", Summary: "Sign out one direct-account session", Access: apikit.Admin, Requires: apikit.ScopeAdmin, AdminScope: "direct_account", Lane: apikit.Security, Cost: apikit.Constant, Status: 204, Errors: []string{"unauthorized", "password_change_required", "not_permitted", "invalid_request", "internal"},
	}, Handler: func(ctx context.Context, r *http.Request, _ struct{}) (struct{}, error) {
		if d.Identity == nil {
			return struct{}{}, &apierror.Error{Code: "internal"}
		}
		id := r.PathValue("id")
		if id == "all" {
			id = ""
		}
		if err := d.Identity.RevokeDirectSessions(ctx, directBearer(r), id); err != nil {
			return struct{}{}, foundationAuthError(err)
		}
		return struct{}{}, nil
	}})
	if err != nil {
		return nil, err
	}
	err = apikit.Register(registry, apikit.Route[struct{}, struct{}]{Metadata: apikit.Metadata{Audience: apikit.Clients, Admission: laneAuth,
		ID: "post_direct_sign_out_everywhere", Method: "POST", Path: "/v1/direct/sessions/sign-out-everywhere", Summary: "Sign this direct account out of every device", Access: apikit.Admin, Requires: apikit.ScopeSession, AdminScope: "direct_account", Lane: apikit.Security, Cost: apikit.Constant, Status: 204, Errors: []string{"unauthorized", "password_change_required", "not_permitted", "internal"},
	}, Handler: func(ctx context.Context, r *http.Request, _ struct{}) (struct{}, error) {
		if d.Identity == nil {
			return struct{}{}, &apierror.Error{Code: "internal"}
		}
		if err := d.Identity.SignOutEverywhere(ctx, directBearer(r)); err != nil {
			return struct{}{}, foundationAuthError(err)
		}
		return struct{}{}, nil
	}})
	if err != nil {
		return nil, err
	}
	err = apikit.Register(registry, apikit.Route[struct{}, identity.DirectSnapshot]{Metadata: apikit.Metadata{Audience: apikit.Clients, Admission: laneDefault,
		ID: "get_direct_account", Method: "GET", Path: "/v1/direct", Summary: "Read the current direct account and its profiles", Access: apikit.Device, Requires: apikit.ScopeRead, Lane: apikit.Security, Cost: apikit.Constant, Status: 200, Errors: []string{"unauthorized", "password_change_required", "internal"},
	}, Handler: func(ctx context.Context, r *http.Request, _ struct{}) (identity.DirectSnapshot, error) {
		if d.Identity == nil {
			return identity.DirectSnapshot{}, &apierror.Error{Code: "internal"}
		}
		out, err := d.Identity.DirectMe(ctx, directBearer(r))
		if err != nil {
			return out, foundationAuthError(err)
		}
		return out, nil
	}})
	if err != nil {
		return nil, err
	}
	err = apikit.Register(registry, apikit.Route[struct{}, identity.RegistrationPolicy]{Metadata: apikit.Metadata{Audience: apikit.Clients, Admission: laneAuth,
		ID: "get_direct_registration_policy", Method: "GET", Path: "/v1/direct/registration-policy", Summary: "Read owner sign-up and device-approval policy", Access: apikit.Owner, Requires: apikit.ScopeAdmin, Lane: apikit.Security, Cost: apikit.Constant, Status: 200, Errors: []string{"unauthorized", "not_permitted", "internal"},
	}, Handler: func(ctx context.Context, _ *http.Request, _ struct{}) (identity.RegistrationPolicy, error) {
		if d.Identity == nil {
			return identity.RegistrationPolicy{}, &apierror.Error{Code: "internal"}
		}
		out, err := d.Identity.RegistrationPolicySnapshot(ctx)
		if err != nil {
			return out, &apierror.Error{Code: "internal", Cause: err}
		}
		return out, nil
	}})
	if err != nil {
		return nil, err
	}
	err = apikit.Register(registry, apikit.Route[identity.RegistrationPolicy, identity.RegistrationPolicy]{Metadata: apikit.Metadata{Audience: apikit.Clients, Admission: laneAuth,
		ID: "put_direct_registration_policy", Method: "PUT", Path: "/v1/direct/registration-policy", Summary: "Save owner sign-up and device-approval policy", Access: apikit.Owner, Requires: apikit.ScopeAdmin, Lane: apikit.Security, Cost: apikit.Constant, BodyLimit: 4096, Status: 200, Errors: []string{"unauthorized", "not_permitted", "invalid_request", "internal"},
	}, Handler: func(ctx context.Context, _ *http.Request, body identity.RegistrationPolicy) (identity.RegistrationPolicy, error) {
		if d.Identity == nil {
			return identity.RegistrationPolicy{}, &apierror.Error{Code: "internal"}
		}
		out, err := d.Identity.SaveRegistrationPolicy(ctx, body)
		if err != nil {
			return out, foundationAuthError(err)
		}
		return out, nil
	}})
	if err != nil {
		return nil, err
	}
	err = apikit.Register(registry, apikit.Route[struct{}, identity.AuthCapabilities]{Metadata: apikit.Metadata{Audience: apikit.Clients, Admission: laneAuth,
		ID: "get_auth_capabilities", Method: "GET", Path: "/v1/auth/capabilities", Summary: "Read the server's available sign-in methods", Access: apikit.Public, Requires: apikit.ScopeOpen, Lane: apikit.Default, Cost: apikit.Constant, Status: 200, Errors: []string{"internal"},
	}, Handler: func(ctx context.Context, _ *http.Request, _ struct{}) (identity.AuthCapabilities, error) {
		if d.Identity == nil {
			return identity.AuthCapabilities{}, &apierror.Error{Code: "internal"}
		}
		out, err := d.Identity.Capabilities(ctx)
		if err != nil {
			return out, &apierror.Error{Code: "internal", Cause: err}
		}
		return out, nil
	}})
	if err != nil {
		return nil, err
	}
	err = apikit.Register(registry, apikit.Route[struct{}, CapabilitiesDocument]{Metadata: apikit.Metadata{Audience: apikit.Integrations, Admission: laneDefault,
		ID: "get_capabilities", Method: "GET", Path: "/v1/capabilities", Summary: "Read this viewer's feature states and published limits", Access: apikit.Device, Requires: apikit.ScopeRead, Lane: apikit.Default, Cost: apikit.Constant, Status: 200, Errors: []string{"unauthorized", "internal"},
	}, Handler: func(_ context.Context, r *http.Request, _ struct{}) (CapabilitiesDocument, error) {
		return d.viewerCapabilities(r)
	}})
	if err != nil {
		return nil, err
	}
	err = apikit.Register(registry, apikit.Route[struct{}, BootstrapDocument]{Metadata: apikit.Metadata{Audience: apikit.Clients, Admission: laneBrowsing,
		ID: "get_bootstrap", Method: "GET", Path: "/v1/bootstrap", Summary: "Read this viewer's startup state", Access: apikit.Device, Requires: apikit.ScopeRead, Lane: apikit.Browsing, Cost: apikit.PageSized, Status: 200, Query: []string{"deviceClass"}, Errors: []string{"unauthorized", "not_permitted", "invalid_request", "internal"},
	}, Handler: func(_ context.Context, r *http.Request, _ struct{}) (BootstrapDocument, error) {
		return d.bootstrap(r)
	}})
	if err != nil {
		return nil, err
	}
	if err := registerConnectionSettings(registry, d); err != nil {
		return nil, err
	}
	if err := registerSetupTransport(registry, d); err != nil {
		return nil, err
	}
	if err := registerIdentityProof(registry, d); err != nil {
		return nil, err
	}
	if err := registerConnectivity(registry, d); err != nil {
		return nil, err
	}
	registerPlaybackV1(registry, d)
	if err := registerBackups(registry, d); err != nil {
		return nil, err
	}
	if err := registerClientReports(registry, d); err != nil {
		return nil, err
	}
	registerBulkJobs(registry, d)
	registerBulkMetadataPreview(registry, d)
	registerFileInfo(registry, d)
	registerAppReports(registry, d)
	registerHomeDestinationPicker(registry, d)
	registerHomeDestinationReceipt(registry, d)
	if err := registerPersonalReads(registry, d); err != nil {
		return nil, err
	}
	registerDownloadRequests(registry, d)
	if err := registerSelectionActions(registry, d); err != nil {
		return nil, err
	}
	registerDownloads(registry, d)
	registerSocial(registry, d)
	registerPlaylistWindows(registry, d)
	registerScanLoop(registry, d)
	registerMetadataAttention(registry, d)
	registerMetadataSettings(registry, d)
	registerMetadataRefresh(registry, d)
	registerSourceReadSettings(registry, d)
	registerSourceFolders(registry, d)
	registerSourceStatus(registry, d)
	registerSourceTest(registry, d)
	registerSourceAdvanced(registry, d)
	registerSourceProgram(registry, d)
	registerDAVConnectionTest(registry, d)
	registerRcloneConnectionTest(registry, d)
	registerRcloneConfigurationProviders(registry, d)
	registerRcloneSourceSave(registry, d)
	registerRcloneConfigSource(registry, d)
	registerDriveOAuth(registry, d)
	registerScreenMetadataReads(registry, d)
	registerFreshnessRoutes(registry, d)
	registerCapabilityReport(registry)
	registerHardwareFacts(registry)
	registerLiveContracts(registry, d)
	registerIdentityRoutes(registry, d)
	// Lane C's /v1/events (886f5011) on the shared contextual registry.
	err = apikit.Register(registry, apikit.Route[struct{}, eventfeed.Page]{Metadata: apikit.Metadata{Audience: apikit.Integrations, Admission: laneRealtime,
		ID: "get_events", Method: "GET", Path: "/v1/events", Summary: "Read the authorized event ring by long-poll or server-sent stream", Access: apikit.Device, Requires: apikit.ScopeRead, Lane: apikit.Realtime, Cost: apikit.Constant, Status: 200, SSE: true, Query: []string{"after", "waitMs"}, Errors: []string{"unauthorized", "invalid_request", "invalid_cursor", "event_wait_limit", "event_stream_exists", "stream_unavailable", "internal"},
	}, RawHandler: func(w http.ResponseWriter, r *http.Request) {
		if d.Events == nil {
			registry.Errors().Write(w, "", &apierror.Error{Code: "internal"})
			return
		}
		d.events(w, r)
	}})
	if err != nil {
		return nil, err
	}
	if err := registerDeviceRoutes(registry, d); err != nil {
		return nil, err
	}
	if err := d.registerSubtitleRoutes(registry); err != nil {
		return nil, err
	}
	if err := d.registerTrickplayImageRoutes(registry); err != nil {
		return nil, err
	}
	if err := registerCredentialRoutes(registry, d); err != nil {
		return nil, err
	}
	if err := registerIntegrationCredentials(registry, d); err != nil {
		return nil, err
	}
	if err := registerIntegrationReads(registry, d); err != nil {
		return nil, err
	}
	if err := registerIntegrationSubtitleValidation(registry, d); err != nil {
		return nil, err
	}
	if err := registerCompatibilityRoutes(registry, d); err != nil {
		return nil, err
	}
	if err := registerPlatformRoutes(registry, d); err != nil {
		return nil, err
	}
	if err := registerWebhookRoutes(registry, d); err != nil {
		return nil, err
	}
	if err := registerMemberRoutes(registry, d); err != nil {
		return nil, err
	}
	if err := registerRememberedAccountRoutes(registry, d); err != nil {
		return nil, err
	}
	if err := registerMCP(registry, d); err != nil {
		return nil, err
	}
	if err := registerReadingBrowseQueryRoutes(registry, d); err != nil {
		return nil, err
	}
	if err := registerReadingFacetQueryRoutes(registry, d); err != nil {
		return nil, err
	}
	if err := registerBrowseSelectionQueryRoutes(registry, d); err != nil {
		return nil, err
	}
	if err := registerBrowseCountRoutes(registry, d); err != nil {
		return nil, err
	}
	if err := registerSearchSortRoutes(registry, d); err != nil {
		return nil, err
	}
	if err := registerUnitProjections(registry, d); err != nil {
		return nil, err
	}
	if err := registerSettingsConfiguration(registry, d); err != nil {
		return nil, err
	}
	if err := registerConversionSettings(registry, d); err != nil {
		return nil, err
	}
	if err := registerHTTPContracts(registry); err != nil {
		return nil, err
	}
	if err := registerUserMutationRoutes(registry, d); err != nil {
		return nil, err
	}
	registry.Freeze()
	return registry, nil
}

func (d Dependencies) foundationRoutes(mux *http.ServeMux) {
	registry := d.registry
	if registry == nil {
		var err error
		if registry, err = FoundationRegistry(d); err != nil {
			panic(err)
		}
	}
	d.mountRegistry(mux, registry)
}

// ratingSystemsTag is the validator of GET /v1/rating-systems, which only a
// new build changes.
var ratingSystemsTag = func() string {
	raw, _ := json.Marshal(identity.RatingSystems())
	sum := sha256.Sum256(raw)
	return "ratings-" + hex.EncodeToString(sum[:8])
}()
