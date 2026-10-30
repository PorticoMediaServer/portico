package playbackv1

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/playback"
	"github.com/PorticoMediaServer/portico/server/internal/subtitles"
)

// Spec defaults (§5.8, §6).
const (
	DefaultLease       = 120 * time.Second
	ReportPlaying      = 10 * time.Second
	ReportPaused       = 30 * time.Second
	DefaultReadyWait   = 1500 * time.Millisecond
	PreparingRetryMs   = 500
	maxStartPositionMs = 1 << 40
)

// TrackRef is a track id in a request where JSON null means "off"
// (`subtitleTrackId: null`). Off is stored as TrackOff.
type TrackRef string

const TrackOff TrackRef = "-"

func (t *TrackRef) UnmarshalJSON(b []byte) error {
	if string(b) == "null" {
		*t = TrackOff
		return nil
	}
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	*t = TrackRef(s)
	return nil
}

// QueueRef ties a session to its queue entry.
type QueueRef struct {
	QueueID string `json:"queueId"`
	EntryID string `json:"entryId"`
}

// AudioRequest chooses an audio track (spec §5.4).
type AudioRequest struct {
	TrackID  string `json:"trackId"`
	Channels int    `json:"channels,omitempty"`
}

// SubtitleRequest chooses subtitles (spec §5.5); a null trackId turns them off.
type SubtitleRequest struct {
	TrackID  TrackRef `json:"trackId"`
	Delivery string   `json:"delivery,omitempty"`
}

// StartRequest is POST /v1/playback/sessions (spec §5.1).
type StartRequest struct {
	DiscTitle      *catalog.DiscTitleRef `json:"discTitle,omitempty"`
	CastTransferID string                `json:"castTransferId,omitempty"`
	ItemID         string                `json:"itemId,omitempty"`
	ChannelID      string                `json:"channelId,omitempty"`
	// ChannelGeneration is the guide generation the viewer tuned from (a fence
	// against a lineup that changed since); absent: the current one.
	ChannelGeneration string           `json:"channelGeneration,omitempty"`
	Queue             *QueueRef        `json:"queue,omitempty"`
	TransferID        string           `json:"transferId,omitempty"`
	VersionID         string           `json:"versionId,omitempty"`
	PartIndex         int              `json:"partIndex,omitempty"`
	StartPositionMs   *int64           `json:"startPositionMs,omitempty"`
	StartFrom         string           `json:"startFrom,omitempty"`
	State             string           `json:"state,omitempty"`
	Quality           *Quality         `json:"quality,omitempty"`
	Audio             *AudioRequest    `json:"audio,omitempty"`
	Subtitles         *SubtitleRequest `json:"subtitles,omitempty"`
	Network           string           `json:"network,omitempty"`
	ReplacesSessionID string           `json:"replacesSessionId,omitempty"`
	// ThroughputKbps is the client's own estimate of what its connection to
	// this server carries. Automatic starts at Original regardless of this
	// estimate; the client asks for later quality changes. With any other
	// quality a start the connection plainly cannot carry is refused
	// (connection_too_slow).
	ThroughputKbps int `json:"throughputKbps,omitempty"`
}

// Change is PATCH /v1/playback/sessions/{id} (spec §5.7).
type Change struct {
	// Replan applies the playing device's current capability document without
	// replacing the occurrence or its saved version, quality and track choices.
	Replan           bool             `json:"replan,omitempty"`
	State            string           `json:"state,omitempty"`
	VersionID        string           `json:"versionId,omitempty"`
	PartIndex        *int             `json:"partIndex,omitempty"`
	Audio            *AudioRequest    `json:"audio,omitempty"`
	Subtitles        *SubtitleRequest `json:"subtitles,omitempty"`
	Quality          *Quality         `json:"quality,omitempty"`
	Seek             *SeekRequest     `json:"seek,omitempty"`
	SubtitleOffsetUS *string          `json:"subtitleOffsetUs,omitempty"`
	SubtitleOffsetMs *int64           `json:"subtitleOffsetMs,omitempty"`
	// PositionMs is where the player is now. A change that starts a new
	// presentation starts it there; without it the last reported position is
	// used, which can be a report interval old.
	PositionMs *int64 `json:"positionMs,omitempty"`
	// ThroughputKbps is the client's current connection estimate (see
	// StartRequest.ThroughputKbps). It is checked against a quality asked for
	// in the same change.
	ThroughputKbps *int `json:"throughputKbps,omitempty"`
	// Failure says the device's player could not play the current
	// presentation. The server remembers that this way of delivering the file
	// failed on this device and re-plans the same session, at the current
	// position, the next way up. unsupported_media: there is no other way.
	Failure *ReportedError `json:"failure,omitempty"`
}
type SeekRequest struct {
	PositionMs int64  `json:"positionMs"`
	ID         string `json:"id"`
	// Live (channels, §18.6): to the live edge; positionMs is ignored.
	Live bool `json:"live,omitempty"`
}

// StopRequest is the optional DELETE body (spec §5.8).
type StopRequest struct {
	PositionMs         *int64 `json:"positionMs,omitempty"`
	Generation         *int   `json:"generation,omitempty"`
	ExpectedGeneration *int   `json:"expectedGeneration,omitempty"`
}

// SessionView is the session resource (spec §5.1).
// SessionControl is what the session's controllers asked for, apart from what
// the player reports (RN-09): the revision moves only on an accepted pause,
// play or seek, so a player applies each newer one once and never mistakes
// its own report for a command.
type SessionControl struct {
	Revision string `json:"revision"`
	State    string `json:"state"`
}

type SessionView struct {
	Control      *SessionControl       `json:"control,omitempty"`
	DiscTitle    *catalog.DiscTitleRef `json:"discTitle,omitempty"`
	ID           string                `json:"id"`
	Revision     string                `json:"revision"`
	Kind         string                `json:"kind"`
	Role         string                `json:"role"`
	State        string                `json:"state"`
	ItemID       string                `json:"itemId,omitempty"`
	VersionID    string                `json:"versionId,omitempty"`
	ChannelID    string                `json:"channelId,omitempty"`
	Queue        *QueueRef             `json:"queue,omitempty"`
	Lease        Lease                 `json:"lease"`
	Presentation Presentation          `json:"presentation"`
	// RetryAfterMs is set only on a 202 while the presentation is prepared; the
	// client replays the same start (spec §17.2).
	RetryAfterMs int `json:"retryAfterMs,omitempty"`
	// End says why an ended session ended, so a device that missed the
	// session.updated event learns it from a re-read: "terminated" (an
	// administrator, with their message), "paused_timeout" (paused longer than
	// the paused-session limit, with its message), "stopped", "transferred",
	// "lease_expired", "replaced" and so on.
	End *SessionEnd `json:"end,omitempty"`

	// ObservedPlayback is only the current renderer snapshot accepted by its
	// device timeline. It is distinct from source, plan and capability facts.
	ObservedPlayback *playback.ObservedPlayback `json:"observedPlayback,omitempty"`
	// PlaybackInfo compares this media generation's committed source and target.
	// Renderer/screen observations remain separately attributed above.
	PlaybackInfo *playback.PlaybackInfo `json:"playbackInfo,omitempty"`
}

// SessionEnd is why a session ended and what the viewer is told.
type SessionEnd struct {
	Reason  string `json:"reason"`
	Message string `json:"message,omitempty"`
}
type Lease struct {
	ExpiresAt     string `json:"expiresAt"`
	ReportEveryMs int    `json:"reportEveryMs"`
}
type Presentation struct {
	ChoiceRevision int `json:"choiceRevision,omitempty"`
	// SeekID names the latest accepted seek of this session (VOD).
	SeekID     string `json:"seekId,omitempty"`
	Generation int    `json:"generation"`
	Mode       string `json:"mode"`
	URL        string `json:"url"`
	// Engine is the player engine the device must play this presentation with:
	// "native" (the platform's own player) or "mpv". The server chose it from
	// the device's capability document and the viewer's player setting.
	Engine          string         `json:"engine"`
	StartPositionMs int64          `json:"startPositionMs"`
	Subtitles       []SubtitleFile `json:"subtitles"`
	Decision        Decisions      `json:"decision"`
	BitrateKbps     int            `json:"bitrateKbps,omitempty"`
	// Quality is the quality in force: what was asked for, or the viewer's
	// saved default for this network, with automatic's current step in id.
	Quality *Quality `json:"quality,omitempty"`
	// VideoHeight is the effective converted display height from the committed
	// delivery plan and its source geometry; absent for copy or unknown geometry.
	VideoHeight int `json:"videoHeight,omitempty"`
	// AudioTrackID is the audio track being played ("a1").
	AudioTrackID string `json:"audioTrackId,omitempty"`
	// Subtitle is the subtitle track in force and how it reaches the screen;
	// absent when subtitles are off.
	Subtitle *PresentationSubtitle `json:"subtitle,omitempty"`
	// Container is what the URL delivers: the file's own container for direct
	// play ("mp4", "mkv", "flac"…), or "fmp4_hls" / "mpegts_hls" for a stream. A
	// player that must be told the segment format up front (Cast) reads it here.
	Container string `json:"container,omitempty"`
	// AudioRender is an audio presentation's plan (spec §18.1, version 2): how the
	// client fetches and decodes it, with the exact gapless trim and the gains.
	AudioRender *AudioRender `json:"audioRender,omitempty"`
	// Linear is a channel presentation's linear clock (spec §11, §18.6).
	Linear *LinearView `json:"linear,omitempty"`
}
type SubtitleFile struct {
	TimeDomain   string `json:"timeDomain"`
	OffsetMs     int64  `json:"offsetMs"`
	CompanionURL string `json:"companionUrl,omitempty"`
	FontsURL     string `json:"fontsUrl,omitempty"`
	// LinesURL (image subtitles): how many of the track's lines are forced.
	LinesURL string `json:"linesUrl,omitempty"`
	TrackID  string `json:"trackId"`
	Format   string `json:"format"`
	URL      string `json:"url"`
}

