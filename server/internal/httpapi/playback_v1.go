package httpapi

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strings"

	"github.com/PorticoMediaServer/portico/apikit"
	"github.com/PorticoMediaServer/portico/apikit/apierror"
	"github.com/PorticoMediaServer/portico/server/internal/access"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/hosted"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/playback"
	"github.com/PorticoMediaServer/portico/server/internal/playbackv1"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

// Playback Protocol v1 (Spec — Playback Protocol v1) is served from the typed
// apikit registry: every route declares its access rule, lane, cost class and
// errors, and the authorizer below enforces the rule before a body is read.

// v1Caller is the admitted principal and the device its token is bound to.
type v1Caller struct {
	identity.Principal
	DeviceID string
}
type v1CallerKey struct{}

func callerFrom(ctx context.Context) v1Caller {
	c, _ := ctx.Value(v1CallerKey{}).(v1Caller)
	return c
}

// v1ErrorDefinitions are the route errors v1 playback adds to the defaults.
// Messages are US English fallbacks; clients localise by code.
func v1ErrorDefinitions() []apierror.Definition {
	return []apierror.Definition{
		{Code: "idempotency_conflict", Status: 409, Message: "This request key belongs to a different change.", Retry: apierror.Never},
		{Code: "receipt_expired", Status: 410, Message: "This change needs review because its receipt is no longer available.", Retry: apierror.Never},
		{Code: "receipt_unknown", Status: 404, Message: "No receipt is available for this change yet.", Retry: apierror.SameRequest},
		{Code: "invalid_mutation_key", Status: 400, Message: "The change could not be identified. Refresh before trying again.", Retry: apierror.Never},

		{Code: "timeout", Status: 503, Message: "The operation timed out. Retry shortly.", Retry: apierror.SameRequest},
		// The writer refused the change before it began: nothing was saved, and
		// sending the same request again after Retry-After is safe.
		{Code: "writer_capacity", Status: 503, Message: "The server is busy saving other changes. Try again shortly.", Retry: apierror.SameRequest},
		// A new read refused while the database resets its write-ahead log:
		// nothing ran, and the same request can be sent after Retry-After.
		{Code: "database_busy", Status: 503, Message: "The server is briefly busy maintaining its database. Try again shortly.", Retry: apierror.SameRequest},
		{Code: "access_refused", Status: 403, Message: identity.ErrAccessRefused.Error(), Retry: apierror.Never},
		{Code: "move_collision", Status: 409, Message: identity.ErrMoveCollision.Error(), Retry: apierror.Never},
		{Code: "move_confirmation", Status: 409, Message: identity.ErrMoveConfirmation.Error(), Retry: apierror.Never},
		{Code: "claim_move_required", Status: 409, Message: "In Settings, change sign-in from Local to Portico Account and confirm the move before connecting this server.", Retry: apierror.Never},
		{Code: "move_code_invalid", Status: 410, Message: identity.ErrMoveCode.Error(), Retry: apierror.Never},
		{Code: "account_move_waiting", Status: 403, Message: identity.ErrMoveWaiting.Error(), Retry: apierror.Never},
		{Code: "factor_setup_required", Status: 403, Message: identity.ErrFactorSetupRequired.Error(), Retry: apierror.Never},
		{Code: "sign_in_kind_portico", Status: 403, Message: identity.ErrSignInKindPortico.Error(), Retry: apierror.Never},
		{Code: "sign_in_kind_local", Status: 403, Message: identity.ErrSignInKindLocal.Error(), Retry: apierror.Never},
		{Code: "portico_services_unavailable", Status: 503, Message: hosted.ErrServicesUnavailable.Error(), Retry: apierror.SameRequest},
		{Code: "idempotency_key_required", Status: 428, Message: "Send an Idempotency-Key with this request.", Retry: apierror.Never},
		{Code: "session_ended", Status: 410, Message: "This playback has ended.", Retry: apierror.Never},
		// Playback observations (P2-19/20). A client that cannot report is
		// refused before any session exists; clients localise by code.
		{Code: "client_update_required", Status: 400, Message: "Update the app to play on this server.", Retry: apierror.Never},
		{Code: "observation_epoch_changed", Status: 409, Message: "Another player now reports this playback. Claim it again to continue.", Retry: apierror.AfterRefresh},
		{Code: "observation_owned", Status: 409, Message: "Another player on this device reports this playback.", Retry: apierror.Never},
		{Code: "observation_capacity", Status: 503, Message: "The server is following as many players as it can. Try again shortly.", Retry: apierror.SameRequest},
		{Code: "conversion_capacity", Status: 429, Message: "This server is already converting as many streams as it is allowed. Wait a moment or stop another stream, then try again.", Retry: apierror.SameRequest},
		{Code: "stream_limit_reached", Status: 409, Message: "This account is already playing as many streams as it is allowed.", Retry: apierror.Never},
		{Code: "transcode_not_allowed", Status: 403, Message: "This title needs converting, and converting is turned off for this account.", Retry: apierror.Never},
		{Code: "source_unavailable", Status: 503, Message: "The file for this title can't be reached right now.", Retry: apierror.SameRequest},
		// The selected storage is offline, while the binding's identity, lifetime,
		// root and authorization all still hold: the same request is worth retrying
		// unchanged when the storage reconnects.
		{Code: "storage_offline", Status: 503, Message: "This title’s storage is offline right now.", Retry: apierror.SameRequest},
		{Code: "unsupported_media", Status: 422, Message: "This title can't be played on this device.", Retry: apierror.Never},
		{Code: "connection_too_slow", Status: 422, Message: "Your connection to this server is too slow for this quality. Choose a lower quality or Automatic.", Retry: apierror.Never},
		{Code: "feature_restricted", Status: 403, Message: "This feature is not permitted for this profile.", Retry: apierror.Never},
		{Code: "queue_ended", Status: 409, Message: "There is nothing after this in the queue.", Retry: apierror.Never},
		{Code: "unsupported_selector", Status: 422, Message: "This selection can't be played as a queue.", Retry: apierror.Never},
		{Code: "queue_too_large", Status: 422, Message: "This queue is too long. Remove some of it or start a new one.", Retry: apierror.Never},
		{Code: "prepare_not_allowed", Status: 409, Message: "The next item can't be prepared now.", Retry: apierror.AfterRefresh},
		{Code: "prepared_expired", Status: 410, Message: "The prepared next item expired.", Retry: apierror.Never},
		{Code: "prepared_canceled", Status: 409, Message: "The prepared next item was canceled.", Retry: apierror.Never},
		{Code: "not_implemented", Status: 501, Message: "This server doesn't offer this yet.", Retry: apierror.Never},
		{Code: "playlist_capacity", Status: 409, Message: "This is more than a playlist can hold.", Retry: apierror.Never},
		{Code: "queue_building", Status: 503, Message: "This part of the queue is still loading. Try again in a moment.", Retry: apierror.SameRequest},
		{Code: "visibility_building", Status: 503, Message: "Library visibility is being prepared. Retry shortly.", Retry: apierror.SameRequest},
		{Code: "device_not_found", Status: 404, Message: "This device isn't available.", Retry: apierror.Never},
		{Code: "not_controllable", Status: 403, Message: "This device can't be controlled from here.", Retry: apierror.Never},
		{Code: "stream_exists", Status: 409, Message: "This device already has an event stream open.", Retry: apierror.Never},
		// Lane C /v1/events (886f5011). C81: its own code; BE-playback's
		// stream_exists never retries, this one reconnects with the last ID.
		{Code: "event_stream_exists", Status: 409, Message: "An existing stream was retired; reconnect using your last event ID.", Retry: apierror.SameRequest},
		{Code: "stream_unavailable", Status: 503, Message: "This connection cannot stream events.", Retry: apierror.SameRequest},
		// SRV017: a fifth parked long poll on one account and installation.
		// The client keeps its cursor and reads without waiting until it may
		// park again; Retry-After is 2 s.
		{Code: "event_wait_limit", Status: 429, Message: "This device already has its event waits open. Read without waiting, then wait again.", Retry: apierror.SameRequest},
		{Code: "administration_unavailable", Status: 503, Message: "Administration is temporarily unavailable.", Retry: apierror.SameRequest},
		{Code: "confirmation_mismatch", Status: 409, Message: "Type the name exactly as shown to confirm.", Retry: apierror.Never},
		{Code: "password_change_required", Status: 403, Message: "Change your password before continuing.", Retry: apierror.Never},
		// A PIN change elsewhere: this device enters the profile's PIN again (C-DESIGN §4).
		{Code: "profile_pin_reentry", Status: 403, Message: identity.ErrProfilePINReentry.Error(), Retry: apierror.Never},
		// A remembered main profile enters its PIN again before it manages the account.
		{Code: "profile_pin_manage", Status: 403, Message: identity.ErrProfilePINManage.Error(), Retry: apierror.Never},
		{Code: "no_tuner_available", Status: 409, Message: "Every tuner is in use.", Retry: apierror.AfterRefresh},
		// Channels on v1 sessions (§18.6).
		{Code: "channel_unavailable", Status: 503, Message: "This channel can't be played right now.", Retry: apierror.SameRequest},
		{Code: "seek_unavailable", Status: 409, Message: "That position isn't in the channel's buffer.", Retry: apierror.AfterRefresh},
		// Lane C invitation preview (33129079).
		{Code: "invitation_not_found", Status: 404, Message: "This invitation is not available.", Retry: apierror.Never},
		{Code: "rate_limited", Status: 429, Message: "Too many attempts. Try again later.", Retry: apierror.SameRequest},
		// Plex-model server backups (spec §4): owner-only, published with the
		// API registry.
		{Code: "client_log_budget", Status: 429, Message: "The daily client log limit has been reached.", Retry: apierror.Never},
		{Code: "device_session_required", Status: 403, Message: "Sign in on this device to upload logs.", Retry: apierror.Never},
		{Code: "restore_lock_unavailable", Status: 409, Message: "Portico cannot safely restore data in this folder. Use a folder on a local disk and try again.", Retry: apierror.Never},
		{Code: "restore_source_invalid", Status: 400, Message: "This restore source can't be used. Pick one of the listed backups or a Portico database file on the server.", Retry: apierror.Never},
		{Code: "restore_pre_release", Status: 409, Message: "This backup does not match this pre-release Portico baseline and can't be restored.", Retry: apierror.Never},
		{Code: "restore_integrity_failed", Status: 422, Message: "This backup failed its integrity check and can't be restored.", Retry: apierror.Never},
		{Code: "restore_not_portico", Status: 422, Message: "This file is not a Portico database.", Retry: apierror.Never},
		{Code: "insufficient_disk", Status: 507, Message: "There isn't enough free disk space for this operation.", Retry: apierror.SameRequest},
	}
}

