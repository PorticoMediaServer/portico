package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/live"
	"github.com/PorticoMediaServer/portico/server-next/internal/playback"
)

// LiveCountdown says a live stream stops at EndsAt so a recording can take its tuner.
type LiveCountdown struct {
	EndsAt time.Time `json:"endsAt"`
	Reason string    `json:"reason"`
}

// LiveWindow is what a live session can play: from its oldest kept moment to live. The app
// pauses and rewinds within it by asking the stream for a moment with at=.
type LiveWindow struct {
	OldestAt time.Time `json:"oldestAt"`
	LiveAt   time.Time `json:"liveAt"`
}

// LiveHLS makes HLS of a live channel for a device that cannot play the source's MPEG-TS as it
// is: the media engine (package mediaengine).
type LiveHLS interface {
	// StartLive decides how the device plays the channel: nil for the source's stream as it
	// is, or the HLS it starts for the device.
	StartLive(ctx context.Context, r live.Conversion) (*live.Play, error)
	// ServeLive answers a request for the HLS's playlist (index.m3u8) or a file it names.
	ServeLive(w http.ResponseWriter, r *http.Request, sessionID, file string)
	StopLive(sessionID string)
	// LiveFailed says the conversion failed for good.
	LiveFailed(sessionID string) bool
}

// LiveHLSWith gives the server the media engine's live HLS. Call it before serving.
func (s *Server) LiveHLSWith(engine LiveHLS) { s.liveHLS = engine }

// liveStreamRoute serves a live session's stream: the source's own MPEG-TS, from the moment
// asked for (at=, RFC 3339) or from live. The address names its session and is its
// permission, like every media address; it ends with the session. A session played as HLS
// is served beneath it: index.m3u8 (at= asks for a moment) and the files it names.
func (s *Server) liveStreamRoute() {
	files := &Route{Pattern: "GET /v1/live/streams/{grant}/{file}", Access: Public, Scope: ScopeOpen}
	s.routes = append(s.routes, files)
	s.mux.HandleFunc(files.Pattern, func(w http.ResponseWriter, r *http.Request) {
		v, ok := s.viewings.ByGrant(r.PathValue("grant"))
		if !ok || v.Play == nil || s.liveHLS == nil {
			s.writeError(w, r, ErrNotFound())
			return
		}
		s.liveHLS.ServeLive(w, r, v.ID, r.PathValue("file"))
	})
	route := &Route{Pattern: "GET /v1/live/streams/{grant}", Access: Public, Scope: ScopeOpen}
	s.routes = append(s.routes, route)
	s.mux.HandleFunc(route.Pattern, func(w http.ResponseWriter, r *http.Request) {
		v, ok := s.viewings.ByGrant(r.PathValue("grant"))
		if !ok {
			s.writeError(w, r, ErrNotFound())
			return
		}
		in := v.Input
		var at time.Time
		if text := r.URL.Query().Get("at"); text != "" {
			var err error
			if at, err = time.Parse(time.RFC3339Nano, text); err != nil {
				s.writeError(w, r, ErrField("at", "Use a time such as 2026-11-10T20:00:00Z."))
				return
			}
		}
		w.Header().Set("Content-Type", "video/mp2t")
		w.Header().Set("Cache-Control", "no-store")
		in.Read(r.Context(), at, w)
	})
}

// noTuner names what is using every tuner: the account's own streams and recordings by name,
// anyone else's as another viewer or a recording.
func noTuner(busy *live.NoTuner, userID int64) *Error {
	var names []string
	for _, u := range busy.Using {
		switch {
		case u.UserID == userID && u.Name != "":
			names = append(names, u.Name)
		case u.UserID == userID && !u.Record:
			names = append(names, "Another of your devices")
		case u.Record:
			names = append(names, "A recording")
		default:
			names = append(names, "Another viewer")
		}
	}
	if len(names) == 0 {
		return ErrConflict("no_tuner", "Every tuner is busy. Try again in a moment.")
	}
	return ErrConflict("no_tuner", "Every tuner is busy: "+strings.Join(names, ", ")+".")
}

