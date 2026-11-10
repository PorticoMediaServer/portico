package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/playback"
)

// Playback returns the service that runs viewing sessions, for the program to wire the media
// engine into and to save progress with when it shuts down.
func (s *Server) Playback() *playback.Service { return s.playback }

func (s *Server) playbackRoutes() {
	// Playback belongs to a device, so these take a signed-in person and not a key.
	for _, route := range []*Route{
		Handle(s, "GET /v1/devices/{id}/capabilities", SignedIn, s.getCapabilities),
		Handle(s, "PUT /v1/devices/{id}/capabilities", SignedIn, s.putCapabilities),
		Handle(s, "GET /v1/items/{id}/playback-options", WithProfile, s.playbackOptions),
		Handle(s, "POST /v1/playback/sessions", WithProfile, s.startPlayback),
		Handle(s, "GET /v1/playback/sessions", WithProfile, s.accountPlayback),
		Handle(s, "GET /v1/playback/sessions/{id}", WithProfile, s.getPlayback),
		Handle(s, "PATCH /v1/playback/sessions/{id}", WithProfile, s.changePlayback),
		Handle(s, "PUT /v1/playback/sessions/{id}/progress", WithProfile, s.reportPlayback),
		Handle(s, "DELETE /v1/playback/sessions/{id}", WithProfile, s.stopPlayback),
	} {
		route.Scope = ScopeSession
	}
	Handle(s, "GET /v1/admin/playback/stream-limits", Owner, s.getStreamLimits)
	Handle(s, "PUT /v1/admin/playback/stream-limits", Owner, s.putStreamLimits)
	Handle(s, "GET /v1/admin/playback/failures", Owner, s.listPlaybackFailures)
	Handle(s, "DELETE /v1/admin/playback/failures/{id}", Owner, s.forgetPlaybackFailure)
}

// The refusals of playback. Each says what happened in plain words and whether trying again
// can help.
func playbackError(err error) error {
	var invalid *playback.Invalid
	var limit *playback.LimitReached
	switch {
	case err == nil:
		return nil
	case errors.Is(err, db.ErrNotFound):
		return ErrNotFound()
	case errors.As(err, &invalid):
		return ErrField(invalid.Field, invalid.Message)
	case errors.Is(err, playback.ErrEnded):
		return refusal(http.StatusGone, "session_ended", "This playback has ended.", false)
	case errors.Is(err, playback.ErrKeyReused):
		return errKeyReused()
	case errors.As(err, &limit) && limit.Scope == "account":
		return ErrConflict("stream_limit_reached", "This account is already playing as many streams as it is allowed.")
	case errors.As(err, &limit):
		return ErrConflict("server_stream_limit_reached", "This server is already playing as many streams as its owner allows.")
	case errors.Is(err, playback.ErrUnsupported):
		return refusal(http.StatusUnprocessableEntity, "unsupported_media", "This title can't be played on this device.", false)
	case errors.Is(err, playback.ErrConversionOff):
		return refusal(http.StatusForbidden, "conversion_off", "This title needs converting, and converting is turned off on this server.", false)
	case errors.Is(err, playback.ErrTooSlow):
		return refusal(http.StatusUnprocessableEntity, "connection_too_slow",
			"Your connection to this server is too slow for this quality. Choose a lower quality or Automatic.", false)
	case errors.Is(err, playback.ErrUpdateRequired):
		return refusal(http.StatusBadRequest, "client_update_required", "Update the app to play on this server.", false)
	case errors.Is(err, playback.ErrSourceUnavailable):
		e := refusal(http.StatusServiceUnavailable, "source_unavailable", "The file for this title can't be reached right now.", true)
		e.RetryAfterMs = 5000
		return e
	case errors.Is(err, playback.ErrBusy):
		return ErrTooMany("conversion_capacity", "This server is converting as many streams as it is allowed. Playback starts by itself when one is free.", 0)
	}
	return err
}