func v1Catalogue() *apierror.Registry {
	catalogue, err := apierror.New(v1ErrorDefinitions()...)
	if err != nil {
		panic(err)
	}
	catalogue.Translate(databaseRefusal)
	return catalogue
}

// databaseRefusal names the typed answer for work the database refused before
// it began: a write the writer turned away (a full queue) or that met a lock
// after admission, answered writer_capacity with the writer's own estimate of
// when its queue drains (1-30 s); or a new read refused while a WAL reset held
// reader admission, answered database_busy.
func databaseRefusal(err error) *apierror.Error {
	if mutation := userMutationError(err); mutation != nil {
		return mutation
	}
	switch {
	case dbwork.Overloaded(err):
		return &apierror.Error{Code: "writer_capacity", RetryAfterSeconds: dbwork.WriteGate().RetryAfterSeconds(), Cause: err}
	case errors.Is(err, dbwork.ErrReadPressure):
		return &apierror.Error{Code: "database_busy", RetryAfterSeconds: 1, Cause: err}
	case errors.Is(err, dbwork.ErrLongReadBusy):
		// Another whole-database read (an integrity check, another preview)
		// holds the one permit; it ends on its own.
		return &apierror.Error{Code: "database_busy", RetryAfterSeconds: 5, Cause: err}
	}
	return nil
}

