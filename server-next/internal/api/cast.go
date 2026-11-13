package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/auth"
	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/receiver"
)

// Google Cast. By the owner's ruling a server signed in with local accounts has no Google
// Cast at all: these routes are not found there, and nothing of Cast touches a Portico
// address. On a server that uses Portico Accounts, a phone casting to a Chromecast makes a
// pairing code for its profile and hands it to the receiver over Google's own channel; the
// receiver trades it for a sign-in that can play what that profile may see and nothing else.
// AirPlay is the system's own picker and needs nothing here.

const castCodeLife = 5 * time.Minute

// roleCast is the role in the token of a Cast receiver's sign-in, which can only play.
const roleCast = "cast"

// signInKindPortico is the value of the settings row sign_in_kind on a server that uses
// Portico Accounts. Absent means local accounts.
const signInKindPortico = "portico_account"

// CastConfig is the answer of GET /v1/cast/config.
type CastConfig struct {
	ApplicationID string `json:"applicationId"`
}

// CastCodeRequest names the caller's own playback when it is moving to the TV.
type CastCodeRequest struct {
	SourceSessionID string `json:"sourceSessionId,omitempty"`
}

// CastCode is a pairing code for the receiver the phone is casting to.
type CastCode struct {
	Code      string    `json:"code"`
	ExpiresAt time.Time `json:"expiresAt"`
}

// CastRedeem is the receiver trading its code for a sign-in. Device is the receiver itself,
// with an installation id the receiver page made and keeps.
type CastRedeem struct {
	Code   string     `json:"code"`
	Device DeviceInfo `json:"device"`
}

func castCodeHash(typed string) string { return auth.Hash("cast|" + auth.CodeKey(typed)) }

// castOffered says whether this server offers Google Cast: only with Portico Accounts.
func (s *Server) castOffered(ctx context.Context) (bool, error) {
	kind, _, err := s.db.Setting(ctx, "sign_in_kind")
	return kind == signInKindPortico, err
}

func (s *Server) castRoutes() {
	s.mux.Handle("GET /receiver/cast/", receiver.Handler())
	Handle(s, "GET /v1/cast/config", WithProfile, func(r *http.Request, _ None) (CastConfig, error) {
		offered, err := s.castOffered(r.Context())
		if err != nil {
			return CastConfig{}, err
		}
		if !offered {
			return CastConfig{}, ErrNotFound()
		}
		return CastConfig{ApplicationID: receiver.ApplicationID}, nil
	}).Scope = ScopeSession
	Handle(s, "POST /v1/cast/codes", WithProfile, func(r *http.Request, req CastCodeRequest) (CastCode, error) {
		ctx, now := r.Context(), s.now()
		if _, err := s.castApplication(ctx); err != nil {
			return CastCode{}, err
		}
		c := CallerFrom(ctx)
		out := CastCode{Code: auth.NewCode(2), ExpiresAt: now.Add(castCodeLife).UTC()}
		var move castMove
		if req.SourceSessionID != "" {
			source, err := s.playback.Get(req.SourceSessionID, viewer(r), c.SessionID)
			if err != nil {
				return CastCode{}, refuse(err)
			}
			move = castMove{source: source.ID, item: source.Item.PublicID, until: out.ExpiresAt}
		}
		err := s.db.StartCast(ctx, castCodeHash(out.Code), c.UserID, c.ProfileID, out.ExpiresAt, now)
		if err == nil && move.source != "" {
			s.castRemember(castCodeHash(out.Code), move)
		}
		return out, err
	}).Scope = ScopeSession
	Handle(s, "POST /v1/cast/redeem", Public, s.redeemCast)
}

// castApplication is the Cast application id, or not found where there is no Cast.
func (s *Server) castApplication(ctx context.Context) (string, error) {
	offered, err := s.castOffered(ctx)
	if err != nil {
		return "", err
	}
	if !offered {
		return "", ErrNotFound()
	}
	if receiver.ApplicationID == "" {
		return "", ErrNotFound()
	}
	return receiver.ApplicationID, nil
}