func (s *Server) playbackCaller(r *http.Request) playback.Caller {
	c := CallerFrom(r.Context())
	networks := s.networks()
	addr, ok := networks.Client(r)
	return playback.Caller{Viewer: db.Viewer{UserID: c.UserID, ProfileID: c.ProfileID}, SignInID: c.SessionID,
		AtHome: ok && networks.AtHome(addr)}
}

// deviceID is the device a capabilities route is about: one of the caller's own by id, or
// "current" for the one asking.
func (s *Server) deviceID(r *http.Request) (string, error) {
	id := r.PathValue("id")
	if id != "current" {
		return id, nil
	}
	device, err := s.db.SessionDevice(r.Context(), CallerFrom(r.Context()).SessionID)
	return device.PublicID, playbackError(err)
}

func (s *Server) getCapabilities(r *http.Request, _ None) (CapabilityDocument, error) {
	id, err := s.deviceID(r)
	if err != nil {
		return CapabilityDocument{}, err
	}
	document, _, err := s.db.DeviceCapabilities(r.Context(), CallerFrom(r.Context()).UserID, id)
	return CapabilityDocument{raw: json.RawMessage(document)}, playbackError(err)
}

func (s *Server) putCapabilities(r *http.Request, req CapabilityDocument) (None, error) {
	id, err := s.deviceID(r)
	if err != nil {
		return None{}, err
	}
	var object map[string]json.RawMessage
	if json.Unmarshal(req.raw, &object) != nil || len(object) == 0 {
		return None{}, ErrInvalid("Send the device's capability document.")
	}
	if err := s.playback.CheckCapabilities(req.raw); err != nil {
		return None{}, refusal(http.StatusBadRequest, "client_update_required", "Update the app to play on this server.", false)
	}
	return None{}, playbackError(s.db.PutDeviceCapabilities(r.Context(), CallerFrom(r.Context()).UserID, id, string(req.raw), s.now()))
}

// What the media engine cannot do yet. Each is refused by name, so an app can say so
// instead of showing a stream that never starts.
func notYet(code, message string) *Error {
	return refusal(http.StatusNotImplemented, code, message, false)
}

func subtitleAsk(pick *SubtitlePick) (track *string, delivery string, err error) {
	if pick == nil {
		return nil, "", nil
	}
	switch pick.Delivery {
	case "", "auto", "embeddedClient", "burn":
	case "sidecar":
		return nil, "", notYet("subtitle_delivery_unavailable", "This server can't send subtitles beside the stream yet. Let it choose, or burn them in.")
	default:
		return nil, "", ErrField("subtitles.delivery", "The delivery is auto, embeddedClient or burn.")
	}
	off := ""
	if pick.TrackID == nil {
		return &off, pick.Delivery, nil
	}
	return pick.TrackID, pick.Delivery, nil
}

// playbackOptions previews a start. The query can name a version, tracks and a quality to
// see how they would be delivered: versionId, audioId, subtitleId ("none" for off), quality
// (a mode) with qualityId or the max… numbers, network and throughputKbps.
func (s *Server) playbackOptions(r *http.Request, _ None) (PlaybackOptions, error) {
	q := r.URL.Query()
	number := func(name string) int { n, _ := strconv.Atoi(q.Get(name)); return max(n, 0) }
	ask := playback.Ask{VersionID: q.Get("versionId"), Network: q.Get("network"), ThroughputKbps: number("throughputKbps")}
	if q.Has("audioId") {
		id := q.Get("audioId")
		ask.Audio = &id
	}
	if q.Has("subtitleId") {
		id := q.Get("subtitleId")
		if id == "none" {
			id = ""
		}
		ask.Subtitle = &id
	}
	if q.Has("quality") {
		ask.Quality = &playback.Quality{Mode: q.Get("quality"), ID: q.Get("qualityId"), MaxVideoBitrateKbps: number("maxVideoBitrateKbps"),
			MaxHeight: number("maxHeight"), MaxAudioBitrateKbps: number("maxAudioBitrateKbps")}
	}
	options, err := s.playback.Options(r.Context(), s.playbackCaller(r), r.PathValue("id"), ask)
	if err != nil {
		return PlaybackOptions{}, playbackError(err)
	}
	return optionsOut(options), nil
}