// v1Error maps a domain or service error to a registered API error. Restricted
// content is not_found on every path (SEC-02).
func v1Error(err error) error {
	if mutation := userMutationError(err); mutation != nil {
		return mutation
	}

	var public *apierror.Error
	if err == nil || errors.As(err, &public) {
		return err
	}
	if errors.Is(err, identity.ErrProfilePINReentry) {
		return &apierror.Error{Code: "profile_pin_reentry", Cause: err}
	}
	if errors.Is(err, identity.ErrProfilePINManage) {
		return &apierror.Error{Code: "profile_pin_manage", Cause: err}
	}
	var field *playbackv1.FieldError
	var refused playback.ErrDeliveryRefused
	var stale *playbackv1.RevisionError
	var fault *playback.ControlFault
	if errors.As(err, &stale) {
		return &apierror.Error{Code: "revision_mismatch", Current: stale.Current, Cause: err}
	}
	code := ""
	switch {
	case errors.Is(err, catalog.ErrDiscSelectionChanged):
		code = "revision_mismatch"
	case errors.Is(err, storage.ErrStorageOffline):
		code = "storage_offline"
	case errors.Is(err, catalog.ErrSourceBusy), errors.Is(err, storage.ErrPlaybackSource), errors.Is(err, storage.ErrPlaybackTimeout), errors.Is(err, storage.ErrBusy):
		code = "source_unavailable"
	case errors.Is(err, catalog.ErrDiscPreparing):
		code = "source_unavailable"
	case errors.As(err, &field):
		fields := []apierror.Field{}
		if field.Path != "" {
			fields = append(fields, apierror.Field{Path: field.Path, Code: "invalid", Message: "Check this value."})
		}
		return &apierror.Error{Code: "invalid_request", Fields: fields, Cause: err}
	case errors.Is(err, identity.ErrNotVisible):
		code = "not_found"
	case errors.Is(err, identity.ErrFactorSetupRequired):
		code = "factor_setup_required"
	case errors.Is(err, identity.ErrForbidden):
		code = "not_permitted"
	case errors.Is(err, identity.ErrUnauthorized):
		code = "unauthorized"
	case errors.Is(err, identity.ErrContentRestricted), errors.Is(err, access.ErrChannelDenied),
		errors.Is(err, sql.ErrNoRows), errors.Is(err, playbackv1.ErrNotFound):
		code = "not_found"
	case errors.Is(err, errFeatureRestricted):
		code = "feature_restricted"
	case errors.Is(err, playback.ErrOwnerAccountCap):
		return &apierror.Error{Code: "stream_limit_reached", Reason: "owner_account_cap", Cause: err}
	case errors.Is(err, playback.ErrOwnerServerCap):
		return &apierror.Error{Code: "stream_limit_reached", Reason: "owner_server_cap", Cause: err}
	case errors.Is(err, access.ErrStreamLimit), errors.Is(err, access.ErrSchedule):
		code = "stream_limit_reached"
	case errors.Is(err, playback.ErrConversionCapacity):
		return &apierror.Error{Code: "conversion_capacity", RetryAfterSeconds: 2, Cause: err}
	case errors.Is(err, playback.ErrTranscodingDisabled):
		code = "transcode_not_allowed"
	case errors.As(err, &refused), errors.Is(err, playback.ErrIncompatible), errors.Is(err, playback.ErrQualityUnavailable):
		code = "unsupported_media"
	case errors.Is(err, playbackv1.ErrEnded):
		code = "session_ended"
	case errors.Is(err, playbackv1.ErrClientUpdateRequired):
		code = "client_update_required"
	case errors.Is(err, playbackv1.ErrObservationEpoch):
		code = "observation_epoch_changed"
	case errors.Is(err, playbackv1.ErrObservationOwned):
		code = "observation_owned"
	case errors.Is(err, playbackv1.ErrObservationsFull):
		return &apierror.Error{Code: "observation_capacity", RetryAfterSeconds: 2, Cause: err}
	case errors.Is(err, playbackv1.ErrRevisionRequired):
		code = "revision_required"
	case errors.Is(err, playbackv1.ErrRevision):
		code = "revision_mismatch"
	case errors.Is(err, playbackv1.ErrIdempotencyMismatch):
		code = "idempotency_key_reused"
	case errors.Is(err, playbackv1.ErrKeyRequired):
		code = "idempotency_key_required"
	case errors.Is(err, playbackv1.ErrQueueEnded):
		code = "queue_ended"
	case errors.Is(err, playbackv1.ErrUnsupportedSelector):
		code = "unsupported_selector"
	case errors.Is(err, playbackv1.ErrQueueTooLarge):
		code = "queue_too_large"
	case errors.Is(err, playbackv1.ErrQueueBuilding):
		code = "queue_building"
	case errors.Is(err, catalog.ErrVisibilityBuilding):
		code = "visibility_building"
	case errors.Is(err, catalog.ErrPlaylistCapacity):
		code = "playlist_capacity"
	case errors.Is(err, playbackv1.ErrPrepareNotAllowed):
		code = "prepare_not_allowed"
	case errors.Is(err, playbackv1.ErrPreparedExpired):
		code = "prepared_expired"
	case errors.Is(err, playbackv1.ErrPreparedCanceled):
		code = "prepared_canceled"
	case errors.Is(err, playbackv1.ErrNotImplemented):
		code = "not_implemented"
	case errors.Is(err, playbackv1.ErrNotControllable):
		code = "not_controllable"
	case errors.Is(err, playbackv1.ErrStreamExists):
		code = "stream_exists"
	case errors.Is(err, identity.ErrDeviceUnknown):
		code = "device_not_found"
	case errors.Is(err, playbackv1.ErrNoTuner):
		code = "no_tuner_available"
	case errors.Is(err, playbackv1.ErrChannelsUnavailable):
		code = "channel_unavailable"
	case errors.As(err, &fault):
		switch fault.Code {
		case "not_found", "access_revoked":
			code = "not_found" // SEC-02: a channel the viewer can't see isn't there
		case "lease_expired", "playback_ended":
			code = "session_ended"
		case "window_expired", "media_gap", "invalid_seek", "seek_identity_conflict", "media_generation_expired":
			code = "seek_unavailable"
		case "source_capacity_unavailable":
			code = "no_tuner_available"
		default:
			code = "channel_unavailable"
		}
	case errors.As(err, new(*playbackv1.ThroughputError)):
		code = "connection_too_slow"
	case errors.Is(err, context.DeadlineExceeded):
		return &apierror.Error{Code: "timeout", RetryAfterSeconds: 2, Cause: err}
	default:
		return err
	}
	if code == "queue_building" || code == "visibility_building" {
		return &apierror.Error{Code: code, RetryAfterSeconds: 2, Cause: err}
	}
	var slow *playbackv1.ThroughputError
	if errors.As(err, &slow) {
		// Reason names the widest fixed quality the measured connection carries.
		return &apierror.Error{Code: "connection_too_slow", Reason: slow.Suggested, Cause: err}
	}
	var transition *playbackv1.TransitionError
	if errors.As(err, &transition) {
		return &apierror.Error{Code: code, Reason: transition.Reason, Cause: err}
	}
	return &apierror.Error{Code: code, Cause: err}
}