func (s *Server) redeemCast(r *http.Request, req CastRedeem) (SignInResponse, error) {
	ctx, now := r.Context(), s.now()
	if _, err := s.castApplication(ctx); err != nil {
		return SignInResponse{}, err
	}
	limits := s.sourceLimit(r)
	if wait, err := s.db.LimitWait(ctx, now, limits...); err != nil {
		return SignInResponse{}, err
	} else if wait > 0 {
		return SignInResponse{}, errTooManyAttempts(wait)
	}
	session, refresh, err := s.newSession(r, req.Device, now)
	if err != nil {
		return SignInResponse{}, err
	}
	st, err := s.db.RedeemCast(ctx, castCodeHash(req.Code), session, now)
	if errors.Is(err, db.ErrBadCredentials) {
		return SignInResponse{}, s.wrongCode(r, limits)
	}
	if err != nil {
		return SignInResponse{}, err
	}
	s.castRedeemed(castCodeHash(req.Code), st.ID)
	// Only the sign-in: the receiver is told nothing of the account or its other profiles.
	return SignInResponse{Session: s.session(st, now, refresh)}, nil
}

// castRoutes is everything a Cast receiver's sign-in may call: read the title it was sent,
// play it, report on its own session, publish its own capabilities and run its own queue.
// Everything else is refused, and so is any of these on another device's session, queue or
// capabilities. Signing out (DELETE /v1/auth/session) needs no sign-in check and stays open.
var castRoutes = map[string]bool{
	"GET /v1/items/{id}":                          true,
	"GET /v1/items/{id}/playback-options":         true,
	"POST /v1/playback/sessions":                  true,
	"GET /v1/playback/sessions/{id}":              true,
	"PATCH /v1/playback/sessions/{id}":            true,
	"PUT /v1/playback/sessions/{id}/progress":     true,
	"DELETE /v1/playback/sessions/{id}":           true,
	"GET /v1/devices/{id}/capabilities":           true,
	"PUT /v1/devices/{id}/capabilities":           true,
	"POST /v1/queues":                             true,
	"GET /v1/queues":                              true,
	"GET /v1/queues/{id}":                         true,
	"PATCH /v1/queues/{id}":                       true,
	"DELETE /v1/queues/{id}":                      true,
	"GET /v1/queues/{id}/entries":                 true,
	"POST /v1/queues/{id}/entries":                true,
	"DELETE /v1/queues/{id}/entries/{entryId}":    true,
	"POST /v1/queues/{id}/entries/{entryId}/move": true,
	"POST /v1/queues/{id}/advance":                true,
	"POST /v1/queues/{id}/prepare-next":           true,
	"POST /v1/queues/{id}/commit-next":            true,
}

// castMay says whether a Cast receiver may make this request: a route on the list, and only
// on its own session, its own queue and its own capabilities.
func (s *Server) castMay(r *http.Request, pattern string, c Caller) (bool, error) {
	if !castRoutes[pattern] {
		return false, nil
	}
	id := r.PathValue("id")
	_, path, _ := strings.Cut(pattern, " ")
	switch {
	case strings.HasPrefix(path, "/v1/devices/"):
		return id == "current", nil
	case strings.HasPrefix(path, "/v1/playback/sessions/"):
		_, err := s.playback.Get(id, db.Viewer{UserID: c.UserID, ProfileID: c.ProfileID}, c.SessionID)
		if err != nil && pattern == "DELETE /v1/playback/sessions/{id}" {
			// A repeated stop after the ended session was swept is still successful.
			// An existing session on another device stays forbidden.
			for _, other := range s.playback.AccountSessions(c.UserID) {
				if other.ID == id {
					return false, nil
				}
			}
			return true, nil
		}
		return err == nil, nil
	case strings.HasPrefix(path, "/v1/queues/"):
		return s.ownQueue(r.Context(), c, id)
	}
	return true, nil
}

// ownQueue says whether a queue is one of the caller's own device's.
func (s *Server) ownQueue(ctx context.Context, c Caller, id string) (bool, error) {
	q, err := s.db.Queue(ctx, db.Viewer{UserID: c.UserID, ProfileID: c.ProfileID}, id)
	if errors.Is(err, db.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	device, err := s.db.SessionDevice(ctx, c.SessionID)
	return err == nil && device.ID == q.DeviceID, err
}
