package httpapi

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/networkpolicy"
	"github.com/PorticoMediaServer/portico/server/internal/requestinput"
	"github.com/PorticoMediaServer/portico/server/internal/storagepaths"
	"io"
	"log"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/PorticoMediaServer/portico/apikit"
	"github.com/PorticoMediaServer/portico/apikit/apierror"
	"github.com/PorticoMediaServer/portico/apikit/httpclientclose"
	"github.com/PorticoMediaServer/portico/server/internal/access"
	"github.com/PorticoMediaServer/portico/server/internal/administration"
	"github.com/PorticoMediaServer/portico/server/internal/backup"
	"github.com/PorticoMediaServer/portico/server/internal/buildinfo"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/downloads"
	"github.com/PorticoMediaServer/portico/server/internal/driveoauth"
	"github.com/PorticoMediaServer/portico/server/internal/eventfeed"
	"github.com/PorticoMediaServer/portico/server/internal/hosted"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/imagework"
	"github.com/PorticoMediaServer/portico/server/internal/ingestion"
	"github.com/PorticoMediaServer/portico/server/internal/livechannels"
	"github.com/PorticoMediaServer/portico/server/internal/livechannels/dvr"
	librarychannels "github.com/PorticoMediaServer/portico/server/internal/livechannels/library"
	"github.com/PorticoMediaServer/portico/server/internal/lyrics"
	"github.com/PorticoMediaServer/portico/server/internal/mediaanalysis"
	"github.com/PorticoMediaServer/portico/server/internal/metadata"
	"github.com/PorticoMediaServer/portico/server/internal/mounts"
	"github.com/PorticoMediaServer/portico/server/internal/networking"
	"github.com/PorticoMediaServer/portico/server/internal/operations"
	"github.com/PorticoMediaServer/portico/server/internal/ownedcapture"
	"github.com/PorticoMediaServer/portico/server/internal/playback"
	"github.com/PorticoMediaServer/portico/server/internal/playbackruntime"
	"github.com/PorticoMediaServer/portico/server/internal/playbackv1"
	"github.com/PorticoMediaServer/portico/server/internal/preparedmedia"
	"github.com/PorticoMediaServer/portico/server/internal/rcloneprogram"
	"github.com/PorticoMediaServer/portico/server/internal/reading"
	"github.com/PorticoMediaServer/portico/server/internal/recordingaccess"
	"github.com/PorticoMediaServer/portico/server/internal/remotemedia"
	"github.com/PorticoMediaServer/portico/server/internal/remotesources"
	"github.com/PorticoMediaServer/portico/server/internal/selectionactions"
	"github.com/PorticoMediaServer/portico/server/internal/social"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
	"github.com/PorticoMediaServer/portico/server/internal/subtitles"
	"github.com/PorticoMediaServer/portico/server/internal/telemetry"
	"github.com/PorticoMediaServer/portico/server/internal/webhooks"
)

type Dependencies struct {
	TelemetryContext context.Context
	streamTraffic    *telemetry.Traffic
	RcloneProgram    *rcloneprogram.Status
	// DriveOAuth holds the owner's in-flight Google sign-ins; nil disables them.
	DriveOAuth        *driveoauth.Store
	StorageLayout     *storagepaths.Layout
	ConnectionUsage   func() ConnectionUsage
	LocalArtworkUsage func() (int64, bool)
	CacheDirectory    string
	Restart           func()
	ListeningPort     int
	// LocalHTTPURL comes solely from the actual bound loopback-capable listener.
	// Empty means no localhost route is reported, never an inferred default.
	LocalHTTPURL string
	// SettingsApplied runs after an owner saves console settings so snapshot
	// readers (delivery configuration) refresh off the request path.
	SettingsApplied               func(context.Context)
	configurationOwnsLiveSettings bool
	// PlaybackV1 is the Playback Protocol v1 service; nil builds one from DB,
	// Playback and Subtitles (tests pass one to shorten leases).
	PlaybackV1 *playbackv1.Service
	// AudioMedia measures audio facts and converts audio exactly for the
	// version 2 render plan (spec §18.1, §18.7); nil: plans need no conversion
	// and missing facts stay missing.
	AudioMedia     AudioMedia
	WebDirectory   string
	Administration *administration.Service
	// Backups is the Plex-model backup service; nil answers backups as
	// unavailable, which is the right default for tests that do not run one.
	Backups          *backup.Service
	Prepared         *preparedmedia.Service
	Console          *operations.Store
	Downloads        *downloads.Service
	SelectionActions *selectionactions.Service
	Scheduler        *operations.Scheduler
	Measurements     *operations.Measurements
	Events           *eventfeed.Hub
	Subtitles        *subtitles.Service
	Lyrics           *lyrics.Service
	LyricsBulk       *lyrics.Bulk
	Analysis         *mediaanalysis.Service
	LyricsProbe      string
	Networking       *networking.ClaimHandler
	// CustomCertificate serves the owner's certificate for a custom domain. Nil
	// omits it from the connectivity report, which is the right default for a
	// test that does not run one.
	CustomCertificate *networking.CustomCertificate
	RouteIdentity     http.Handler
	LiveChannels      *livechannels.Store
	DVR               *dvr.Store
	LibraryChannels   *librarychannels.Store
	PlaybackRuntime   *playbackruntime.Runtime
	Storage           *storage.Client
	Mounts            *mounts.Service
	RemoteSources     *remotesources.Service
	DB                *sql.DB
	Identity          *identity.Service
	Catalog           *catalog.Service
	FacetReads        FacetReadRuntime
	GenreReads        GenreReadRuntime
	// FileInfoRuntime is the one process FileInfo window runtime. It shares the
	// process readprep store; nil answers metadata_unavailable rather than
	// serving an empty or locally built page.
	FileInfoRuntime *catalog.FileInfoRuntime
	// PrepareBrowseSelections is a startup composition/cohort gate. Default false
	// retains the current foreground path until all clients support preparation.
	PrepareBrowseSelections bool
	PrepareReadingFacets    bool
	PrepareReadingBrowse    bool
	Reading                 *reading.Service
	Ingestion               *ingestion.Service
	Playback                *playback.Service
	// DiscCaptures is private byte custody only. Composition supplies it before
	// New copies Dependencies; it does not enable remote disc admission.
	DiscConditional playback.DiscConditionalSource
	DiscCaptures    *ownedcapture.Service
	Hosted          *hosted.Service
	Metadata        *metadata.Service
	Origins         []string
	// SetupHosts explicitly authorizes browser bootstrap authorities for custom
	// DNS names/reverse proxies. Host/Origin agreement alone cannot prove one.
	SetupHosts     []string
	TrustedProxies []netip.Prefix
	// Access carries the people, diagnostics and connectivity seams (workstream G2).
	Access AccessArea
	// Webhooks is the owner's outbound webhooks; nil answers them as unavailable.
	Webhooks *webhooks.Store
	// Watchdog reports database health. Nil leaves the server reporting healthy,
	// which is the right default for a test that does not run one.
	Watchdog *dbwork.Watchdog
	// admission is installed by New; it is not a caller-supplied dependency.
	admission *admission
	// directSchema caches the one-off probe for the direct identity tables. It is
	// installed by New for the same reason: it is a fact about this database, not
	// a choice a caller makes.
	directSchema *recordingaccess.Schema
	// principals is the bounded, authority-fenced principal cache. See
	// principal_cache.go for the discipline it keeps.
	principals *principalCache
	// access is the library-authority cache, kept on the same terms.
	access *accessCache
	// restrictions is the viewer content-restriction cache, kept on the same terms.
	restrictions     *restrictionCache
	connectionPolicy *connectionPolicyCache
	// self is the finished router, for the MCP server's calls to this server's
	// own API (mcp.go).
	self *selfHandler
	// credentials holds API keys and personal access tokens to their scopes and
	// limits (api_gate.go).
	credentials *credentialGate
	// v1 is the Playback Protocol v1 resource service (playback_v1*.go),
	// installed by New like the caches above.
	v1     *playbackv1.Service
	social *social.Store
	// registry is the typed apikit registry (foundation and playback v1).
	registry             *apikit.Registry
	httpContractsPrivate bool
}

func write(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if v != nil {
		_ = json.NewEncoder(w).Encode(v)
	}
}

// routeMux is the live router's mux. RoutePattern asks it for the registered
// pattern of a request seen outside the mux (where r.Pattern is empty).
var (
	routeMuxMu sync.RWMutex
	routeMux   *http.ServeMux
)