// watchSourceChannel starts a live session on a source's channel: it joins the channel's
// shared input, or takes a free tuner for it.
func (s *Server) watchSourceChannel(r *http.Request, ch db.LiveChannel, asked string) (PlaybackSession, error) {
	ctx := r.Context()
	c := s.playbackCaller(r)
	// A key or a token without a device has no name to show, and its account's limit is read
	// through a device: such a caller is held to none here, as it cannot play files either.
	device, err := s.db.SessionDevice(ctx, c.SignInID)
	if err != nil && !errors.Is(err, db.ErrNotFound) {
		return PlaybackSession{}, err
	}
	limit := 0
	if err == nil {
		if limit, err = s.playback.AccountLimit(ctx, c.SignInID); err != nil {
			return PlaybackSession{}, err
		}
	}
	limits, err := s.playback.Limits(ctx)
	if err != nil {
		return PlaybackSession{}, err
	}
	// Every stream counts but what this device plays now, which this start replaces. The place
	// is taken in the same step as the count, before the seconds of reaching the source, so
	// two starts at once cannot both take the last one; it is given back if this start fails.
	var held string
	if err := s.playback.AdmitOther(c.Viewer.UserID, c.SignInID, limit, limits.Total, func() {
		held = s.viewings.Hold(c.Viewer.UserID, c.SignInID)
	}); err != nil {
		return PlaybackSession{}, playbackError(err)
	}
	defer s.viewings.Release(held) // nothing once the viewing has started in it
	locator, sourceID, tuners, err := s.db.ChannelLocator(ctx, ch.ID)
	if err != nil {
		return PlaybackSession{}, err
	}
	// A source that signs in keeps no address in its channels: it is made here, in memory.
	src, err := s.db.LiveSourceByID(ctx, sourceID)
	if err != nil {
		return PlaybackSession{}, err
	}
	var cfg live.LiveSourceConfig
	json.Unmarshal([]byte(src.Config), &cfg)
	var signIn live.LiveSignIn
	if src.Kind == "xtream" {
		if signIn, err = s.sources.SignIn(src.PublicID, src.Secret); err != nil {
			return PlaybackSession{}, ErrConflict("source_sign_in", "This channel's source needs its sign-in again.")
		}
	}
	if locator, err = live.StreamAddress(src.Kind, cfg, signIn, locator); err != nil {
		return PlaybackSession{}, ErrConflict("source_sign_in", "This channel's source needs its sign-in again.")
	}
	use := &live.Use{UserID: c.Viewer.UserID, Name: device.Name, Away: liveNetwork(asked, c.AtHome) != playback.NetworkLocal}
	// What this device watches live gives its tuner back first, so it can change channel.
	s.viewings.StopDevice(c.Viewer.UserID, c.SignInID)
	in, err := s.tuners.Tune(ch.ID, sourceID, tuners, locator, use)
	var busy *live.NoTuner
	if errors.As(err, &busy) {
		return PlaybackSession{}, noTuner(busy, c.Viewer.UserID)
	}
	if err != nil {
		return PlaybackSession{}, err
	}
	for _, other := range s.playback.AccountSessions(c.Viewer.UserID) {
		if c.SignInID != 0 && other.SignInID == c.SignInID { // this device stops what it was playing
			s.playback.Stop(ctx, other.ID, c.Viewer.UserID, c.SignInID, nil)
		}
	}
	v := s.viewings.Start(c.Viewer.UserID, c.Viewer.ProfileID, c.SignInID, ch.PublicID, in, use, held)
	if s.liveHLS != nil {
		// The same planner as a title's decides how this device plays the channel.
		conversion := live.Conversion{SessionID: v.ID, Input: in, Network: liveNetwork(asked, c.AtHome)}
		if device.PublicID != "" {
			if doc, _, err := s.db.DeviceCapabilities(ctx, c.Viewer.UserID, device.PublicID); err == nil {
				conversion.Capabilities = json.RawMessage(doc)
			} else if !errors.Is(err, db.ErrNotFound) {
				s.viewings.Stop(v.ID, c.Viewer.UserID)
				return PlaybackSession{}, err
			}
		}
		prefs, err := s.db.Preferences(ctx, c.Viewer.ProfileID, device.ID)
		if err == nil {
			conversion.Preferences = prefs
			conversion.Limits, err = s.db.PlayLimits(ctx, c.Viewer.UserID)
		}
		var play *live.Play
		if err == nil {
			play, err = s.liveHLS.StartLive(ctx, conversion)
		}
		if err != nil {
			// Nothing plays, so the tuner is given back now, not when the lease runs out.
			s.viewings.Stop(v.ID, c.Viewer.UserID)
			return PlaybackSession{}, playbackError(err)
		}
		if !s.viewings.SetPlay(v.ID, play) {
			// The viewing ended while its channel was being measured (the device started
			// again, or the session was stopped): its Ended came before there was anything to
			// stop, so what was just made is stopped here, or it would hold its place against
			// the owner's limit on conversions until a restart.
			if play != nil {
				s.liveHLS.StopLive(v.ID)
			}
			return PlaybackSession{}, ErrNotFound()
		}
		v.Play = play
	}
	out := s.liveSessionOut(*v)
	out.status = http.StatusCreated
	return out, nil
}

