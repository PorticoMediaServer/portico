package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/playback"
	"github.com/PorticoMediaServer/portico/server-next/internal/receivers"
)

// Playing on another device. A device that accepts playback from the account's other devices
// says so with PUT /v1/receivers/current and is listed for them. Another device can ask it to
// play something (moving what it plays there) or to let it control what it plays. The asked
// device hears of it on its change feed and answers; its person is asked once per asking
// device, and the device remembers that answer itself. A device that moves playback keeps
// playing until the other reports playing, and the server then stops it.

// playFromOtherDevices is the setting, on each device, that lets the account's other devices
// play on it and control it. Off refuses them all.
const playFromOtherDevices = "devices.playFromOtherDevices"

// ReceiverOpen is the answer of PUT /v1/receivers/current.
type ReceiverOpen struct {
	ID string `json:"id"`
	// RenewEveryMs is how often to send it again. A device that stops is no longer listed
	// two minutes later.
	RenewEveryMs int64 `json:"renewEveryMs"`
}

// ReceiverItem is one of the account's devices that accept playback.
type ReceiverItem struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Platform string `json:"platform,omitempty"`
	// Current is the device asking.
	Current bool `json:"current"`
	// Allowed says the device asking may already control this one without asking first.
	Allowed bool `json:"allowed"`
	// Playing is what the device plays now, if anything.
	Playing *AccountStream `json:"playing,omitempty"`
}

// ReceiverAsk is the body of POST /v1/receivers/{id}/requests: play (with Play) or control.
type ReceiverAsk struct {
	Kind string          `json:"kind"`
	Play *receivers.Play `json:"play,omitempty"`
	// SessionID, with play, is the asking device's own session: it keeps playing until the
	// other device reports playing, and then stops.
	SessionID string `json:"sessionId,omitempty"`
}

// ReceiverAnswer is the body of PATCH /v1/receivers/current/requests/{requestId}: allowed,
// declined, playing (with the session now playing it) or failed.
type ReceiverAnswer struct {
	Answer    string `json:"answer"`
	SessionID string `json:"sessionId,omitempty"`
}

// RequestFrom is the device that asked. SignInID changes when that device signs in again, so a
// device that remembers whom it allowed names them by it.
type RequestFrom struct {
	DeviceID   string `json:"deviceId"`
	DeviceName string `json:"deviceName"`
	SignInID   string `json:"signInId"`
}

// ReceiverRequest is one request to a device.
type ReceiverRequest struct {
	ID         string      `json:"id"`
	ReceiverID string      `json:"receiverId"`
	Kind       string      `json:"kind"`
	From       RequestFrom `json:"from"`
	// State is asking, allowed, playing, declined, failed or cancelled.
	State string `json:"state"`
	// Failed says why: timed_out (not playing within two minutes), couldnt_start, or
	// not_accepting (the device stopped accepting playback).
	Failed string          `json:"failed,omitempty"`
	Play   *receivers.Play `json:"play,omitempty"`
	// MovingSessionID is the session that stops when the device plays; SessionID is the
	// device's own session once it does.
	MovingSessionID string    `json:"movingSessionId,omitempty"`
	SessionID       string    `json:"sessionId,omitempty"`
	CreatedAt       time.Time `json:"createdAt"`
	// ExpiresAt is when a request not yet playing has failed.
	ExpiresAt time.Time `json:"expiresAt"`

	status int
}

func (r ReceiverRequest) reply(http.Header) int { return r.status }

func requestOut(r receivers.Request) ReceiverRequest {
	return ReceiverRequest{ID: r.ID, ReceiverID: r.To.PublicID, Kind: r.Kind, State: r.State, Failed: r.Failed, Play: r.Play,
		From:            RequestFrom{DeviceID: r.From.PublicID, DeviceName: r.From.Name, SignInID: r.From.SignInPublicID},
		MovingSessionID: r.Moving, SessionID: r.Playing, CreatedAt: r.CreatedAt.UTC(), ExpiresAt: r.CreatedAt.Add(receivers.Timeout).UTC()}
}