// RoutePattern returns the registered route pattern for r (for example
// "GET /v1/items/{id}"), or "" when no route matches. The pattern never
// carries identifiers, tokens or query strings; only the path is matched.
func RoutePattern(r *http.Request) string {
	if r == nil {
		return ""
	}
	routeMuxMu.RLock()
	mux := routeMux
	routeMuxMu.RUnlock()
	if mux == nil {
		return ""
	}
	_, pattern := mux.Handler(r)
	return pattern
}

// httpFailureWindow is one rate-limit bucket for failure()'s 5xx log: the
// first of each (status, code) logs at once with its cause, the rest collapse
// to at most one line per minute with the suppressed count.
type httpFailureWindow struct {
	started    time.Time
	suppressed int
}

var (
	httpFailureMu      sync.Mutex
	httpFailureWindows = map[string]*httpFailureWindow{}
)

const (
	httpFailureWindowLength = time.Minute
	httpFailureClassCap     = 128
)

// boundFailureDetail keeps the logged cause to one bounded line: no controls,
// at most 160 characters. Bodies, tokens and query strings never reach here:
// only the error value is formatted, never the request.
func boundFailureDetail(err error) string {
	v := strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return ' '
		}
		return r
	}, err.Error())
	if len(v) > 160 {
		v = v[:160] + "…"
	}
	return v
}

func reportHTTPFailure(status int, code string, err error) {
	if err == nil {
		return
	}
	key := strconv.Itoa(status) + "\x00" + code
	httpFailureMu.Lock()
	now := time.Now()
	current, ok := httpFailureWindows[key]
	if ok && now.Sub(current.started) < httpFailureWindowLength {
		current.suppressed++
		httpFailureMu.Unlock()
		return
	}
	suppressed := 0
	if ok {
		suppressed = current.suppressed
		current.started, current.suppressed = now, 0
	} else {
		if len(httpFailureWindows) >= httpFailureClassCap {
			for k, v := range httpFailureWindows {
				if now.Sub(v.started) >= httpFailureWindowLength {
					delete(httpFailureWindows, k)
				}
			}
			if len(httpFailureWindows) >= httpFailureClassCap {
				clear(httpFailureWindows)
			}
		}
		httpFailureWindows[key] = &httpFailureWindow{started: now}
	}
	httpFailureMu.Unlock()
	if suppressed > 0 {
		log.Printf("request failed (%d %s): %s; %d more like this since the last report", status, code, boundFailureDetail(err), suppressed)
		return
	}
	log.Printf("request failed (%d %s): %s", status, code, boundFailureDetail(err))
}

// writerRefused answers work the database refused before it began — a write
// the writer turned away (writer_capacity) or a read held behind a WAL reset
// (database_busy) — with its typed, retryable answer, so a feature's own error
// mapping can't present it as that feature being unavailable. It reports
// whether it answered.
func writerRefused(w http.ResponseWriter, e error) bool {
	if userMutationError(e) == nil && !dbwork.Overloaded(e) && !errors.Is(e, dbwork.ErrReadPressure) && !errors.Is(e, dbwork.ErrLongReadBusy) {
		return false
	}
	failure(w, e)
	return true
}

