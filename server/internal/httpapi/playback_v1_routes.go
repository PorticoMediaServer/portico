package httpapi

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/apikit"
	"github.com/PorticoMediaServer/portico/apikit/apierror"
	"github.com/PorticoMediaServer/portico/server/internal/access"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/contentaccess"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/mediaanalysis"
	"github.com/PorticoMediaServer/portico/server/internal/operations"
	"github.com/PorticoMediaServer/portico/server/internal/playback"
	"github.com/PorticoMediaServer/portico/server/internal/playbackv1"
	"github.com/PorticoMediaServer/portico/server/internal/sonic"
)

// v1Route registers one route, failing registry construction loudly: a route
// that cannot be registered is a programming error, not a runtime condition.
func v1Route[Req, Resp any](r *apikit.Registry, m apikit.Metadata, h func(context.Context, *http.Request, Req) (Resp, error)) {
	if !slices.Contains(m.Errors, "timeout") {
		m.Errors = append(m.Errors, "timeout")
	}
	if err := apikit.Register(r, apikit.Route[Req, Resp]{Metadata: m, Handler: func(ctx context.Context, q *http.Request, in Req) (Resp, error) {
		out, err := h(ctx, q, in)
		return out, v1Error(err)
	}}); err != nil {
		panic(m.ID + ": " + err.Error())
	}
}

// playbackV1 is the router's single v1 resource service.
func (d Dependencies) playbackV1() *playbackv1.Service { return d.v1 }