// startPlayback answers 201 with the session, 200 for a start that was sent before, or 202
// with retryAfterMs while the stream gets ready: the app then sends the same request again.
func (s *Server) startPlayback(r *http.Request, req PlaybackStart) (PlaybackSession, error) {
	key := r.Header.Get("Idempotency-Key")
	if len(key) > 128 {
		return PlaybackSession{}, ErrInvalid("The Idempotency-Key is at most 128 characters.")
	}
	if key == "" {
		key = db.NewID("once") // a start sent without a key is never taken for a repeat
	}
	if req.ItemID == "" {
		return PlaybackSession{}, ErrField("itemId", "Say which title to play.")
	}
	in := playback.StartInput{Caller: s.playbackCaller(r), Key: key, Hash: requestHash(req), ItemID: req.ItemID,
		PositionMs: req.StartPositionMs, FromStart: req.StartFrom == "beginning" || req.StartFrom == "startOver",
		Paused: req.State == playback.StatePaused,
		Ask:    playback.Ask{VersionID: req.VersionID, Quality: req.Quality, Network: req.Network, ThroughputKbps: req.ThroughputKbps}}
	if req.Audio != nil {
		in.Ask.Audio = &req.Audio.TrackID
	}
	var err error
	if in.Ask.Subtitle, in.Ask.SubtitleDelivery, err = subtitleAsk(req.Subtitles); err != nil {
		return PlaybackSession{}, err
	}
	if len(req.DiscTitle) > 0 && string(req.DiscTitle) != "null" {
		return PlaybackSession{}, notYet("disc_playback_unavailable", "This server can't play discs yet.")
	}
	session, ready, created, err := s.playback.Start(r.Context(), in)
	if err != nil {
		return PlaybackSession{}, playbackError(err)
	}
	out := sessionOut(session)
	switch {
	case !ready:
		out.status, out.RetryAfterMs = http.StatusAccepted, playback.RetryAfter.Milliseconds()
	case created:
		out.status = http.StatusCreated
	}
	return out, nil
}

func (s *Server) getPlayback(r *http.Request, _ None) (PlaybackSession, error) {
	c := s.playbackCaller(r)
	session, err := s.playback.Get(r.PathValue("id"), c.Viewer, c.SignInID)
	return sessionOut(session), playbackError(err)
}

func (s *Server) changePlayback(r *http.Request, req PlaybackChange) (PlaybackSession, error) {
	if req.State != "" && req.State != playback.StatePlaying && req.State != playback.StatePaused {
		return PlaybackSession{}, ErrField("state", "The state is playing or paused.")
	}
	change := playback.Change{State: req.State, Replan: req.Replan, PositionMs: req.PositionMs, Failure: req.Failure,
		Ask: playback.Ask{VersionID: req.VersionID, Quality: req.Quality, SubtitleOffsetMs: req.SubtitleOffsetMs,
			ThroughputKbps: req.ThroughputKbps}}
	if req.Audio != nil {
		change.Ask.Audio = &req.Audio.TrackID
	}
	var err error
	if change.Ask.Subtitle, change.Ask.SubtitleDelivery, err = subtitleAsk(req.Subtitles); err != nil {
		return PlaybackSession{}, err
	}
	if req.Seek != nil {
		position := max(req.Seek.PositionMs, 0)
		change.SeekMs, change.SeekID = &position, req.Seek.ID
	}
	session, err := s.playback.Change(r.Context(), r.PathValue("id"), s.playbackCaller(r), change)
	return sessionOut(session), playbackError(err)
}