// itemAdmission maps an item check made after authentication (SEC-02): a title
// the caller may not reach, for any reason (its library isn't shared with them,
// a restriction, it doesn't exist), is not found. It is never 401, which a
// client would answer by refreshing or signing out (review P1).
func itemAdmission(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, identity.ErrUnauthorized) || errors.Is(err, sql.ErrNoRows) || errors.Is(err, identity.ErrContentRestricted) || errors.Is(err, playbackv1.ErrNotFound) {
		return &apierror.Error{Code: "not_found", Cause: err}
	}
	return v1Error(err)
}

// v1Authorize enforces a route's access rule and hands the handler the caller.
func (d Dependencies) v1Authorize(ctx context.Context, r *http.Request, m apikit.Metadata) (context.Context, error) {
	// Integration merge (BE-playback registry x lane C's typed routes, C81):
	// one authorizer for every typed route, carrying lane C's rules - a valid
	// non-owner on an owner route is not_permitted, the "direct_account" admin
	// scope is the direct account's own manage right, and sign-in states
	// (password change, device approval) keep their own codes.
	// This one read is the account-scoped profile picker, not a viewer read.
	// DirectMe validates the live family, epoch, membership and narrowed-grant
	// refusal itself; generic principal authentication intentionally rejects
	// account-only sign-ins before a profile has been selected. Do not attach
	// a viewer principal or extend this admission to any other device route.
	if m.ID == "get_direct_account" && m.Method == http.MethodGet && m.Path == "/v1/direct" {
		if d.Identity == nil {
			return nil, &apierror.Error{Code: "internal"}
		}
		if _, err := d.Identity.DirectMe(ctx, directBearer(r)); err != nil {
			return nil, foundationAuthError(err)
		}
		return ctx, nil
	}
	// PIN recovery starts from a fresh account-only sign-in when no profile
	// can be selected. These two operations use the direct account's manage
	// authority, including its required-factor gate, without becoming viewers.
	if m.ID == "get_direct_pin_recovery" && m.Method == http.MethodGet && m.Path == "/v1/direct/pin-recovery" ||
		m.ID == "post_direct_profile_pin_reset" && m.Method == http.MethodPost && m.Path == "/v1/direct/profiles/{id}/pin-reset" {
		if d.Identity == nil {
			return nil, &apierror.Error{Code: "internal"}
		}
		if _, err := d.Identity.PINRecovery(ctx, directBearer(r)); err != nil {
			return nil, v1AuthError(err)
		}
		return ctx, nil
	}
	var p identity.Principal
	var err error
	switch {
	case m.Access == apikit.Owner:
		p, err = d.owner(r)
		if errors.Is(err, identity.ErrUnauthorized) {
			if _, valid := d.principal(r); valid == nil {
				return nil, &apierror.Error{Code: "not_permitted"}
			}
		}
	case m.Access == apikit.Admin && m.AdminScope == "direct_account":
		if p, err = d.principal(r); err == nil {
			if d.Identity == nil {
				return nil, &apierror.Error{Code: "not_permitted"}
			}
			account, e := d.Identity.DirectMe(ctx, directBearer(r))
			if e != nil {
				return nil, v1AuthError(e)
			}
			if !account.CanManage {
				return nil, &apierror.Error{Code: "not_permitted"}
			}
		}
	case m.Access == apikit.Admin:
		p, err = d.administrator(r)
	default:
		p, err = d.principal(r)
	}
	if err != nil {
		return nil, v1AuthError(err)
	}
	// Conceal FileInfo item identities before the registry validates query
	// fields; hidden and absent items must not have different error shapes.
	if fileInfoLaneRoute(m) {
		if _, _, e := d.detailContentFenceFor(r, p); e != nil {
			return nil, v1Error(e)
		}
	}
	// Playback routes act for a device; other typed routes don't need one.
	device, err := d.deviceOf(ctx, p)
	if err != nil && (m.Access == apikit.Device || m.Access == apikit.ViewerItem) {
		return nil, v1Error(err)
	}
	if m.Access == apikit.ViewerItem {
		if item := r.PathValue("itemId"); item != "" {
			if err = itemAdmission(d.itemAccess(ctx, p, item)); err != nil {
				return nil, err
			}
		}
	}
	if m.Access == apikit.ViewerLibrary {
		// The registry refuses to register this label without {libraryId}, so
		// the check below always has a library to make.
		library := r.PathValue("libraryId")
		if library == "" {
			return nil, &apierror.Error{Code: "not_found"}
		}
		if err = d.allowedLibrary(ctx, p, library); err != nil {
			return nil, itemAdmission(err)
		}
	}
	ctx = context.WithValue(ctx, foundationPrincipalKey{}, p)
	return context.WithValue(ctx, v1CallerKey{}, v1Caller{Principal: p, DeviceID: device}), nil
}

