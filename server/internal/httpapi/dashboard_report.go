package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/operations"
	"github.com/PorticoMediaServer/portico/server/internal/reportwindow"
	"github.com/PorticoMediaServer/portico/server/internal/telemetry"
)

// A future device clock must not turn the server's in-progress quarter into
// an aggregate. Reject it rather than silently changing a shared context.
func validateReportContext(zone, until string) error {
	_, at, e := reportwindow.Context(zone, until)
	if e != nil || at.After(time.Now().Truncate(reportwindow.Grain)) {
		return operations.ErrInvalid
	}
	return nil
}

// Report validation is a caller error on these owner reads. Keep the generic
// failure mapping for database/service errors and other route families.
func dashboardFailure(w http.ResponseWriter, err error) {
	if errors.Is(err, operations.ErrInvalid) || errors.Is(err, reportwindow.ErrInvalid) {
		consoleError(w, operations.ErrInvalid)
		return
	}
	failure(w, err)
}

type ReportContext struct {
	TimeZone string `json:"timeZone"`
	Until    string `json:"until"`
}
type DashboardTopPlayed struct {
	reportwindow.Interval
	Items   []catalog.DashboardTop `json:"items"`
	TopKind string                 `json:"topKind"`
}
type DashboardReport struct {
	Context   ReportContext            `json:"context"`
	History   catalog.DashboardHistory `json:"history"`
	People    catalog.DashboardPeople  `json:"people"`
	Summary   catalog.PlaySummary      `json:"summary"`
	TopPlayed DashboardTopPlayed       `json:"topPlayed"`
	Table     catalog.PlayHistoryPage  `json:"table"`
	Load      telemetry.Reading        `json:"load"`
}

var dashboardReportQuery = []string{"timeZone", "until", "period", "window", "peoplePeriod", "topPeriod", "topKind", "accountId", "libraryId", "media", "q", "sort", "direction", "topAccountId", "peopleCursor", "peopleLimit", "tableCursor", "tableLimit"}

func (d Dependencies) dashboardReportRoute(mux *http.ServeMux, service *telemetryService) {
	if d.Catalog == nil || d.Identity == nil {
		return
	}
	d.projectedHandle(mux, "GET /v1/admin/dashboard/report", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		p, e := d.owner(r)
		if e != nil {
			accessFailure(w, e)
			return
		}
		audience := historyAudience(p)
		if e := consoleQuery(r, dashboardReportQuery...); e != nil {
			consoleError(w, e)
			return
		}
		values := r.URL.Query()
		if e := validateReportContext(values.Get("timeZone"), values.Get("until")); e != nil {
			dashboardFailure(w, operations.ErrInvalid)
			return
		}
		period := values.Get("period")
		if period == "" {
			period = "30d"
		}
		peoplePeriod := values.Get("peoplePeriod")
		if peoplePeriod == "" {
			peoplePeriod = period
		}
		topPeriod := values.Get("topPeriod")
		if topPeriod == "" {
			topPeriod = period
		}
		window := values.Get("window")
		if window == "" {
			window = "24h"
		}
		if window == "10m" || window == "1h" {
			dashboardFailure(w, operations.ErrInvalid)
			return
		}
		limit := func(key string, fallback, max int) (int, error) {
			if raw := values.Get(key); raw != "" {
				n, e := strconv.Atoi(raw)
				if e != nil || n < 1 || n > max {
					return 0, operations.ErrInvalid
				}
				return n, nil
			}
			return fallback, nil
		}
		tableLimit, e := limit("tableLimit", 100, 200)
		if e != nil {
			dashboardFailure(w, e)
			return
		}
		peopleLimit, e := limit("peopleLimit", 12, 100)
		if e != nil {
			dashboardFailure(w, e)
			return
		}
		query := catalog.PlayHistoryQuery{Audience: audience, TimeZone: values.Get("timeZone"), Until: values.Get("until"), Period: period, AccountID: values.Get("accountId"), LibraryID: values.Get("libraryId"), Media: values.Get("media"), Search: values.Get("q"), Sort: values.Get("sort"), Direction: values.Get("direction"), Cursor: values.Get("tableCursor"), Limit: tableLimit}
		out := DashboardReport{Context: ReportContext{TimeZone: query.TimeZone, Until: query.Until}}
		// One actual, request-bounded read transaction: every aggregate borrows
		// this context. It is released before response and creates no token.
		e = dbwork.WithReadSnapshot(r.Context(), d.DB, func(ctx context.Context) error {
			var e error
			if out.History, e = d.Catalog.DashboardHistory(ctx, query, values.Get("topKind")); e != nil {
				return e
			}
			if out.Table, e = d.Catalog.PlayHistory(ctx, query); e != nil {
				return e
			}
			if out.Summary, e = d.Catalog.PlaySummary(ctx, query); e != nil {
				return e
			}
			people := catalog.PlayHistoryQuery{Audience: audience, TimeZone: query.TimeZone, Until: query.Until, Period: peoplePeriod, Cursor: values.Get("peopleCursor"), Limit: peopleLimit}
			if out.People, e = d.Catalog.DashboardPeople(ctx, people); e != nil {
				return e
			}
			topQuery := query
			topQuery.Period = topPeriod
			topQuery.AccountID = values.Get("topAccountId")
			topQuery.Media = ""
			top, e := d.Catalog.DashboardHistory(ctx, topQuery, values.Get("topKind"))
			if e != nil {
				return e
			}
			buckets := make([]reportwindow.Bucket, len(top.Buckets))
			for i, b := range top.Buckets {
				buckets[i] = reportwindow.Bucket{Start: b.Start, End: b.End, Partial: b.Partial}
			}
			out.TopPlayed = DashboardTopPlayed{Interval: reportwindow.Interval{Bounds: top.Bounds, Buckets: buckets}, Items: top.Top, TopKind: top.TopKind}
			if window == "live" {
				var ok bool
				out.Load, ok = service.collector().Read(window, query.TimeZone, query.Until)
				if !ok {
					return operations.ErrInvalid
				}
				return nil
			}
			out.Load, e = telemetry.ReadAggregate(ctx, d.DB, window, query.TimeZone, query.Until)
			if e == reportwindow.ErrInvalid {
				return operations.ErrInvalid
			}
			return e
		})
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
