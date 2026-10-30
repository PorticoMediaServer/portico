package playbackv1

// Channels on v1 sessions (spec §11, §18.6; Plan — Client Playback Migration
// §10, B4). A Live TV or Library Channels playback is an ordinary v1 session:
// POST /v1/playback/sessions {channelId} starts it (surfing is
// replacesSessionId), PATCH sets play/pause, a seek within the retained window
// or go live, the timeline renews it and says where the viewer is, and
// DELETE/end stops its producer. The linear state and the runtime's authority
// are playback.ChannelSessions; no v2 occurrence exists.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/playback"
)

// ChannelEngine is playback.ChannelSessions as the v1 layer uses it.
type ChannelEngine interface {
	Configured() bool
	StartTx(ctx context.Context, tx *sql.Tx, p identity.Principal, in playback.ChannelStart) (playback.LinearSelection, error)
	ReadTx(ctx context.Context, tx *sql.Tx, p identity.Principal, id string) (playback.ChannelState, error)
	SetIntentTx(ctx context.Context, tx *sql.Tx, p identity.Principal, id string, desired playback.LinearDesired) (playback.LinearDesired, error)
	ObserveTx(ctx context.Context, tx *sql.Tx, id, generation string, sequence, positionUS int64, acknowledgedSeek string) error
	EndTx(ctx context.Context, tx *sql.Tx, id string) (bool, error)
}

var (
	// ErrNoTuner: every tuner of the live source is in use (409 no_tuner_available).
	ErrNoTuner = errors.New("no tuner available")
	// ErrChannelsUnavailable: this server can't play channels (503).
	ErrChannelsUnavailable = errors.New("channel playback unavailable")
)

func init() { startTargets["channelId"] = (*Service).startChannel }

func isChannel(r row) bool { return r.kind == "live" || r.kind == "channel" }

// LinearView is a channel presentation's linear clock (spec §11): positions are
// milliseconds on the channel's timeline, whose origin is originMs.
type LinearView struct {
	ReservationRequiredAtMS *int64 `json:"reservationRequiredAtMs,omitempty"`
	// NowMS is the server's clock when this view was made, sent with a tuner
	// deadline so a player counts down against server time, not its own clock.
	NowMS               *int64               `json:"nowMs,omitempty"`
	Name                string               `json:"name"`
	Programme           *LinearProgrammeView `json:"programme,omitempty"`
	Next                *LinearProgrammeView `json:"next,omitempty"`
	OriginMs            int64                `json:"originMs"`
	WindowStartMs       int64                `json:"windowStartMs"`
	WindowEndMs         int64                `json:"windowEndMs"`
	LiveEdgeMs          int64                `json:"liveEdgeMs"`
	State               string               `json:"state"`
	ErrorCode           string               `json:"errorCode,omitempty"`
	Seek                *LinearSeekView      `json:"seek,omitempty"`
	ConfirmedPositionMs *int64               `json:"confirmedPositionMs,omitempty"`
	// Clocks map media time to this timeline (position = media time +
	// offsetMs over [startMs, endMs)), for a player that reports media time
	// rather than program dates (mpv).
	Clocks []LinearClockView `json:"clocks,omitempty"`
}

// LinearClockView is one clock epoch of the timeline.
type LinearClockView struct {
	StartMs  int64 `json:"startMs"`
	EndMs    int64 `json:"endMs"`
	OffsetMs int64 `json:"offsetMs"`
}
type LinearProgrammeView struct {
	ID      string `json:"id"`
	ItemID  string `json:"itemId,omitempty"`
	Title   string `json:"title"`
	StartMs int64  `json:"startMs"`
	EndMs   int64  `json:"endMs"`
}

// LinearSeekView is the seek the viewer asked for and whether their reports
// have reached it.
type LinearSeekView struct {
	ID           string `json:"id"`
	Live         bool   `json:"live,omitempty"`
	PositionMs   *int64 `json:"positionMs,omitempty"`
	Acknowledged bool   `json:"acknowledged"`
	Error        string `json:"error,omitempty"`
}

// channelReference reads a v1 channelId: "live:<sourceId>:<channelId>" or
// "library:<channelId>", at the viewer's guide generation or the current one.
func (s *Service) channelReference(ctx context.Context, channelID, generation string) (playback.LinearReference, string, error) {
	bad := &FieldError{Path: "channelId"}
	var ref playback.LinearReference
	kind := ""
	switch {
	case strings.HasPrefix(channelID, "live:"):
		source, channel, ok := strings.Cut(strings.TrimPrefix(channelID, "live:"), ":")
		if !ok || source == "" || channel == "" {
			return ref, "", bad
		}
		ref, kind = playback.LinearReference{Kind: "live-source", SourceID: source, ChannelID: channel}, "live"
		if generation == "" {
			if err := s.DB.QueryRowContext(ctx, `SELECT active_generation FROM live_sources WHERE id=? AND state='active'`, source).Scan(&generation); err != nil {
				return ref, "", ErrNotFound
			}
		}
	case strings.HasPrefix(channelID, "library:"):
		channel := strings.TrimPrefix(channelID, "library:")
		if channel == "" {
			return ref, "", bad
		}
		ref, kind = playback.LinearReference{Kind: "library-channel", ChannelID: channel}, "channel"
		if generation == "" {
			if err := s.DB.QueryRowContext(ctx, `SELECT active_generation FROM lc_channels WHERE id=? AND enabled=1`, channel).Scan(&generation); err != nil {
				return ref, "", ErrNotFound
			}
		}
	default:
		return ref, "", bad
	}
	ref.Generation = generation
	return ref, kind, nil
}