func failure(w http.ResponseWriter, e error) {
	if httpclientclose.Writer(w) {
		w.WriteHeader(httpclientclose.Status)
		return
	}
	if err := userMutationError(e); err != nil {
		policyErrors.Write(w, w.Header().Get("X-Request-Id"), err)
		return
	}
	if errors.Is(e, identity.ErrOwnerResetExpired) {
		policyErrors.Write(w, w.Header().Get("X-Request-Id"), &apierror.Error{Code: "owner_reset_expired"})
		return
	}
	if errors.Is(e, identity.ErrOwnerResetFileWrite) {
		policyErrors.Write(w, w.Header().Get("X-Request-Id"), &apierror.Error{Code: "owner_reset_file_unavailable"})
		return
	}

	if errors.Is(e, identity.ErrFactorSetupRequired) {
		policyErrors.Write(w, w.Header().Get("X-Request-Id"), &apierror.Error{Code: "factor_setup_required"})
		return
	}
	if revisionFailure(w, e) {
		return
	}
	// A saved list, playlist or collection that moved since it was read
	// answers as every other revision-fenced change does.
	if errors.Is(e, catalog.ErrPlaylistConflict) {
		policyError(w, "revision_mismatch")
		return
	}
	if errors.Is(e, catalog.ErrVisibilityBuilding) {
		w.Header().Set("Retry-After", "1")
		write(w, http.StatusServiceUnavailable, map[string]any{"error": map[string]any{"code": "visibility_building", "message": "Library visibility is being prepared. Retry shortly.", "retryable": true}})
		return
	}
	if errors.Is(e, preparedmedia.ErrInput) || errors.Is(e, preparedmedia.ErrConflict) || errors.Is(e, preparedmedia.ErrUnavailable) || errors.Is(e, preparedmedia.ErrSourceChanged) {
		preparedFailure(w, e)
		return
	}
	for _, moveErr := range []struct {
		err    error
		code   string
		status int
	}{{identity.ErrMoveCollision, "move_collision", 409}, {identity.ErrMoveConfirmation, "move_confirmation", 409}, {identity.ErrMoveCode, "move_code_invalid", 410}, {identity.ErrMoveWaiting, "account_move_waiting", 403}} {
		if errors.Is(e, moveErr.err) {
			var collision *identity.MoveCollisionError
			public := &apierror.Error{Code: moveErr.code}
			if errors.As(e, &collision) {
				public.Current = MoveCollisionDetails{ExistingUsername: collision.Username, ServerName: collision.ServerName}
			}
			policyErrors.Write(w, w.Header().Get("X-Request-Id"), public)
			return
		}
	}
	if errors.Is(e, identity.ErrSignInKindPortico) || errors.Is(e, identity.ErrSignInKindLocal) {
		code := "sign_in_kind_portico"
		if errors.Is(e, identity.ErrSignInKindLocal) {
			code = "sign_in_kind_local"
		}
		write(w, 403, map[string]any{"error": map[string]any{"code": code, "message": e.Error(), "retryable": false}})
		return
	}
	if errors.Is(e, hosted.ErrServicesUnavailable) {
		policyErrors.Write(w, w.Header().Get("X-Request-Id"), &apierror.Error{Code: "portico_services_unavailable"})
		return
	}
	status, code := 500, "internal"
	if requestinput.Is(e) {
		status, code = 400, "invalid_request"
	}
	if errors.Is(e, errFeatureRestricted) {
		status, code = 403, "feature_restricted"
	}
	if errors.Is(e, identity.ErrPasswordChangeRequired) {
		status, code = 403, "password_change_required"
	}
	if errors.Is(e, identity.ErrCurrentPasswordIncorrect) {
		status, code = 403, "current_password_incorrect"
	}
	if errors.Is(e, identity.ErrAccountLocked) {
		status, code = 429, "account_locked"
		var locked *identity.AccountLockError
		if errors.As(e, &locked) {
			w.Header().Set("Retry-After", strconv.FormatInt(locked.RetryAfter, 10))
		} else {
			w.Header().Set("Retry-After", "1")
		}
	}
	if errors.Is(e, identity.ErrProfileSelection) {
		status, code = 409, "profile_selection_required"
	}
	if errors.Is(e, identity.ErrProfilePIN) {
		status, code = 403, "profile_pin_invalid"
	}
	if errors.Is(e, identity.ErrProfileLocked) {
		status, code = 429, "profile_pin_locked"
		var wait *identity.ProfilePINError
		if errors.As(e, &wait) {
			w.Header().Set("Retry-After", strconv.FormatInt(wait.RetryAfterSeconds, 10))
		}
	}
	if errors.Is(e, identity.ErrProfilePIN) {
		var wait *identity.ProfilePINError
		if errors.As(e, &wait) {
			w.Header().Set("Retry-After", strconv.FormatInt(wait.RetryAfterSeconds, 10))
		}
	}
	if errors.Is(e, identity.ErrProfileCapacity) {
		status, code = 409, "profile_capacity"
	}
	if errors.Is(e, identity.ErrProfileChanged) {
		status, code = 409, "profile_changed"
	}
	if errors.Is(e, identity.ErrPrimaryProfile) {
		status, code = 409, "primary_profile"
	}
	// Workstream H: profiles, restrictions, devices, two-factor, registration.
	if errors.Is(e, identity.ErrRestrictionInput) || errors.Is(e, identity.ErrDeviceInput) || errors.Is(e, identity.ErrBrowserAccountInput) {
		status, code = 400, "invalid_request"
	}
	if errors.Is(e, identity.ErrRestrictionChanged) {
		status, code = 409, "restrictions_changed"
	}
	if errors.Is(e, identity.ErrContentRestricted) {
		status, code = 404, "not_found"
	}
	if errors.Is(e, identity.ErrAvatarUpload) {
		status, code = 415, "avatar_unsupported"
	}
	if errors.Is(e, identity.ErrAvatarMissing) {
		status, code = 404, "avatar_not_found"
	}
	if errors.Is(e, identity.ErrFactorRequired) {
		status, code = 401, "two_factor_required"
	}
	if errors.Is(e, identity.ErrFactorInvalid) {
		status, code = 403, "two_factor_invalid"
	}
	if errors.Is(e, identity.ErrFactorEnrolled) {
		status, code = 409, "two_factor_enrolled"
	}
	if errors.Is(e, identity.ErrFactorMissing) {
		status, code = 409, "two_factor_missing"
	}
	if errors.Is(e, identity.ErrDeviceUnknown) {
		status, code = 404, "device_not_found"
	}
	if errors.Is(e, identity.ErrRegistrationClosed) {
		status, code = 403, "registration_closed"
	}
	if errors.Is(e, identity.ErrRegistrationInvite) {
		status, code = 403, "registration_invitation_required"
	}
	if errors.Is(e, identity.ErrRegistrationTaken) {
		status, code = 409, "username_taken"
	}
	if errors.Is(e, identity.ErrTopShelfToken) {
		status, code = 401, "topshelf_token_expired"
	}
	if errors.Is(e, catalog.ErrListeningResumeUnavailable) {
		status, code = 409, "listening_resume_unavailable"
	}
	if errors.Is(e, catalog.ErrListeningPreferencesConflict) {
		status, code = 409, "listening_preferences_conflict"
	}
	if errors.Is(e, catalog.ErrLibraryConfigurationConflict) {
		status, code = 409, "library_configuration_conflict"
	}
	if errors.Is(e, catalog.ErrShowSettingsConflict) {
		status, code = 409, "show_settings_conflict"
	}
	if errors.Is(e, catalog.ErrSourceBusy) || errors.Is(e, storage.ErrBusy) {
		status, code = 409, "source_busy"
	}
	if errors.Is(e, catalog.ErrSourceChanged) || errors.Is(e, storage.ErrRemoteChanged) || errors.Is(e, storage.ErrInventoryChanged) {
		status, code = 409, "source_changed"
	}
	if errors.Is(e, playback.ErrStaleChapter) {
		status, code = 409, "stale_chapter_projection"
	}
	if errors.Is(e, playback.ErrChapterCursor) {
		status, code = 400, "invalid_chapter_cursor"
	}
	if errors.Is(e, playback.ErrStaleOffer) {
		status, code = 409, "stale_playback_offer"
	}
	// CD-51: 401 only for a failed credential; a refusal of a valid session is
	// 403, or 404 where existence stays hidden.
	if refusalStatus, refusalCode, ok := identity.Refusal(e); ok {
		status, code = refusalStatus, refusalCode
		if status == 401 {
			code = "unauthorized"
		}
	}
	// A proven Portico Account that is not (or no longer) a member here: the
	// client drops the server or returns to sign-in (Spec — Hosted at Scale).
	if errors.Is(e, identity.ErrAccessRefused) {
		status, code = 403, "access_refused"
	}
	// A session migration 0120 ended: re-admit silently with an identity
	// assertion; this is not a sign-out.
	if errors.Is(e, identity.ErrSessionMigrated) {
		status, code = 401, "session_migrated"
	}
	if errors.Is(e, hosted.ErrClockSkew) {
		status, code = 401, "clock_skew"
	}
	if errors.Is(e, sql.ErrNoRows) {
		status, code = 404, "not_found"
	}
	if errors.Is(e, playback.ErrGrantEnded) {
		status, code = 404, "presentation_ended"
	}
	if errors.Is(e, metadata.ErrMBConflict) {
		status, code = 409, "metadata_conflict"
	}
	if errors.Is(e, metadata.ErrScreenConflict) || errors.Is(e, metadata.ErrTVDBConflict) {
		status, code = 409, "metadata_conflict"
	}
	if errors.Is(e, catalog.ErrCollectionConflict) {
		status, code = 409, "collection_conflict"
	}
	if errors.Is(e, catalog.ErrSourceAlreadyConfigured) {
		status, code = 409, "source_already_configured"
	}
	if errors.Is(e, identity.ErrConflict) {
		status, code = 409, "conflict"
	}
	// The owner's remote sign-in choice and a member's allowed hours. Both are
	// refusals of someone whose credentials are fine, so neither is a 401, and
	// neither ends a sign-in a device already holds.
	if errors.Is(e, identity.ErrRemoteSignIn) {
		status, code = 403, "remote_sign_in_not_allowed"
	}
	if errors.Is(e, access.ErrSchedule) {
		status, code = 403, "outside_allowed_hours"
	}
	if errors.Is(e, playback.ErrIncompatible) {
		status, code = 422, "unsupported_source"
	}
	if errors.Is(e, playback.ErrOwnerAccountCap) {
		status, code = 409, "owner_account_cap"
	}
	if errors.Is(e, playback.ErrOwnerServerCap) {
		status, code = 409, "owner_server_cap"
	}
	if errors.Is(e, playback.ErrTranscodingDisabled) {
		status, code = 422, "transcoding_disabled"
	}
	if errors.Is(e, playback.ErrCapabilityDocument) || errors.Is(e, playback.ErrRouteFailure) {
		status, code = 400, "invalid_playback_report"
	}
	var refused playback.ErrDeliveryRefused
	if errors.As(e, &refused) {
		// A policy refusal is an answer, not a fault: the code names the
		// preference or clamp that closed every route.
		status, code = 422, refused.Code
	}
	message := "The request could not be completed."
	if errors.Is(e, playback.ErrGrantEnded) {
		message = playback.ErrGrantEnded.Error()
	}
	if status == 401 {
		message = publicErrorMessage("unauthorized")
	} else if status == 403 && errors.Is(e, identity.ErrForbidden) {
		message = identity.ErrForbidden.Error()
	}
	if code == "remote_sign_in_not_allowed" || code == "outside_allowed_hours" {
		message = publicErrorMessage(code)
	}
	retryable := false
	if errors.Is(e, playback.ErrSegmentPreparing) {
		status, code, retryable = 503, "segment_preparing", true
		w.Header().Set("Retry-After", "1")
	}
	if errors.Is(e, playback.ErrAudioRenditionUnavailable) {
		status, code, retryable = 422, "audio_rendition_unavailable", false
	}
	if errors.Is(e, playback.ErrFiniteUnsupported) {
		status, code, retryable = 422, "unsupported_timing", false
	}
	if errors.Is(e, playback.ErrConversionCapacity) {
		status, code, retryable = 429, "conversion_capacity", true
		w.Header().Set("Retry-After", "2")
	}
	// A volume with no room left is beyond-target and recoverable: the eviction
	// sweep may free space within seconds, so it answers retryable rather than
	// presenting as a failure of the title.
	if errors.Is(e, playback.ErrConversionSpace) {
		status, code, retryable = 503, "conversion_storage_full", true
		w.Header().Set("Retry-After", "10")
	}
	if errors.Is(e, identity.ErrBusy) {
		status, code, retryable = 503, "authentication_busy", true
		w.Header().Set("Retry-After", "2")
	}
	if errors.Is(e, metadata.ErrLocalMetadataOnly) {
		status, code, message = 409, "local_metadata_only", metadata.ErrLocalMetadataOnly.Error()
	}
	if errors.Is(e, metadata.ErrLibraryAgentConflict) {
		status, code, message = 409, "metadata_conflict", metadata.ErrLibraryAgentConflict.Error()
	}
	if errors.Is(e, metadata.ErrLibraryAgentInput) {
		status, code = 400, "invalid_metadata_source"
	}
	if errors.Is(e, metadata.ErrLibraryLanguageInput) {
		status, code = 400, "invalid_metadata_language"
	}
	if errors.Is(e, catalog.ErrPlaylistCapacity) {
		status, code = 409, "playlist_capacity"
	}
	if errors.Is(e, catalog.ErrPersonalCapacity) {
		status, code, retryable = 429, "personal_capacity", true
	}
	if errors.Is(e, catalog.ErrPersonalConflict) {
		status, code = 409, "personal_state_conflict"
	}
	if errors.Is(e, catalog.ErrOperationExpired) {
		status, code = 409, "operation_expired"
	}
	if errors.Is(e, catalog.ErrPersonalReview) {
		status, code = 409, "personal_needs_review"
	}
	if errors.Is(e, catalog.ErrPersonalResolution) {
		status, code = 409, "personal_needs_resolution"
	}
	if errors.Is(e, catalog.ErrOperationConflict) {
		status, code = 409, "idempotency_key_reused"
	}
	if errors.Is(e, catalog.ErrStaleContinuation) {
		status, code, retryable = 409, "stale_continuation", true
	}
	if errors.Is(e, catalog.ErrCursor) {
		status, code = 400, "invalid_cursor"
	}
	var pathError *os.PathError
	var databaseError interface{ Code() int }
	if errors.As(e, &pathError) {
		status, code, message, retryable = 503, "source_unavailable", "Storage is unavailable. Check the source and retry.", true
	} else if errors.As(e, &databaseError) {
		status, code, message = 500, "persistence_error", "The request could not be saved."
	}
	// A full volume is the one persistence failure with an owner-visible cause
	// and an owner-visible remedy. "The request could not be saved" tells them
	// nothing; this tells them the disk is full.
	if dbwork.Full(e) || errors.Is(e, dbwork.ErrDatabaseFull) {
		status, code, message, retryable = 507, "storage_full", "The server has run out of disk space. Free space on the server's volume and retry.", true
		w.Header().Set("Retry-After", "30")
	}
	// The writer turned the write away before it began, or met a lock it
	// should never meet; either way nothing was saved and a retry is safe.
	// server_busy stays the answer for a full HTTP lane; this one is the
	// writer's own, with its estimate of when the queue drains.
	if dbwork.Overloaded(e) {
		status, code, message, retryable = 503, "writer_capacity", "The server is busy saving other changes. Try again shortly.", true
		w.Header().Set("Retry-After", strconv.Itoa(dbwork.WriteGate().RetryAfterSeconds()))
	}
	if errors.Is(e, dbwork.ErrReadPressure) {
		status, code, message, retryable = 503, "database_busy", "The server is briefly busy maintaining its database. Try again shortly.", true
		w.Header().Set("Retry-After", "1")
	}
	if errors.Is(e, dbwork.ErrLongReadBusy) {
		status, code, message, retryable = 503, "database_busy", "The server is briefly busy maintaining its database. Try again shortly.", true
		w.Header().Set("Retry-After", "5")
	}
	if errors.Is(e, imagework.ErrBusy) {
		status, code, message, retryable = 503, "server_busy", "Image processing is busy. Retry shortly.", true
		w.Header().Set("Retry-After", "1")
	}
	if errors.Is(e, context.DeadlineExceeded) {
		status, code, message, retryable = 503, "timeout", "The operation timed out. Retry shortly.", true
	}
	if errors.Is(e, mounts.ErrUnavailable) {
		status, code, message, retryable = 503, "mount_unavailable", mounts.ErrUnavailable.Error(), true
	}
	if errors.Is(e, remotemedia.ErrPolicy) {
		status, code, message = 403, "network_policy", remotemedia.ErrPolicy.Error()
	}
	// The selected storage is reachable but offline. Identity, lifetime, root and
	// authorization were valid, so the same request may be retried unchanged.
	if errors.Is(e, storage.ErrStorageOffline) {
		status, code, message, retryable = 503, "storage_offline", "This title’s storage is offline right now.", true
		w.Header().Set("Retry-After", "2")
	}
	if errors.Is(e, storage.ErrBusy) || errors.Is(e, storage.ErrPlaybackTimeout) || errors.Is(e, storage.ErrPlaybackSource) {
		status, code, message, retryable = 503, "playback_source_unavailable", "Playback source is unavailable; retry when storage responds.", true
		w.Header().Set("Retry-After", "2")
		if errors.Is(e, storage.ErrBusy) {
			code = "playback_source_busy"
		}
		if errors.Is(e, storage.ErrPlaybackTimeout) {
			code = "playback_source_timeout"
		}
	}
	if errors.Is(e, remotemedia.ErrBusy) {
		status, code, message, retryable = 503, "remote_busy", remotemedia.ErrBusy.Error(), true
	}
	if errors.Is(e, remotemedia.ErrUnavailable) {
		status, code, message, retryable = 502, "remote_unavailable", remotemedia.ErrUnavailable.Error(), true
	}
	if errors.Is(e, remotemedia.ErrStalled) {
		status, code, message, retryable = 504, "remote_stalled", remotemedia.ErrStalled.Error(), true
	}
	if errors.Is(e, remotemedia.ErrDenied) {
		status, code, message = 502, "remote_denied", remotemedia.ErrDenied.Error()
	}
	if errors.Is(e, remotemedia.ErrUnsupported) {
		status, code, message = 422, "unsupported_source", remotemedia.ErrUnsupported.Error()
	}
	if status >= 500 {
		// Server-side failures keep their cause in the log; the client sees only the code.
		reportHTTPFailure(status, code, e)
	}
	write(w, status, map[string]any{"error": map[string]any{"code": code, "message": message, "retryable": retryable}})
}