// PresentationSubtitle is the subtitle track a presentation shows.
type PresentationSubtitle struct {
	TrackID string `json:"trackId"`
	// Kind is the capability name of the track's format: text, ass, pgs,
	// vobsub or dvb.
	Kind string `json:"kind"`
	// Delivery is how it reaches the screen: "sidecar" (a WebVTT file in
	// subtitles[], drawn by the app), "device" (the engine draws the track
	// itself from the file it is playing), or "burn" (in the picture).
	Delivery string `json:"delivery"`
}

// Caller is who is asking: the admitted principal, its device, and admission's
// clamps (spec §5.3).
type Caller struct {
	UploadLimited *bool
	// PlaybackProtocol is the Portico-Playback version the client declared.
	PlaybackProtocol int
	// TransportClass is the actual normalized client link declaration, independent of Remote.
	TransportClass string
	Principal      identity.Principal
	DeviceID       string
	// AuthorizeItem checks visibility without taking a stream slot.
	AuthorizeItem func(context.Context, string) error
	// AdminMaxVideoBitrateBPS is the member's remote cap when this request is remote.
	AdminMaxVideoBitrateBPS int
	Remote                  bool
	// Admit applies media admission (library access, restrictions, member
	// limits, schedule; ARCH-MEDIA-21) to an item before a new presentation
	// takes a slot, returning the remote bitrate cap. Replays never re-admit.
	Admit func(ctx context.Context, item string) (int, error)
	// AdmitChannel applies the live channel policy (allow/deny list and
	// schedule) to a channel start. Replays never re-admit.
	AdmitChannel func(ctx context.Context, channel string) error
	// Ceiling is the video bitrate ceiling this caller's connection is under
	// right now (the member's remote cap and the upload budget; zero is none),
	// read without admitting anything: no stream is counted and no schedule or
	// item policy is applied. session is the v1 session being changed, whose
	// own share of the budget is not counted against it.
	Ceiling func(ctx context.Context, session string) (int, error)
	// SavedQuality reads the viewer's saved default quality for this
	// connection (quality.home or quality.remote): "original", "automatic" or
	// a ladder id. Empty means registry default; an error retains Original.
	SavedQuality func(ctx context.Context) (string, error)
	// SavedMusicQuality reads the source-defined music preference for this connection.
	SavedMusicQuality func(ctx context.Context) (string, error)
}

// defaultQuality is the quality of a start that names none: the viewer's
// saved default for where the request comes from.
func (c *Caller) defaultQuality(ctx context.Context, kind string) Quality {
	music := ""
	if musicKind(kind) && c.SavedMusicQuality != nil {
		var err error
		music, err = c.SavedMusicQuality(ctx)
		if err != nil {
			return Quality{Mode: QualityOriginal}
		}
		if music != "" {
			return musicQuality(music)
		}
	}
	saved := ""
	if c.SavedQuality != nil {
		var err error
		saved, err = c.SavedQuality(ctx)
		if err != nil {
			return Quality{Mode: QualityOriginal}
		}
	}
	return defaultQuality(saved, music, c.TransportClass, kind)
}

type replanKey struct{}

// Replan names what an admission continues: the v1 session it re-plans or
// replaces, and a prepared entry's media session that already exists. Neither
// is a new stream, so the stream limit doesn't count them.
type Replan struct {
	Session, Media string
	// Remote retains the playing connection for a controller on another device.
	Remote bool
}

// WithReplan marks an admission as continuing an existing stream.
func WithReplan(ctx context.Context, session, media string) context.Context {
	return context.WithValue(ctx, replanKey{}, Replan{Session: session, Media: media})
}

// ReplanOf is what an admission continues (zero for a new stream).
func ReplanOf(ctx context.Context) Replan {
	r, _ := ctx.Value(replanKey{}).(Replan)
	return r
}

func (c *Caller) admit(ctx context.Context, item string) error {
	if c.Admit == nil {
		return nil
	}
	limit, err := c.Admit(ctx, item)
	if err != nil {
		return err
	}
	if c.Remote {
		c.AdminMaxVideoBitrateBPS = limit
	}
	return nil
}

// replanContext is ctx carrying what this caller is allowed right now, for a
// change that makes a presentation produce new bytes without admitting it
// again (a subtitle burned in or turned off). A caller that cannot say carries
// nothing, and the re-plan is refused.
func (c *Caller) replanContext(ctx context.Context, session string) (context.Context, error) {
	if c.Ceiling == nil {
		return ctx, nil
	}
	ctx = WithReplan(ctx, session, "")
	r := ReplanOf(ctx)
	r.Remote = c.Remote
	ctx = context.WithValue(ctx, replanKey{}, r)
	limit, err := c.Ceiling(ctx, session)
	if err != nil {
		return ctx, err
	}
	if c.Remote {
		c.AdminMaxVideoBitrateBPS = limit
	}
	return playback.WithReplanCeilings(ctx, playback.ReplanCeilings{Remote: c.Remote, MaxVideoBitrateBPS: limit}), nil
}

func (c *Caller) admitChannel(ctx context.Context, channel string) error {
	if c.AdmitChannel == nil {
		return nil
	}
	return c.AdmitChannel(ctx, channel)
}

// storedRequest is what a session keeps of its request for re-planning.
type storedRequest struct {
	DiscTitle *catalog.DiscTitleRef `json:"discTitle,omitempty"`
	// SelectedDiscTitle is the accepted presentation's actual title, including
	// a default-main selection. It is separate from explicit request intent so
	// default starts retain their existing resume and track-carry semantics.
	SelectedDiscTitle *catalog.DiscTitleRef `json:"selectedDiscTitle,omitempty"`
	State             string                `json:"state,omitempty"`
	// ControlRevision counts accepted pause, play and seek commands after the
	// start (RN-09); observations never move it. SeekID names the latest
	// accepted seek, so an equal target is still a new seek (lane 03).
	ControlRevision  int64            `json:"controlRevision,omitempty"`
	SeekID           string           `json:"seekId,omitempty"`
	VersionID        string           `json:"versionId,omitempty"`
	Quality          Quality          `json:"quality"`
	Audio            *AudioRequest    `json:"audio,omitempty"`
	Subtitles        *SubtitleRequest `json:"subtitles,omitempty"`
	SubtitleOffsetUS string           `json:"subtitleOffsetUs,omitempty"`
	SubtitleOffsetMs int64            `json:"subtitleOffsetMs,omitempty"`
	Network          string           `json:"network,omitempty"`
	// Carry is the audio and subtitle choice this playback carries from the
	// queue entry before (nil: none; the Settings decide).
	Carry *TrackCarry `json:"carry,omitempty"`
}

func (r storedRequest) selectedDiscTitle() *catalog.DiscTitleRef {
	if r.SelectedDiscTitle != nil {
		return r.SelectedDiscTitle
	}
	return r.DiscTitle
}

// normalized is a stored request with a quality mode: one stored before
// qualities had a mode, or with none, is the original.
func (r storedRequest) normalized() storedRequest {
	if r.Quality.Mode == "" {
		r.Quality.Mode = QualityOriginal
	}
	r.Quality.Step = ""
	if r.Quality.Mode == QualityAutomatic && r.Quality.ID == QualityOriginal {
		r.Quality.ID = ""
	}
	return r
}

// row is one playback_v1_sessions row.
type row struct {
	id, device, account, profile, authority, startKey, startDigest string
	kind, role, state, item, version, channel, queueID, entryID    string
	transfer, group, request, media, presentation, audioTrack      string
	subtitleTrack, location, endReason, message                    string
	partIndex, revision, generation, mediaGeneration               int
	startPosition, position, lastSeq, bandwidth, lease, created    int64
	updated, ended                                                 int64
	// pausedSince is when the session entered paused, in epoch ms (0 when it
	// isn't paused). Every state write maintains it in SQL (pausedSinceUpdate).
	pausedSince int64
}

const rowColumns = `id,device_id,account_id,profile_id,authority,start_key,start_digest,kind,role,state,item_id,version_id,channel_id,queue_id,entry_id,transfer_id,group_id,request,media_session_id,presentation,audio_track_id,subtitle_track_id,location,end_reason,message,part_index,revision,generation,media_generation,start_position_ms,position_ms,last_seq,bandwidth_kbps,lease_expires_ms,created_ms,updated_ms,ended_ms,paused_since_ms`