func receiverError(err error) error {
	switch {
	case errors.Is(err, receivers.ErrNotAccepting), errors.Is(err, db.ErrNotFound):
		return ErrNotFound()
	case errors.Is(err, receivers.ErrAnswered):
		return ErrConflict("request_over", "This request was already answered, cancelled or ran out of time.")
	case errors.Is(err, receivers.ErrBusy):
		return ErrTooMany("receiver_busy", "This device is being asked too many things at once. Try again shortly.", 10*time.Second)
	case errors.Is(err, receivers.ErrKeyReused):
		return errKeyReused()
	}
	return err
}

func (s *Server) receiverRoutes() {
	// A device is the thing that plays, so these take a signed-in person and not a key.
	for _, route := range []*Route{
		Handle(s, "GET /v1/receivers", WithProfile, s.listReceivers),
		Handle(s, "PUT /v1/receivers/current", WithProfile, s.openReceiver),
		Handle(s, "DELETE /v1/receivers/current", WithProfile, func(r *http.Request, _ None) (None, error) {
			s.receivers.Close(CallerFrom(r.Context()).SessionID)
			return None{}, nil
		}),
		Handle(s, "POST /v1/receivers/{id}/requests", WithProfile, s.askReceiver),
		Handle(s, "GET /v1/receivers/{id}/requests/{requestId}", WithProfile, func(r *http.Request, _ None) (ReceiverRequest, error) {
			req, err := s.receivers.Get(CallerFrom(r.Context()).SessionID, r.PathValue("requestId"))
			if err == nil && req.To.PublicID != r.PathValue("id") && r.PathValue("id") != "current" {
				err = db.ErrNotFound
			}
			return requestOut(req), receiverError(err)
		}),
		Handle(s, "DELETE /v1/receivers/{id}/requests/{requestId}", WithProfile, func(r *http.Request, _ None) (ReceiverRequest, error) {
			req, err := s.receivers.Cancel(CallerFrom(r.Context()).SessionID, r.PathValue("requestId"))
			return requestOut(req), receiverError(err)
		}),
		Handle(s, "GET /v1/receivers/current/requests", WithProfile, func(r *http.Request, _ None) (Page[ReceiverRequest], error) {
			out := Page[ReceiverRequest]{Items: []ReceiverRequest{}}
			for _, req := range s.receivers.Pending(CallerFrom(r.Context()).SessionID) {
				out.Items = append(out.Items, requestOut(req))
			}
			return out, nil
		}),
		Handle(s, "PATCH /v1/receivers/current/requests/{requestId}", WithProfile, s.answerRequest),
	} {
		route.Scope = ScopeSession
	}
}

// sweepMemory fails requests a device did not start in time, so the device that asked hears
// so even when nobody reads it, until ctx ends.
func (s *Server) sweepMemory(ctx context.Context) {
	tick := time.NewTicker(5 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			s.receivers.Sweep()
		}
	}
}

// signedInDevice is the caller's device as the receivers know it.
func (s *Server) signedInDevice(ctx context.Context) (receivers.Device, error) {
	c := CallerFrom(ctx)
	if c.SessionID == 0 {
		return receivers.Device{}, ErrUnauthorized()
	}
	d, err := s.db.SessionDevice(ctx, c.SessionID)
	if errors.Is(err, db.ErrNotFound) {
		return receivers.Device{}, ErrUnauthorized()
	}
	return receivers.Device{SignInID: c.SessionID, UserID: c.UserID, ProfileID: c.ProfileID, DeviceID: d.ID, PublicID: d.PublicID,
		Name: d.Name, Platform: d.Platform, SignInPublicID: d.SignInPublicID}, err
}

// acceptsPlayback reads the device's "Play from other devices" for the profile on it.
func (s *Server) acceptsPlayback(ctx context.Context, profileID, deviceID int64) (bool, error) {
	stored, err := s.db.StoredPreferences(ctx, profileID, "", deviceID)
	for _, p := range stored {
		if p.Device && p.Key == playFromOtherDevices {
			return p.Value != "false", err
		}
	}
	return true, err
}

