package api

import (
	"errors"
	"net/http"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

func (s *Server) currentRules(r *http.Request, lv db.LiveViewer, all bool) ([]db.RecordingRule, error) {
	profile := viewer(r).ProfileID
	if all {
		profile = 0
	}
	rules, err := s.db.Rules(r.Context(), profile)
	if err != nil || all {
		return rules, err
	} // owner Settings manages every rule
	out := []db.RecordingRule{}
	for _, rule := range rules {
		ok, err := s.personalRuleVisible(r, lv, rule)
		if err != nil {
			return nil, err
		}
		if ok {
			out = append(out, rule)
		}
	}
	return out, nil
}

func (s *Server) personalRuleVisible(r *http.Request, lv db.LiveViewer, rule db.RecordingRule) (bool, error) {
	if rule.ProfileID != viewer(r).ProfileID {
		return false, nil
	}
	source, err := s.db.LiveSourceByID(r.Context(), rule.SourceID)
	if errors.Is(err, db.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if source.Paused || source.Audience != "everyone" && !(source.Audience == "owner" && lv.Owner) {
		return false, nil
	}
	return s.db.RuleRatingVisible(r.Context(), rule, lv.MaxAge, s.now())
}