func newPlaybackV1(d Dependencies) *playbackv1.Service {
	s := buildPlaybackV1(d)
	if s == nil {
		return nil
	}
	s.StopAuthorityTx = func(ctx context.Context, tx *sql.Tx, c playbackv1.Caller) error {
		if d.Identity == nil || tx == nil || dbwork.Snapshot(ctx) != tx || c.DeviceID == "" {
			return identity.ErrForbidden
		}
		family, err := d.Identity.SessionFamilyTx(ctx, tx, c.Principal)
		if err != nil {
			return err
		}
		if family.DeviceID != c.DeviceID {
			return identity.ErrForbidden
		}
		return nil
	}
	s.ControlsAuthorityTx = func(ctx context.Context, tx *sql.Tx, c playbackv1.Caller) error {
		_, err := d.captureOwnControlsRecipientTx(ctx, tx, c)
		return err
	}
	s.CastQueueAuthority = d.castQueueAuthorityTx
	s.CastQueueStartAuthority = d.castQueueStartAuthorityTx
	s.CastQueueTransferCheck = func(ctx context.Context, tx *sql.Tx, c playbackv1.Caller, id, q, e, item string) error {
		if d.Identity == nil {
			return identity.ErrForbidden
		}
		return d.Identity.CastQueueTransferStartTx(ctx, tx, c.Principal, id, q, e, item)
	}
	s.CastQueueTransferCommit = func(ctx context.Context, tx *sql.Tx, c playbackv1.Caller, id, q, e, item, target, key, digest string) error {
		if d.Identity == nil {
			return identity.ErrForbidden
		}
		return d.Identity.CommitCastQueueTransferTx(ctx, tx, c.Principal, id, q, e, item, target, key, digest)
	}
	s.CastQueueTransferReplay = func(ctx context.Context, tx *sql.Tx, c playbackv1.Caller, id, target, key, digest string) error {
		if d.Identity == nil {
			return identity.ErrForbidden
		}
		return d.Identity.ReplayCastQueueTransferTx(ctx, tx, c.Principal, id, target, key, digest)
	}

	// Queue sources and windows are authorized like every other catalog read:
	// library access, then the item's restrictions (SEC-02).
	s.QueryKeys = func(ctx context.Context, tx *sql.Tx, p identity.Principal, source playbackv1.Selector) (catalog.QueueSelection, error) {
		if d.Catalog == nil {
			return catalog.QueueSelection{}, playbackv1.ErrUnsupportedSelector
		}
		viewer, err := d.queueCatalogViewerTx(ctx, tx, p)
		if err != nil {
			return catalog.QueueSelection{}, err
		}
		if source.Query != nil && source.Query.SelectionRevision != "" {
			browseViewer := viewer
			browseViewer.Fence, err = d.browseFenceTx(ctx, tx, p, viewer.Restrictions)
			if err != nil {
				return catalog.QueueSelection{}, err
			}
			if err = d.Catalog.CheckQueueBrowseRevisionTx(ctx, tx, browseViewer, source.Query); err != nil {
				if errors.Is(err, catalog.ErrStaleContinuation) {
					return catalog.QueueSelection{}, playbackv1.ErrRevision
				}
				return catalog.QueueSelection{}, err
			}
		}
		saved := ""
		if source.Saved != nil {
			saved = source.Saved.ID
		}
		var homeRows map[string][]int64
		var labels map[string]string
		if held, ok := ctx.Value(queueHomeRowsKey{}).(queueHomeRows); ok {
			homeRows, labels = held.members, held.titles
		}
		selection, err := d.Catalog.QueueKeysTx(ctx, tx, viewer, catalog.ResourceActor{Authority: p.Authority, AccountID: p.AccountID, ProfileID: p.ProfileID}, source.Query, saved, homeRows)
		if errors.Is(err, catalog.ErrStaleContinuation) {
			return catalog.QueueSelection{}, playbackv1.ErrRevision
		}
		if err == nil && selection.CheckBasis != nil {
			library, basis := source.Query.LibraryID, source.Query.SelectionBasis
			selection.CheckBasis = func(ctx context.Context, tx *sql.Tx) error {
				if err := d.allowedLibraryTx(ctx, p, library, tx); err != nil {
					return err
				}
				restrictions, err := contentaccess.RestrictionsTx(ctx, tx, p.Viewer)
				if err != nil {
					return err
				}
				fresh := catalog.Viewer{Profile: identity.PersonalKey(p.Viewer), Libraries: []string{library}, Restrictions: restrictions}
				err = d.Catalog.CheckBrowseSelectionBasisTx(ctx, tx, fresh, library, basis)
				if errors.Is(err, catalog.ErrStaleContinuation) {
					return playbackv1.ErrRevision
				}
				return err
			}
		}
		if err == nil && source.Query != nil {
			// A Home row's queue is named after the row.
			if ids := catalog.BrowseHomeRows(source.Query.Query); len(ids) == 1 && labels[ids[0]] != "" {
				selection.Label = labels[ids[0]]
			}
		}
		return selection, err
	}
	s.RadioKeys = func(ctx context.Context, tx *sql.Tx, p identity.Principal, request catalog.RadioBatchRequest) (catalog.RadioBatch, error) {
		if d.Catalog == nil {
			return catalog.RadioBatch{}, playbackv1.ErrUnsupportedSelector
		}
		viewer, err := d.queueCatalogViewerTx(ctx, tx, p)
		if err != nil {
			return catalog.RadioBatch{}, err
		}
		return d.Catalog.MusicRadioBatchTx(ctx, tx, viewer, request)
	}
	s.MixKeys = func(ctx context.Context, tx *sql.Tx, p identity.Principal, id string) ([]string, error) {
		if d.Catalog == nil {
			return nil, playbackv1.ErrUnsupportedSelector
		}
		viewer, err := d.queueCatalogViewerTx(ctx, tx, p)
		if err != nil {
			return nil, err
		}
		return d.Catalog.MusicMixKeysTx(ctx, tx, viewer, time.Now(), id)
	}
	s.JourneyKeys = func(ctx context.Context, tx *sql.Tx, p identity.Principal, request playbackv1.JourneySelector) ([]string, error) {
		if d.Catalog == nil {
			return nil, playbackv1.ErrUnsupportedSelector
		}
		viewer, err := d.queueCatalogViewerTx(ctx, tx, p)
		if err != nil {
			return nil, err
		}
		journey, err := d.Catalog.MusicJourneyTx(ctx, tx, viewer, request.From, request.To, request.Length)
		if errors.Is(err, sql.ErrNoRows) || errors.Is(err, sonic.ErrUnavailable) {
			return nil, playbackv1.ErrUnsupportedSelector
		}
		return journey.Tracks, err
	}
	s.SmoothOrder = func(ctx context.Context, tx *sql.Tx, p identity.Principal, ids []string) (catalog.MusicSmoothOrder, error) {
		if d.Catalog == nil {
			return catalog.MusicSmoothOrder{}, playbackv1.ErrUnsupportedSelector
		}
		viewer, err := d.queueCatalogViewerTx(ctx, tx, p)
		if err != nil {
			return catalog.MusicSmoothOrder{}, err
		}
		return d.Catalog.MusicSmoothOrderTx(ctx, tx, viewer, ids)
	}
	s.LibraryCheck = func(ctx context.Context, tx *sql.Tx, p identity.Principal, library string) error {
		return d.allowedLibraryTx(ctx, p, library, tx)
	}
	s.Visibility = func(ctx context.Context, tx *sql.Tx, p identity.Principal, item string) (string, []any, error) {
		return contentaccess.VisibleItemsSQL(ctx, tx, p, item, func(library string) error { return d.allowedLibraryTx(ctx, p, library, tx) })
	}
	s.PostPlay = func(ctx context.Context, tx *sql.Tx, p identity.Principal) (playbackv1.PostPlayPolicy, error) {
		values, _, _, err := operations.EffectivePreferences(tx, p.Viewer, "")
		if err != nil {
			return playbackv1.PostPlayPolicy{}, err
		}
		return playbackv1.PostPlayPolicy{Autoplay: values.Bool("playback.autoplayNext"), CountdownMs: values.Int("playback.upNextCountdownMs"), StartDuringCredits: values.Bool("playback.startNextDuringCredits"), Protection: values.Bool("playback.passoutProtection"), Episodes: values.Int("playback.passoutAfterEpisodes")}, nil
	}
	if d.AudioMedia != nil {
		s.MeasureAudio = d.AudioMedia.MeasureAudio
	}
	if d.Console != nil {
		// Marker skips land in the runtime diagnostics lane; the console store owns
		// validation, retention and the lane cap.
		s.SkipEvidence = func(ctx context.Context, code string, fields map[string]int64) {
			_ = d.Console.Record(ctx, "runtime", "info", "playback", code, fields)
		}
		// The paused-session limit (Plex "Terminate Sessions Paused for Longer
		// Than"): video sessions paused longer than this are ended by the
		// sweeper. Unset or non-positive: off; a read error: off for that tick.
		s.PausedLimit = func(ctx context.Context) time.Duration {
			document, err := d.Console.Settings(ctx, operations.AllowServerScope)
			if err != nil {
				return 0
			}
			if minutes := document.Effective.PausedSessionTimeoutMinutes; minutes > 0 {
				return time.Duration(minutes) * time.Minute
			}
			return 0
		}
	}
	// A member's allowed hours close while they watch: the sweeper ends their
	// sessions with the reason, so every player says why (N20).
	if d.Access.Access != nil && d.DB != nil {
		db, enforcer := d.DB, d.enforcer()
		s.OutsideHours = func(ctx context.Context, account string) bool {
			tx, done, err := dbwork.BeginRead(ctx, db)
			if err != nil {
				return false
			}
			defer done()
			return errors.Is(enforcer.AdmitAccount(ctx, tx, account), access.ErrSchedule)
		}
	}
	// Channels on v1 sessions (§18.6): the runtime's channel sessions, its wake,
	// and the live sources' tuner capacity.
	if rt := d.PlaybackRuntime; rt != nil && rt.Channels != nil && rt.Channels.Configured() {
		s.Channels = rt.Channels
		s.ChannelWake = rt.Wake
		s.TunerAvailable = liveTunerAvailable
	}
	s.CreatePlaylist = func(ctx context.Context, p identity.Principal, key, name, summary string, items []string) (string, int64, error) {
		if d.Catalog == nil {
			return "", 0, playbackv1.ErrUnsupportedSelector
		}
		m := catalog.PlaylistMutation{OperationID: key, Name: &name, ItemIDs: items}
		if summary != "" {
			m.Summary = &summary
		}
		// The playlists route's own authorization: the caller, and every item's library.
		// Bind the catalogue to this request so its write waits and statements
		// carry the request deadline. The root service falls back to the
		// background context, and a save whose catalogue write waited on a busy
		// write gate would then outlive its own budget and the client's request.
		out, err := d.Catalog.WithContext(ctx).MutatePlaylist(resourceActor(p), "", "create", "", m, func(tx *sql.Tx) error {
			for _, item := range items {
				var library string
				if err := tx.QueryRow(`SELECT cl.library_id FROM catalog_entities e JOIN catalog_libraries cl ON cl.id=e.library_id WHERE e.public_id=pid_blob(?)`, item).Scan(&library); err != nil {
					return err
				}
				if err := d.resourceAuthorize(tx, p, library); err != nil {
					return err
				}
			}
			return d.resourceAuthorize(tx, p, "")
		})
		if errors.Is(err, catalog.ErrOperationConflict) {
			err = playbackv1.ErrIdempotencyMismatch
		}
		return out.PlaylistID, out.Revision, err
	}
	s.Unrestricted = func(ctx context.Context, tx *sql.Tx, p identity.Principal) (bool, error) {
		return contentaccess.Unrestricted(ctx, tx, p, func(library string) error { return d.allowedLibraryTx(ctx, p, library, tx) })
	}
	return s
}

func buildPlaybackV1(d Dependencies) *playbackv1.Service {
	if s := d.PlaybackV1; s != nil {
		if s.DB == nil {
			s.DB = d.DB
		}
		if s.Playback == nil {
			s.Playback = d.Playback
		}
		if s.Subtitles == nil {
			s.Subtitles = d.Subtitles
		}
		return s
	}
	return playbackv1.New(d.DB, d.Playback, d.Subtitles)
}

type noBody struct{}

// registerPlaybackV1 adds every Playback Protocol v1 route to the registry.
func registerPlaybackV1(r *apikit.Registry, d Dependencies) {
	registerPlaybackV1Options(r, d)
	registerPlaybackV1Sessions(r, d)
	registerPlaybackV1Queues(r, d)
	registerPlaybackV1Admin(r, d)
}

// AdminTerminateRequest is POST /v1/admin/sessions/{id}:terminate.
type AdminTerminateRequest struct {
	Message string `json:"message,omitempty"`
}

