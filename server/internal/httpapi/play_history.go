package httpapi

import (
	"net/http"
	"github.com/PorticoMediaServer/portico/server/internal/requestinput"
	"strconv"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

// historyAudience tells the catalogue read which live principal is asking, so
// retained plays of removed profiles stay visible only in the owner's main
// profile's history and activity views.
func historyAudience(p identity.Principal) *catalog.HistoryAudience {
	return &catalog.HistoryAudience{AccountID: p.AccountID, ProfileID: p.ProfileID, Epoch: p.Epoch}
}

// playHistoryRoutes serves the server's play history to its owner: every play,
// newest first, filtered by person, library and time (Settings › Server › Play
// history). A viewer's own History is GET /v1/history.
func (d Dependencies) playHistoryRoutes(mux *http.ServeMux) {
	if d.Catalog == nil || d.Identity == nil {
		return
	}
	d.projectedHandle(mux, "GET /v1/admin/play-history", func(w http.ResponseWriter, r *http.Request) {
		p, e := d.owner(r)
		if e != nil {
			accessFailure(w, e)
			return
		}
		audience := historyAudience(p)
		query := r.URL.Query()
		if e := validateReportContext(query.Get("timeZone"), query.Get("until")); e != nil {
			dashboardFailure(w, e)
			return
		}
		for key, values := range query {
			if len(values) != 1 || (key != "accountId" && key != "libraryId" && key != "period" && key != "cursor" && key != "limit" && key != "media" && key != "q" && key != "sort" && key != "direction" && key != "timeZone" && key != "until") {
				dashboardFailure(w, requestinput.New("play history accepts accountId, libraryId, period, cursor and limit"))
				return
			}
		}
		limit := 0
		if raw := query.Get("limit"); raw != "" {
			n, e := strconv.Atoi(raw)
			if e != nil || n < 1 {
				dashboardFailure(w, catalog.ErrCursor)
				return
			}
			limit = n
		}
		out, e := d.Catalog.PlayHistory(r.Context(), catalog.PlayHistoryQuery{Audience: audience, TimeZone: query.Get("timeZone"), Until: query.Get("until"), AccountID: query.Get("accountId"), LibraryID: query.Get("libraryId"), Period: query.Get("period"), Cursor: query.Get("cursor"), Limit: limit, Media: query.Get("media"), Search: query.Get("q"), Sort: query.Get("sort"), Direction: query.Get("direction")})
		if e != nil {
			dashboardFailure(w, e)
			return
		}
		if _, e = d.owner(r); e != nil {
			accessFailure(w, e)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		write(w, 200, out)
	})
	d.dashboardHistoryRoutes(mux)
	// The Dashboard's viewing statistics: totals of the same plays, for a period.
	d.projectedHandle(mux, "GET /v1/admin/play-history/summary", func(w http.ResponseWriter, r *http.Request) {
		p, e := d.owner(r)
		if e != nil {
			accessFailure(w, e)
			return
		}
		audience := historyAudience(p)
		query := r.URL.Query()
		if e := validateReportContext(query.Get("timeZone"), query.Get("until")); e != nil {
			dashboardFailure(w, e)
			return
		}
		for key, values := range query {
			if len(values) != 1 || (key != "period" && key != "timeZone" && key != "until" && key != "accountId" && key != "libraryId" && key != "media" && key != "q") {
				dashboardFailure(w, requestinput.New("the play history summary accepts period"))
				return
			}
		}
		period := query.Get("period")
		if period == "" {
			period = "24h"
		}
		out, e := d.Catalog.PlaySummary(r.Context(), catalog.PlayHistoryQuery{Audience: audience, Period: period, TimeZone: query.Get("timeZone"), Until: query.Get("until"), AccountID: query.Get("accountId"), LibraryID: query.Get("libraryId"), Media: query.Get("media"), Search: query.Get("q")})
		if e != nil {
			dashboardFailure(w, e)
			return
		}
		if _, e = d.owner(r); e != nil {
			accessFailure(w, e)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		write(w, 200, out)
	})
}
