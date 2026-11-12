package api

import (
	"cmp"
	"net/http"
	"slices"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

// liveSearchKind is the search group of Live TV channels, shown last and only to a profile
// allowed Live TV (browse rules 30, 32 and 33). To any other profile kind=channel is an
// unknown kind, as if there were no Live TV.
const liveSearchKind = "channel"

// liveSearchMost is the most matching channels one search reads, as searchCheckCap is for
// titles: a count that stops there is marked as a minimum.
const liveSearchMost = 2000

// liveSearchCursor is where the next page of the channel group starts.
type liveSearchCursor struct {
	Sort   string `json:"s"`
	Desc   bool   `json:"d,omitempty"`
	Offset int    `json:"o"`
}

// searchChannels answers the channel group of a search: best match first (the whole name,
// then names beginning with what was typed, then the rest, each by channel number) or by
// name. ok is false for a profile not allowed Live TV, which has no such group.
func (s *Server) searchChannels(r *http.Request, q db.SearchQuery) (group SearchGroup, ok bool, err error) {
	lv, err := s.db.ViewerLive(r.Context(), viewer(r))
	if err != nil || !lv.LiveTV {
		return SearchGroup{}, false, err
	}
	sort := cmp.Or(q.Sort, db.SearchBest)
	if sort != db.SearchBest && sort != db.SortTitle {
		return SearchGroup{}, true, ErrInvalid("Channels can be sorted by best match or by name.")
	}
	limit, most := searchGroupFirst, searchGroupMost
	if q.Kind == liveSearchKind {
		limit, most = 50, 200
	}
	if q.Limit > 0 {
		limit = min(q.Limit, most)
	}
	var from liveSearchCursor
	if q.Cursor != "" {
		if decodeOpaque(q.Cursor, &from) != nil || from.Sort != sort || from.Desc != q.Desc || from.Offset < 0 {
			return SearchGroup{}, true, ErrInvalid("The cursor does not belong to this search.")
		}
	}
	matches, more, err := s.db.SearchLiveChannels(r.Context(), viewer(r), db.LiveChannelQuery{Owner: lv.Owner}, q.Text, liveSearchMost)
	if err != nil {
		return SearchGroup{}, true, searchRefusal(err)
	}
	shown := matches[:0]
	for _, m := range matches {
		if mayWatch(lv, m.LiveChannel) {
			shown = append(shown, m)
		}
	}
	slices.SortStableFunc(shown, func(a, b db.LiveChannelMatch) int {
		if sort == db.SortTitle {
			order := cmp.Or(cmp.Compare(a.SortName, b.SortName), cmp.Compare(a.ID, b.ID))
			if q.Desc {
				return -order
			}
			return order
		}
		return cmp.Or(cmp.Compare(a.Tier, b.Tier), cmp.Compare(a.Number, b.Number), cmp.Compare(a.ID, b.ID))
	})
	group = SearchGroup{Kind: liveSearchKind, Channels: []LiveChannel{}}
	if q.Cursor == "" {
		total := len(shown)
		group.Total, group.TotalIsMinimum = &total, more
	}
	end := min(from.Offset+limit, len(shown))
	for _, m := range shown[min(from.Offset, end):end] {
		group.Channels = append(group.Channels, channelOut(m.LiveChannel))
	}
	if end < len(shown) {
		group.NextCursor = encodeOpaque(liveSearchCursor{Sort: sort, Desc: q.Desc, Offset: end})
	}
	return group, true, nil
}

// The first results of each group when every group is asked for, and the most.
const searchGroupFirst, searchGroupMost = 12, 40
