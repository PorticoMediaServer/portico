package api

import (
	"net/http"
	"time"
)

// HistoryPage is one page of the profile's own History, newest first.
type HistoryPage struct {
	Items      []HistoryEntry `json:"items"`
	NextCursor string         `json:"nextCursor,omitempty"`
}

// HistoryEntry is one finished play. Item, with the profile's state on it (where it stopped),
// is absent when the title has left the server: the entry keeps its name and kind and cannot
// be opened. Apps group entries by the local day of playedAt.
type HistoryEntry struct {
	ID        string `json:"id"`
	PlayedAt  string `json:"playedAt"`
	WatchedMs int64  `json:"watchedMs,omitempty"`
	Kind      string `json:"kind"`
	Title     string `json:"title"`
	Subtitle  string `json:"subtitle,omitempty"`
	Item      *Item  `json:"item,omitempty"`
}

// historyPeriods are the choices History offers. "24h" is the last 24 hours, not since midnight.
var historyPeriods = map[string]time.Duration{
	"24h": 24 * time.Hour, "7d": 7 * 24 * time.Hour, "30d": 30 * 24 * time.Hour, "90d": 90 * 24 * time.Hour, "all": 0,
}

func (s *Server) historyRoutes() {
	Handle(s, "GET /v1/history", WithProfile, func(r *http.Request, _ None) (HistoryPage, error) {
		query := r.URL.Query()
		period := query.Get("period")
		if period == "" {
			period = "all"
		}
		span, ok := historyPeriods[period]
		if !ok {
			return HistoryPage{}, ErrField("period", "Use 24h, 7d, 30d, 90d or all.")
		}
		var since int64
		if span > 0 {
			since = s.now().Add(-span).UnixMilli()
		}
		limit, err := limitParam(query)
		if err != nil {
			return HistoryPage{}, err
		}
		page, err := s.db.History(r.Context(), viewer(r), since, query.Get("cursor"), limit)
		if err != nil {
			return HistoryPage{}, refuse(err)
		}
		out := HistoryPage{Items: make([]HistoryEntry, len(page.Entries)), NextCursor: page.NextCursor}
		for n, e := range page.Entries {
			out.Items[n] = HistoryEntry{ID: e.ID, PlayedAt: timeString(e.PlayedAt), WatchedMs: e.WatchedMS, Kind: e.Kind.String(),
				Title: e.Title, Subtitle: e.Subtitle}
			if e.Item != nil {
				item := itemShape(*e.Item)
				out.Items[n].Item = &item
			}
		}
		return out, nil
	})
	Handle(s, "DELETE /v1/history/{id}", WithProfile, func(r *http.Request, _ None) (None, error) {
		return None{}, refuse(s.db.RemoveHistoryEntry(r.Context(), viewer(r), r.PathValue("id")))
	})
	// Clear history. Apps ask for a confirmation first: it cannot be undone.
	Handle(s, "DELETE /v1/history", WithProfile, func(r *http.Request, _ None) (None, error) {
		return None{}, s.db.ClearHistory(r.Context(), viewer(r), s.now())
	})
}
