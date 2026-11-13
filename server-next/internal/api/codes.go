package api

import (
	"errors"
	"net/http"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/auth"
	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

// Signing in on a TV without typing a password there: the TV shows a code, someone signed
// in on a phone approves it, and the TV is signed in to their account and asks who is
// watching. It never has more rights than whoever approved it.

const (
	tvCodeLife     = 10 * time.Minute
	tvPollInterval = 5 * time.Second
)

// CodeStart is a TV asking for a code to show.
type CodeStart struct {
	Device DeviceInfo `json:"device"`
}

// CodeStarted is what the TV shows and keeps.
type CodeStarted struct {
	// Code is shown on screen for the person to type on their phone, such as "K7QP-2MXD".
	Code string `json:"code"`
	// DeviceSecret is kept by the TV and sent to POST /v1/auth/codes/{id}/redeem to collect the sign-in.
	DeviceSecret string    `json:"deviceSecret"`
	ExpiresAt    time.Time `json:"expiresAt"`
	// IntervalMs is how long to wait between asks.
	IntervalMs int64 `json:"intervalMs"`
}

// CodeReview is the device waiting behind a code, shown to the person before they approve it.
type CodeReview struct {
	Device    DeviceInfo `json:"device"`
	ExpiresAt time.Time  `json:"expiresAt"`
}

// CodeDecision approves the device behind a code, or with deny set, refuses it.
type CodeDecision struct {
	Deny bool `json:"deny,omitempty"`
}

// CodeCollect is the TV asking whether it has been signed in. The code in the path says
// which request; the secret is what proves the asker is the TV that made it.
type CodeCollect struct {
	DeviceSecret string `json:"deviceSecret"`
}

// CodeResult is "pending" with the wait before asking again, or "approved" with the sign-in.
type CodeResult struct {
	Status     string          `json:"status"`
	IntervalMs int64           `json:"intervalMs,omitempty"`
	SignIn     *SignInResponse `json:"signIn,omitempty"`
}

func errNoCode() *Error {
	return refusal(http.StatusNotFound, "code_not_found", "That code is not right, or it has expired.", false)
}

func (s *Server) codeRoutes() {
	Handle(s, "POST /v1/auth/codes", Public, s.startCode)
	// Approving a sign-in is for a signed-in person: a key never mints a session.
	Handle(s, "GET /v1/auth/codes/{id}", WithProfile, s.reviewCode).Scope = ScopeSession
	Handle(s, "POST /v1/auth/codes/{id}/approve", WithProfile, s.decideCode).Scope = ScopeSession
	Handle(s, "POST /v1/auth/codes/{id}/redeem", Public, s.collectCode)
}

func tvCodeHash(typed string) string { return auth.Hash("tv|" + auth.CodeKey(typed)) }

func (s *Server) startCode(r *http.Request, req CodeStart) (CodeStarted, error) {
	device, err := checkDevice(req.Device)
	if err != nil {
		return CodeStarted{}, err
	}
	now := s.now()
	out := CodeStarted{Code: auth.NewCode(2), DeviceSecret: auth.NewSecret(), ExpiresAt: now.Add(tvCodeLife).UTC(), IntervalMs: tvPollInterval.Milliseconds()}
	err = s.db.StartTVSignIn(r.Context(), db.TVRequest{CodeHash: tvCodeHash(out.Code), PollHash: auth.Hash(out.DeviceSecret),
		Device: device, ExpiresAt: out.ExpiresAt}, now)
	return out, err
}

// wrongCode counts a code nobody issued and answers that it was not found.
func (s *Server) wrongCode(r *http.Request, limits []db.Limit) error {
	if err := s.db.RecordFailure(r.Context(), s.now(), limits...); err != nil {
		return err
	}
	return errNoCode()
}

func (s *Server) reviewCode(r *http.Request, _ None) (CodeReview, error) {
	limits := s.sourceLimit(r)
	if wait, err := s.db.LimitWait(r.Context(), s.now(), limits...); err != nil {
		return CodeReview{}, err
	} else if wait > 0 {
		return CodeReview{}, errTooManyAttempts(wait)
	}
	device, expires, err := s.db.TVSignIn(r.Context(), tvCodeHash(r.PathValue("id")), s.now())
	if errors.Is(err, db.ErrNotFound) {
		return CodeReview{}, s.wrongCode(r, limits)
	}
	return CodeReview{ExpiresAt: expires.UTC(), Device: DeviceInfo{InstallationID: device.InstallationID, Name: device.Name,
		Platform: device.Platform, App: device.App, AppVersion: device.AppVersion}}, err
}

func (s *Server) decideCode(r *http.Request, req CodeDecision) (None, error) {
	caller, limits := CallerFrom(r.Context()), s.sourceLimit(r)
	if wait, err := s.db.LimitWait(r.Context(), s.now(), limits...); err != nil {
		return None{}, err
	} else if wait > 0 {
		return None{}, errTooManyAttempts(wait)
	}
	// Only the main profile carries the account's role, so only an approval from there
	// lets the TV have it too.
	err := s.db.DecideTVSignIn(r.Context(), tvCodeHash(r.PathValue("id")), caller.UserID, !caller.Manage, !req.Deny, s.now())
	if errors.Is(err, db.ErrNotFound) {
		return None{}, s.wrongCode(r, limits)
	}
	return None{}, err
}

func (s *Server) collectCode(r *http.Request, req CodeCollect) (CodeResult, error) {
	ctx, now := r.Context(), s.now()
	session, refresh := s.mintSession(r, now)
	st, err := s.db.CollectTVSignIn(ctx, auth.Hash(req.DeviceSecret), session, now)
	switch {
	case errors.Is(err, db.ErrPending):
		return CodeResult{Status: "pending", IntervalMs: tvPollInterval.Milliseconds()}, nil
	case errors.Is(err, db.ErrDenied):
		return CodeResult{}, refusal(http.StatusForbidden, "sign_in_denied", "This sign-in was not approved.", false)
	case errors.Is(err, db.ErrNotFound), errors.Is(err, db.ErrBadCredentials):
		return CodeResult{}, refusal(http.StatusGone, "code_expired", "This code has expired. Start again.", false)
	case err != nil:
		return CodeResult{}, err
	}
	signIn, err := s.signedIn(ctx, st, now, refresh)
	return CodeResult{Status: "approved", SignIn: &signIn}, err
}