// decode reads one JSON object into v. On a PUT, PATCH or DELETE a body type
// with an expectedRevision field is a revision-fenced change of something that
// exists: absence (or null) is 428 revision_required, never a silent revision
// 0 (BE-API-09); an explicit 0 is kept where 0 is a valid revision. A POST
// shares body types with creations, so a POST that changes an existing
// resource opts in with decodeLegacyRevision.
func decode(w http.ResponseWriter, r *http.Request, v any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	raw, e := io.ReadAll(r.Body)
	if e != nil || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return requestinput.New("invalid JSON request")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if e = d.Decode(v); e != nil {
		return requestinput.New("invalid JSON request")
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return requestinput.New("one JSON object required")
	}
	switch r.Method {
	case http.MethodPut, http.MethodPatch, http.MethodDelete:
		return requireRevisionJSON(raw, v)
	}
	return nil
}

// principal resolves the Authorization header to an authenticated, library-
// authorised viewer.
//
// The three checks it runs — the session family, the recovery route, and
// library authority — used to open three read transactions and therefore take
// three pooled connections out of eight, before the handler had done any work.
// They now share one read snapshot, which is both cheaper and stricter: all
// three see one state rather than three consecutive ones. The snapshot is bound
// by the request context, so the lane's budget reaches authentication instead of
// stopping at the handler door.
func (d Dependencies) principal(r *http.Request) (identity.Principal, error) {
	p, err := d.resolvePrincipal(r)
	if err == nil {
		err = d.featureAllowed(r.Context(), p, requestFeature(r))
	}
	return p, err
}
func (d Dependencies) resolvePrincipal(r *http.Request) (identity.Principal, error) {
	header := r.Header.Get("Authorization")
	if !strings.HasPrefix(header, "Bearer ") {
		return identity.Principal{}, identity.ErrUnauthorized
	}
	token := strings.TrimPrefix(header, "Bearer ")
	// The cache is keyed on the token's digest, never on the token, and it is
	// consulted only on lanes where a few seconds of lag in a revocation is
	// acceptable. Everything that touches authority or playback resolves live.
	private := d.privateSetupPeer(r)
	key := ""
	strict := strictPrincipal(dbwork.ClassFrom(r.Context(), dbwork.ClassInteractive))
	if !strict && !private {
		key = identity.Digest(token)
		if cached, ok := d.principals.lookup(key, time.Now()); ok && !(cached.Grant == identity.GrantCastTarget && castOccurrenceRoute(r.Method, r.URL.Path)) {
			// The cache remembers who a token is, not what this request is: a
			// narrowed session is judged against every request it makes.
			if e := cached.PermitsRequest(r.Context()); e != nil {
				return identity.Principal{}, e
			}
			d.admission.rememberCredential(token, cached)
			return cached, nil
		}
	}
	generation := dbwork.AuthorityGeneration()
	var p identity.Principal
	resolve := func(ctx context.Context) error {
		var e error
		if p, e = d.Identity.AuthenticateContext(ctx, token); e != nil {
			return e
		}
		if e = d.allowedLibrary(ctx, p, ""); e != nil {
			return e
		}
		if item, queue := castOccurrenceResource(r.Method, r.URL.Path); item != "" || queue != "" {
			return d.castOccurrenceAllowed(ctx, p, item, queue)
		}
		return nil
	}
	var err error
	if d.DB == nil {
		err = resolve(r.Context())
	} else {
		err = dbwork.WithReadSnapshot(r.Context(), d.DB, resolve)
	}
	// A denial is never cached: the reason may have been transient, and a cached
	// refusal is the one mistake here a viewer would actually see.
	if err == nil && key != "" && !(p.Grant == identity.GrantCastTarget && castOccurrenceRoute(r.Method, r.URL.Path)) {
		d.principals.store(key, p, time.Now(), generation)
	}
	if err == nil {
		d.admission.rememberCredential(token, p)
	}
	return p, err
}