func (s *Server) openReceiver(r *http.Request, _ None) (ReceiverOpen, error) {
	d, err := s.signedInDevice(r.Context())
	if err != nil {
		return ReceiverOpen{}, err
	}
	on, err := s.acceptsPlayback(r.Context(), d.ProfileID, d.DeviceID)
	switch {
	case err != nil:
		return ReceiverOpen{}, err
	case !on:
		s.receivers.Close(d.SignInID)
		return ReceiverOpen{}, ErrConflict("play_from_other_devices_off", "Play from other devices is turned off on this device.")
	}
	s.receivers.Open(d)
	return ReceiverOpen{ID: d.PublicID, RenewEveryMs: receivers.RenewEvery.Milliseconds()}, nil
}

// listReceivers is Play on's list of the account's devices, each with what it plays. A title
// the asking profile may not see is listed without its name.
func (s *Server) listReceivers(r *http.Request, _ None) (Page[ReceiverItem], error) {
	c := CallerFrom(r.Context())
	list := s.receivers.List(c.UserID)
	sessions := s.playback.AccountSessions(c.UserID)
	ids := make([]int64, len(sessions))
	for n, v := range sessions {
		ids[n] = v.Item.ID
	}
	titles, err := s.db.VisibleTitles(r.Context(), viewer(r), ids)
	if err != nil {
		return Page[ReceiverItem]{}, err
	}
	out := Page[ReceiverItem]{Items: make([]ReceiverItem, 0, len(list))}
	for _, rc := range list {
		item := ReceiverItem{ID: rc.PublicID, Name: rc.Name, Platform: rc.Platform, Current: rc.SignInID == c.SessionID}
		for _, allowed := range rc.Allowed {
			item.Allowed = item.Allowed || allowed == c.SessionID
		}
		for _, v := range sessions {
			if v.SignInID == rc.SignInID {
				stream := streamOut(v, titles, c.SessionID)
				item.Playing = &stream
			}
		}
		out.Items = append(out.Items, item)
	}
	return out, nil
}

func (s *Server) askReceiver(r *http.Request, req ReceiverAsk) (ReceiverRequest, error) {
	ctx := r.Context()
	key, err := idempotencyKey(r)
	if err != nil {
		return ReceiverRequest{}, err
	}
	from, err := s.signedInDevice(ctx)
	if err != nil {
		return ReceiverRequest{}, err
	}
	c := s.playbackCaller(r)
	switch req.Kind {
	case receivers.KindControl:
		req.Play, req.SessionID = nil, ""
	case receivers.KindPlay:
		if req.Play == nil || req.Play.ItemID == "" {
			return ReceiverRequest{}, ErrField("play.itemId", "Say which title to play.")
		}
		// Nobody can make another device play what they may not see themselves, nor ask a
		// device for what the profile on it may not see: both are not found.
		ids, err := s.db.PlayableItems(ctx, c.Viewer, []string{req.Play.ItemID})
		if err == nil && len(ids) != 1 {
			err = db.ErrNotFound
		}
		if to, open := s.receivers.Find(c.Viewer.UserID, r.PathValue("id")); err == nil && open {
			there := db.Viewer{UserID: to.UserID, ProfileID: to.ProfileID}
			if ids, err = s.db.PlayableItems(ctx, there, []string{req.Play.ItemID}); err == nil && len(ids) != 1 {
				err = db.ErrNotFound
			}
		}
		if err != nil {
			return ReceiverRequest{}, receiverError(err)
		}
		req.Play.PositionMs = max(req.Play.PositionMs, 0)
		if req.Play.QueueID != "" {
			if _, err := s.db.Queue(ctx, c.Viewer, req.Play.QueueID); err != nil {
				return ReceiverRequest{}, ErrField("play.queueId", "This is not one of this device's queues.")
			}
		}
		if req.SessionID != "" {
			if _, err := s.playback.Get(req.SessionID, c.Viewer, c.SignInID); err != nil {
				return ReceiverRequest{}, ErrField("sessionId", "This is not what this device is playing.")
			}
		}
	default:
		return ReceiverRequest{}, ErrField("kind", "The kind is play or control.")
	}
	made, created, err := s.receivers.Ask(from, r.PathValue("id"), req.Kind, req.Play, req.SessionID, key, requestHash(req))
	out := requestOut(made)
	if created {
		out.status = http.StatusCreated
	}
	return out, receiverError(err)
}