// registerPlaybackV1Admin is Now Playing (spec §14): the active sessions and
// terminate, for administrators. admin.sessions events say when to refetch.
func registerPlaybackV1Admin(r *apikit.Registry, d Dependencies) {
	registerRouteFailureMemory(r, d)
	v1Route(r, apikit.Metadata{Audience: apikit.Integrations, Admission: laneAdmin, ID: "list_admin_sessions", Method: "GET", Path: "/v1/admin/sessions", Summary: "Active playback sessions: who, what, how it's delivered and from where",
		Access: apikit.Admin, Requires: apikit.ScopeAdmin, AdminScope: "playback", Lane: apikit.Default, Cost: apikit.PageSized, Query: []string{"limit", "cursor"}, QueryIntegers: []apikit.QueryInteger{{Name: "limit", Minimum: 0, Default: 10, Maximum: 200, EmptyIsDefault: true, Description: "Zero selects the default page size."}}, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted"}},
		func(ctx context.Context, q *http.Request, _ noBody) (playbackv1.AdminSessionPage, error) {
			limit := 0
			if text := q.URL.Query().Get("limit"); text != "" {
				n, err := strconv.Atoi(text)
				if err != nil {
					return playbackv1.AdminSessionPage{}, &playbackv1.FieldError{Path: "limit"}
				}
				limit = n
			}
			return d.dashboardSessions(ctx, q, limit, q.URL.Query().Get("cursor"))
		})
	v1Route(r, apikit.Metadata{Audience: apikit.Integrations, Admission: laneAdmin, ID: "terminate_admin_session", Method: "POST", Path: "/v1/admin/sessions/{id}:terminate", Summary: "End a viewer's playback, showing them an optional message; idempotent",
		Access: apikit.Admin, Requires: apikit.ScopeAdmin, AdminScope: "playback", Lane: apikit.Default, Cost: apikit.Constant, BodyLimit: 4 << 10, OptionalBody: true, Status: 204, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found"}},
		func(ctx context.Context, q *http.Request, in AdminTerminateRequest) (noBody, error) {
			return noBody{}, d.playbackV1().Terminate(ctx, q.PathValue("id"), in.Message)
		})
}

// assumedPlaybackProtocol is the Portico-Playback version taken for a request
// that names none. It is zero in the server, so such a client is refused at
// start. This package's tests set it to the current version (see
// playback_protocol_test.go), so their direct requests act as current clients;
// the refusal itself is tested with an explicit older version.
var assumedPlaybackProtocol int

// v1Caller turns the admitted caller into the service's caller, with media
// admission for new presentations and channel admission for channel starts.
func (d Dependencies) v1ServiceCaller(q *http.Request, c v1Caller) playbackv1.Caller {
	uploadLimited := false
	protocol := assumedPlaybackProtocol
	if declared := q.Header.Get("Portico-Playback"); declared != "" {
		protocol, _ = strconv.Atoi(declared)
	}
	return playbackv1.Caller{PlaybackProtocol: protocol, UploadLimited: &uploadLimited, Principal: c.Principal, DeviceID: c.DeviceID, Remote: d.v1Remote(q), TransportClass: d.deliveryContext(q).TransportClass, AuthorizeItem: func(ctx context.Context, item string) error {
		return itemAdmission(d.itemAccess(ctx, c.Principal, item))
	}, Admit: func(ctx context.Context, item string) (int, error) {
		// A channel start admits with no item: there is no title to fence,
		// only the account-level limits. The channel allow/deny list runs
		// separately through AdmitChannel.
		if item != "" {
			if err := itemAdmission(d.itemAccess(ctx, c.Principal, item)); err != nil {
				return 0, err
			}
		}
		decision, err := d.admitPlayback(q.WithContext(ctx), c.Principal, item)
		if err != nil {
			return 0, itemAdmission(err)
		}
		if replan := playbackv1.ReplanOf(ctx); replan.Remote {
			ceilings, err := d.replanCeilingsAt(q.WithContext(ctx), c.Principal, replan.Session, true)
			if err != nil {
				return 0, err
			}
			decision.MaxVideoBitrateBPS = narrowBitrate(decision.MaxVideoBitrateBPS, ceilings.MaxVideoBitrateBPS)
		}
		uploadLimited = decision.UploadLimited
		return decision.MaxVideoBitrateBPS, nil
	}, AdmitChannel: func(ctx context.Context, channel string) error {
		if err := d.admitChannel(q.WithContext(ctx), c.Principal, channel); err != nil {
			return itemAdmission(err)
		}
		return nil
	}, Ceiling: func(ctx context.Context, session string) (int, error) {
		ceilings, err := d.replanCeilingsAt(q.WithContext(ctx), c.Principal, session, d.requestIsRemote(q) || playbackv1.ReplanOf(ctx).Remote)
		return ceilings.MaxVideoBitrateBPS, err
	}, SavedQuality: func(ctx context.Context) (string, error) {
		return d.savedQuality(q.WithContext(ctx), c.Principal)
	}, SavedMusicQuality: func(ctx context.Context) (string, error) {
		return d.savedMusicQuality(q.WithContext(ctx), c.Principal)
	}}
}

// savedQuality is the viewer's saved default quality for the connection a
// request arrives on: quality.home inside the owner's home networks,
// quality.remote outside them, or quality.mobile on actual cellular transport.
func (d Dependencies) savedQuality(q *http.Request, p identity.Principal) (string, error) {
	if d.DB == nil {
		return playback.QualityOriginal, nil
	}
	tx, done, err := dbwork.BeginRead(q.Context(), d.DB)
	if err != nil {
		return "", err
	}
	defer done()
	values, sources, _, err := operations.EffectivePreferences(tx, p.Viewer, deviceClassHeader(q))
	if err != nil {
		return "", err
	}
	class, _ := playback.NetworkClassOf(d.requestIsRemote(q))
	return playback.SavedQualityPreference(playback.WithPreferenceSources(values, sources), class, d.deliveryContext(q).TransportClass), nil
}

func (d Dependencies) savedMusicQuality(q *http.Request, p identity.Principal) (string, error) {
	if d.DB == nil {
		return playback.QualityOriginal, nil
	}
	tx, done, err := dbwork.BeginRead(q.Context(), d.DB)
	if err != nil {
		return "", err
	}
	defer done()
	values, sources, _, err := operations.EffectivePreferences(tx, p.Viewer, deviceClassHeader(q))
	if err != nil {
		return "", err
	}
	class, _ := playback.NetworkClassOf(d.requestIsRemote(q))
	return playback.SavedMusicQualityPreference(playback.WithPreferenceSources(values, sources), class, d.deliveryContext(q).TransportClass), nil
}

// replanCeilingsAt is what admission would allow this caller's connection right
// now, read without admitting anything: no stream is counted, and the schedule
// and the item policy are not applied. It is whether the connection is remote,
// as admission decides it, and there the member's bitrate cap narrowed by the
// upload budget. session is the v1 session being changed, whose own share of
// the budget is not counted against it.
func (d Dependencies) replanCeilingsAt(q *http.Request, p identity.Principal, session string, remote bool) (playback.ReplanCeilings, error) {
	out := playback.ReplanCeilings{Remote: remote}
	if !out.Remote || d.Access.Access == nil || d.DB == nil {
		return out, nil
	}
	document, err := d.settingsDocument(q.Context())
	if err != nil {
		return out, err
	}
	tx, done, err := dbwork.BeginRead(q.Context(), d.DB)
	if err != nil {
		return out, err
	}
	defer done()
	if out.MaxVideoBitrateBPS, err = d.enforcer().RemoteBitrateCap(q.Context(), tx, p); err != nil {
		return out, err
	}
	if document.Effective.UploadCapacityKbps > 0 {
		ceiling, _, err := uploadBudgetCeiling(q.Context(), tx, document.Effective.UploadCapacityKbps, session, time.Now())
		if err != nil {
			return out, err
		}
		out.MaxVideoBitrateBPS = narrowBitrate(out.MaxVideoBitrateBPS, ceiling)
	}
	return out, nil
}

// v1Remote is whether a request came from outside this server's LAN, decided by
// the server from the connection, never by the client's own claim. Remote
// bitrate caps apply only then.
func (d Dependencies) v1Remote(q *http.Request) bool {
	return d.requestIsRemote(q)
}

// v1Reach is a preview's reach: the same remote flag and member cap a start
// from this connection would get, read without admitting anything.
func (d Dependencies) v1Reach(q *http.Request, p identity.Principal) playbackv1.Reach {
	reach := playbackv1.Reach{SavedQuality: playback.QualityOriginal, TransportClass: d.deliveryContext(q).TransportClass, Remote: d.v1Remote(q), DeviceID: callerFrom(q.Context()).DeviceID}
	if saved, err := d.savedQuality(q, p); err == nil {
		reach.SavedQuality = saved
	}
	if saved, err := d.savedMusicQuality(q, p); err == nil {
		reach.SavedMusicQuality = saved
	} else {
		reach.SavedMusicQuality = playback.QualityOriginal
	}
	if !reach.Remote || d.Access.Access == nil || d.DB == nil {
		return reach
	}
	tx, done, err := dbwork.BeginRead(q.Context(), d.DB)
	if err != nil {
		return reach
	}
	defer done()
	if limit, err := d.enforcer().RemoteBitrateCap(q.Context(), tx, p); err == nil {
		reach.AdminMaxVideoBitrateBPS = limit
	}
	return reach
}

func setSessionHeaders(ctx context.Context, v playbackv1.SessionView) {
	apikit.SetHeader(ctx, "ETag", `"`+v.Revision+`"`)
}

func registerPlaybackV1Sessions(r *apikit.Registry, d Dependencies) {
	observationRate := newObservationLimiter()
	sessionErrors := []string{"invalid_request", "unauthorized", "not_found", "session_ended", "revision_required", "revision_mismatch"}
	v1Route(r, apikit.Metadata{Audience: apikit.Clients, Admission: lanePlayback, ID: "prepare_airplay_receiver", Method: "POST", Path: "/v1/playback/sessions/{id}/receivers/airplay", Summary: "Prepare the current choices as native HLS before wireless handoff", Access: apikit.Device, Requires: apikit.ScopePlayback, Lane: apikit.Media, Cost: apikit.Constant, BodyLimit: 1024, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "session_ended", "revision_mismatch", "conversion_capacity", "transcode_not_allowed", "unsupported_media"}},
		func(ctx context.Context, q *http.Request, in playbackv1.AirPlayPrepare) (playbackv1.AirPlayPresentation, error) {
			return d.playbackV1().PrepareAirPlay(ctx, d.v1ServiceCaller(q, callerFrom(ctx)), q.PathValue("id"), in)
		})

	v1Route(r, apikit.Metadata{Audience: apikit.Clients, Admission: lanePlayback, ID: "start_playback_session", Method: "POST", Path: "/v1/playback/sessions", Summary: "Start playback (201), or report that it is being prepared (202)",
		Access: apikit.Device, Requires: apikit.ScopePlayback, Lane: apikit.Media, Cost: apikit.Constant, BodyLimit: 8 << 10, Status: 201, SuccessStatuses: []int{202},
		Errors: []string{"invalid_request", "unauthorized", "not_found", "idempotency_key_required", "idempotency_key_reused", "stream_limit_reached", "conversion_capacity", "transcode_not_allowed", "source_unavailable", "storage_offline", "unsupported_media", "connection_too_slow", "feature_restricted", "no_tuner_available", "queue_building"}},
		func(ctx context.Context, q *http.Request, in playbackv1.StartRequest) (playbackStartResponse, error) {
			c := d.v1ServiceCaller(q, callerFrom(ctx))
			started, err := d.playbackV1().StartPrepared(ctx, c, q.Header.Get("Idempotency-Key"), in)
			if err != nil {
				if errors.Is(err, playback.ErrOwnerAccountCap) {
					if detail, e := d.playbackV1().AccountStreamLimit(ctx, c); e == nil {
						return playbackStartResponse{}, &apierror.Error{Code: "stream_limit_reached", Reason: "owner_account_cap", Current: detail, Cause: err}
					}
				}
				return playbackStartResponse{}, err
			}
			out, err := startPreparedResponse(started)
			if err != nil {
				return playbackStartResponse{}, err
			}
			if started.Preparing {
				apikit.SetStatus(ctx, 202)
				apikit.SetHeader(ctx, "Retry-After", "1")
			}
			if started.Session != nil {
				setSessionHeaders(ctx, *started.Session)
			}
			return out, nil
		})
	v1Route(r, apikit.Metadata{Audience: apikit.Clients, Admission: laneSecurityFence, ID: "get_playback_session", Method: "GET", Path: "/v1/playback/sessions/{id}", Summary: "Read a playback session",
		Access: apikit.Device, Requires: apikit.ScopeRead, Lane: apikit.Media, Cost: apikit.Constant, Status: 200, Errors: []string{"unauthorized", "not_found"}},
		func(ctx context.Context, q *http.Request, _ noBody) (playbackv1.SessionView, error) {
			// The command revision does not change for lease renewals or renderer
			// observations. Let apikit hash the complete response for GET; PATCH
			// continues to use the revision carried in the body for If-Match.
			return d.playbackV1().Get(ctx, d.v1ServiceCaller(q, callerFrom(ctx)), q.PathValue("id"))
		})
	v1Route(r, apikit.Metadata{Audience: apikit.Clients, Admission: lanePlayback, ID: "change_playback_session", Method: "PATCH", Path: "/v1/playback/sessions/{id}", Summary: "Change tracks, quality, version or state, or report that the player failed; a change to the bytes re-plans the same session as a new generation and keeps its queue",
		Access: apikit.Device, Requires: apikit.ScopePlayback, Lane: apikit.Media, Cost: apikit.Constant, BodyLimit: 4 << 10, Status: 200,
		Errors: append(sessionErrors, "stream_limit_reached", "conversion_capacity", "transcode_not_allowed", "unsupported_media", "connection_too_slow")},
		func(ctx context.Context, q *http.Request, in playbackv1.Change) (playbackv1.SessionView, error) {
			v, err := d.playbackV1().Patch(ctx, d.v1ServiceCaller(q, callerFrom(ctx)), q.PathValue("id"), q.Header.Get("If-Match"), in)
			if err == nil {
				setSessionHeaders(ctx, v)
			}
			return v, err
		})
	v1Route(r, apikit.Metadata{Audience: apikit.Clients, Admission: lanePlaybackStop, ID: "stop_playback_session", Method: "DELETE", Path: "/v1/playback/sessions/{id}", Summary: "Stop playback; idempotent",
		Access: apikit.Device, Requires: apikit.ScopePlayback, Lane: apikit.Media, Cost: apikit.Constant, BodyLimit: 1 << 10, OptionalBody: true, Status: 204, Errors: []string{"invalid_request", "unauthorized", "not_found", "revision_mismatch"}},
		func(ctx context.Context, q *http.Request, in playbackv1.StopRequest) (noBody, error) {
			if in.ExpectedGeneration != nil {
				return noBody{}, d.playbackV1().StopGeneration(ctx, d.v1ServiceCaller(q, callerFrom(ctx)), q.PathValue("id"), *in.ExpectedGeneration)
			}
			return noBody{}, d.playbackV1().Stop(ctx, d.v1ServiceCaller(q, callerFrom(ctx)), q.PathValue("id"), in.PositionMs, in.Generation)
		})
	v1Route(r, apikit.Metadata{Audience: apikit.Clients, Admission: lanePlayback, ID: "claim_playback_observer", Method: "POST", Path: "/v1/playback/sessions/{id}/observer", Summary: "Become the player that reports this session; returns the epoch its observations carry",
		Access: apikit.Device, Requires: apikit.ScopePlayback, Lane: apikit.Media, Cost: apikit.Constant, BodyLimit: 1 << 10, Status: 200, Errors: append(sessionErrors, "observation_owned", "observation_capacity")},
		func(ctx context.Context, q *http.Request, in playbackv1.ObserverClaim) (playbackv1.ObserverGrant, error) {
			return d.playbackV1().ClaimObserver(ctx, d.v1ServiceCaller(q, callerFrom(ctx)), q.PathValue("id"), in)
		})
	v1Route(r, apikit.Metadata{Audience: apikit.Clients, Admission: laneObservation, ID: "put_playback_observation", Method: "PUT", Path: "/v1/playback/sessions/{id}/observation", Summary: "Report the player's latest state about once a second; the latest wins and a missed one is replaced by the next",
		Access: apikit.Device, Requires: apikit.ScopePlayback, Lane: apikit.Media, Cost: apikit.Constant, BodyLimit: 4 << 10, Status: 200, Errors: append(sessionErrors, "observation_epoch_changed", "rate_limited")},
		func(ctx context.Context, q *http.Request, in playbackv1.Observation) (playbackv1.ObservationAck, error) {
			caller := callerFrom(ctx)
			if !observationRate.allow(caller, time.Now()) {
				return playbackv1.ObservationAck{}, &apierror.Error{Code: "rate_limited", RetryAfterSeconds: 1}
			}
			apikit.SetHeader(ctx, "Cache-Control", "no-store")
			service, id := d.v1ServiceCaller(q, caller), q.PathValue("id")
			ack, err := d.playbackV1().Observe(ctx, service, id, in)
			if err != nil || !ack.Channel {
				return ack, err
			}
			// A channel's view (window, programme, seek outcome) rides on the
			// ack, so its player needs no second request a second. The digest
			// and the view come from one read snapshot, and the view is built
			// only when the tag the player holds is stale.
			view, tag, err := d.playbackV1().ChannelObservationView(ctx, service, id, in.ViewTag)
			if err == nil {
				ack.ViewTag = tag
				if view != nil {
					ack.Session = view
				}
				return ack, nil
			}
			// The exact read refused or failed (a lapsed lease, a channel the
			// viewer may no longer watch): take the full read, which ends the
			// session or shows the ended/default view.
			full, err := d.playbackV1().Get(ctx, service, id)
			if err != nil {
				return ack, nil
			}
			raw, err := json.Marshal(full)
			if err != nil {
				return ack, nil
			}
			sum := sha256.Sum256(raw)
			ack.ViewTag = hex.EncodeToString(sum[:16])
			if ack.ViewTag != in.ViewTag {
				ack.Session = &full
			}
			return ack, nil
		})
}

func registerPlaybackV1Options(r *apikit.Registry, d Dependencies) {
	v1Route(r, apikit.Metadata{Audience: apikit.Clients, Admission: laneDefault, ID: "put_device_capabilities", Method: "PUT", Path: "/v1/me/devices/current/capabilities", Summary: "Replace this device's capability document: what each of its player engines can play, the viewer's player setting and overrides",
		Access: apikit.Device, Requires: apikit.ScopePlayback, Lane: apikit.Default, Cost: apikit.Constant, BodyLimit: playback.MaxCapabilityDocumentBytes, Status: 204,
		Errors: []string{"invalid_request", "unauthorized", "revision_mismatch"}},
		func(ctx context.Context, q *http.Request, in playback.CapabilityDocument) (noBody, error) {
			c := callerFrom(ctx)
			revision, err := d.playbackV1().PutCapabilities(ctx, c.Principal, c.DeviceID, q.Header.Get("If-Match"), in)
			if err == nil {
				apikit.SetHeader(ctx, "ETag", playbackv1.ETag(revision))
			}
			return noBody{}, err
		})
	v1Route(r, apikit.Metadata{Audience: apikit.Clients, Admission: laneBrowsing, ID: "get_device_capabilities", Method: "GET", Path: "/v1/me/devices/current/capabilities", Summary: "Read this device's capability document",
		Access: apikit.Device, Requires: apikit.ScopeRead, Lane: apikit.Default, Cost: apikit.Constant, Status: 200,
		Errors: []string{"unauthorized", "not_found"}},
		func(ctx context.Context, _ *http.Request, _ noBody) (playbackv1.CapabilitiesDocument, error) {
			// The revision names the whole document: a client that holds it is
			// answered 304 before the document is read or encoded.
			if revision, err := d.playbackV1().CapabilitiesRevision(ctx, callerFrom(ctx).DeviceID); err == nil && apikit.Conditional(ctx, strconv.FormatInt(revision, 10)) {
				return playbackv1.CapabilitiesDocument{}, nil
			}
			doc, err := d.playbackV1().Capabilities(ctx, callerFrom(ctx).DeviceID)
			if err == nil {
				apikit.SetHeader(ctx, "ETag", playbackv1.ETag(doc.Revision))
			}
			return doc, err
		})
	v1Route(r, apikit.Metadata{Audience: apikit.Clients, Admission: lanePlayback, ID: "get_playback_options", Method: "GET", Path: "/v1/items/{itemId}/playback-options", Summary: "Read what a title offers and what this device would get",
		Access: apikit.ViewerItem, Requires: apikit.ScopeRead, Lane: apikit.Media, Cost: apikit.Constant, Status: 200,
		Query:  []string{"discTitleId", "discTitleRevision", "versionId", "partId", "audioId", "subtitleId", "quality", "qualityId", "maxVideoBitrateKbps", "maxHeight", "maxAudioBitrateKbps", "throughputKbps"},
		Errors: []string{"invalid_request", "unauthorized", "not_found"}},
		func(ctx context.Context, q *http.Request, _ noBody) (playbackv1.Options, error) {
			c := callerFrom(ctx)
			preview, err := previewFrom(q)
			if err != nil {
				return playbackv1.Options{}, err
			}
			return d.playbackV1().Options(ctx, c.Principal, q.PathValue("itemId"), preview, d.v1Markers(q, c.Principal), d.v1Reach(q, c.Principal))
		})
}

// previewFrom reads the options preview query (plan §7 item 15).
func previewFrom(q *http.Request) (playbackv1.Preview, error) {
	v := q.URL.Query()
	p := playbackv1.Preview{VersionID: v.Get("versionId"), PartID: v.Get("partId"), AudioID: v.Get("audioId"), SubtitleID: v.Get("subtitleId"), Quality: playbackv1.Quality{Mode: v.Get("quality"), ID: v.Get("qualityId")}}
	if v.Get("discTitleId") != "" || v.Get("discTitleRevision") != "" {
		if len(v.Get("discTitleId")) != 64 || len(v.Get("discTitleRevision")) != 64 || p.VersionID == "" {
			return p, &playbackv1.FieldError{Path: "discTitleId"}
		}
		p.DiscTitle = &catalog.DiscTitleRef{ID: v.Get("discTitleId"), Revision: v.Get("discTitleRevision")}
	}

	number := func(key string) (int, error) {
		raw := v.Get(key)
		if raw == "" {
			return 0, nil
		}
		n, err := strconv.Atoi(raw)
		if err != nil || n < 0 {
			return 0, &playbackv1.FieldError{Path: key}
		}
		return n, nil
	}
	var err error
	if p.ThroughputKbps, err = number("throughputKbps"); err != nil {
		return p, err
	}
	if p.Quality.MaxVideoBitrateKbps, err = number("maxVideoBitrateKbps"); err != nil {
		return p, err
	}
	if p.Quality.MaxHeight, err = number("maxHeight"); err != nil {
		return p, err
	}
	if p.Quality.MaxAudioBitrateKbps, err = number("maxAudioBitrateKbps"); err != nil {
		return p, err
	}
	if p.Quality.Mode == "" {
		if p.Quality.MaxVideoBitrateKbps+p.Quality.MaxHeight+p.Quality.MaxAudioBitrateKbps > 0 || p.Quality.ID != "" {
			return p, &playbackv1.FieldError{Path: "quality"}
		}
		return p, nil
	}
	return p, p.Quality.Validate("quality")
}

// v1Markers projects intro/credits markers through the analysis viewer
// projection under this viewer's library authorization.
func (d Dependencies) v1Markers(q *http.Request, p identity.Principal) playbackv1.MarkerFunc {
	if d.Catalog == nil || d.Identity == nil {
		return nil
	}
	return func(ctx context.Context, tx *sql.Tx, _ identity.Principal, item, source string) ([]playbackv1.Marker, error) {
		r := q.Clone(ctx)
		r.SetPathValue("id", item)
		library, fence, err := d.detailFenceFor(r, p)
		if err != nil {
			return nil, nil
		}
		set, err := mediaanalysis.ViewerMarkersTx(ctx, tx, d.markerAccess(p, library, item, fence), source)
		if err != nil {
			return nil, nil
		}
		out := make([]playbackv1.Marker, 0, len(set.Markers))
		for _, m := range set.Markers {
			kind := m.Kind
			if kind == "outro" {
				kind = "credits"
			}
			out = append(out, playbackv1.Marker{ID: m.ID, Type: kind, StartMs: int64(m.StartSeconds * 1000), EndMs: int64(m.EndSeconds * 1000), Confidence: "detected", AutomaticSafe: m.AutomaticSafe})
		}
		return out, nil
	}
}

func setQueueETag(ctx context.Context, revision string) {
	apikit.SetHeader(ctx, "ETag", `"`+revision+`"`)
}

func registerPlaybackV1Queues(r *apikit.Registry, d Dependencies) {
	command := []string{"invalid_request", "unauthorized", "not_found", "revision_required", "revision_mismatch", "unsupported_selector", "queue_too_large", "queue_building"}
	reply := func(ctx context.Context, out playbackv1.QueueReply, err error) (playbackv1.QueueReply, error) {
		if err == nil {
			setQueueETag(ctx, out.Queue.Revision)
		}
		return out, err
	}
	caller := func(ctx context.Context, q *http.Request) playbackv1.Caller {
		return d.v1ServiceCaller(q, callerFrom(ctx))
	}
	v1Route(r, apikit.Metadata{Audience: apikit.Clients, Admission: lanePlayback, ID: "create_queue", Method: "POST", Path: "/v1/queues", Summary: "Play: replace this device's queue from selectors, optionally starting the first session",
		Access: apikit.Device, Requires: apikit.ScopePlayback, Lane: apikit.Media, Cost: apikit.SelectionAsync, BodyLimit: 64 << 10, Status: 201,
		Errors: append(command, "idempotency_key_required", "idempotency_key_reused", "stream_limit_reached", "conversion_capacity", "transcode_not_allowed", "unsupported_media", "source_unavailable", "storage_offline")},
		func(ctx context.Context, q *http.Request, in playbackv1.CreateQueueRequest) (playbackv1.QueueReply, error) {
			sources := []*playbackv1.Selector{in.Selector}
			for i := range in.Segments {
				sources = append(sources, &in.Segments[i].Source)
			}
			ctx, err := d.withQueueHomeRows(ctx, q, sources...)
			if err != nil {
				return playbackv1.QueueReply{}, err
			}
			c := caller(ctx, q)
			out, err := d.playbackV1().CreateQueue(ctx, c, q.Header.Get("Idempotency-Key"), in)
			if errors.Is(err, playback.ErrOwnerAccountCap) {
				if detail, e := d.playbackV1().AccountStreamLimit(ctx, c); e == nil {
					return out, &apierror.Error{Code: "stream_limit_reached", Reason: "owner_account_cap", Current: detail, Cause: err}
				}
			}
			return reply(ctx, out, err)
		})
	v1Route(r, apikit.Metadata{Audience: apikit.Clients, Admission: laneBrowsing, ID: "get_queue", Method: "GET", Path: "/v1/queues/{id}", Summary: "Read a queue's header",
		Access: apikit.Device, Requires: apikit.ScopeRead, Lane: apikit.Browsing, Cost: apikit.Constant, Status: 200, Errors: []string{"unauthorized", "not_found", "revision_mismatch"}},
		func(ctx context.Context, q *http.Request, _ noBody) (playbackv1.QueueView, error) {
			out, err := d.playbackV1().Queue(ctx, caller(ctx, q), q.PathValue("id"))
			if err == nil {
				setQueueETag(ctx, out.Revision)
			}
			return out, err
		})
	number := func(q *http.Request, key string, fallback int64) (int64, error) {
		raw := q.URL.Query().Get(key)
		if raw == "" {
			return fallback, nil
		}
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || n < 0 {
			return 0, &playbackv1.FieldError{Path: key}
		}
		return n, nil
	}

	v1Route(r, apikit.Metadata{Audience: apikit.Clients, Admission: laneBrowsing, ID: "get_queue_entry_for_item", Method: "GET", Path: "/v1/queues/{id}/entry", Summary: "Resolve a visible episode outside the loaded queue window for an in-player jump", Access: apikit.Device, Requires: apikit.ScopeRead, Lane: apikit.Browsing, Cost: apikit.SelectionAsync, Status: 200, Query: []string{"itemId"}, Errors: []string{"invalid_request", "unauthorized", "not_found", "queue_building", "revision_mismatch"}}, func(ctx context.Context, q *http.Request, _ noBody) (playbackv1.QueueItemView, error) {
		item := q.URL.Query().Get("itemId")
		if item == "" || len(item) > 128 {
			return playbackv1.QueueItemView{}, &playbackv1.FieldError{Path: "itemId"}
		}
		return d.playbackV1().EntryForItem(ctx, caller(ctx, q), q.PathValue("id"), item)
	})
	v1Route(r, apikit.Metadata{Audience: apikit.Clients, Admission: laneBrowsing, ID: "get_queue_window", Method: "GET", Path: "/v1/queues/{id}/window", Summary: "Read the entries around the current one (at most 200)",
		Access: apikit.Device, Requires: apikit.ScopeRead, Lane: apikit.Browsing, Cost: apikit.PageSized, Query: []string{"around", "before", "after"}, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_found", "revision_mismatch"}},
		func(ctx context.Context, q *http.Request, _ noBody) (playbackv1.EntryPage, error) {
			if around := q.URL.Query().Get("around"); around != "" && around != "current" {
				return playbackv1.EntryPage{}, &playbackv1.FieldError{Path: "around"}
			}
			before, err := number(q, "before", 20)
			if err != nil {
				return playbackv1.EntryPage{}, err
			}
			after, err := number(q, "after", 50)
			if err != nil {
				return playbackv1.EntryPage{}, err
			}
			return d.playbackV1().Window(ctx, caller(ctx, q), q.PathValue("id"), before, after)
		})
	v1Route(r, apikit.Metadata{Audience: apikit.Clients, Admission: laneBrowsing, ID: "list_queue_entries", Method: "GET", Path: "/v1/queues/{id}/entries", Summary: "Read a page of entries from a position (at most 200)",
		Access: apikit.Device, Requires: apikit.ScopeRead, Lane: apikit.Browsing, Cost: apikit.PageSized, Query: []string{"from", "limit", "cursor"}, QueryIntegers: emptyLimitQuery(50, 200), Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_found", "revision_mismatch"}},
		func(ctx context.Context, q *http.Request, _ noBody) (playbackv1.EntryPage, error) {
			from, err := number(q, "from", 0)
			if err != nil {
				return playbackv1.EntryPage{}, err
			}
			if cursor := q.URL.Query().Get("cursor"); cursor != "" {
				text, ok := strings.CutPrefix(cursor, "from:")
				if from, err = strconv.ParseInt(text, 10, 64); !ok || err != nil || from < 0 {
					return playbackv1.EntryPage{}, &playbackv1.FieldError{Path: "cursor"}
				}
			}
			limit, err := number(q, "limit", 50)
			if err != nil {
				return playbackv1.EntryPage{}, err
			}
			return d.playbackV1().Entries(ctx, caller(ctx, q), q.PathValue("id"), from, int(limit))
		})

	v1Route(r, apikit.Metadata{Audience: apikit.Clients, Admission: lanePlayback, ID: "batch_queue", Method: "POST", Path: "/v1/queues/{id}/batch", Summary: "Play a mixed selection next or later in its source order", Access: apikit.Device, Requires: apikit.ScopePlayback, Lane: apikit.Media, Cost: apikit.SelectionAsync, BodyLimit: 32 << 10, Status: 200, Errors: command}, func(ctx context.Context, q *http.Request, in playbackv1.BatchQueueRequest) (playbackv1.BatchQueueResult, error) {
		ctx, err := d.withQueueHomeRows(ctx, q, &in.Source)
		if err != nil {
			return playbackv1.BatchQueueResult{}, err
		}
		out, err := d.playbackV1().BatchQueue(ctx, caller(ctx, q), q.PathValue("id"), q.Header.Get("If-Match"), in)
		return reply(ctx, out, err)
	})
	v1Route(r, apikit.Metadata{Audience: apikit.Clients, Admission: lanePlayback, ID: "add_queue_segment", Method: "POST", Path: "/v1/queues/{id}/segments", Summary: "Play next, add to the queue, or insert after an entry",
		Access: apikit.Device, Requires: apikit.ScopePlayback, Lane: apikit.Media, Cost: apikit.SelectionAsync, BodyLimit: 32 << 10, Status: 200, Errors: command},
		func(ctx context.Context, q *http.Request, in playbackv1.AddSegmentRequest) (playbackv1.QueueReply, error) {
			ctx, err := d.withQueueHomeRows(ctx, q, &in.Source)
			if err != nil {
				return playbackv1.QueueReply{}, err
			}
			out, err := d.playbackV1().AddSegment(ctx, caller(ctx, q), q.PathValue("id"), q.Header.Get("If-Match"), in)
			return reply(ctx, out, err)
		})
	v1Route(r, apikit.Metadata{Audience: apikit.Clients, Admission: laneDefault, ID: "remove_queue_entry", Method: "DELETE", Path: "/v1/queues/{id}/entries/{entryId}", Summary: "Remove one entry",
		Access: apikit.Device, Requires: apikit.ScopePlayback, Lane: apikit.Default, Cost: apikit.Constant, Status: 200, Errors: command},
		func(ctx context.Context, q *http.Request, _ noBody) (playbackv1.QueueReply, error) {
			out, err := d.playbackV1().RemoveEntry(ctx, caller(ctx, q), q.PathValue("id"), q.Header.Get("If-Match"), q.PathValue("entryId"))
			return reply(ctx, out, err)
		})
	v1Route(r, apikit.Metadata{Audience: apikit.Clients, Admission: laneDefault, ID: "move_queue_entry", Method: "POST", Path: "/v1/queues/{id}/entries/{entryId}:move", Summary: "Move one entry before or after another",
		Access: apikit.Device, Requires: apikit.ScopePlayback, Lane: apikit.Default, Cost: apikit.Constant, BodyLimit: 1 << 10, Status: 200, Errors: command},
		func(ctx context.Context, q *http.Request, in playbackv1.MoveRequest) (playbackv1.QueueReply, error) {
			out, err := d.playbackV1().MoveEntry(ctx, caller(ctx, q), q.PathValue("id"), q.Header.Get("If-Match"), q.PathValue("entryId"), in)
			return reply(ctx, out, err)
		})
	v1Route(r, apikit.Metadata{Audience: apikit.Clients, Admission: laneDefault, ID: "update_queue", Method: "PATCH", Path: "/v1/queues/{id}", Summary: "Set repeat, or shuffle on or off",
		Access: apikit.Device, Requires: apikit.ScopePlayback, Lane: apikit.Default, Cost: apikit.Constant, BodyLimit: 1 << 10, Status: 200, Errors: command},
		func(ctx context.Context, q *http.Request, in playbackv1.QueueChange) (playbackv1.QueueReply, error) {
			out, err := d.playbackV1().UpdateQueue(ctx, caller(ctx, q), q.PathValue("id"), q.Header.Get("If-Match"), in)
			return reply(ctx, out, err)
		})
	v1Route(r, apikit.Metadata{Audience: apikit.Clients, Admission: lanePlayback, ID: "advance_queue", Method: "POST", Path: "/v1/queues/{id}:advance", Summary: "Move to the next, previous or a chosen entry; starts its session when one is playing",
		Access: apikit.Device, Requires: apikit.ScopePlayback, Lane: apikit.Media, Cost: apikit.Constant, BodyLimit: 1 << 10, Status: 200,
		Errors: append(command, "queue_ended", "stream_limit_reached", "conversion_capacity", "transcode_not_allowed", "unsupported_media")},
		func(ctx context.Context, q *http.Request, in playbackv1.AdvanceRequest) (playbackv1.QueueReply, error) {
			out, err := d.playbackV1().Advance(ctx, caller(ctx, q), q.PathValue("id"), q.Header.Get("If-Match"), in)
			return reply(ctx, out, err)
		})
	v1Route(r, apikit.Metadata{Audience: apikit.Clients, Admission: lanePlayback, ID: "prepare_queue_next", Method: "POST", Path: "/v1/queues/{id}:prepare-next", Summary: "Prepare the next entry's audio before the current one ends (spec §18.2; private until committed)",
		Access: apikit.Device, Requires: apikit.ScopePlayback, Lane: apikit.Media, Cost: apikit.Constant, BodyLimit: 1 << 10, Status: 200,
		Errors: append(command, "idempotency_key_required", "idempotency_key_reused", "queue_ended", "prepare_not_allowed", "conversion_capacity", "not_implemented")},
		func(ctx context.Context, q *http.Request, in playbackv1.PrepareNextRequest) (playbackv1.PreparedNext, error) {
			out, err := d.playbackV1().PrepareNext(ctx, caller(ctx, q), q.PathValue("id"), q.Header.Get("If-Match"), q.Header.Get("Idempotency-Key"), in)
			return out, v1Error(err)
		})
	v1Route(r, apikit.Metadata{Audience: apikit.Clients, Admission: lanePlayback, ID: "commit_queue_next", Method: "POST", Path: "/v1/queues/{id}:commit-next", Summary: "Commit the prepared next entry at the audio boundary: a new session inheriting state, rate, quality and track (spec §18.3)",
		Access: apikit.Device, Requires: apikit.ScopePlayback, Lane: apikit.Media, Cost: apikit.Constant, BodyLimit: 1 << 10, Status: 200,
		Errors: append(command, "prepared_expired", "prepared_canceled", "not_implemented")},
		func(ctx context.Context, q *http.Request, in playbackv1.CommitNextRequest) (playbackv1.QueueReply, error) {
			out, err := d.playbackV1().CommitNext(ctx, caller(ctx, q), q.PathValue("id"), in)
			return reply(ctx, out, err)
		})
	v1Route(r, apikit.Metadata{Audience: apikit.Clients, Admission: laneDefault, ID: "save_queue_as_playlist", Method: "POST", Path: "/v1/queues/{id}:save-as-playlist", Summary: "Save the queue, in play order, as a new playlist; a long queue is copied in the background and a replay of the key reports progress",
		Access: apikit.Device, Requires: apikit.ScopePlayback, Lane: apikit.Default, Cost: apikit.Constant, BodyLimit: 8 << 10, Status: 201,
		Errors: append(command, "idempotency_key_required", "idempotency_key_reused")},
		func(ctx context.Context, q *http.Request, in playbackv1.SaveAsPlaylistRequest) (playbackv1.SavedPlaylist, error) {
			out, err := d.playbackV1().SaveQueueAsPlaylist(ctx, caller(ctx, q), q.PathValue("id"), q.Header.Get("If-Match"), q.Header.Get("Idempotency-Key"), in)
			return out, v1Error(err)
		})
	v1Route(r, apikit.Metadata{Audience: apikit.Clients, Admission: laneDefault, ID: "delete_queue", Method: "DELETE", Path: "/v1/queues/{id}", Summary: "Clear the queue; idempotent",
		Access: apikit.Device, Requires: apikit.ScopePlayback, Lane: apikit.Default, Cost: apikit.Constant, Status: 204, Errors: []string{"unauthorized", "not_found"}},
		func(ctx context.Context, q *http.Request, _ noBody) (noBody, error) {
			return noBody{}, d.playbackV1().DeleteQueue(ctx, caller(ctx, q), q.PathValue("id"))
		})
}

// queueHomeRowsKey carries the Home rows a queue's query selectors name,
// resolved through Home when Play was pressed, to the selector compiler.
type queueHomeRowsKey struct{}
type queueHomeRows struct {
	members map[string][]int64
	titles  map[string]string
}

// withQueueHomeRows resolves every Home row the selectors name, as the viewer's
// Home shows it now (its first 500 titles, as the row's See all reads it), so
// playing a row plays exactly what the row held at that moment. A selector
// without a Home row costs nothing here.
func (d Dependencies) withQueueHomeRows(ctx context.Context, r *http.Request, sources ...*playbackv1.Selector) (context.Context, error) {
	ids := []string{}
	for _, source := range sources {
		if source == nil || source.Query == nil {
			continue
		}
		for _, id := range catalog.BrowseHomeRows(source.Query.Query) {
			if !slices.Contains(ids, id) {
				ids = append(ids, id)
			}
		}
	}
	if len(ids) == 0 || d.Catalog == nil {
		return ctx, nil
	}
	p, err := d.principal(r)
	if err != nil {
		return ctx, err
	}
	restrictions, _, err := d.viewerRestrictions(r, p)
	if err != nil {
		return ctx, err
	}
	request, err := d.homeRequestFor(r, p, restrictions)
	if err != nil {
		return ctx, err
	}
	held := queueHomeRows{members: map[string][]int64{}, titles: map[string]string{}}
	cat := d.Catalog.WithContext(ctx)
	for _, id := range ids {
		members, title, err := cat.HomeRowSnapshot(request, id)
		if err != nil {
			return ctx, err
		}
		held.members[id], held.titles[id] = members, title
	}
	return context.WithValue(ctx, queueHomeRowsKey{}, held), nil
}

// queueCatalogViewerTx is shared by finite selectors and generated radio
// batches. Both use current account/profile restrictions and library admission.
func (d Dependencies) queueCatalogViewerTx(ctx context.Context, tx *sql.Tx, p identity.Principal) (catalog.Viewer, error) {
	restrictions, err := contentaccess.RestrictionsTx(ctx, tx, p.Viewer)
	if err != nil {
		return catalog.Viewer{}, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT library_id FROM catalog_libraries WHERE retired=0 ORDER BY library_id`)
	if err != nil {
		return catalog.Viewer{}, err
	}
	var candidates []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			break
		}
		candidates = append(candidates, id)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return catalog.Viewer{}, err
	}
	libraries := []string{}
	for _, id := range candidates {
		if d.allowedLibraryTx(ctx, p, id, tx) == nil {
			libraries = append(libraries, id)
		}
	}
	scope, _ := json.Marshal(libraries)
	return catalog.Viewer{Profile: identity.PersonalKey(p.Viewer), Fence: catalog.RestrictionFence(restrictions) + ":" + string(scope), Libraries: libraries, Restrictions: restrictions}, nil
}