// strictPrincipalFor resolves a principal against live state whatever lane the
// request is on. A long-lived transport calls it before it parks, so a durable
// revocation cannot leave a stream authorised until the cache entry expires.
func (d Dependencies) strictPrincipalFor(r *http.Request) (identity.Principal, error) {
	return d.principal(r.WithContext(dbwork.WithClass(r.Context(), dbwork.ClassSecurityFence)))
}
func (d Dependencies) owner(r *http.Request) (identity.Principal, error) {
	return d.ownerContext(r.Context(), r)
}
func (d Dependencies) itemAccess(ctx context.Context, p identity.Principal, id string) error {
	lib, e := d.Catalog.WithContext(ctx).LibraryForItem(id)
	if e != nil {
		return e
	}
	if err := d.allowedLibrary(ctx, p, lib); err != nil {
		return err
	}
	return d.restrictedItem((&http.Request{}).WithContext(ctx), p, id)
}
func New(d Dependencies) http.Handler {
	d.streamTraffic = &telemetry.Traffic{}
	d.configurePlaybackEvidence()
	if d.Storage == nil {
		if binary, e := os.Executable(); e == nil {
			d.Storage = storage.New(binary)
		}
	}
	mux := http.NewServeMux()
	// Admission is created with the router and lives as long as it. Its lanes
	// publish this server's foreground-pressure signal, which is what background
	// loops yield to.
	d.directSchema = recordingaccess.NewSchema()
	d.principals = newPrincipalCache()
	d.access = newAccessCache()
	d.restrictions = newRestrictionCache()
	if d.credentials == nil {
		d.credentials = newCredentialGate()
	}
	d.connectionPolicy = &connectionPolicyCache{}
	if d.self == nil {
		d.self = &selfHandler{}
	}
	d.self.mux = mux
	// The typed registry captures d, so everything its handlers read must be
	// installed first.
	if d.Console == nil && d.DB != nil {
		d.Console = operations.New(d.DB)
	}
	if d.Console != nil && d.Console.ConfigFile != nil {
		prior := d.Console.ConfigChanged
		d.configurationOwnsLiveSettings = true
		d.Console.ConfigChanged = func(ctx context.Context) {
			if prior != nil {
				prior(ctx)
			}
			if err := d.applyLiveSettings(ctx); err != nil {
				log.Printf("Settings runtime projection could not refresh: %v", err)
			}
		}
	}
	if d.Catalog != nil && d.Prepared != nil {
		d.Catalog.SeparateFolderRetirement = catalog.SeparateFolderHooks{
			CancelJob:     preparedmedia.CancelForRetirementTx,
			Interrupt:     func(id string) { d.Prepared.Interrupt(context.Background(), id) },
			RevokeReceipt: downloads.RevokeForRetirement,
		}
	}
	if d.Scheduler != nil && d.Catalog != nil {
		found := false
		for _, kind := range d.Scheduler.Kinds() {
			if kind.Kind == "personal-state" {
				found = true
			}
		}
		if !found {
			if err := d.Scheduler.Register(d.Catalog.BulkAdapter(d.bulkAccess)); err != nil {
				panic(err)
			}
			if err := d.Scheduler.Register(d.Catalog.LibraryRemovalAdapter()); err != nil {
				panic(err)
			}
			if err := d.Scheduler.Register(d.Catalog.MutationAdapter()); err != nil {
				panic(err)
			}
			if err := d.Scheduler.Register(d.Catalog.FolderRemovalAdapter()); err != nil {
				panic(err)
			}
		}
	}
	gatekeeper := newAdmission()
	gatekeeper.trustedProxies = d.TrustedProxies
	gatekeeper.registerPressure()
	d.admission = gatekeeper
	// Before the registry: lane C's /v1/events handler reads d.Events (merge).
	if d.Events == nil && d.DB != nil {
		d.Events = eventfeed.New(d.DB)
	}
	if d.Events != nil && d.Hosted != nil && d.Events.LibraryVisible == nil {
		d.Events.LibraryVisible = func(tx *sql.Tx, p identity.Principal, library string) bool {
			return d.Hosted.AllowedTx(p, library, tx) == nil
		}
	}
	if d.Events != nil && d.Catalog != nil && d.Events.ContentVisible == nil {
		d.Events.ContentVisible = d.eventContentVisible
	}
	// Before the registry, which captures d (lane C's typed admin routes).
	if d.Administration == nil && d.DB != nil {
		d.Administration = administration.New(d.DB)
	}
	if d.Catalog != nil {
		d.Catalog.BulkCommands = d.bulkCommandHooks()
	}
	if d.Downloads != nil {
		d.Downloads.SetRequestAccess(d.downloadRequestAccess)
		d.Downloads.SetLibraryAccess(func(ctx context.Context, tx *sql.Tx, p identity.Principal, library string) error {
			return d.allowedLibraryTx(ctx, p, library, tx)
		})
	}
	d.v1 = newPlaybackV1(d)
	if d.Reading == nil {
		d.Reading = d.readingService()
	}
	if d.Downloads != nil {
		d.Downloads.SetReading(d.Reading)
	}
	if d.v1 != nil {
		d.v1.Traffic = d.streamTraffic
	}
	d.social = d.Social()
	if registry, err := FoundationRegistry(d); err != nil {
		panic(err)
	} else {
		d.registry = registry
		d.self.registry = registry
	}
	for _, add := range devRoutes {
		add(d, mux)
	}
	d.installationRoutes(mux)
	d.dlnaRoutes(mux)
	d.foundationRoutes(mux)
	d.consoleRoutes(mux)
	d.directIdentityRoutes(mux)
	d.identityRoutes(mux)
	if d.Networking != nil {
		d.Networking.SetRequestAuthorizer(NetworkingClaimAuthorizer(d.Identity, d.Origins, d))
		d.Networking.SetManagementAuthorizer(func(r *http.Request) (networking.ClaimRequestGuard, error) {
			p, err := d.owner(r)
			if err != nil {
				return nil, err
			}
			return func(ctx context.Context, tx *sql.Tx) error { return d.ownerAuthorityTx(ctx, tx, p) }, nil
		})
		d.Networking.Register(httpRegistrar{d, mux})
	} else {
		d.unconfiguredNetworkingRoutes(mux)
	}
	d.liveChannelRoutes(mux, d.LiveChannels)
	d.guideImageRoutes(mux)
	d.dvrRoutes(mux)
	d.libraryChannelRoutes(mux)
	authRate := &limiter{trustedProxies: d.TrustedProxies, proxies: func(r *http.Request) []netip.Prefix { return d.trustedProxyPrefixes(r.Context()) }}
	d.linearMediaRoutes(mux)
	d.mountRoutes(mux)
	d.bundledRcloneRoutes(mux)
	d.remoteSourceRoutes(mux)
	d.readingRoutes(mux)
	d.episodeRoutes(mux)
	d.playbackChapterRoutes(mux)
	d.playbackOfferRoutes(mux)
	d.preparedMediaRoutes(mux)
	d.audioRoutes(mux)
	d.lyricRoutes(mux)
	d.analysisRoutes(mux)
	d.segmentRoutes(mux)
	d.trickplayRoutes(mux)
	d.searchRoutes(mux)
	d.peopleRoutes(mux)
	d.onboardingRoutes(mux)
	d.administrationRoutes(mux)
	d.adminRoutes(mux)
	d.accessAreaRoutes(mux)
	d.playHistoryRoutes(mux)
	d.inventoryRoutes(mux)
	d.manualMetadataRoutes(mux)
	d.metadataRepairRoutes(mux)
	d.metadataEditorRoutes(mux)
	d.supportRoutes(mux)
	d.operationsRoutes(mux)
	d.playbackDiagnosticsRoutes(mux)
	d.concurrencyRoutes(mux)
	d.pprofRoutes(mux)
	d.telemetryRoutes(mux)
	d.deliveryRoutes(mux)
	d.activityRoutes(mux)
	d.discoveryRoutes(mux)
	d.browseRoutes(mux)
	d.notificationRoutes(mux)
	d.socialRoutes(mux)
	d.httpHandle(mux, "GET /v1/attribution", func(w http.ResponseWriter, r *http.Request) {
		write(w, 200, map[string]any{"providers": []map[string]string{{"name": "TMDB", "notice": metadata.Attribution, "url": "https://www.themoviedb.org"}, {"name": "TheTVDB", "notice": "Metadata provided by TheTVDB.", "url": "https://thetvdb.com"}, {"name": "MusicBrainz", "notice": "Music metadata provided by MusicBrainz.", "url": "https://musicbrainz.org"}, {"name": "Cover Art Archive", "notice": "Cover art provided by Cover Art Archive / MusicBrainz and Internet Archive. Images copyright their respective owners.", "url": "https://coverartarchive.org"}}})
	})
	// hostedConfigured says this build can reach the Portico Account service at all (every release
	// build can); hostedAttached says this server actually signs people in with Portico Accounts,
	// which is whether its claim is installed. A sign-in page decides by the second: a server set
	// up for direct sign-in and never claimed must never be shown the Portico Account hand-off.
	// This answers anyone, before sign-in, so it says only what a client needs
	// to decide how to sign in and whether it is new enough. The exact build
	// (id, source digest, build time) is for the owner, who reads it from the
	// maintenance document; published here it tells a scanner which servers
	// carry a known flaw.
	d.httpHandle(mux, "GET /v1/system", func(w http.ResponseWriter, r *http.Request) {
		kind, err := d.Identity.SignInKind(r.Context())
		if err != nil {
			failure(w, err)
			return
		}
		move, ends, err := d.Identity.PublicAccountMoveWindow(r.Context())
		if err != nil {
			failure(w, err)
			return
		}
		var moveEnds *string
		if !ends.IsZero() {
			value := ends.Format(time.RFC3339)
			moveEnds = &value
		}
		write(w, 200, systemAPI(SystemInfo{Move: move, MoveEndsAt: moveEnds, ID: d.Identity.ID(), Name: d.Identity.Name(), SignIn: kind, SetupRequired: d.Identity.SetupRequired(), HostedConfigured: d.Hosted != nil && d.Hosted.Configured(), HostedAttached: d.Identity.Hosted(), Version: buildinfo.Info()["version"]}))
	})
	d.httpHandle(mux, "GET /v1/me", func(w http.ResponseWriter, r *http.Request) {
		p, e := d.principal(r)
		if e != nil {
			failure(w, e)
			return
		}
		write(w, 200, map[string]any{"viewer": p.Viewer})
	})
	d.httpHandle(mux, "DELETE /v1/sessions/current", d.sessionFamilyLogout)
	d.httpHandle(mux, "POST /v1/hosted/wake", func(w http.ResponseWriter, r *http.Request) {
		var body hosted.Signed
		if e := decode(w, r, &body); e != nil {
			failure(w, e)
			return
		}
		if e := d.Hosted.Wake(r.Context(), body); e != nil {
			failure(w, e)
			return
		}
		w.WriteHeader(202)
	})
	d.httpHandle(mux, "GET /v1/libraries", d.libraryList)

	d.httpHandle(mux, "POST /v1/libraries", func(w http.ResponseWriter, r *http.Request) {
		p, e := d.administrator(r)
		if e != nil {
			failure(w, e)
			return
		}
		var body libraryCreationRequest
		if e := decode(w, r, &body); e != nil {
			failure(w, e)
			return
		}
		if body.Kind == "book" || body.Kind == "comic" || body.Kind == "audiobook" {
			if body.MetadataAgent != "" && body.MetadataAgent != metadata.AgentLocal {
				failure(w, catalog.ErrAdminQuery)
				return
			}
			body.MetadataAgent = metadata.AgentLocal
		}
		folders := body.Folders
		if len(folders) == 0 {
			folders = []string{body.Path}
		} else if body.Path != "" {
			failure(w, catalog.ErrAdminQuery)
			return
		}
		// The language is validated before creating anything: a rejected
		// language creates no library and queues no scan. An unknown agent
		// still reports invalid_metadata_source from the creation
		// transaction, so only pre-check languages for a known agent.
		if body.MetadataLanguage != "" && metadata.ValidLibraryKind(body.Kind) {
			effective := body.MetadataAgent
			if effective == "" {
				effective = metadata.AgentOnline
			}
			if (effective == metadata.AgentOnline || effective == metadata.AgentLocal) && !metadata.LibraryLanguageOffered(body.Kind, effective, body.MetadataLanguage) {
				failure(w, metadata.ErrLibraryLanguageInput)
				return
			}
		}
		// Unless explicitly deferred, the first scan is queued in the same
		// transaction as creation (B71): a library must never sit configured
		// with nothing queued until someone presses Scan now. The scan itself
		// runs in the background lane; queueing is idempotent per source.
		queue := func(tx *sql.Tx, l catalog.Library) error {
			// The source is chosen before the first scan is queued, so a
			// local-only library never makes a single online request.
			if body.MetadataAgent != "" && body.MetadataAgent != metadata.AgentOnline {
				if err := metadata.ApplyLibraryAgentTx(r.Context(), tx, l.ID, body.MetadataAgent); err != nil {
					return err
				}
			}
			if body.MetadataLanguage != "" {
				if err := metadata.ApplyLibraryLanguageTx(r.Context(), tx, l.ID, body.MetadataLanguage); err != nil {
					return err
				}
			}
			if d.Ingestion == nil || (body.ScanAfter != nil && !*body.ScanAfter) {
				return nil
			}
			_, err := d.Ingestion.QueueTx(r.Context(), tx, l.ID)
			return err
		}
		expectedRemoteSource, e := body.ExpectedRemoteSource.Selection()
		if e != nil {
			failure(w, e)
			return
		}
		l, e := d.Catalog.CreateFoldersExpectedSourceAuthorizedThen(r.Context(), body.Name, body.Kind, folders, expectedRemoteSource, func(tx *sql.Tx) error { return d.libraryAdministrationAuthorize(r.Context(), tx, p, "") }, queue)
		if e != nil {
			failure(w, e)
			return
		}
		write(w, 201, l)
	})
	d.httpHandle(mux, "POST /v1/libraries/{id}/network-roots", func(w http.ResponseWriter, r *http.Request) {
		p, _, e := d.storageOwner(r)
		if e != nil {
			failure(w, e)
			return
		}
		var body struct {
			Roots            []string `json:"roots"`
			ExpectedRevision *int64   `json:"expectedRevision,omitempty"`
		}
		if e := decode(w, r, &body); e != nil {
			failure(w, e)
			return
		}
		approvals, e := d.Playback.ApproveNetworkRootsAuthorized(r.Context(), r.PathValue("id"), body.Roots, body.ExpectedRevision, func(tx *sql.Tx) error { return d.resourceAuthorize(tx, p, r.PathValue("id")) })
		if e != nil {
			failure(w, e)
			return
		}
		write(w, 200, map[string]any{"approvals": approvals})
	})
	d.httpHandle(mux, "POST /v1/libraries/{id}/scans", func(w http.ResponseWriter, r *http.Request) {
		p, e := d.administrator(r)
		if e != nil {
			failure(w, e)
			return
		}
		j, e := d.Ingestion.QueueMode(r.Context(), r.PathValue("id"), "", r.URL.Query().Get("mode"), func(tx *sql.Tx) error { return d.libraryAdministrationAuthorize(r.Context(), tx, p, r.PathValue("id")) })
		if e != nil {
			failure(w, e)
			return
		}
		write(w, 202, j)
	})
	d.httpHandle(mux, "GET /v1/admin/ingestion/jobs/{id}", func(w http.ResponseWriter, r *http.Request) {
		if _, e := d.libraryAdministrationJobRead(r, r.PathValue("id")); e != nil {
			failure(w, e)
			return
		}
		j, e := d.Ingestion.Get(r.PathValue("id"))
		if e != nil {
			failure(w, e)
			return
		}
		if _, e = d.libraryAdministrationJobRead(r, r.PathValue("id")); e != nil {
			failure(w, e)
			return
		}
		write(w, 200, j)
	})
	d.httpHandle(mux, "DELETE /v1/admin/ingestion/jobs/{id}", func(w http.ResponseWriter, r *http.Request) {
		p, e := d.administrator(r)
		if e != nil {
			failure(w, e)
			return
		}
		if e := d.Ingestion.Control(r.Context(), r.PathValue("id"), "cancel", func(tx *sql.Tx) error {
			return d.libraryAdministrationJobAuthorize(r.Context(), tx, p, r.PathValue("id"))
		}); e != nil {
			failure(w, e)
			return
		}
		w.WriteHeader(204)
	})
	d.projectedHandle(mux, "GET /v1/items", d.itemsReadHandler)
	d.httpHandle(mux, "GET /v1/items/{id}/art/{kind}", func(w http.ResponseWriter, r *http.Request) {
		p, e := d.principal(r)
		if e == nil {
			e = d.itemArtworkAccess(r.Context(), p, r.PathValue("id"))
		}
		if e != nil {
			failure(w, e)
			return
		}
		if d.Metadata == nil {
			failure(w, errors.New("artwork unavailable"))
			return
		}
		f, mime, e := d.Metadata.EntityArtworkVariant(r.Context(), metadata.RepairTarget{Kind: "item", ID: r.PathValue("id")}, r.PathValue("kind"), "", artworkSize(r), r.URL.Query().Get("v"))
		if e != nil {
			if artworkVersionFailure(w, e) {
				return
			}
			if errors.Is(e, metadata.ErrArtworkPending) {
				w.Header().Set("Retry-After", "5")
				w.Header().Set("Cache-Control", "no-store")
				policyError(w, "artwork_pending")
				return
			}
			failure(w, e)
			return
		}
		defer f.Close()
		serveArtwork(w, r, f, mime)
	})
	d.projectedHandle(mux, "GET /v1/items/{id}", d.itemReadHandler)
	d.audioDecodeRoutes(mux)
	d.httpHandle(mux, "GET /v1/media/{grant}/{file}", func(w http.ResponseWriter, r *http.Request) {
		grant := r.PathValue("grant")
		_, p, item, e := d.Playback.ResolveGrantContext(r.Context(), grant)
		if e == nil {
			e = d.itemAccess(r.Context(), p, item)
			if e == nil {
				d.admission.rememberCredential(grant, p)
			}
		}
		if e != nil {
			failure(w, e)
			return
		}
		f, e := d.Playback.OpenHLSFileContext(r.Context(), grant, r.PathValue("file"))
		if e != nil {
			if errors.Is(e, playback.ErrHLSArtifactUnavailable) {
				w.Header().Set("Retry-After", "1")
				write(w, 503, map[string]any{"error": map[string]any{"code": "stream_not_ready", "message": "Stream segment is not ready.", "retryable": true}})
				return
			}
			failure(w, e)
			return
		}
		defer f.Close()
		info, e := f.Stat()
		if e != nil {
			failure(w, e)
			return
		}
		check := func() error {
			_, p, item, e := d.Playback.ResolveGrantContext(r.Context(), grant)
			if e == nil {
				e = d.itemAccess(r.Context(), p, item)
				if e == nil {
					d.admission.rememberCredential(grant, p)
				}
			}
			return e
		}
		if strings.HasSuffix(r.PathValue("file"), ".m3u8") {
			serveHLSManifest(w, r, f, check)
			return
		}
		switch filepath.Ext(r.PathValue("file")) {
		case ".mp4":
			w.Header().Set("Content-Type", "video/mp4")
		case ".m4s":
			w.Header().Set("Content-Type", "video/iso.segment")
		default:
			w.Header().Set("Content-Type", "video/mp2t")
		}
		// A rolling deadline: what is bounded is the gap between two successful
		// writes, never the length of the response, so a slow-but-alive client on a
		// long file is never cut off while a client that has gone silent is
		// reclaimed with its slot, its file and its helper.
		body := withRollingDeadline(w)
		defer body.release()
		http.ServeContent(body, r, r.PathValue("file"), info.ModTime(), &guardedFile{File: f, check: newStreamAuthority(check).check})
	})
	d.httpHandle(mux, "GET /v1/media/{grant}", func(w http.ResponseWriter, r *http.Request) {
		grant := r.PathValue("grant")
		aid, p, item, e := d.Playback.ResolveGrantContext(r.Context(), grant)
		if e == nil {
			e = d.itemAccess(r.Context(), p, item)
			if e == nil {
				d.admission.rememberCredential(grant, p)
			}
		}
		if e != nil {
			failure(w, e)
			return
		}
		prepared, handled, preparedErr := d.Playback.OpenPrepared(r.Context(), grant)
		if preparedErr != nil {
			preparedFailure(w, preparedErr)
			return
		}
		if handled {
			defer prepared.Close()
			mime := "video/mp4"
			if prepared.Version.Facts.VideoCodec == "" {
				mime = "audio/mp4"
			}
			w.Header().Set("Content-Type", mime)
			w.Header().Set("Cache-Control", "private, no-store")
			body := withRollingDeadline(w)
			defer body.release()
			http.ServeContent(body, r, "prepared.mp4", time.Time{}, &guardedReadSeeker{ReadSeeker: prepared, check: newStreamAuthority(func() error {
				_, p, item, e := d.Playback.ResolveGrantContext(r.Context(), grant)
				if e == nil {
					e = d.itemAccess(r.Context(), p, item)
					if e == nil {
						d.admission.rememberCredential(grant, p)
					}
				}
				return e
			}).check})
			return
		}
		d.serveOriginal(w, r, grant, aid, false, func() error {
			_, p, item, e := d.Playback.ResolveGrantContext(r.Context(), grant)
			if e == nil {
				e = d.itemAccess(r.Context(), p, item)
				if e == nil {
					d.admission.rememberCredential(grant, p)
				}
			}
			return e
		})
	})
	admitted := gatekeeper.wrap(mux, mux)
	routeMuxMu.Lock()
	routeMux = mux
	routeMuxMu.Unlock()
	d.self.h = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !d.allowTransport(w, r) {
			return
		}
		r = detachCanceledConnection(w, r)
		if d.self != nil && d.self.registry != nil {
			if m, ok := d.self.registry.Lookup(r.Method, r.URL.EscapedPath()); ok && m.Protocol == apikit.ProtocolCompatibility {
				r = normalizeCompatibilityCredential(r)
				w.Header().Set("Cache-Control", "no-store")
				if err := d.requireIntegrations(r.Context()); err != nil {
					policyErrors.Write(w, "", err)
					return
				}
			}
		}
		// A key in the address is taken out of it before anything else reads
		// the request: no handler, log or error ever sees it there.
		r = liftQueryCredential(r)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		// 'wasm-unsafe-eval' lets the page compile WebAssembly (the subtitle renderer the web player
		// loads from this origin). It does not allow eval or any other way of running text as script.
		w.Header().Set("Content-Security-Policy", webContentSecurityPolicy)
		if r.TLS != nil {
			w.Header().Set("Strict-Transport-Security", "max-age=31536000")
		}
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Cache-Control", "no-store")
		origin := r.Header.Get("Origin")
		if origin != "" {
			allowed := d.browserOriginAllowed(r, origin)
			if r.URL.Path == "/v1/setup" || r.URL.Path == "/v1/setup/browser" || r.URL.Path == "/v1/setup/app" {
				allowed = d.sameRequestOrigin(r, origin)
			}
			if !allowed {
				write(w, http.StatusForbidden, map[string]any{"error": map[string]any{"code": "origin_not_allowed", "message": "This origin is not permitted.", "retryable": false}})
				return
			}
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
			if r.Method == "OPTIONS" && r.Header.Get("Access-Control-Request-Private-Network") == "true" {
				// A page on a public origin asking for this server on a home network. The
				// answer only lets the browser send the request; the request itself is
				// still held to the origin rule above and to its own authentication.
				w.Header().Set("Access-Control-Allow-Private-Network", "true")
			}
			w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, Range, If-Match, If-None-Match, Idempotency-Key, Portico-Mutation-Root, Last-Event-ID, Portico-Client, Portico-Playback, X-Portico-Transport-Class, X-Portico-Device-Class, X-Portico-App-Language, X-Portico-Device-Name, X-Portico-Device-Platform, X-Portico-Installation-Id, X-Portico-App, X-Portico-App-Version")
			w.Header().Set("Access-Control-Allow-Methods", "GET, HEAD, POST, PUT, PATCH, DELETE, OPTIONS")
			// Converted audio publishes the decoder's trim so a browser can align
			// Opus pre-skip and AAC priming; hidden headers read as null and look
			// like zero trim. These are response metadata, never credentials.
			w.Header().Set("Access-Control-Expose-Headers", "Content-Range, Accept-Ranges, Content-Length, Content-Disposition, X-Reading-Item, X-Reading-Source, Location, Retry-After, ETag, Idempotent-Replayed, RateLimit, RateLimit-Policy, X-Audio-Trim-Start, X-Audio-Trim-End, X-Audio-Frames")
			// Without this a browser remembers a preflight for about five seconds, so the hosted web
			// app (always cross-origin here, always sending Authorization) pays an OPTIONS before
			// nearly every call. The answer only says which methods and headers this origin may use;
			// the real request is still checked against the origin list every time. Browsers cap the
			// value themselves (two hours in Chromium, a day in Firefox).
			w.Header().Set("Access-Control-Max-Age", "86400")
		}
		if r.Method == "OPTIONS" {
			w.WriteHeader(204)
			return
		}
		r = r.WithContext(identity.WithSignInSource(networkpolicy.WithHome(r.Context(), !d.requestIsRemote(r)), d.signInPeer(r)))
		r = r.WithContext(d.withSessionAdmission(r))
		if castTargetRoute(r.Method, r.URL.Path) {
			r = r.WithContext(identity.WithPlaybackRequest(r.Context()))
		}
		if installation := r.Header.Get("X-Portico-Installation-Id"); installation != "" {
			claim := identity.DeviceRegistration{
				InstallationID: installation,
				Name:           r.Header.Get("X-Portico-Device-Name"),
				Platform:       r.Header.Get("X-Portico-Device-Platform"),
				App:            r.Header.Get("X-Portico-App"),
				AppVersion:     r.Header.Get("X-Portico-App-Version"),
			}
			ctx, err := identity.WithIssuingDevice(r.Context(), claim, d.signInPeer(r))
			if err != nil {
				failure(w, err)
				return
			}
			r = r.WithContext(ctx)
		}
		if r.Method == "POST" && (strings.HasPrefix(r.URL.Path, "/v1/direct/") || strings.HasPrefix(r.URL.Path, "/v1/auth/") || r.URL.Path == "/v1/setup" || r.URL.Path == "/v1/cast/preview" || r.URL.Path == "/v1/cast/redeem" || r.URL.Path == "/v1/cast/reconnect" ||
			// Starting, looking up and deciding a TV code, and using an invitation:
			// refused cheaply here before the database-backed limits are asked.
			// The TV's own poll for its code is not here: a household of TVs
			// behind one address polls legitimately.
			r.URL.Path == "/v1/quick-connect" || r.URL.Path == "/v1/quick-connect/review" || r.URL.Path == "/v1/quick-connect/decision" || strings.HasPrefix(r.URL.Path, "/v1/access/invitations/")) {
			if !authRate.allow(r) {
				w.Header().Set("Retry-After", "60")
				write(w, 429, map[string]any{"error": map[string]any{"code": "rate_limited", "message": "Too many authentication attempts. Try again shortly.", "retryable": true}})
				return
			}
		}
		// Every request carries the edge's own view of how far the client is;
		// delivery planning reads it from the context rather than guessing.
		if strings.HasPrefix(r.URL.Path, "/v1/") {
			if _, pattern := mux.Handler(r); pattern == "" {
				// No route answers this method and path: the mux's own reply is
				// a text body, so answer in the envelope the clients read
				// (BE-API-08). A path that exists for other methods is 405.
				unmatchedRoute(w, r, mux)
				return
			}
		}
		// An API key or personal access token is resolved, held to the route's
		// scope and charged to its own limits before anything else sees it.
		r, release, ok := d.admitCredential(w, r, mux)
		if !ok {
			return
		}
		if release != nil {
			defer release()
		}
		r, ok = d.bindUserMutation(w, r)
		if !ok {
			return
		}
		admitted.ServeHTTP(w, d.withDelivery(r))
		if d.Hosted != nil && r.Method != "GET" && r.Method != "HEAD" && membershipPath(r.URL.Path) {
			// The journal triggers already recorded any change in the writer's
			// transaction; this only tells the push loop to look now rather
			// than at its next deadline. A request that changed nothing costs
			// the loop one indexed read.
			d.Hosted.MembershipChanged()
		}
	})
	return &credentialUsageHandler{Handler: d.self.h, gate: d.credentials, store: d.Access.Access, playback: d.v1}
}