// item_id is the catalogue's INTEGER entity id; the row carries the public id.
// rowSelect reads it back as one (empty once the item is gone), and rowValues
// resolves it on insert (0 for a channel session, which has none).
const rowSelect = `id,device_id,account_id,profile_id,authority,start_key,start_digest,kind,role,state,COALESCE((SELECT pid(item.public_id) FROM catalog_entities item WHERE item.id=playback_v1_sessions.item_id),''),version_id,channel_id,queue_id,entry_id,transfer_id,group_id,request,media_session_id,presentation,audio_track_id,subtitle_track_id,location,end_reason,message,part_index,revision,generation,media_generation,start_position_ms,position_ms,last_seq,bandwidth_kbps,lease_expires_ms,created_ms,updated_ms,ended_ms,paused_since_ms`

const rowValues = `VALUES(?,?,?,?,?,?,?,?,?,?,COALESCE((SELECT id FROM catalog_entities WHERE public_id=pid_blob(NULLIF(?,''))),0),?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`

// pausedSinceUpdate keeps paused_since_ms with the state in the same UPDATE:
// entering paused stamps now, staying paused keeps the old stamp, any other
// state clears it to 0. Its two arguments are the new state and now in epoch
// ms (the unqualified columns read the row's old values).
const pausedSinceUpdate = `paused_since_ms=CASE WHEN ?='paused' THEN CASE WHEN state='paused' AND paused_since_ms>0 THEN paused_since_ms ELSE ? END ELSE 0 END`

type scanner interface{ Scan(...any) error }

func scanRow(s scanner) (row, error) {
	var r row
	err := s.Scan(&r.id, &r.device, &r.account, &r.profile, &r.authority, &r.startKey, &r.startDigest, &r.kind, &r.role, &r.state, &r.item, &r.version, &r.channel, &r.queueID, &r.entryID, &r.transfer, &r.group, &r.request, &r.media, &r.presentation, &r.audioTrack, &r.subtitleTrack, &r.location, &r.endReason, &r.message, &r.partIndex, &r.revision, &r.generation, &r.mediaGeneration, &r.startPosition, &r.position, &r.lastSeq, &r.bandwidth, &r.lease, &r.created, &r.updated, &r.ended, &r.pausedSince)
	return r, err
}