func (s *Server) liveSessionOut(v live.Viewing) PlaybackSession {
	state := playback.StatePlaying
	if v.Paused {
		state = playback.StatePaused
	}
	oldest, now := v.Input.Window()
	out := PlaybackSession{ID: v.ID, Kind: "live", State: state, Channel: &playback.LibraryChannelLink{ID: v.ChannelID},
		Lease:        PlaybackLease{ExpiresAt: v.Lease.UTC(), ReportEveryMs: live.ReportEvery.Milliseconds()},
		Presentation: Presentation{Mode: "live", URL: "/v1/live/streams/" + v.Grant, Engine: "copy", Route: "original"},
		Live:         &LiveWindow{OldestAt: oldest.UTC(), LiveAt: now.UTC()}}
	if v.Play != nil {
		out.Presentation = Presentation{Mode: "live", URL: "/v1/live/streams/" + v.Grant + "/index.m3u8", Engine: v.Play.Engine,
			Route: v.Play.Route, Container: v.Play.Container}
	}
	if !v.CountdownTo.IsZero() {
		out.Countdown = &LiveCountdown{EndsAt: v.CountdownTo.UTC(), Reason: v.CountdownReason}
	}
	if err := v.Input.Stopped(); err != nil {
		out.State, out.End = playback.StateEnded, &SessionEnd{Reason: "failed", Message: "The channel stopped."}
	} else if v.Play != nil && s.liveHLS != nil && s.liveHLS.LiveFailed(v.ID) {
		out.State, out.End = playback.StateEnded, &SessionEnd{Reason: "failed", Message: "This channel could not be converted for this device."}
	}
	return out
}

// liveNetwork is the kind of connection a live start is on, as a title's start decides it:
// the server tells home from away, and the app may say it is on a cellular network.
func liveNetwork(asked string, atHome bool) string {
	switch {
	case asked == playback.NetworkCellular:
		return playback.NetworkCellular
	case atHome:
		return playback.NetworkLocal
	}
	return playback.NetworkRemote
}

// liveSession answers the playback routes for a live session.
func (s *Server) liveSession(r *http.Request, paused *bool) (PlaybackSession, error) {
	userID := CallerFrom(r.Context()).UserID
	v, ok := s.viewings.Get(r.PathValue("id"), userID)
	if ok && paused != nil {
		v, ok = s.viewings.Report(v.ID, userID, *paused)
	}
	if !ok {
		if message, stopped := s.viewings.StoppedFor(r.PathValue("id"), userID); stopped {
			// Stopped by the owner or an administrator: the viewer is told so (area 7 rule 42).
			return PlaybackSession{ID: r.PathValue("id"), Kind: "live", State: playback.StateEnded,
				End: &SessionEnd{Reason: playback.EndTerminated, Message: message}}, nil
		}
		return PlaybackSession{}, ErrNotFound()
	}
	return s.liveSessionOut(v), nil
}
