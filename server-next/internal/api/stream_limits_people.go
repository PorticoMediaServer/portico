package api

import (
	"encoding/json"
	"net/http"
)

// StreamLimitsPeopleEdit changes just the named accounts' stream limits. Null inherits the
// per-person default; zero is unlimited; a positive number is that account's own limit.
type StreamLimitsPeopleEdit struct {
	People map[string]*int `json:"people"`
}

func (s *Server) patchStreamLimitPeople(r *http.Request, req StreamLimitsPeopleEdit) (StreamLimits, error) {
	for _, n := range req.People {
		if n != nil && *n < 0 {
			return StreamLimits{}, ErrField("people", "A limit is a whole number, or 0 for none.")
		}
	}
	value, err := s.db.SetAccountStreamLimits(r.Context(), req.People, s.now())
	if err != nil {
		return StreamLimits{}, err
	}
	var out StreamLimits
	err = json.Unmarshal([]byte(value), &out)
	return out, err
}
