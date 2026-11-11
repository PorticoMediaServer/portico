package api

import (
	"net/http"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/console"
	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

// The server's lifetime statistics for the owner's Dashboard: plays started, time played and
// data sent at home and away, each split by kind of media and by how it was delivered, how many
// people played and the most streams at once, for every day of the server's life. Today's
// totals include what has been counted and not yet written.

// Statistics is the totals over a range of days, by day, week or month.
type Statistics struct {
	From string `json:"from"`
	To   string `json:"to"`
	By   string `json:"by"`
	// FirstDay is the first day the server has totals for, absent when it has none yet.
	FirstDay string            `json:"firstDay,omitempty"`
	Points   []StatisticsPoint `json:"points"`
	// Totals are the range's totals. people and streams_peak are the highest of any day: a
	// person who played on two days is one person, not two.
	Totals map[string]int64 `json:"totals"`
}

// StatisticsPoint is one day, week (from Monday) or month.
type StatisticsPoint struct {
	Start  string           `json:"start"`
	Values map[string]int64 `json:"values"`
}

func (s *Server) statisticsRoutes() {
	Handle(s, "GET /v1/admin/statistics", Owner, s.statistics)
}

// highest says a metric is a level reached, which a longer span takes the highest of.
func highest(metric string) bool {
	return metric == console.MetricPeople || metric == console.MetricStreamsPeak
}

func (s *Server) statistics(r *http.Request, _ None) (Statistics, error) {
	q := r.URL.Query()
	now := s.now().In(time.Local)
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local)
	first, err := s.db.FirstDay(r.Context())
	if err != nil {
		return Statistics{}, err
	}
	out := Statistics{By: q.Get("by"), Points: []StatisticsPoint{}, Totals: map[string]int64{}}
	if out.By == "" {
		out.By = "day"
	}
	if out.By != "day" && out.By != "week" && out.By != "month" {
		return out, ErrField("by", "Choose day, week or month.")
	}
	from, to := today.AddDate(0, 0, -29), today
	for name, at := range map[string]*time.Time{"from": &from, "to": &to} {
		raw := q.Get(name)
		if raw == "" {
			continue
		}
		if name == "from" && raw == "all" {
			if first > 0 {
				*at = console.DayTime(first)
			}
			continue
		}
		t, err := time.ParseInLocation(time.DateOnly, raw, time.Local)
		if err != nil {
			return out, ErrField(name, `Give a date such as "2026-11-10", or "all" for from.`)
		}
		*at = t
	}
	if to.Before(from) {
		return out, ErrField("to", "The end comes before the start.")
	}
	if first > 0 {
		out.FirstDay = console.DayTime(first).Format(time.DateOnly)
	}
	out.From, out.To = from.Format(time.DateOnly), to.Format(time.DateOnly)
	prefix := q.Get("metric")
	rows, err := s.db.DailyTotals(r.Context(), console.Day(from), console.Day(to), prefix)
	if err != nil {
		return out, err
	}
	// Today's counts not yet written are added to today's: people counted since the last
	// write add to the day's people, and the most streams is the higher of the two.
	if day, pending := s.totals.Today(); day >= console.Day(from) && day <= console.Day(to) {
		at := map[string]int{}
		for n, row := range rows {
			if row.Day == day {
				at[row.Metric] = n
			}
		}
		for metric, value := range pending {
			if !strings.HasPrefix(metric, prefix) {
				continue
			}
			n, ok := at[metric]
			switch {
			case !ok:
				rows = append(rows, db.DailyTotal{Day: day, Metric: metric, Value: value})
			case metric == console.MetricStreamsPeak:
				rows[n].Value = max(rows[n].Value, value)
			default:
				rows[n].Value += value
			}
		}
	}
	start := func(day int) string {
		t := console.DayTime(day)
		switch out.By {
		case "week":
			t = t.AddDate(0, 0, -((int(t.Weekday()) + 6) % 7))
		case "month":
			t = time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.Local)
		}
		return t.Format(time.DateOnly)
	}
	add := func(values map[string]int64, metric string, value int64) {
		if highest(metric) {
			values[metric] = max(values[metric], value)
		} else {
			values[metric] += value
		}
	}
	index := map[string]int{}
	for _, row := range rows {
		key := start(row.Day)
		n, ok := index[key]
		if !ok {
			n = len(out.Points)
			index[key] = n
			out.Points = append(out.Points, StatisticsPoint{Start: key, Values: map[string]int64{}})
		}
		add(out.Points[n].Values, row.Metric, row.Value)
		add(out.Totals, row.Metric, row.Value)
	}
	return out, nil
}