type querier interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func loadRow(ctx context.Context, q querier, id string) (row, error) {
	r, err := scanRow(q.QueryRowContext(ctx, `SELECT `+rowSelect+` FROM playback_v1_sessions WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return r, ErrNotFound
	}
	return r, err
}

func (s *Service) lease() time.Duration {
	if s.LeaseDuration > 0 {
		return s.LeaseDuration
	}
	return DefaultLease
}

func (s *Service) reportEvery(state string) time.Duration {
	if s.ReportEvery > 0 {
		return s.ReportEvery
	}
	if state == "paused" {
		return ReportPaused
	}
	return ReportPlaying
}

// view is the session resource for a read that has no channel state yet.
func (s *Service) view(r row) SessionView { return s.sessionView(r, nil, 0) }

// sessionView renders a session, optionally from channel state read in the
// caller's own snapshot. channelPresentation is reused when it is not (reads,
// starts and changes), and channelPresentationFrom when it is (the observation
// ack's one-read view).
func (s *Service) sessionView(r row, channel *playback.ChannelState, nowMS int64) SessionView {
	v := SessionView{DiscTitle: r.stored().selectedDiscTitle(), ID: r.id, Revision: strconv.Itoa(r.revision), Kind: r.kind, Role: r.role, State: r.state, ItemID: r.item, VersionID: r.version, ChannelID: r.channel}
	if r.ended > 0 {
		v.State = "ended"
		v.End = &SessionEnd{Reason: r.endReason, Message: r.message}
		if v.End.Reason == "" {
			v.End.Reason = "stopped"
		}
	}
	if v.State == "preparing" {
		v.State = "playing"
		var req struct {
			State string `json:"state"`
		}
		_ = json.Unmarshal([]byte(r.request), &req)
		if req.State == "paused" {
			v.State = "paused"
		}
	}
	if r.queueID != "" {
		v.Queue = &QueueRef{QueueID: r.queueID, EntryID: r.entryID}
	}
	v.Lease = Lease{ExpiresAt: time.UnixMilli(r.lease).UTC().Format(time.RFC3339Nano), ReportEveryMs: int(s.reportEvery(v.State) / time.Millisecond)}
	_ = json.Unmarshal([]byte(r.presentation), &v.Presentation)
	if v.Presentation.Subtitles == nil {
		v.Presentation.Subtitles = []SubtitleFile{}
	}
	v.Presentation.Generation = r.generation
	v.Presentation.ChoiceRevision = r.choiceRevision()
	if isChannel(r) {
		if channel != nil {
			v.Presentation = s.channelPresentationFrom(*channel, nowMS)
		} else {
			v.Presentation = s.channelPresentation(r)
		}
	} else {
		v.Presentation.SeekID = r.stored().SeekID
	}
	if r.ended == 0 && !isChannel(r) {
		// A channel's desired state and seek live in its channel view.
		v.Control = r.control()
	}
	return v
}

// control is the session's desired state and its command revision.
func (r row) control() *SessionControl {
	stored := r.stored()
	state := stored.State
	if state != "paused" {
		state = "playing"
	}
	return &SessionControl{Revision: strconv.FormatInt(stored.ControlRevision+1, 10), State: state}
}

// owns reports whether the caller may see and control a session: its own
// profile, on its playing device or a controller device (spec §9.1).
func owns(c Caller, r row) bool {
	return r.account == c.Principal.AccountID && r.profile == c.Principal.ProfileID && r.authority == c.Principal.Authority
}

var keyPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{16,128}$`)

func newID(prefix string) string {
	var raw [15]byte
	_, _ = rand.Read(raw[:])
	return prefix + base64.RawURLEncoding.EncodeToString(raw[:])
}

func digest(v any) string {
	raw, _ := json.Marshal(v)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// validate checks a start request's shape.
func (r StartRequest) validate() error {
	if r.CastTransferID != "" && (r.Queue == nil || !keyPattern.MatchString(r.CastTransferID)) {
		return &FieldError{Path: "castTransferId"}
	}

	targets := 0
	for _, set := range []bool{r.ItemID != "", r.ChannelID != "", r.Queue != nil, r.TransferID != ""} {
		if set {
			targets++
		}
	}
	if targets != 1 {
		return &FieldError{Path: "itemId"}
	}
	if r.StartPositionMs != nil && (*r.StartPositionMs < 0 || *r.StartPositionMs > maxStartPositionMs) || r.StartPositionMs != nil && r.StartFrom != "" {
		return &FieldError{Path: "startPositionMs"}
	}
	switch r.StartFrom {
	case "", "resume", "beginning", "live", "startOver":
	default:
		return &FieldError{Path: "startFrom"}
	}
	switch r.State {
	case "", "playing", "paused":
	default:
		return &FieldError{Path: "state"}
	}
	switch r.Network {
	case "", "local", "remote", "cellular":
	default:
		return &FieldError{Path: "network"}
	}
	if r.PartIndex < 0 || r.PartIndex > 1000 {
		return &FieldError{Path: "partIndex"}
	}
	if r.ThroughputKbps < 0 || r.ThroughputKbps > 10_000_000 {
		return &FieldError{Path: "throughputKbps"}
	}
	if r.Quality != nil && r.Quality.Step != "" {
		// A step moves a session that exists; a start has nothing to step from.
		return &FieldError{Path: "quality.step"}
	}
	if r.Quality != nil {
		if err := r.Quality.Validate("quality"); err != nil {
			return err
		}
	}
	return validateTracks(r.Audio, r.Subtitles)
}

func validateTracks(a *AudioRequest, sub *SubtitleRequest) error {
	if a != nil && (a.TrackID == "" || len(a.TrackID) > 128 || a.Channels < 0 || a.Channels > 16) {
		return &FieldError{Path: "audio"}
	}
	if sub != nil {
		switch sub.Delivery {
		case "", "auto", "sidecar", "embeddedClient", "burn":
		default:
			return &FieldError{Path: "subtitles.delivery"}
		}
		if len(sub.TrackID) > 128 {
			return &FieldError{Path: "subtitles.trackId"}
		}
	}
	return nil
}

// presentationTarget is one media start: what to play and how.
type presentationTarget struct {
	disc              *playback.DiscPlayContinuation // opaque live original POST; never a wire field
	pendingActivation bool
	useStoredResume   bool
	item, version     string
	// part is the version's part to play (its part index; 0 is the first).
	part      int
	request   storedRequest
	startMs   int64
	replacing *playback.SessionReplacement
	requestID string
	// subtitleOp names the subtitle selection (idempotent per presentation).
	subtitleOp string
	// privateFor makes a private presentation (§18.2) of the given media
	// session's prepared next item, until privateUntilMs.
	subtitleOffsetUS string
	airPlay          bool
	privateFor       string
	privateUntilMs   int64
	// throughputKbps is the connection estimate the request carried (0: none).
	// Automatic changes only when the client asks for a step.
	throughputKbps int
}

// ThroughputError refuses a presentation the client's measured connection
// cannot carry. Suggested is the widest fixed quality that fits ("" none).
type ThroughputError struct{ Suggested string }

func (e *ThroughputError) Error() string { return "the connection is too slow for this quality" }

// subtitleKindOf is the capability name of a subtitle format.
func subtitleKindOf(format string) string {
	switch strings.ToLower(format) {
	case "ass", "ssa":
		return playback.SubtitleASS
	case "pgs", "hdmv_pgs_subtitle", "sup", "pgssub":
		return playback.SubtitlePGS
	case "vobsub", "dvd_subtitle", "dvdsub", "idx":
		return playback.SubtitleVobSub
	case "dvb_subtitle", "dvbsub", "dvb":
		return playback.SubtitleDVB
	}
	return playback.SubtitleText
}

// subtitleWeight is how a chosen subtitle track weighs in planning: the kind
// an engine must draw for it not to be burned in, playback.SubtitleBurnAlways
// when it is burned in whatever the engine, and "" when it costs nothing
// (off, no track, text delivered beside the stream, a styled track shown as
// its plain words, or a track the client insists on drawing itself).
func subtitleWeight(chosen *subtitles.Resource, req *SubtitleRequest, keepStyling bool) string {
	if chosen == nil || req == nil || chosen.Renderer != "burn_in" || req.Delivery == "embeddedClient" || styledAsText(chosen, req, keepStyling) {
		return ""
	}
	kind := subtitleKindOf(chosen.Format)
	if req.Delivery == "burn" || kind == playback.SubtitleText {
		return playback.SubtitleBurnAlways
	}
	return kind
}

// styledAsText is whether a styled (ASS/SSA) track an engine would otherwise
// burn reaches the viewer as its plain words in a text sidecar: the client
// asked for delivery "sidecar", or the viewer turned "Styled subtitles" off and
// the client named no delivery. An engine that draws the style itself is still
// handed the original (applySubtitles decides that first).
func styledAsText(chosen *subtitles.Resource, req *SubtitleRequest, keepStyling bool) bool {
	if chosen == nil || req == nil || chosen.Renderer != "burn_in" || subtitleKindOf(chosen.Format) != playback.SubtitleASS {
		return false
	}
	return req.Delivery == "sidecar" || (req.Delivery == "" || req.Delivery == "auto") && !keepStyling
}

// keepsStyling reads the viewer's "Styled subtitles" setting only when it can
// matter: a chosen styled track that would otherwise be burned in.
func (s *Service) keepsStyling(ctx context.Context, p identity.Principal, chosen *subtitles.Resource) bool {
	if chosen == nil || chosen.Renderer != "burn_in" || subtitleKindOf(chosen.Format) != playback.SubtitleASS || s.Playback == nil {
		return true
	}
	return s.Playback.KeepsSubtitleStyling(ctx, p)
}

// subtitleResources lists one file's selectable subtitle tracks.
func (s *Service) subtitleResources(ctx context.Context, p identity.Principal, item, asset string, discTitle ...*catalog.DiscTitleRef) []subtitles.Resource {
	if s.Subtitles == nil {
		return nil
	}
	tx, done, err := dbwork.BeginRead(ctx, s.DB)
	if err != nil {
		return nil
	}
	defer done()
	var listed subtitles.Catalog
	if len(discTitle) > 0 && discTitle[0] != nil {
		listed, err = s.discSubtitleCatalog(ctx, tx, p, item, asset, *discTitle[0])
	} else {
		listed, err = s.Subtitles.ListTx(ctx, tx, p, item, asset)
	}
	if err != nil {
		return nil
	}
	out := []subtitles.Resource{}
	for _, r := range listed.Resources {
		if r.SourceID == asset && r.Enabled {
			out = append(out, r)
		}
	}
	return out
}

func resourceByID(resources []subtitles.Resource, id string) *subtitles.Resource {
	for i := range resources {
		if resources[i].ID == id {
			return &resources[i]
		}
	}
	return nil
}

func firstOffset(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// present creates a presentation (a playback_sessions row with its own grant)
// and returns it with its view. It does not wait for production.
//
// It is where everything a viewer can choose meets the device: the version
// (the best one for this device when none is named), the quality (the saved
// default when none is named), the audio and subtitle tracks (what the
// playback carries from the queue entry before when none is named, else the
// viewer's Settings), and the engine the plan names.
func (s *Service) present(ctx context.Context, c Caller, t presentationTarget) (playback.Session, Presentation, error) {
	if s.Playback == nil {
		return playback.Session{}, Presentation{}, playback.ErrIncompatible
	}
	kind, err := readItemKind(ctx, s.DB, t.item)
	if err != nil {
		return playback.Session{}, Presentation{}, err
	}
	video := kind != "song" && kind != "track" && kind != "audiobook_file"
	request := t.request.normalized()
	discTitle := request.selectedDiscTitle()
	if discTitle != nil {
		if t.version == "" || t.part != 0 || !video {
			return playback.Session{}, Presentation{}, &FieldError{Path: "discTitle"}
		}
	}
	if !video && request.Quality.Mode == QualityFixed || video && request.Quality.Mode == QualityCD {
		return playback.Session{}, Presentation{}, &FieldError{Path: "quality"}
	}
	base := playback.V1Choice{AdminMaxVideoBitrateBPS: c.AdminMaxVideoBitrateBPS, Remote: c.Remote, AirPlay: t.airPlay, PrivateFor: t.privateFor, PrivateUntilMs: t.privateUntilMs, PendingActivation: t.pendingActivation, UseStoredResume: t.useStoredResume && request.DiscTitle == nil, DiscTitle: discTitle}
	request.Quality.apply(&base)
	version := t.version
	if t.disc == nil && version == "" && video && t.part == 0 {
		version = s.bestVersion(ctx, c.Principal, t.item, base, t.throughputKbps)
	}
	asset, err := s.partAsset(ctx, t.item, version, t.part)
	if err != nil {
		return playback.Session{}, Presentation{}, err
	}
	if asset == "" {
		// No version named and only one to choose from: the file a start plays.
		asset, _ = s.Playback.SelectedAsset(ctx, t.item, &base)
	}
	choice := base
	choice.AssetID = asset
	if err = s.audioChoice(ctx, c, t.item, kind, &choice); err != nil {
		return playback.Session{}, Presentation{}, err
	}
	var carry *TrackCarry
	if video {
		carry = request.Carry
	}
	if a := request.Audio; a != nil {
		index, ok := ParseStreamID("a", a.TrackID)
		if !ok {
			return playback.Session{}, Presentation{}, &FieldError{Path: "audio.trackId"}
		}
		choice.AudioStream = &index
	} else if carry != nil {
		choice.CarriedAudioLanguage = carry.AudioLanguage
	}
	// A private resource is not a shared catalogue track. Initial automatic
	// text selection is initialized from sealed streams by the legacy writer;
	// explicit/carry selection needs the separate accepted private transition.
	if t.disc != nil && (!video || t.part != 0 || request.Subtitles != nil && request.Subtitles.TrackID != TrackOff && request.Subtitles.TrackID != "" || request.SubtitleOffsetMs != 0 || carry != nil && carry.SubtitleMode == "on") {
		return playback.Session{}, Presentation{}, playback.ErrIncompatible
	}
	// The subtitle track, known before planning: a styled or image track an
	// engine cannot draw itself costs a converted picture, which decides the
	// engine.
	subtitle := request.Subtitles
	var resources []subtitles.Resource
	if t.disc == nil && video && asset != "" && (subtitle != nil && subtitle.TrackID != TrackOff && subtitle.TrackID != "" || subtitle == nil && carry != nil && carry.SubtitleMode == "on") {
		resources = s.subtitleResources(ctx, c.Principal, t.item, asset, discTitle)
	}
	if subtitle == nil {
		subtitle = carriedSubtitle(carry, subtitles.PlaybackPlan{Resources: resources})
	}
	var chosen *subtitles.Resource
	if subtitle != nil && subtitle.TrackID != TrackOff && subtitle.TrackID != "" {
		chosen = resourceByID(resources, string(subtitle.TrackID))
		if discTitle != nil && chosen == nil {
			return playback.Session{}, Presentation{}, &FieldError{Path: "subtitles.trackId"}
		}
	}
	keepStyling := s.keepsStyling(ctx, c.Principal, chosen)
	choice.SubtitleKind = subtitleWeight(chosen, subtitle, keepStyling)
	// A styled or image track has an original the server hands over as it is
	// (the .ass with its fonts, the .sup, the .idx and .sub, the DVB packets),
	// so an engine that draws the kind needs no burn-in beside a stream either.
	choice.SubtitleBesideStream = chosen != nil && subtitles.RenderingFormat(chosen.Format) != "text"
	if t.airPlay && chosen != nil && subtitle != nil && subtitle.Delivery == "burn" {
		choice.SubtitleKind = playback.SubtitleBurnAlways
		choice.SubtitleBesideStream = false
	}

	// Quality against the connection the client measured.
	if t.throughputKbps > 0 && video && asset != "" && request.Quality.Mode != QualityAutomatic {
		if plan, planErr := s.Playback.PlanV1(ctx, c.Principal, t.item, choice); planErr == nil {
			if need := planKbps(plan); need > 0 && t.throughputKbps*5 < need*4 {
				return playback.Session{}, Presentation{}, &ThroughputError{Suggested: suggestedQuality(t.throughputKbps, sourceFactsOf(&plan))}
			}
		}
	}

	var media playback.Session
	if t.disc != nil {
		choice.SubtitleOff = request.Subtitles != nil && (request.Subtitles.TrackID == TrackOff || request.Subtitles.TrackID == "")
		media, err = t.disc.CreateV1(ctx, c.Principal, t.item, t.requestID, choice, t.replacing)
	} else {
		media, err = s.Playback.CreateV1(ctx, c.Principal, t.item, t.requestID, choice, t.replacing)
	}
	if errors.Is(err, sql.ErrNoRows) && t.version != "" {
		return media, Presentation{}, &FieldError{Path: "versionId"}
	}
	if err != nil {
		return media, Presentation{}, err
	}
	quality := request.Quality
	view := Presentation{Mode: "stream", URL: media.StreamURL, Engine: playback.EngineNative, StartPositionMs: t.startMs, Subtitles: []SubtitleFile{}, Quality: &quality}
	if media.Mode == "direct" {
		view.Mode = "direct"
	}
	// §18.1: an audio presentation's render plan, pinned to this presentation.
	view.AudioRender = audioRenderOf(media.AudioPlan)
	if choice.AudioServerRender && view.AudioRender.playable() {
		view.URL = view.AudioRender.URL
		view.Mode = "direct"
	}
	plan, err := s.Playback.SessionPlan(ctx, media.ID)
	if err != nil {
		return media, Presentation{}, err
	}
	if plan != nil {
		s.describe(&view, *plan, choice)
	}
	if a := request.Audio; a != nil && plan != nil && plan.AudioStream >= 0 && streamID("a", plan.AudioStream) != a.TrackID {
		// The chosen track doesn't exist in this version.
		_ = s.Playback.StopV1(context.WithoutCancel(ctx), media.ID)
		return media, Presentation{}, &FieldError{Path: "audio.trackId"}
	}
	if t.disc != nil {
		// No readiness/source/cache/helper work before final acceptance. The
		// ordinary plan is real metadata; exact private subtitle URLs are added
		// only after activation in the final V1 writer below.
		return media, view, nil
	}
	// Whether the engine the plan named draws the chosen track itself.
	// A styled track shown as plain words weighs nothing in planning, but an
	// engine that draws the style is still handed the original.
	draws := false
	if kind := subtitleWeight(chosen, subtitle, true); chosen != nil && plan != nil && kind != "" {
		profile := s.deviceDocument(ctx, c.DeviceID).Profile(plan.Engine)
		draws = !playback.SubtitleNeedsBurnIn(profile, kind, plan.Mode, choice.SubtitleBesideStream)
	}
	// A burn-in chosen with the start re-plans what was just planned, under the
	// same admission.
	admitted := playback.WithReplanCeilings(ctx, playback.ReplanCeilings{Remote: choice.Remote, MaxVideoBitrateBPS: choice.AdminMaxVideoBitrateBPS})
	result, err := s.applySubtitles(admitted, c.Principal, t.item, media.ID, subtitle, request.SubtitleOffsetMs, t.startMs, t.subtitleOp, draws, firstOffset(t.subtitleOffsetUS, request.SubtitleOffsetUS))
	if err != nil && request.Subtitles == nil {
		// A carried choice that cannot be applied to this file is not the
		// viewer's request: the title plays without it.
		result, err = s.applySubtitles(admitted, c.Principal, t.item, media.ID, nil, request.SubtitleOffsetMs, t.startMs, t.subtitleOp+"-n", false)
	}
	if err != nil {
		_ = s.Playback.StopV1(context.WithoutCancel(ctx), media.ID)
		return media, Presentation{}, err
	}
	view.Subtitles, view.Subtitle = result.files, result.shown
	if result.replaced != nil {
		// Burn-in: the same playback session, re-planned with the subtitle in the
		// video, under a new grant and generation.
		media.StreamURL, media.Generation = result.replaced.StreamURL, result.replaced.Generation
		s.adoptReplaced(ctx, &view, media.ID, result.replaced, choice)
	}
	return media, view, nil
}

// describe fills a presentation view from its plan: the engine, the delivery
// decision, the bitrate, the container and the audio track.
func (s *Service) describe(view *Presentation, plan playback.DeliveryPlan, choice playback.V1Choice) {
	view.Engine = plan.Engine
	if view.Engine == "" {
		view.Engine = playback.EngineNative
	}
	view.Decision = decisions(plan, choice, plan.SourceVideoCodec != "")
	view.BitrateKbps = planKbps(plan)
	view.Container = plan.OutputContainer
	if choice.AudioServerRender && view.AudioRender.playable() {
		// This URL names the separately pinned audio bytes. The general delivery
		// plan may still describe the original file or another conversion.
		view.Container = view.AudioRender.Container
		view.BitrateKbps = 0 // source bitrate is not a measurement of converted output
	}
	view.VideoHeight = playback.ActivityVideoHeight(plan)
	if plan.AudioStream >= 0 {
		view.AudioTrackID = streamID("a", plan.AudioStream)
	}
}

// adoptReplaced points a presentation view at a stream a subtitle selection
// replaced (burn-in on or off): its URL, mode and decision.
func (s *Service) adoptReplaced(ctx context.Context, view *Presentation, media string, replaced *subtitles.Presentation, choice playback.V1Choice) {
	view.URL = replaced.StreamURL
	view.Mode = "stream"
	if replaced.Mode == "direct" {
		view.Mode = "direct"
	}
	if plan, err := s.Playback.SessionPlan(ctx, media); err == nil && plan != nil {
		s.describe(view, *plan, choice)
	}
}

// bestVersion picks the version a start that names none plays: the one this
// device plays with the least done to it (as it is, then repackaged, then with
// its sound converted, then fully converted) under the quality asked for, and
// among equals the catalogue's best version (compactcatalog.BestVersionOrder:
// the taller picture, then the larger file), which is the order the versions
// are listed in. A title with one version, or one whose versions cannot be
// compared, returns "", and the catalogue's best version is played.
func (s *Service) bestVersion(ctx context.Context, p identity.Principal, item string, choice playback.V1Choice, throughputKbps int) string {
	tx, done, err := dbwork.BeginRead(ctx, s.DB)
	if err != nil {
		return ""
	}
	assets, err := readAssets(ctx, tx, item)
	done()
	if err != nil {
		return ""
	}
	groups := versionsOf(assets)
	if len(groups) < 2 {
		return ""
	}
	if len(groups) > maxComparedVersions {
		groups = groups[:maxComparedVersions]
	}
	best, bestCost := "", 1<<30
	for _, group := range groups {
		head := group[0]
		candidate := choice
		candidate.AssetID = head.id
		candidate.PrivateFor = ""
		plan, err := s.Playback.PlanV1(ctx, p, item, candidate)
		if err != nil {
			continue
		}
		cost := 3
		switch plan.Strategy {
		case playback.DeliveryOriginal:
			cost = 0
		case playback.DeliveryCopyRemux:
			cost = 1
		case playback.DeliveryAudioConversion:
			cost = 2
		}
		if throughputKbps > 0 {
			if need := planKbps(plan); need > 0 && throughputKbps*5 < need*4 {
				cost += 100
			}
		}
		if cost < bestCost {
			best, bestCost = head.id, cost
		}
	}
	return best
}

// maxComparedVersions bounds how many versions of one title are planned to
// choose between them.
const maxComparedVersions = 8

func (s *Service) readyWait() time.Duration {
	if s.ReadyWait > 0 {
		return s.ReadyWait
	}
	return DefaultReadyWait
}

// Start is POST /v1/playback/sessions. It returns the session and whether it is
// still being prepared (202). A replay with the same key returns the same
// session; the same key with another body is ErrIdempotencyMismatch (422
// idempotency_key_reused, the one API-wide code; spec §17.1).
func (s *Service) Start(ctx context.Context, c Caller, key string, req StartRequest) (SessionView, bool, error) {
	if err := s.requireObservationProtocol(c); err != nil {
		return SessionView{}, false, err
	}
	if req.DiscTitle != nil && (req.ItemID == "" && req.Queue == nil || req.VersionID == "" || req.PartIndex != 0 || req.ChannelID != "" || req.TransferID != "" || req.CastTransferID != "" || len(req.DiscTitle.ID) != 64 || len(req.DiscTitle.Revision) != 64) {
		return SessionView{}, false, &FieldError{Path: "discTitle"}
	}

	if key == "" {
		return SessionView{}, false, ErrKeyRequired
	}
	if !keyPattern.MatchString(key) {
		return SessionView{}, false, &FieldError{Path: "Idempotency-Key"}
	}
	if err := req.validate(); err != nil {
		return SessionView{}, false, err
	}
	want := digest(req)
	existing, err := scanRow(s.DB.QueryRowContext(ctx, `SELECT `+rowSelect+` FROM playback_v1_sessions WHERE device_id=? AND start_key=?`, c.DeviceID, key))
	if err == nil {
		if !owns(c, existing) {
			return SessionView{}, false, ErrNotFound
		}
		if existing.startDigest != want {
			return SessionView{}, false, ErrIdempotencyMismatch
		}
		if req.CastTransferID != "" {
			if err = s.replayCastTransfer(ctx, c, req.CastTransferID, existing, key, want); err != nil {
				return SessionView{}, false, err
			}
		}
		return s.replayStart(ctx, c, existing)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return SessionView{}, false, err
	}
	switch {
	case req.ItemID != "":
		return s.startItem(ctx, c, key, want, req)
	default:
		return s.startOther(ctx, c, key, want, req)
	}
}

// startOther is filled in by later slices (channels, queues, transfers).
var startTargets = map[string]func(*Service, context.Context, Caller, string, string, StartRequest) (SessionView, bool, error){}

func (s *Service) startOther(ctx context.Context, c Caller, key, digest string, req StartRequest) (SessionView, bool, error) {
	kind := "channelId"
	switch {
	case req.Queue != nil:
		kind = "queue"
	case req.TransferID != "":
		kind = "transferId"
	}
	if start, ok := startTargets[kind]; ok {
		return start(s, ctx, c, key, digest, req)
	}
	return SessionView{}, false, &FieldError{Path: kind}
}

// replayStart answers a repeated start: 202 until the presentation is ready.
func (s *Service) replayStart(ctx context.Context, c Caller, r row) (SessionView, bool, error) {
	if err := c.authorizeSession(ctx, r); err != nil {
		return SessionView{}, false, err
	}
	r = s.expireIfLapsed(ctx, r)
	if r.state != "preparing" || r.ended > 0 {
		return s.view(r), false, nil
	}
	ready, err := s.mediaReady(ctx, r)
	if err != nil {
		return SessionView{}, false, err
	}
	if !ready {
		v := s.view(r)
		v.RetryAfterMs = PreparingRetryMs
		return v, true, nil
	}
	return s.markReady(ctx, r)
}

func (s *Service) mediaReady(ctx context.Context, r row) (bool, error) {
	p := r.presentationView()
	mode := "hls"
	if p.Mode == "direct" {
		mode = "direct"
	}
	return s.ready(ctx, playback.Session{ID: r.media, Mode: mode, StreamURL: p.URL})
}

func (s *Service) ready(ctx context.Context, media playback.Session) (bool, error) {
	if s.ReadyCheck != nil {
		return s.ReadyCheck(ctx, media)
	}
	return s.Playback.ReadyWithin(ctx, media, s.readyWait())
}

// markReady moves a prepared session to its requested state.
func (s *Service) markReady(ctx context.Context, r row) (SessionView, bool, error) {
	var req struct {
		State string `json:"state"`
	}
	_ = json.Unmarshal([]byte(r.request), &req)
	state := "playing"
	if req.State == "paused" {
		state = "paused"
	}
	now := s.now()
	err := dbwork.WithWriteTxContext(ctx, s.DB, dbwork.ClassPlaybackStart, func(ctx context.Context, tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE playback_v1_sessions SET state=?,revision=revision+1,updated_ms=?,`+pausedSinceUpdate+` WHERE id=? AND state='preparing' AND revision=? AND ended_ms=0`, state, now.UnixMilli(), state, now.UnixMilli(), r.id, r.revision)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 1 {
			r.state, r.revision = state, r.revision+1
			return s.sessionEventsTx(ctx, tx, r, now, false)
		}
		return nil
	})
	if err != nil {
		return SessionView{}, false, err
	}
	s.hub.wake()
	current, err := loadRow(ctx, s.DB, r.id)
	return s.view(current), current.state == "preparing", err
}

func (s *Service) startItem(ctx context.Context, c Caller, key, startDigest string, req StartRequest) (SessionView, bool, error) {
	kind, err := readItemKind(ctx, s.DB, req.ItemID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return SessionView{}, false, ErrNotFound
		}
		return SessionView{}, false, err
	}
	sessionKind := "vod"
	if kind == "song" || kind == "audiobook_file" || kind == "track" {
		sessionKind = "audio"
	}
	stored := storedRequest{DiscTitle: req.DiscTitle, VersionID: req.VersionID, Quality: c.defaultQuality(ctx, kind), Audio: req.Audio, Subtitles: req.Subtitles, Network: req.Network}
	if req.Quality != nil {
		stored.Quality = *req.Quality
	}
	return s.startPresentation(ctx, c, key, startDigest, req, row{kind: sessionKind, role: "local", item: req.ItemID, version: req.VersionID, partIndex: req.PartIndex}, stored)
}

// startPresentation creates the first presentation of a new session and
// records it. base carries the kind, role, target and links.
func (s *Service) startPresentation(ctx context.Context, c Caller, key, startDigest string, req StartRequest, base row, stored storedRequest) (SessionView, bool, error) {
	if err := s.requireObservationProtocol(c); err != nil {
		return SessionView{}, false, err
	}
	release := s.admission.hold(c.Principal.AccountID)
	defer release()
	ctx, preparationErr := s.prepareDiscStart(ctx, c, key, startDigest, req, base, stored)
	if preparationErr != nil {
		return SessionView{}, false, preparationErr
	}
	var continuation *playback.DiscPlayContinuation
	if s.Playback != nil {
		var err error
		continuation, err = s.Playback.DiscPlayContinuation(ctx)
		if err != nil {
			return SessionView{}, false, err
		}
	}
	id := ""
	originalRequest := ""
	if continuation != nil {
		defer continuation.Close(context.WithoutCancel(ctx))
		// This is the same local video start, not a transfer/group surrogate or
		// permission inferred from nil admission. The original factory retains
		// separate worker and current-POST proofs throughout the writers.
		if base.kind != "vod" || base.partIndex != 0 || c.Admit == nil || c.Principal.Grant == identity.GrantCastTarget || req.TransferID != "" || req.CastTransferID != "" {
			return SessionView{}, false, playback.ErrIncompatible
		}
		var asset string
		var err error
		id, asset, originalRequest, err = continuation.OriginalStart(c.Principal, c.DeviceID, key, startDigest, base.item, base.partIndex)
		if err != nil {
			return SessionView{}, false, err
		}
		if base.version != "" && base.version != asset {
			return SessionView{}, false, playback.ErrStaleOffer
		}
		base.version = asset
	} else {
		id = newID("ps_")
	}
	var replacing *playback.SessionReplacement
	var old row
	if req.ReplacesSessionID != "" {
		var err error
		old, err = loadRow(ctx, s.DB, req.ReplacesSessionID)
		if err != nil || old.device != c.DeviceID || !owns(c, old) {
			return SessionView{}, false, &FieldError{Path: "replacesSessionId"}
		}
		if old.ended == 0 && old.media != "" {
			if err := dbwork.QueryRow(ctx, s.DB, `SELECT generation FROM playback_sessions WHERE id=? AND account_id=? AND profile_id=? AND state NOT IN('stopped','ended','failed')`, old.media, old.account, old.profile).Scan(&old.mediaGeneration); err != nil {
				return SessionView{}, false, err
			}
			replacing = &playback.SessionReplacement{ID: old.media, Generation: old.mediaGeneration, Deferred: true}
		}
	}
	admitCtx := ctx
	if req.ReplacesSessionID != "" && old.ended == 0 {
		admitCtx = WithReplan(ctx, old.id, "")
	}
	if err := c.admit(admitCtx, base.item); err != nil {
		return SessionView{}, false, err
	}
	startMs := int64(0)
	if req.StartPositionMs != nil {
		startMs = *req.StartPositionMs
	}
	target := presentationTarget{pendingActivation: true, useStoredResume: req.StartPositionMs == nil && (req.StartFrom == "" || req.StartFrom == "resume"), item: base.item, version: base.version, part: base.partIndex, request: stored, startMs: startMs, replacing: replacing, requestID: "v1." + id + ".1", subtitleOp: "v1-" + id + "-1",
		throughputKbps: req.ThroughputKbps}
	if continuation != nil {
		target.disc, target.requestID = continuation, originalRequest
	}
	media, view, err := s.present(ctx, c, target)

	if err != nil {
		if media.ID != "" {
			_ = s.Playback.StopV1(context.WithoutCancel(ctx), media.ID)
		}
		return SessionView{}, false, err
	}
	if req.StartPositionMs == nil && (req.StartFrom == "" || req.StartFrom == "resume") {
		view.StartPositionMs = int64(media.ResumeSeconds * 1000)
	}
	ready := false
	if continuation == nil {
		ready, err = s.ready(ctx, media)
	}
	if err != nil {
		_ = s.Playback.StopV1(context.WithoutCancel(ctx), media.ID)
		return SessionView{}, false, err
	}
	state := "playing"
	if req.State == "paused" {
		state = "paused"
	}
	if !ready {
		state = "preparing"
	}
	if view.Quality != nil {
		// The quality in force (automatic's current step included) is what a
		// later step or re-plan continues from.
		stored.Quality = *view.Quality
	}
	stored.State = req.State
	if continuation == nil && base.kind == "vod" && stored.DiscTitle == nil {
		// A track picked with the start is carried to the queue's next entry.
		stored.Carry = s.carryChoice(ctx, c.Principal, base.item, media.ID, view, req.Audio != nil, req.Subtitles != nil, stored.Carry)
	}
	requestJSON, _ := json.Marshal(stored)
	presentationJSON, _ := json.Marshal(view)
	now := s.now()
	r := base
	r.id, r.device, r.account, r.profile, r.authority = id, c.DeviceID, c.Principal.AccountID, c.Principal.ProfileID, c.Principal.Authority
	r.startKey, r.startDigest, r.state, r.request, r.revision, r.generation = key, startDigest, state, string(requestJSON), 1, 1
	r.media, r.mediaGeneration, r.presentation, r.startPosition, r.position = media.ID, media.Generation, string(presentationJSON), view.StartPositionMs, view.StartPositionMs
	r.lease, r.created, r.updated = now.Add(s.lease()).UnixMilli(), now.UnixMilli(), now.UnixMilli()
	r.bandwidth = int64(req.ThroughputKbps)
	if state == "paused" {
		r.pausedSince = now.UnixMilli()
	}
	r.location = "local"
	if c.Remote {
		r.location = "remote"
	}
	if r.version == "" {
		if plan, _ := s.Playback.SessionPlan(ctx, media.ID); plan != nil {
			r.version = plan.SourceID
		}
	}
	err = dbwork.WithWriteTxContext(ctx, s.DB, dbwork.ClassPlaybackStart, func(ctx context.Context, tx *sql.Tx) error {
		selected, err := s.Playback.PresentationDiscTitleTx(ctx, tx, media.ID, media.Generation)
		if err != nil {
			return err
		}
		if asked := stored.selectedDiscTitle(); asked != nil && (selected == nil || *asked != *selected) {
			return playback.ErrStaleOffer
		}
		stored.SelectedDiscTitle = selected
		acceptedRequest, err := json.Marshal(stored)
		if err != nil {
			return err
		}
		r.request = string(acceptedRequest)
		if bound, ok := ctx.Value(queueInitialStartKey{}).(queueInitialStart); ok && c.Principal.Grant == identity.GrantCastTarget {
			if err := s.recheckInitialQueueStartTx(ctx, tx, c, bound, base); err != nil {
				return err
			}
		}
		if bound, ok := ctx.Value(queueStartCustodyKey{}).(queueStartCustody); ok {
			if err := s.recheckQueueStartCustodyTx(ctx, tx, c, bound); err != nil {
				return err
			}
		}
		if bound, ok := ctx.Value(castTransferStartKey{}).(castTransferStart); ok {
			if err := s.recheckCastTransferStartTx(ctx, tx, c, bound, base); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO playback_v1_sessions(`+rowColumns+`) `+rowValues,
			r.id, r.device, r.account, r.profile, r.authority, r.startKey, r.startDigest, r.kind, r.role, r.state, r.item, r.version, r.channel, r.queueID, r.entryID, r.transfer, r.group, r.request, r.media, r.presentation, r.audioTrack, r.subtitleTrack, r.location, r.endReason, r.message, r.partIndex, r.revision, r.generation, r.mediaGeneration, r.startPosition, r.position, r.lastSeq, r.bandwidth, r.lease, r.created, r.updated, r.ended, r.pausedSince); err != nil {
			return err
		}

		if bound, ok := ctx.Value(castTransferStartKey{}).(castTransferStart); ok {
			if s.CastQueueTransferCommit == nil {
				return identity.ErrForbidden
			}
			if err := s.CastQueueTransferCommit(ctx, tx, c, bound.intent, bound.queue, bound.entry, bound.item, r.id, key, startDigest); err != nil {
				return err
			}
		}

		if bound, ok := ctx.Value(queueStartCustodyKey{}).(queueStartCustody); ok {
			if _, err := tx.ExecContext(ctx, `UPDATE queues_v1 SET session_id=?,current_entry_id=? WHERE id=?`, r.id, bound.currentEntry, bound.queue); err != nil {
				return err
			}
		}

		if c.UploadLimited != nil {
			if _, e := tx.ExecContext(ctx, `UPDATE playback_v1_sessions SET upload_limited=? WHERE id=?`, *c.UploadLimited, r.id); e != nil {
				return e
			}
		}
		if err := s.Playback.PublishPresentationTx(ctx, tx, c.Principal, media.ID, view.StartPositionMs, replacing, false); err != nil {
			return err
		}
		if continuation != nil {
			if err := continuation.ActivateTx(ctx, tx, r.id, media); err != nil {
				return err
			}
			if s.Subtitles != nil {
				plan, err := s.Subtitles.PlanTx(ctx, tx, c.Principal, base.item, media.ID)
				if err != nil {
					return err
				}
				result := subtitlePlanResult(plan, subtitleResult{files: []SubtitleFile{}})
				view.Subtitles, view.Subtitle = result.files, result.shown
			}
			acceptedView, err := json.Marshal(view)
			if err != nil {
				return err
			}
			r.presentation = string(acceptedView)
			if _, err = tx.ExecContext(ctx, `UPDATE playback_v1_sessions SET presentation=? WHERE id=? AND media_session_id=? AND media_generation=?`, r.presentation, r.id, media.ID, media.Generation); err != nil {
				return err
			}
		}
		if err := s.recordRadioStartTx(ctx, tx, c, r); err != nil {
			return err
		}
		if old.id != "" && old.ended == 0 {
			if err := s.endTx(ctx, tx, old, now, "replaced", ""); err != nil {
				return err
			}
		}
		if err := s.sessionEventsTx(ctx, tx, r, now, true); err != nil {
			return err
		}
		if continuation != nil {
			return continuation.AcceptTx(ctx, tx, r.id, media)
		}
		return nil
	})
	if err != nil {
		_ = s.Playback.StopV1(context.WithoutCancel(ctx), media.ID)
		// A concurrent start with the same key won: replay it.
		if existing, e := scanRow(s.DB.QueryRowContext(ctx, `SELECT `+rowSelect+` FROM playback_v1_sessions WHERE device_id=? AND start_key=?`, c.DeviceID, key)); e == nil {
			if existing.startDigest != startDigest {
				return SessionView{}, false, ErrIdempotencyMismatch
			}
			if req.CastTransferID != "" {
				if e = s.replayCastTransfer(ctx, c, req.CastTransferID, existing, key, startDigest); e != nil {
					return SessionView{}, false, e
				}
			}
			return s.replayStart(ctx, c, existing)
		}
		return SessionView{}, false, err
	}
	if replacing != nil {
		_ = s.Playback.StopV1(context.WithoutCancel(ctx), old.media)
	}
	s.hub.wake()
	s.sweep.kick(s)
	v := s.view(r)
	if state == "preparing" {
		v.RetryAfterMs = PreparingRetryMs
	}
	return v, state == "preparing", nil
}

// recheckInitialQueueStartTx repeats an initial Cast start's admission in the
// accepting writer: the same queue, selected entry and resolved item, the
// viewer's live device queue with no session yet, and the current family and
// active pairing. Any drift refuses the start.
func (s *Service) recheckInitialQueueStartTx(ctx context.Context, tx *sql.Tx, c Caller, bound queueInitialStart, base row) error {
	if bound.queue == "" || bound.entry == "" || bound.item == "" || base.queueID != bound.queue || base.entryID != bound.entry || base.item != bound.item {
		return ErrRevision
	}
	if err := s.castQueueStartAuthorityTx(ctx, tx, c, bound.queue, bound.entry, bound.item); err != nil {
		return err
	}
	q, err := loadQueue(ctx, tx, bound.queue)
	if err != nil {
		return ErrNotFound
	}
	if !ownsQueue(c, q) || q.session != "" || q.revision != bound.revision {
		return ErrRevision
	}
	l, err := s.readLayout(ctx, tx, q)
	if err != nil {
		return err
	}
	p, ok := l.positionOf(bound.entry)
	if !ok {
		return ErrNotFound
	}
	_, item, err := l.entryAtErr(p)
	if err != nil {
		return err
	}
	if item != bound.item {
		return ErrRevision
	}
	return nil
}

// sessionEventsTx publishes a session change: session.updated to its device and
// to the profile's other devices, admin.sessions to administrators ("the list
// changed; refetch", §17.14).
func (s *Service) sessionEventsTx(ctx context.Context, tx *sql.Tx, r row, now time.Time, listChanged bool) error {
	resource := &EventResource{Kind: "session", ID: r.id}
	state := r.state
	if r.ended > 0 {
		state = "ended"
	}
	data := map[string]any{"state": state, "deviceId": r.device, "generation": r.generation}
	if r.ended == 0 && !isChannel(r) {
		data["control"] = r.control()
	}
	if r.message != "" {
		data["message"] = r.message
	}
	if r.endReason != "" {
		data["reason"] = r.endReason
	}
	rev := strconv.Itoa(r.revision)
	if err := publishTx(ctx, tx, now, DeviceAudience(r.device), "session.updated", resource, rev, data); err != nil {
		return err
	}
	if err := publishTx(ctx, tx, now, ProfileAudience(r.authority, r.account, r.profile), "session.updated", resource, rev, data); err != nil {
		return err
	}
	if listChanged {
		return publishTx(ctx, tx, now, AdminAudience, "admin.sessions", nil, "", nil)
	}
	return nil
}

// grantReplacingTx writes the unwritten evidence of the sessions playing a
// media session the server is about to replace, while it still accepts it.
func (s *Service) grantReplacingTx(ctx context.Context, tx *sql.Tx, media string) error {
	ids, err := liveSessionsOn(ctx, tx, media)
	if err != nil {
		return err
	}
	for _, id := range ids {
		if err = s.drainObservationTx(ctx, tx, id); err != nil {
			return err
		}
	}
	return nil
}

func liveSessionsOn(ctx context.Context, tx *sql.Tx, media string) ([]string, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id FROM playback_v1_sessions WHERE media_session_id=? AND ended_ms=0`, media)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// grantReplacedTx publishes a media session the server replaced on its own
// (the HLS init-change fallback rotates its grant) to the v1 session playing
// it, in the replacing transaction (V10): the new media generation, a new
// session generation and the new URL, with the event that tells the device.
// The renderer's unwritten evidence for the old generation landed first
// (grantReplacingTx).
func (s *Service) grantReplacedTx(ctx context.Context, tx *sql.Tx, media string, generation int, url string) error {
	ids, err := liveSessionsOn(ctx, tx, media)
	if err != nil {
		return err
	}
	now := s.now()
	for _, id := range ids {
		r, err := loadRow(ctx, tx, id)
		if err != nil {
			return err
		}
		var presentation Presentation
		if err = json.Unmarshal([]byte(r.presentation), &presentation); err != nil {
			return err
		}
		presentation.URL = url
		encoded, err := json.Marshal(presentation)
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE playback_v1_sessions SET media_generation=?,generation=generation+1,presentation=?,revision=revision+1,updated_ms=? WHERE id=? AND media_session_id=? AND ended_ms=0`, generation, string(encoded), now.UnixMilli(), id, media); err != nil {
			return err
		}
		r.mediaGeneration, r.generation, r.presentation, r.revision = generation, r.generation+1, string(encoded), r.revision+1
		if err = s.sessionEventsTx(ctx, tx, r, now, false); err != nil {
			return err
		}
	}
	if len(ids) > 0 {
		return dbwork.AfterCommit(tx, s.hub.wake)
	}
	return nil
}

// endTx ends a session: its presentation's grant is fenced once the caller
// stops it (StopV1 after commit), and the row records why. The playing
// device's unwritten latest position and watched time are written first.
func (s *Service) endTx(ctx context.Context, tx *sql.Tx, r row, now time.Time, reason, message string) error {
	if err := s.drainObservationTx(ctx, tx, r.id); err != nil {
		return err
	}
	return s.endTxRaw(ctx, tx, r, now, reason, message)
}

// endTxRaw is endTx for a caller that already wrote the latest sample.
func (s *Service) endTxRaw(ctx context.Context, tx *sql.Tx, r row, now time.Time, reason, message string) error {
	res, err := tx.ExecContext(ctx, `UPDATE playback_v1_sessions SET ended_ms=?,end_reason=?,message=?,revision=revision+1,updated_ms=? WHERE id=? AND ended_ms=0`, now.UnixMilli(), reason, message, now.UnixMilli(), r.id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil
	}
	if err = s.retireAirPlayTx(ctx, tx, r.id); err != nil {
		return err
	}
	r.ended, r.endReason, r.message, r.revision = now.UnixMilli(), reason, message, r.revision+1
	if err = dbwork.AfterCommit(tx, func() { s.observations().retire(r.id) }); err != nil {
		return err
	}
	if err = s.sessionEventsTx(ctx, tx, r, now, true); err != nil {
		return err
	}
	if isChannel(r) && s.Channels != nil {
		if _, err = s.Channels.EndTx(ctx, tx, r.id); err != nil {
			return err
		}
	}
	if s.onEnded != nil {
		return s.onEnded(ctx, tx, r, now)
	}
	return nil
}

// End stops a session for a reason other than its own DELETE (lease expiry,
// admin terminate, transfer commit). The grant is fenced before returning.
func (s *Service) End(ctx context.Context, id, reason, message string) error {
	return s.endWhen(ctx, id, reason, message, nil)
}

// endWhen ends a session in one writer transaction: the renderer's unwritten
// latest sample first, then still (when set) decides on the current row
// whether the end applies at all, then the end. A sweep that chose a session
// before it resumed or renewed therefore ends nothing (CS-06), and nothing
// that follows an end runs unless the end really happened.
func (s *Service) endWhen(ctx context.Context, id, reason, message string, still func(context.Context, *sql.Tx, row) (bool, error)) error {
	r, err := loadRow(ctx, s.DB, id)
	if err != nil {
		return err
	}
	if r.ended > 0 {
		return nil
	}
	now := s.now()
	ended := false
	if err = dbwork.WithWriteTxContext(ctx, s.DB, dbwork.ClassEstablishedPlayback, func(ctx context.Context, tx *sql.Tx) error {
		if err := s.drainObservationTx(ctx, tx, id); err != nil {
			return err
		}
		current, err := loadRow(ctx, tx, id)
		if err != nil || current.ended > 0 {
			return err
		}
		if still != nil {
			if ok, err := still(ctx, tx, current); err != nil || !ok {
				return err
			}
			if current, err = loadRow(ctx, tx, id); err != nil {
				return err
			}
		}
		r, ended = current, true
		if err := s.endTxRaw(ctx, tx, r, now, reason, message); err != nil || r.media == "" {
			return err
		}
		// The grant is fenced in the same transaction that ends the session,
		// so there is no moment where the session is over and its media still
		// plays. StopV1 below then stops the stream itself.
		_, err = tx.ExecContext(ctx, `UPDATE playback_sessions SET state='stopped' WHERE id=?`, r.media)
		return err
	}); err != nil || !ended {
		return err
	}
	s.cancelPreparedFor(ctx, id)
	s.hub.wake()
	if isChannel(r) {
		s.wakeChannels()
	}
	s.sweepQueues() // an end is when old sessions may be due for pruning
	if r.media != "" {
		return s.Playback.StopV1(ctx, r.media)
	}
	return nil
}

// expireIfLapsed ends a session whose lease lapsed (spec §5.8) when it is read
// before the sweeper gets to it.
func (s *Service) expireIfLapsed(ctx context.Context, r row) row {
	if r.ended == 0 && r.lease <= s.now().UnixMilli() {
		// As in the sweep (CS-06): the renderer's latest sample is written
		// first, and a session heard from since keeps playing.
		if err := s.endWhen(ctx, r.id, "lease_expired", "", func(_ context.Context, _ *sql.Tx, current row) (bool, error) {
			return current.lease <= s.now().UnixMilli(), nil
		}); err == nil {
			if fresh, err := loadRow(ctx, s.DB, r.id); err == nil {
				return fresh
			}
		}
	}
	return r
}

func (c Caller) authorizeSession(ctx context.Context, r row) error {
	if !owns(c, r) {
		return ErrNotFound
	}
	if r.item != "" && c.AuthorizeItem != nil {
		return c.AuthorizeItem(ctx, r.item)
	}
	return nil
}

// session loads a session the caller may see.
func (s *Service) session(ctx context.Context, c Caller, id string) (row, error) {
	r, err := loadRow(ctx, s.DB, id)
	if err != nil {
		return r, err
	}
	if !owns(c, r) {
		return r, ErrNotFound
	}
	if err := c.authorizeSession(ctx, r); err != nil {
		return r, err
	}
	return s.expireIfLapsed(ctx, r), nil
}

// Get is GET /v1/playback/sessions/{id}. Reading a session renews its lease
// only through its own device's timeline, never here.
func (s *Service) Get(ctx context.Context, c Caller, id string) (SessionView, error) {
	r, err := s.session(ctx, c, id)
	if err != nil {
		return SessionView{}, err
	}
	s.endIfChannelRefused(ctx, r)
	var view SessionView
	err = dbwork.WithReadSnapshot(ctx, s.DB, func(readCtx context.Context) error {
		tx := dbwork.Snapshot(readCtx)
		current, err := loadRow(readCtx, tx, id)
		if err != nil {
			return err
		}
		// The row, ordinary viewer/media authorization and renderer facts share
		// one snapshot. A replacement never supplies output to an obsolete view.
		if err = c.authorizeSession(readCtx, current); err != nil {
			return err
		}
		view = s.view(current)
		if current.ended == 0 {
			view.ObservedPlayback, err = playback.ReadObservedPlayback(readCtx, tx, id, current.generation, view.Presentation.Generation)
			if err == nil && current.kind == "vod" {
				view.PlaybackInfo, err = playback.ReadPlaybackInfoTx(readCtx, tx, current.media, current.mediaGeneration, c.Principal)
			} else if err == nil && current.kind == "audio" && view.Presentation.AudioRender.playable() {
				audio := view.Presentation.AudioRender
				view.PlaybackInfo, err = playback.ReadAudioPlaybackInfoTx(readCtx, tx, current.media, current.mediaGeneration, &playback.AudioPlan{
					Version: audio.Version, ID: audio.ID, Mode: audio.Mode, Codec: audio.Codec,
					SampleRate: audio.SampleRate, Channels: audio.Channels, BitDepth: audio.BitDepth,
				})
			}
		}
		return err
	})
	return view, err
}

// endIfChannelRefused ends a live channel session the viewer is no longer
// permitted to watch (the profile's Live TV switch was turned off mid-play:
// the channel resolver refuses it with feature_restricted), with that reason,
// so the player shows why instead of a channel stuck preparing. Like
// expireIfLapsed, a read of such a session is where it ends.
func (s *Service) endIfChannelRefused(ctx context.Context, r row) row {
	if !isChannel(r) || r.ended != 0 || s.Channels == nil {
		return r
	}
	tx, done, err := dbwork.BeginRead(ctx, s.DB)
	if err != nil {
		return r
	}
	_, err = s.Channels.ReadTx(ctx, tx, rowPrincipal(r), r.id)
	done()
	var fault *playback.ControlFault
	if !errors.As(err, &fault) || fault.Code != "feature_restricted" {
		return r
	}
	if err = s.End(ctx, r.id, "feature_restricted", "Live TV is turned off for this profile."); err != nil {
		return r
	}
	if fresh, err := loadRow(ctx, s.DB, r.id); err == nil {
		return fresh
	}
	return r
}

// IsV1Session reports whether an id names a v1 session (legacy routes share
// their paths with v1 and dispatch on it).
func (s *Service) IsV1Session(ctx context.Context, id string) bool {
	var one int
	return s.DB.QueryRowContext(ctx, `SELECT 1 FROM playback_v1_sessions WHERE id=?`, id).Scan(&one) == nil
}