// v1AuthError keeps lane C's sign-in state codes; everything else maps as v1.
func v1AuthError(err error) error {
	if errors.Is(err, identity.ErrPasswordChangeRequired) {
		return foundationAuthError(err)
	}
	return v1Error(err)
}

// deviceOf is the identity device a bearer token is bound to. Every issued
// token belongs to a device family (an anonymous device when the client sent no
// installation), so a miss means the token is not a device session.
func (d Dependencies) deviceOf(ctx context.Context, p identity.Principal) (string, error) {
	if d.DB == nil || p.Hash == "" {
		return "", identity.ErrUnauthorized
	}
	var device string
	err := d.DB.QueryRowContext(ctx, `SELECT d.device_id FROM authorization_family_tokens t JOIN identity_device_families d ON d.family_id=t.family_id WHERE t.token_hash=?`, p.Hash).Scan(&device)
	if errors.Is(err, sql.ErrNoRows) {
		return "", identity.ErrUnauthorized
	}
	return device, err
}

// muxPattern is the ServeMux pattern that carries an apikit route. ServeMux
// wildcards must be whole segments, so "{id}:advance" is mounted as "{id}" and
// the registry matches the suffix itself.
func muxPattern(method, path string) string {
	// This public suffix crosses the bulk preview-query prefix. The HTTP
	// adapter checks metadataAction == preview before entering the handler.
	// Keep the mapping shared with route inventory and scope resolution.
	if method == "GET" && path == "/v1/metadata/{kind}/{id}/preview" {
		return method + " /v1/metadata/{kind}/{id}/{metadataAction}"
	}
	parts := strings.Split(path, "/")
	for i, part := range parts {
		if strings.HasPrefix(part, "{") {
			if end := strings.IndexByte(part, '}'); end > 0 && end < len(part)-1 {
				parts[i] = part[:end+1]
			}
		}
	}
	return method + " " + strings.Join(parts, "/")
}

// mountRegistry hands every registry route to the registry. Playback Protocol
// v1 is the only way to start, read, change and stop playback: no other
// handler shares its patterns.
func (d Dependencies) mountRegistry(mux *http.ServeMux, registry *apikit.Registry) {
	mounted := map[string]bool{}
	for _, route := range registry.Definitions() {
		if route.HTTP {
			continue
		}
		pattern := muxPattern(route.Method, route.Path)
		if mounted[pattern] {
			continue
		}
		mounted[pattern] = true
		mux.Handle(pattern, registry)
	}
}