// answerRequest takes the asked device's answer. Playing must name the device's own session,
// playing what was asked; the device that asked then stops.
func (s *Server) answerRequest(r *http.Request, req ReceiverAnswer) (ReceiverRequest, error) {
	ctx := r.Context()
	c := s.playbackCaller(r)
	switch req.Answer {
	case receivers.StateAllowed, receivers.StateDeclined, receivers.StateFailed:
	case receivers.StatePlaying:
		asked, err := s.receivers.Get(c.SignInID, r.PathValue("requestId"))
		if err != nil {
			return ReceiverRequest{}, receiverError(err)
		}
		session, err := s.playback.Get(req.SessionID, c.Viewer, c.SignInID)
		if err != nil || asked.Play == nil || session.State == playback.StateEnded || session.Item.PublicID != asked.Play.ItemID {
			return ReceiverRequest{}, ErrField("sessionId", "Name this device's session that plays what was asked.")
		}
	default:
		return ReceiverRequest{}, ErrField("answer", "The answer is allowed, declined, playing or failed.")
	}
	answered, err := s.receivers.Answer(c.SignInID, r.PathValue("requestId"), req.Answer, req.SessionID)
	if err != nil {
		return requestOut(answered), receiverError(err)
	}
	if answered.State == receivers.StatePlaying && answered.Moving != "" {
		// Moved: the device that asked stops, and its app hears so.
		s.playback.Stop(ctx, answered.Moving, c.Viewer.UserID, c.SignInID, nil)
	}
	return requestOut(answered), nil
}

// remoteChange is a PATCH of a session another of the account's devices plays: a command for
// that device, which does it itself. The device must have allowed the caller.
func (s *Server) remoteChange(r *http.Request, id string, req PlaybackChange) (PlaybackSession, error) {
	c := CallerFrom(r.Context())
	target, err := s.playback.Session(id, c.UserID)
	switch {
	case err != nil:
		return PlaybackSession{}, playbackError(err)
	case target.State == playback.StateEnded:
		return PlaybackSession{}, playbackError(playback.ErrEnded)
	case !s.receivers.Allows(target.SignInID, c.SessionID):
		return PlaybackSession{}, refusal(http.StatusForbidden, "control_not_allowed",
			"That device hasn't allowed this one to control it. Ask it from Play on.", false)
	case req.VersionID != "" || req.Quality != nil || req.SubtitleOffsetMs != nil || req.PositionMs != nil || req.Replan ||
		req.Failure != nil || req.ThroughputKbps != 0:
		return PlaybackSession{}, ErrInvalid("Only play, pause, seek, skip, audio and subtitles can be changed from another device.")
	}
	from, err := s.db.SessionDevice(r.Context(), c.SessionID)
	if err != nil {
		return PlaybackSession{}, playbackError(err)
	}
	cmd := playback.Command{State: req.State, Skip: req.Skip, From: from.PublicID}
	if req.Seek != nil {
		position := max(req.Seek.PositionMs, 0)
		cmd.SeekMs = &position
	}
	if req.Audio != nil {
		cmd.AudioID = &req.Audio.TrackID
	}
	if req.Subtitles != nil {
		off := ""
		cmd.SubtitleID = &off
		if req.Subtitles.TrackID != nil {
			cmd.SubtitleID = req.Subtitles.TrackID
		}
	}
	target, err = s.playback.SendCommand(id, c.UserID, cmd, r.Header.Get("Idempotency-Key"))
	if err != nil {
		return PlaybackSession{}, playbackError(err)
	}
	// The controlling device sees the state and the command, not the stream's address, and
	// not the title if its profile may not see it.
	out := PlaybackSession{ID: target.ID, Kind: sessionKind(target.Item.Kind), State: target.State, Command: target.Command,
		Presentation: Presentation{Subtitles: []playback.SubtitleFile{}}}
	if ok, err := s.db.Visible(r.Context(), viewer(r), target.Item.ID); err != nil {
		return PlaybackSession{}, err
	} else if ok {
		out.ItemID = target.Item.PublicID
	}
	return out, nil
}
