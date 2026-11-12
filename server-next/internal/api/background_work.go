package api

import (
	"net/http"
	"time"
)

type BackgroundWorkState struct {
	PausedUntil *time.Time `json:"pausedUntil,omitempty"`
}

func (s *Server) backgroundWorkRoutes() {
	state := func() BackgroundWorkState {
		if until := s.settings.BackgroundPausedUntil(s.now()); !until.IsZero() {
			u := until.UTC()
			return BackgroundWorkState{PausedUntil: &u}
		}
		return BackgroundWorkState{}
	}
	Handle(s, "GET /v1/admin/background/pause", Owner, func(_ *http.Request, _ None) (BackgroundWorkState, error) { return state(), nil })
	Handle(s, "PUT /v1/admin/background/pause", Owner, func(_ *http.Request, _ None) (BackgroundWorkState, error) {
		s.settings.PauseBackground(s.now(), true)
		return state(), nil
	})
	Handle(s, "DELETE /v1/admin/background/pause", Owner, func(_ *http.Request, _ None) (BackgroundWorkState, error) {
		s.settings.PauseBackground(s.now(), false)
		return state(), nil
	})
}