// unmatchedRoute answers a /v1 request no route serves: 405 with Allow when
// the path exists for other methods, else 404, both in the error envelope.
func unmatchedRoute(w http.ResponseWriter, r *http.Request, mux *http.ServeMux) {
	allowed := []string{}
	for _, method := range []string{"GET", "POST", "PUT", "PATCH", "DELETE"} {
		if method == r.Method {
			continue
		}
		probe := r.Clone(r.Context())
		probe.Method = method
		if _, pattern := mux.Handler(probe); pattern != "" {
			allowed = append(allowed, method)
		}
	}
	if len(allowed) > 0 {
		w.Header().Set("Allow", strings.Join(allowed, ", "))
		write(w, http.StatusMethodNotAllowed, map[string]any{"error": map[string]any{"code": "method_not_allowed", "message": "This method is not supported here.", "retryable": false}})
		return
	}
	write(w, http.StatusNotFound, map[string]any{"error": map[string]any{"code": "not_found", "message": "The request could not be completed.", "retryable": false}})
}

// membershipPath lists the routes that can change who is a member here.
func membershipPath(path string) bool {
	return strings.HasPrefix(path, "/v1/admin/access/members") || strings.HasPrefix(path, "/v1/direct/members") ||
		path == "/v1/direct/ownership" || path == "/v1/direct/ownership/custody" || path == "/v1/access/invitations/accept"
}

