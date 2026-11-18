package api

import (
	"encoding/json"
	"net/http"

	"github.com/PorticoMediaServer/portico/server-next/internal/preferences"
)

// passoutDue says "Still watching?" is due before the next entry: the profile has it on and
// as many entries as it names have played one after another with nobody touching the queue.
func (s *Server) passoutDue(r *http.Request, advances int) bool {
	if advances == 0 {
		return false
	}
	stored, err := s.db.Preferences(r.Context(), s.playbackCaller(r).Viewer.ProfileID, 0)
	if err != nil {
		return false
	}
	values := preferences.Defaults()
	for _, key := range []string{"playback.passoutProtection", "playback.passoutAfterEpisodes"} {
		field, _ := preferences.Find(key)
		var raw any
		if json.Unmarshal([]byte(stored[key]), &raw) == nil {
			if value, err := field.Check(raw); err == nil {
				values[key] = value
			}
		}
	}
	return values.Bool("playback.passoutProtection") && advances >= values.Int("playback.passoutAfterEpisodes")
}
