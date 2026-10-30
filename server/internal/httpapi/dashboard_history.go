package httpapi

import (
	"net/http"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"strconv"
)

func (d Dependencies) dashboardHistoryRoutes(mux *http.ServeMux) {
	for _, people := range []bool{false, true} {
		path := "GET /v1/admin/play-history/dashboard"
		if people {
			path = "GET /v1/admin/play-history/people"
		}
		d.httpHandle(mux, path, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "no-store")
			p, e := d.owner(r)
			if e != nil {
				accessFailure(w, e)
				return
			}
			audience := historyAudience(p)
			if e := consoleQuery(r, "period", "accountId", "libraryId", "media", "q", "cursor", "limit", "topKind", "timeZone", "until"); e != nil {
				consoleError(w, e)
				return
			}
			query := r.URL.Query()
			if e := validateReportContext(query.Get("timeZone"), query.Get("until")); e != nil {
				dashboardFailure(w, e)
				return
			}
			limit := 12
			if raw := query.Get("limit"); raw != "" {
				n, e := strconv.Atoi(raw)
				if e != nil || n < 1 || n > 100 {
					dashboardFailure(w, catalog.ErrCursor)
					return
				}
				limit = n
			}
			period := query.Get("period")
			if period == "" {
				period = "30d"
			}
			q := catalog.PlayHistoryQuery{Audience: audience, TimeZone: query.Get("timeZone"), Until: query.Get("until"), Period: period, AccountID: query.Get("accountId"), LibraryID: query.Get("libraryId"), Media: query.Get("media"), Search: query.Get("q"), Cursor: query.Get("cursor"), Limit: limit}
			var out any
			if people {
				out, e = d.Catalog.DashboardPeople(r.Context(), q)
			} else {
				out, e = d.Catalog.DashboardHistory(r.Context(), q, query.Get("topKind"))
			}
			if e != nil {
				dashboardFailure(w, e)
				return
			}
			if _, e = d.owner(r); e != nil {
				accessFailure(w, e)
				return
			}
			write(w, 200, out)
		})
	}
}
