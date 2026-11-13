package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

// StreamLimitsEdit changes only the fields and accounts named in the request.
// A null account inherits the per-person default; zero is unlimited.
type StreamLimitsEdit struct {
	Total                 *int            `json:"total,omitempty"`
	PerPerson             *int            `json:"perPerson,omitempty"`
	People                map[string]*int `json:"people,omitempty"`
	EndPausedVideoAfterMs *int64          `json:"endPausedVideoAfterMs,omitempty"`
}

func (s *Server) patchStreamLimits(r *http.Request, req StreamLimitsEdit) (StreamLimits, error) {
	patch := map[string]any{}
	for field, n := range map[string]*int{"total": req.Total, "perPerson": req.PerPerson} {
		if n == nil {
			continue
		}
		if *n < 0 {
			return StreamLimits{}, ErrField(field, "A limit is a whole number, or 0 for none.")
		}
		patch[field] = *n
	}
	if n := req.EndPausedVideoAfterMs; n != nil {
		if *n < 0 {
			return StreamLimits{}, ErrField("endPausedVideoAfterMs", "A time is a whole number, or 0 for never.")
		}
		patch["endPausedVideoAfterMs"] = *n
	}
	for id, n := range req.People {
		if n != nil && *n < 0 {
			return StreamLimits{}, ErrField("people", "A limit is a whole number, or 0 for none.")
		}
		if _, err := s.db.Member(r.Context(), id); errors.Is(err, db.ErrNotFound) {
			return StreamLimits{}, ErrField("people", "Choose an account on this server.")
		} else if err != nil {
			return StreamLimits{}, err
		}
	}
	if len(req.People) != 0 {
		patch["people"] = req.People
	}
	value, err := s.db.PatchStreamLimits(r.Context(), patch, s.now())
	if err != nil {
		return StreamLimits{}, err
	}
	var out StreamLimits
	err = json.Unmarshal([]byte(value), &out)
	return out, err
}