func (s *Service) startChannel(ctx context.Context, c Caller, key, startDigest string, req StartRequest) (SessionView, bool, error) {
	if err := s.requireObservationProtocol(c); err != nil {
		return SessionView{}, false, err
	}
	if s.Channels == nil || !s.Channels.Configured() {
		return SessionView{}, false, ErrChannelsUnavailable
	}
	switch req.StartFrom {
	case "", "live":
	default:
		return SessionView{}, false, &FieldError{Path: "startFrom"}
	}
	if req.StartPositionMs != nil || req.Queue != nil || req.VersionID != "" || req.Audio != nil || req.Subtitles != nil {
		return SessionView{}, false, &FieldError{Path: "channelId"}
	}
	ref, kind, err := s.channelReference(ctx, req.ChannelID, req.ChannelGeneration)
	if err != nil {
		return SessionView{}, false, err
	}
	var old row
	if req.ReplacesSessionID != "" {
		if old, err = loadRow(ctx, s.DB, req.ReplacesSessionID); err != nil || old.device != c.DeviceID {
			return SessionView{}, false, &FieldError{Path: "replacesSessionId"}
		}
	}
	// Member limits, like a VOD start: the stream count (channel sessions hold
	// no legacy row, so the count reads the v1 table too), the schedule and
	// the remote bitrate cap, then the channel allow/deny list with the
	// schedule again. A replay with the same key never re-admits. A zap
	// continues its old session's stream, so it admits as a replan and is
	// never counted twice against maxStreams.
	admitCtx := ctx
	if req.ReplacesSessionID != "" && old.ended == 0 {
		admitCtx = WithReplan(ctx, old.id, "")
	}
	// Admitted and made countable under one gate per account (admission_gate.go).
	release := s.admission.hold(c.Principal.AccountID)
	defer release()
	if err := c.admit(admitCtx, ""); err != nil {
		return SessionView{}, false, err
	}
	if err := c.admitChannel(ctx, req.ChannelID); err != nil {
		return SessionView{}, false, err
	}
	state := "playing"
	if req.State == "paused" {
		state = "paused"
	}
	now := s.now()
	requestJSON, _ := json.Marshal(map[string]string{"state": state, "channelGeneration": ref.Generation})
	r := row{id: newID("ps_"), device: c.DeviceID, account: c.Principal.AccountID, profile: c.Principal.ProfileID, authority: c.Principal.Authority, startKey: key, startDigest: startDigest,
		kind: kind, role: "local", state: state, channel: req.ChannelID, request: string(requestJSON), revision: 1, generation: 1,
		lease: now.Add(s.lease()).UnixMilli(), created: now.UnixMilli(), updated: now.UnixMilli(), location: "local"}
	if state == "paused" {
		r.pausedSince = now.UnixMilli()
	}
	if c.Remote {
		r.location = "remote"
	}
	document := s.deviceDocument(ctx, c.DeviceID)
	profile := document.Profile(playback.EngineNative)
	// A second engine that plays channels is planned beside the native one
	// unless the viewer chose Native; the producer picks between them.
	var mpv *playback.ClientProfile
	if second := document.Profile(playback.EngineMPV); document.HasEngine(playback.EngineMPV) && second.LiveChannels && document.EngineSetting != playback.EngineSettingNative {
		mpv = &second
	}
	// The narrowed profiles are what StartTx stores in
	// playback_channel_sessions, and linearWorkTx reads them back for every
	// producer of that session (re-tunes and programme boundaries included),
	// so the cap holds for the session's whole life.
	if c.Remote && c.AdminMaxVideoBitrateBPS > 0 {
		profile = playback.WithVideoBitrateCeiling(profile, c.AdminMaxVideoBitrateBPS)
		if mpv != nil {
			narrowed := playback.WithVideoBitrateCeiling(*mpv, c.AdminMaxVideoBitrateBPS)
			mpv = &narrowed
		}
	}
	err = dbwork.WithWriteTxContext(ctx, s.DB, dbwork.ClassPlaybackStart, func(ctx context.Context, tx *sql.Tx) error {
		if ref.Kind == "live-source" && s.TunerAvailable != nil {
			ok, err := s.TunerAvailable(ctx, tx, ref.SourceID, ref.ChannelID, old.id)
			if err != nil {
				return err
			}
			if !ok {
				return ErrNoTuner
			}
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO playback_v1_sessions(`+rowColumns+`) `+rowValues,
			r.id, r.device, r.account, r.profile, r.authority, r.startKey, r.startDigest, r.kind, r.role, r.state, r.item, r.version, r.channel, r.queueID, r.entryID, r.transfer, r.group, r.request, r.media, r.presentation, r.audioTrack, r.subtitleTrack, r.location, r.endReason, r.message, r.partIndex, r.revision, r.generation, r.mediaGeneration, r.startPosition, r.position, r.lastSeq, r.bandwidth, r.lease, r.created, r.updated, r.ended, r.pausedSince); err != nil {
			return err
		}
		if _, err := s.Channels.StartTx(ctx, tx, c.Principal, playback.ChannelStart{SessionID: r.id, Channel: ref, State: state, Profile: profile, MPVProfile: mpv, EngineSetting: document.EngineSetting, LeaseUntil: time.UnixMilli(r.lease)}); err != nil {
			return err
		}
		if old.id != "" && old.ended == 0 {
			if err := s.endTx(ctx, tx, old, now, "replaced", ""); err != nil {
				return err
			}
		}
		return s.sessionEventsTx(ctx, tx, r, now, true)
	})
	if err != nil {
		if existing, e := scanRow(s.DB.QueryRowContext(ctx, `SELECT `+rowSelect+` FROM playback_v1_sessions WHERE device_id=? AND start_key=?`, c.DeviceID, key)); e == nil {
			if existing.startDigest != startDigest {
				return SessionView{}, false, ErrIdempotencyMismatch
			}
			return s.replayStart(ctx, c, existing)
		}
		return SessionView{}, false, err
	}
	if old.id != "" && old.media != "" {
		_ = s.Playback.StopV1(ctx, old.media)
	}
	s.hub.wake()
	s.sweep.kick(s)
	s.wakeChannels()
	return s.view(r), false, nil
}

func (s *Service) wakeChannels() {
	if s.ChannelWake != nil {
		s.ChannelWake()
	}
}

func rowPrincipal(r row) identity.Principal {
	var p identity.Principal
	p.Authority, p.AccountID, p.ProfileID = r.authority, r.account, r.profile
	return p
}

// channelPresentation is a channel session's presentation, read now: the
// stream, and the linear clock (§11). The generation is the timeshift buffer's
// (a new buffer is new bytes: the client reloads; its reports name it).
func (s *Service) channelPresentation(r row) Presentation {
	p := Presentation{Mode: "stream", Subtitles: []SubtitleFile{}}
	if s.Channels == nil || r.ended > 0 {
		return p
	}
	ctx := context.Background()
	tx, done, err := dbwork.BeginRead(ctx, s.DB)
	if err != nil {
		return p
	}
	defer done()
	st, err := s.Channels.ReadTx(ctx, tx, rowPrincipal(r), r.id)
	if err != nil {
		return p
	}
	return s.channelPresentationFrom(st, 0)
}

// patchChannel is PATCH on a channel session (§18.6): state (play/pause; play
// on a failed source is a retry), and a seek within the retained window
// ({positionMs}) or to the live edge ({live: true}). Nothing else applies.
func (s *Service) patchChannel(ctx context.Context, c Caller, r row, change Change) (SessionView, error) {
	if change.VersionID != "" || change.PartIndex != nil || change.Audio != nil || change.Subtitles != nil || change.Quality != nil || change.SubtitleOffsetMs != nil {
		return SessionView{}, &FieldError{Path: "channelId"}
	}
	now := s.now()
	state := r.state
	if change.State != "" {
		state = change.State
	}
	err := dbwork.WithWriteTxContext(ctx, s.DB, dbwork.ClassEstablishedPlayback, func(ctx context.Context, tx *sql.Tx) error {
		if err := s.drainObservationTx(ctx, tx, r.id); err != nil {
			return err
		}
		st, err := s.Channels.ReadTx(ctx, tx, rowPrincipal(r), r.id)
		if err != nil {
			return err
		}
		desired := st.Desired
		desired.State = state
		desired.RetryID = nil
		if st.Media.ErrorCode != "" && state == "playing" {
			retry := newID("rt_")
			desired.RetryID = &retry
		}
		if q := change.Seek; q != nil {
			id := q.ID
			if len(id) < 16 {
				id = newID("sk_")
			}
			seek := &playback.LinearSeek{ID: id, Generation: st.Media.BufferGeneration, Kind: "live"}
			if !q.Live {
				pos := strconv.FormatInt(q.PositionMs*1000, 10)
				seek.Kind, seek.PositionUS = "position", &pos
			}
			desired.Seek = seek
		}
		if _, err = s.Channels.SetIntentTx(ctx, tx, rowPrincipal(r), r.id, desired); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE playback_v1_sessions SET state=?,revision=revision+1,updated_ms=?,`+pausedSinceUpdate+` WHERE id=? AND ended_ms=0`, state, now.UnixMilli(), state, now.UnixMilli(), r.id); err != nil {
			return err
		}
		r.state, r.revision = state, r.revision+1
		return s.sessionEventsTx(ctx, tx, r, now, false)
	})
	if err != nil {
		return SessionView{}, err
	}
	s.hub.wake()
	s.wakeChannels()
	return s.view(r), nil
}

func isControlFault(err error) bool {
	var f *playback.ControlFault
	return errors.As(err, &f)
}
