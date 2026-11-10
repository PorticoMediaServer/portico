package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync"

	"github.com/PorticoMediaServer/portico/server-next/internal/auth"
	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

// The owner's sign-in settings: how long people stay signed in, whether devices may remember
// PINs, how strict wrong guesses are, the shortest password, how long a reset code lasts,
// who must use two-step sign-in, and whether sign-in screens list remembered accounts.
// Only the owner changes them.

// SignInSettings is the owner's sign-in settings. See auth.Policy for what each field takes.
type SignInSettings struct {
	auth.Policy

	revision
}

// SignInSettingsEdit changes the sign-in settings. Fields left out stay as they are.
type SignInSettingsEdit struct {
	StaySignedInDays  *int    `json:"staySignedInDays,omitempty"`
	PINRememberDays   *int    `json:"pinRememberDays,omitempty"`
	PasswordAttempts  *string `json:"passwordAttempts,omitempty"`
	PINAttempts       *string `json:"pinAttempts,omitempty"`
	PasswordMinLength *int    `json:"passwordMinLength,omitempty"`
	ResetCodeMinutes  *int    `json:"resetCodeMinutes,omitempty"`
	RequireTwoStep    *string `json:"requireTwoStep,omitempty"`
	RememberAccounts  *bool   `json:"rememberAccounts,omitempty"`
	EndedSessionsDays *int    `json:"endedSessionsDays,omitempty"`
}

const signInSettingsKey = "sign_in"

// policyState is the settings as requests use them, kept in memory.
type policyState struct {
	mu    sync.RWMutex
	value auth.Policy
}

func (s *Server) policy() auth.Policy {
	s.signInPolicy.mu.RLock()
	defer s.signInPolicy.mu.RUnlock()
	return s.signInPolicy.value
}

func (s *Server) setPolicy(p auth.Policy) {
	s.signInPolicy.mu.Lock()
	s.signInPolicy.value = p
	s.signInPolicy.mu.Unlock()
}

func (s *Server) loadPolicy(ctx context.Context) error {
	policy := auth.DefaultPolicy()
	raw, _, err := s.db.SettingDocument(ctx, signInSettingsKey)
	if err != nil {
		return err
	}
	// A field a newer version added and this one's document lacks keeps its default.
	if raw != "" && (json.Unmarshal([]byte(raw), &policy) != nil || policy.Problem() != "") {
		return errors.New("the stored sign-in settings are damaged")
	}
	s.setPolicy(policy)
	return nil
}

func set[T any](to *T, from *T) {
	if from != nil {
		*to = *from
	}
}

func (s *Server) signInSettingsRoutes() {
	Handle(s, "GET /v1/admin/settings/sign-in", Owner, func(r *http.Request, _ None) (SignInSettings, error) {
		_, saved, err := s.db.SettingDocument(r.Context(), signInSettingsKey)
		return SignInSettings{s.policy(), revision(saved)}, err
	})
	Handle(s, "PATCH /v1/admin/settings/sign-in", Owner, func(r *http.Request, req SignInSettingsEdit) (SignInSettings, error) {
		p := s.policy()
		set(&p.StaySignedInDays, req.StaySignedInDays)
		set(&p.PINRememberDays, req.PINRememberDays)
		set(&p.PasswordAttempts, req.PasswordAttempts)
		set(&p.PINAttempts, req.PINAttempts)
		set(&p.PasswordMinLength, req.PasswordMinLength)
		set(&p.ResetCodeMinutes, req.ResetCodeMinutes)
		set(&p.RequireTwoStep, req.RequireTwoStep)
		set(&p.RememberAccounts, req.RememberAccounts)
		set(&p.EndedSessionsDays, req.EndedSessionsDays)
		if field := p.Problem(); field != "" {
			return SignInSettings{}, ErrField(field, "That is not one of this setting's choices.")
		}
		match, err := ifMatch(r)
		if err != nil {
			return SignInSettings{}, err
		}
		raw, _ := json.Marshal(p)
		saved, err := s.db.SaveSettingDocument(r.Context(), CallerFrom(r.Context()).UserID, signInSettingsKey, string(raw), match, s.now())
		if errors.Is(err, db.ErrStale) {
			return SignInSettings{}, errStale()
		}
		if err != nil {
			return SignInSettings{}, err
		}
		s.setPolicy(p)
		return SignInSettings{p, revision(saved)}, nil
	})
}
