package api

import (
	"errors"
	"net/http"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

// The deterministic spotlight policy is the five newest eligible films/shows. Continue
// Watching supplies the resume target when it has one; nothing is labelled Recommended.
// Only these five picks receive detail metadata, using the ordinary visibility/artwork path.
func (s *Server) homeSpotlight(r *http.Request, continuing []db.Item) ([]HomeSpotlight, error) {
	picks, err := s.db.Spotlight(r.Context(), viewer(r))
	if err != nil {
		return nil, err
	}
	out := make([]HomeSpotlight, 0, len(picks))
	for _, pick := range picks {
		detail, err := s.db.Item(r.Context(), viewer(r), pick.PublicID)
		if errors.Is(err, db.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if !detail.Available || detail.State.Hidden {
			continue
		}
		entry := detailShape(detail)
		// Home needs the short synopsis and genre facts, not versions, credits or external ids.
		entry.Versions, entry.Credits, entry.ExternalIDs = nil, nil, nil
		if utf8.RuneCountInString(entry.Summary) > 300 {
			entry.Summary = string([]rune(entry.Summary)[:300])
		}
		slide := HomeSpotlight{Entry: entry, Play: true, MyList: true, Reason: HomeSpotlightReason{Kind: "recent", Text: HomeSpotlightText{
			Code: "home.row.recentlyAdded", Fallback: "Recently added"}}}
		if pick.Kind == db.KindShow {
			targetID := ""
			for _, it := range continuing {
				if it.RootID == pick.ID && it.Kind == db.KindEpisode {
					targetID = it.PublicID
					break
				}
			}
			if targetID == "" {
				targetID, err = s.db.SpotlightEpisode(r.Context(), viewer(r), pick.ID)
				if err != nil {
					return nil, err
				}
			}
			if targetID == "" {
				continue
			}
			target, err := s.db.Item(r.Context(), viewer(r), targetID)
			if errors.Is(err, db.ErrNotFound) {
				continue
			}
			if err != nil {
				return nil, err
			}
			if !target.Available {
				continue
			}
			shaped := itemShape(target.Item)
			available := true
			shaped.Available = &available
			slide.ResumeTarget = &shaped
		}
		out = append(out, slide)
	}
	return out, nil
}
