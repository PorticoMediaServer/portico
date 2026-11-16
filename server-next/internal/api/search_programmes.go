package api

import (
	"net/http"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

func (s *Server) searchChannelProgrammes(r *http.Request, lv db.LiveViewer, text string, matches []db.LiveChannelMatch) (
	[]db.LiveChannelMatch, map[int64]*LiveProgramme, bool, error) {
	matched := map[int64]*LiveProgramme{}
	known := map[int64]bool{}
	for _, m := range matches {
		known[m.ID] = true
	}
	at := liveCursor{}
	now := s.now()
	for len(matches) <= liveSearchMost {
		channels, next, err := s.visibleChannels(r.Context(), viewer(r), lv, db.LiveChannelQuery{}, at, 100)
		if err != nil {
			return nil, nil, false, err
		}
		programmes, err := s.programmes(r.Context(), viewer(r), lv, channels, now, time.UnixMilli(1<<63-1), 2)
		if err != nil {
			return nil, nil, false, err
		}
		var titles []string
		for _, rows := range programmes {
			future := false
			for _, p := range rows {
				name := p.Title + " " + p.EpisodeTitle
				if p.NotAvailable || p.Slate || p.StartsAt.After(now) && future {
					name = ""
				}
				future = future || p.StartsAt.After(now)
				titles = append(titles, name)
			}
		}
		found, err := db.MatchingLiveTitles(text, titles)
		if err != nil {
			return nil, nil, false, searchRefusal(err)
		}
		place := 0
		for n, rows := range programmes {
			channel := channels[n]
			for k := range rows {
				ok := found[place]
				place++
				if !ok || matched[channel.ID] != nil {
					continue
				}
				matched[channel.ID] = &rows[k]
				if !known[channel.ID] {
					matches = append(matches, db.LiveChannelMatch{LiveChannel: channel, Tier: 3})
					known[channel.ID] = true
				}
			}
		}
		if len(matches) > liveSearchMost {
			return matches[:liveSearchMost], matched, true, nil
		}
		if next == nil {
			return matches, matched, false, nil
		}
		at = *next
	}
	return matches[:liveSearchMost], matched, true, nil
}
