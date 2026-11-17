package api

import (
	"net/http"
)

func (s *Server) onNowHomeRow(r *http.Request) (HomeRow, error) {
	row := HomeRow{ID: "on_now", Title: "On now", Shape: "landscape", Items: []Item{}}
	page, err := s.liveNow(r, None{})
	if err != nil {
		return row, err
	}
	row.OnNow, row.NextCursor = page.Items, page.NextCursor
	return row, nil
}

func (s *Server) initialOnNowHomeRow(r *http.Request) (HomeRow, error) {
	allowed, err := s.db.ViewerLive(r.Context(), viewer(r))
	if err != nil {
		return HomeRow{}, err
	}
	if !allowed.LiveTV {
		return HomeRow{}, nil
	}
	request := r.Clone(r.Context())
	url := *r.URL
	url.RawQuery = ""
	request.URL = &url
	return s.onNowHomeRow(request)
}
