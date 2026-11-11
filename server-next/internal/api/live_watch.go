package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/live"
	"github.com/PorticoMediaServer/portico/server-next/internal/playback"
)

// LiveWindow is what a live session can play: from its oldest kept moment to live. The app
// pauses and rewinds within it by asking the stream for a moment with at=.
type LiveWindow struct {
	OldestAt time.Time `json:"oldestAt"`
	LiveAt   time.Time `json:"liveAt"`
}

// liveStreamRoute serves a live session's stream: the source's own MPEG-TS, from the moment
// asked for (at=, RFC 3339) or from live. The address names its session and is its
// permission, like every media address; it ends with the session.
func (s *Server) liveStreamRoute() {
	route := &Route{Pattern: "GET /v1/live/streams/{grant}", Access: Public, Scope: ScopeOpen}
	s.routes = append(s.routes, route)
	s.mux.HandleFunc(route.Pattern, func(w http.ResponseWriter, r *http.Request) {
		in, ok := s.viewings.ByGrant(r.PathValue("grant"))
		if !ok {
			s.writeError(w, r, ErrNotFound())
			return
		}
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
		case u.Record:
			names = append(names, "A recording")
		default:
			names = append(names, "Another viewer")
		}
	}
	return ErrConflict("no_tuner", "Every tuner is busy: "+strings.Join(names, ", ")+".")
}

// watchSourceChannel starts a live session on a source's channel: it joins the channel's
// shared input, or takes a free tuner for it.
func (s *Server) watchSourceChannel(r *http.Request, ch db.LiveChannel) (PlaybackSession, error) {
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
	account, _ := s.viewings.Count(c.Viewer.UserID)
	for _, other := range s.playback.AccountSessions(c.Viewer.UserID) {
		if c.SignInID == 0 || other.SignInID != c.SignInID {
			account++
		}
	}
	if limit > 0 && account >= limit {
		return PlaybackSession{}, playbackError(&playback.LimitReached{Scope: "account", Limit: limit})
	}
	locator, sourceID, tuners, err := s.db.ChannelLocator(ctx, ch.ID)
	if err != nil {
		return PlaybackSession{}, err
	}
	// A source that signs in keeps no address in its channels: it is made here, in memory.
	src, err := s.db.LiveSourceByID(ctx, sourceID)
	if err != nil {
		return PlaybackSession{}, err
	}
	var cfg live.Config
	json.Unmarshal([]byte(src.Config), &cfg)
	var signIn live.SignIn
	if src.Kind == "xtream" {
		if signIn, err = s.sources.SignIn(src.PublicID, src.Secret); err != nil {
			return PlaybackSession{}, ErrConflict("source_sign_in", "This channel's source needs its sign-in again.")
		}
	}
	if locator, err = live.StreamAddress(src.Kind, cfg, signIn, locator); err != nil {
		return PlaybackSession{}, ErrConflict("source_sign_in", "This channel's source needs its sign-in again.")
	}
	use := &live.Use{UserID: c.Viewer.UserID, Name: device.Name}
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
			s.playback.Stop(ctx, other.ID, c.Viewer.UserID, nil)
		}
	}
	v := s.viewings.Start(c.Viewer.UserID, c.Viewer.ProfileID, c.SignInID, ch.PublicID, in, use)
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
	out := PlaybackSession{ID: v.ID, Kind: "live", State: state, Channel: &playback.ChannelLink{ID: v.ChannelID},
		Lease:        PlaybackLease{ExpiresAt: v.Lease.UTC(), ReportEveryMs: live.ReportEvery.Milliseconds()},
		Presentation: Presentation{Mode: "live", URL: "/v1/live/streams/" + v.Grant, Engine: "copy", Route: "original"},
		Live:         &LiveWindow{OldestAt: oldest.UTC(), LiveAt: now.UTC()}}
	if err := v.Input.Stopped(); err != nil {
		out.State, out.End = playback.StateEnded, &SessionEnd{Reason: "failed", Message: "The channel stopped."}
	}
	return out
}

// liveSession answers the playback routes for a live session.
func (s *Server) liveSession(r *http.Request, paused *bool) (PlaybackSession, error) {
	userID := CallerFrom(r.Context()).UserID
	v, ok := s.viewings.Get(r.PathValue("id"), userID)
	if ok && paused != nil {
		v, ok = s.viewings.Report(v.ID, userID, *paused)
	}
	if !ok {
		return PlaybackSession{}, ErrNotFound()
	}
	return s.liveSessionOut(v), nil
}