// reportPlayback takes a device's latest position and answers how long the session now
// lives and when to report next. A session that is over answers 410; one this server does
// not know (it restarted) answers 404, and the app starts again where it was.
func (s *Server) reportPlayback(r *http.Request, req PlaybackProgress) (PlaybackLease, error) {
	c := s.playbackCaller(r)
	session, err := s.playback.Report(r.Context(), r.PathValue("id"), c.Viewer, c.SignInID,
		playback.Report{State: req.State, PositionMs: req.PositionMs, PlayedMs: req.PlayClockMs})
	if err != nil {
		return PlaybackLease{}, playbackError(err)
	}
	return PlaybackLease{ExpiresAt: session.LeaseUntil.UTC(), ReportEveryMs: session.ReportEvery().Milliseconds(),
		Generation: session.Generation}, nil
}

// stopPlayback is safe to repeat: stopping a session that is unknown or already over succeeds.
func (s *Server) stopPlayback(r *http.Request, req PlaybackStop) (None, error) {
	s.playback.Stop(r.Context(), r.PathValue("id"), CallerFrom(r.Context()).UserID, req.PositionMs)
	return None{}, nil
}

// accountPlayback lists what the account is playing on every device, so a person at their
// stream limit can see it and stop one.
func (s *Server) accountPlayback(r *http.Request, _ None) (AccountPlayback, error) {
	c := s.playbackCaller(r)
	sessions := s.playback.AccountSessions(c.Viewer.UserID)
	ids := make([]int64, len(sessions))
	for n, v := range sessions {
		ids[n] = v.Item.ID
	}
	titles, err := s.db.VisibleTitles(r.Context(), c.Viewer, ids)
	if err != nil {
		return AccountPlayback{}, err
	}
	out := AccountPlayback{Items: make([]AccountStream, 0, len(sessions))}
	for _, v := range sessions {
		stream := AccountStream{ID: v.ID, DeviceID: v.Device.PublicID, DeviceName: v.Device.Name, Kind: sessionKind(v.Item.Kind),
			State: v.State, PositionMs: v.PositionMs, StartedAt: v.StartedAt.UTC(), Current: v.SignInID == c.SignInID}
		if title, ok := titles[v.Item.ID]; ok {
			stream.ItemID, stream.Title, stream.DurationMs = v.Item.PublicID, title, v.Item.DurationMS
		}
		out.Items = append(out.Items, stream)
	}
	out.Limit, err = s.playback.AccountLimit(r.Context(), c.SignInID)
	return out, err
}

func (s *Server) getStreamLimits(r *http.Request, _ None) (StreamLimits, error) {
	limits, err := s.playback.Limits(r.Context())
	return StreamLimits(limits), err
}

func (s *Server) putStreamLimits(r *http.Request, req StreamLimits) (StreamLimits, error) {
	if err := s.playback.SetLimits(r.Context(), playback.Limits(req)); err != nil {
		return StreamLimits{}, playbackError(err)
	}
	return req, nil
}

func (s *Server) listPlaybackFailures(r *http.Request, _ None) (Page[RememberedFailure], error) {
	failures, err := s.db.AllRouteFailures(r.Context(), s.now())
	out := Page[RememberedFailure]{Items: make([]RememberedFailure, 0, len(failures))}
	for _, f := range failures {
		out.Items = append(out.Items, RememberedFailure{ID: f.PublicID, DeviceName: f.DeviceName, ItemID: f.ItemID, Title: f.ItemTitle,
			FileName: f.FileName, Player: f.Player, Route: f.Route, Code: f.Code, Detail: f.Detail, FailedAt: f.FailedAt.UTC(),
			ExpiresAt: f.ExpiresAt.UTC()})
	}
	return out, err
}

func (s *Server) forgetPlaybackFailure(r *http.Request, _ None) (None, error) {
	return None{}, s.db.ForgetRouteFailure(r.Context(), r.PathValue("id"))
}