type guardedFile struct {
	*os.File
	check func() error
	last  time.Time
}

func (f *guardedFile) Read(p []byte) (int, error) {
	if time.Since(f.last) > 100*time.Millisecond {
		if e := f.check(); e != nil {
			return 0, e
		}
		f.last = time.Now()
	}
	return f.File.Read(p)
}

type guardedReader struct {
	readError error
	io.Reader
	check func() error
	last  time.Time
}

func (r *guardedReader) Read(p []byte) (int, error) {
	if time.Since(r.last) > 100*time.Millisecond {
		if e := r.check(); e != nil {
			return 0, e
		}
		r.last = time.Now()
	}
	n, e := r.Reader.Read(p)
	if e != nil && e != io.EOF {
		r.readError = e
	}
	return n, e
}

// Guard before and after IPC: a revocation during a blocked read discards bytes.
type guardedReadSeeker struct {
	io.ReadSeeker
	check func() error
}

func (f *guardedReadSeeker) Read(p []byte) (int, error) {
	if e := f.check(); e != nil {
		return 0, e
	}
	n, e := f.ReadSeeker.Read(p)
	if guard := f.check(); guard != nil {
		return 0, guard
	}
	return n, e
}

func (d Dependencies) itemsReadHandler(w http.ResponseWriter, r *http.Request) {
	p, libraries, fence, e := d.homeScope(r)
	if e != nil {
		failure(w, e)
		return
	}
	lib := r.URL.Query().Get("libraryId")
	if lib != "" {
		if e = d.allowedLibrary(r.Context(), p, lib); e != nil {
			failure(w, e)
			return
		}
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	viewer, e := d.catalogViewer(r, p, libraries, fence)
	if e != nil {
		failure(w, e)
		return
	}
	items, next, e := d.Catalog.WithContext(r.Context()).List(viewer, lib, r.URL.Query().Get("cursor"), limit)
	if e != nil {
		failure(w, e)
		return
	}
	write(w, 200, map[string]any{"items": items, "nextCursor": next})
}

func (d Dependencies) itemReadHandler(w http.ResponseWriter, r *http.Request) {
	p, e := d.principal(r)
	if e == nil {
		e = d.itemAccess(r.Context(), p, r.PathValue("id"))
	}
	if e == nil {
		e = d.restrictedItem(r, p, r.PathValue("id"))
	}
	if e != nil {
		failure(w, e)
		return
	}
	r, ready, proceed := d.prepareFileInfoDocument(w, r, "item")
	if !proceed {
		return
	}
	var item catalog.Item
	e = dbwork.WithReadSnapshot(r.Context(), d.DB, func(ctx context.Context) error {
		var e error
		cat := d.Catalog.WithContext(ctx)
		item, e = cat.Get(identity.PersonalKey(p.Viewer), r.PathValue("id"))
		if e != nil {
			return e
		}
		if ready != nil {
			revision, e := cat.ContentRevision(item.LibraryID, identity.PersonalKey(p.Viewer))
			if e != nil {
				return e
			}
			item.FileInfoOpening, e = d.FileInfoRuntime.AcceptPreparedOpening(ctx, ready.caller, *ready.ready, revision)
			return e
		}
		return nil
	})
	if e != nil {
		fileInfoFailure(w, e)
		return
	}
	if e = d.castOccurrenceAllowed(r.Context(), p, r.PathValue("id"), ""); e != nil {
		failure(w, e)
		return
	}
	if item.FileInfoOpening != "" && conditional(w, r, responseTag("item", item.ID, item.FileInfoOpening)) {
		return
	}
	write(w, 200, polledFileInfoDocument(r, "item", item))
}
