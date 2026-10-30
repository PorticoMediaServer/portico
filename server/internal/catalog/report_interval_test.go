package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/reportwindow"
	"github.com/PorticoMediaServer/portico/server/internal/telemetry"
)

func addReportFact(t *testing.T, db *sql.DB, item int64, id string, at int64, ms int64) int64 {
	t.Helper()
	r, e := db.Exec(`INSERT INTO play_history(playback_id,authority,account_id,profile_id,user_name,item_id,library_id,kind,title,started_ms,updated_ms,watched_ms) VALUES(?,'local','a','p','Person',?,'movies',1,'Report',?,?,999999)`, id, item, at, at)
	if e != nil {
		t.Fatal(e)
	}
	key, e := r.LastInsertId()
	if e != nil {
		t.Fatal(e)
	}
	tx, e := db.Begin()
	if e != nil {
		t.Fatal(e)
	}
	if e = recordPlayedInterval(tx, key, at, at+ms, ms); e != nil {
		tx.Rollback()
		t.Fatal(e)
	}
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
	return key
}

func TestReportCalendarFactsConserveAcrossAllOwnerReads(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	for _, tc := range []struct{ zone, until, period string }{
		{"America/New_York", "2026-03-10T12:00:00Z", "7d"},
		{"America/New_York", "2026-11-01T07:15:00Z", "24h"},
		{"Asia/Kathmandu", "2026-10-22T18:30:00Z", "today"},
		{"Pacific/Chatham", "2027-01-01T01:00:00Z", "30d"},
		{"Australia/Lord_Howe", "2026-10-06T00:00:00Z", "1y"},
	} {
		t.Run(tc.zone+tc.period, func(t *testing.T) {
			db := homeFixture(t)
			s := New(db)
			item := homeNames(t, db)["m1"].ID
			r, e := reportwindow.Resolve(tc.period, tc.zone, tc.until, -1)
			if e != nil {
				t.Fatal(e)
			}
			first := addReportFact(t, db, item, "first", r.FromMS(), 1000)
			addReportFact(t, db, item, "last", r.UntilMS()-900000, 2000)
			addReportFact(t, db, item, "at-until", r.UntilMS(), 3000)
			addReportFact(t, db, item, "before", r.FromMS()-900000, 4000)
			if _, e = db.Exec(`INSERT INTO play_history_time VALUES(?,?,70000)`, first, r.UntilMS()); e != nil {
				t.Fatal(e)
			}
			q := PlayHistoryQuery{Period: tc.period, TimeZone: tc.zone, Until: tc.until, AccountID: "a", LibraryID: "movies", Media: "movies", Search: "Report", Limit: 100}
			check := func(wantPlays, wantTime int64) {
				t.Helper()
				e := dbwork.WithReadSnapshot(context.Background(), db, func(ctx context.Context) error {
					borrowed, done, e := historyRead(ctx, db)
					if e != nil {
						return e
					}
					defer done()
					if borrowed != dbwork.Snapshot(ctx) {
						t.Fatal("composer opened an independent read")
					}
					h, e := s.DashboardHistory(ctx, q, "movies")
					if e != nil {
						return e
					}
					people, e := s.DashboardPeople(ctx, q)
					if e != nil {
						return e
					}
					table, e := s.PlayHistory(ctx, q)
					if e != nil {
						return e
					}
					summary, e := s.PlaySummary(ctx, q)
					if e != nil {
						return e
					}
					var plays, watched, heat, tableTime int64
					for _, b := range h.Buckets {
						for _, k := range b.Kinds {
							plays += k.Plays
							watched += k.WatchedMs
						}
					}
					for _, row := range h.BusiestTimes {
						for _, ms := range row {
							heat += ms
						}
					}
					for _, row := range table.Entries {
						tableTime += row.WatchedMs
					}
					if plays != wantPlays || watched != wantTime || heat != watched || tableTime != watched || *table.Total != plays || summary.Plays != plays || summary.WatchedMs != watched || len(people.Items) != 1 || people.Items[0].Plays != plays || people.Items[0].WatchedMs != watched {
						t.Fatal("interval/time/total disagreement", plays, watched, heat, table, summary, people)
					}
					if len(h.Top) != 1 || h.Top[0].Plays != plays || h.From != table.From || table.From != summary.From || summary.From != people.From || h.Until != tc.until {
						t.Fatal("different interval", h, table)
					}
					return nil
				})
				if e != nil {
					t.Fatal(e)
				}
			}
			check(2, 3000)
			// Ingestion is later; the real event was before until. Same-context
			// fresh reads include it and do not promise immutable page counts.
			addReportFact(t, db, item, "late", r.UntilMS()-900000, 6000)
			check(3, 9000)
		})
	}
}

func TestPlayedTimeSplitsAtQuarterBoundariesAndRetainsOldFacts(t *testing.T) {
	db := homeFixture(t)
	item := homeNames(t, db)["m1"].ID
	start := time.Date(2025, 1, 1, 0, 14, 50, 0, time.UTC).UnixMilli()
	id := addReportFact(t, db, item, "old", start, 1)
	if _, e := db.Exec(`DELETE FROM play_history_time WHERE history_id=?`, id); e != nil {
		t.Fatal(e)
	}
	tx, e := db.Begin()
	if e != nil {
		t.Fatal(e)
	}
	if e = recordPlayedInterval(tx, id, start, start+20000, 20000); e != nil {
		t.Fatal(e)
	}
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
	rows, e := db.Query(`SELECT bucket_ms,watched_ms FROM play_history_time WHERE history_id=? ORDER BY bucket_ms`, id)
	if e != nil {
		t.Fatal(e)
	}
	n := 0
	for rows.Next() {
		var at, ms int64
		if e = rows.Scan(&at, &ms); e != nil {
			t.Fatal(e)
		}
		if at%900000 != 0 || ms != 10000 {
			t.Fatal(at, ms)
		}
		n++
	}
	rows.Close()
	if n != 2 {
		t.Fatal(n)
	}
	all, e := New(db).DashboardHistory(context.Background(), PlayHistoryQuery{Period: "all", TimeZone: "Asia/Kathmandu", Until: "2026-10-22T12:00:00Z"}, "movies")
	if e != nil {
		t.Fatal(e)
	}
	var total int64
	for _, b := range all.Buckets {
		for _, k := range b.Kinds {
			total += k.WatchedMs
		}
	}
	if total != 20000 {
		t.Fatal("older-than13-month facts lost", total)
	}
	if _, e = db.Exec(`DELETE FROM play_history WHERE id=?`, id); e != nil {
		t.Fatal(e)
	}
	var remaining int
	if e = db.QueryRow(`SELECT count(*) FROM play_history_time WHERE history_id=?`, id).Scan(&remaining); e != nil || remaining != 0 {
		t.Fatal(remaining, e)
	}
}

func TestHistoryPanelsBorrowOneReadAcrossConcurrentLateFact(t *testing.T) {
	db := homeFixture(t)
	s := New(db)
	item := homeNames(t, db)["m1"].ID
	at := time.Date(2026, 10, 22, 11, 45, 0, 0, time.UTC).UnixMilli()
	addReportFact(t, db, item, "initial", at, 1000)
	q := PlayHistoryQuery{Period: "today", TimeZone: "Asia/Kathmandu", Until: "2026-10-22T12:00:00Z", Limit: 100}
	e := dbwork.WithReadSnapshot(context.Background(), db, func(ctx context.Context) error {
		people, e := s.DashboardPeople(ctx, q)
		if e != nil {
			return e
		}
		if people.Items[0].Plays != 1 {
			t.Fatal(people)
		}
		addReportFact(t, db, item, "concurrent-late", at, 2000)
		table, e := s.PlayHistory(ctx, q)
		if e != nil {
			return e
		}
		summary, e := s.PlaySummary(ctx, q)
		if e != nil {
			return e
		}
		history, e := s.DashboardHistory(ctx, q, "movies")
		if e != nil {
			return e
		}
		if *table.Total != 1 || summary.Plays != 1 || summary.WatchedMs != 1000 || history.Top[0].Plays != 1 {
			t.Fatal("mixed read transactions", table, summary, history)
		}
		return nil
	})
	if e != nil {
		t.Fatal(e)
	}
	fresh, e := s.PlayHistory(context.Background(), q)
	if e != nil || *fresh.Total != 2 {
		t.Fatal("fresh same-context history froze late event", fresh, e)
	}
}

func TestReportSummaryNativeMillisecondsRoundTrip(t *testing.T) {
	original := PlaySummary{WatchedMs: 1234, MostPlayed: []PlayCount{}, MostActive: []PlayCount{}}
	raw, e := json.Marshal(original)
	if e != nil {
		t.Fatal(e)
	}
	var out PlaySummary
	if e = json.Unmarshal(raw, &out); e != nil || out.WatchedMs != 1234 {
		t.Fatal(string(raw), out, e)
	}
}

// The common All-time start is a visibility-scoped source fact, never the
// first row remaining after a panel filter or a minimum from just one family.
func TestAllTimeUsesCommonVisibleStartAcrossFamiliesAndFilters(t *testing.T) {
	db := homeFixture(t)
	service := New(db)
	item := homeNames(t, db)["m1"].ID
	ctx := context.Background()
	zone := "Asia/Kathmandu"
	until := "2026-10-22T12:00:00Z"
	historyAt := time.Date(2025, 2, 4, 0, 0, 0, 0, time.UTC).UnixMilli()
	addReportFact(t, db, item, "retained", historyAt, 1234)
	telemetryAt := time.Date(2024, 1, 3, 0, 0, 0, 0, time.UTC).UnixMilli()
	if _, e := db.Exec(`INSERT INTO telemetry_rollups(bucket_ms,metric,samples,total,status,detail,first_event_ms,last_event_ms,measurement_revision) VALUES(?,'cpu',2,30,'available','',?,?,1)`, telemetryAt, telemetryAt, telemetryAt+2000); e != nil {
		t.Fatal(e)
	}
	cutoff, _ := time.Parse(time.RFC3339, until)
	// These real but not-yet-eligible facts must not enter the report.
	addReportFact(t, db, item, "at-until-all", cutoff.UnixMilli(), 9000)
	if _, e := db.Exec(`INSERT INTO telemetry_rollups(bucket_ms,metric,samples,total,status,detail,first_event_ms,last_event_ms,measurement_revision) VALUES(?,'cpu',1,99,'available','',?,?,1)`, cutoff.UnixMilli(), cutoff.UnixMilli(), cutoff.UnixMilli()); e != nil {
		t.Fatal(e)
	}
	expected := "2023-12-31T18:15:00Z"
	check := func(q PlayHistoryQuery, want int64) {
		t.Helper()
		e := dbwork.WithReadSnapshot(ctx, db, func(read context.Context) error {
			h, e := service.DashboardHistory(read, q, "movies")
			if e != nil {
				return e
			}
			people, e := service.DashboardPeople(read, q)
			if e != nil {
				return e
			}
			summary, e := service.PlaySummary(read, q)
			if e != nil {
				return e
			}
			table, e := service.PlayHistory(read, q)
			if e != nil {
				return e
			}
			load, e := telemetry.ReadAggregate(read, db, "all", zone, until)
			if e != nil {
				return e
			}
			for _, from := range []string{h.From, people.From, summary.From, table.From, load.From} {
				if from != expected {
					t.Fatal("family/filter changed common start", q, from, expected)
				}
			}
			if summary.Plays != want || *table.Total != want || h.Until != until || load.Until != until || len(h.Buckets) != len(load.Buckets) || len(people.Buckets) != len(h.Buckets) {
				t.Fatal("different All-time intervals/counts", h, summary, table, load)
			}
			if expected == "2023-12-31T18:15:00Z" && len(h.Buckets[0].Kinds) != 0 {
				t.Fatal("invented leading history fact", h.Buckets[0])
			}
			if len(load.Series["cpu"]) != 1 || load.Series["cpu"][0].V != 15 {
				t.Fatal("current quarter/false telemetry fact", load)
			}
			return nil
		})
		if e != nil {
			t.Fatal(e)
		}
	}
	q := PlayHistoryQuery{Period: "all", TimeZone: zone, Until: until, Limit: 100}
	check(q, 1)
	for _, filter := range []PlayHistoryQuery{
		{AccountID: "not-this-account"}, {LibraryID: "not-this-library"}, {Media: "music"}, {Search: "not-this-title"},
	} {
		filter.Period = q.Period
		filter.TimeZone = zone
		filter.Until = until
		filter.Limit = 100
		check(filter, 0)
	}
	// Fresh full history at the same context includes newly known old event-time
	// facts. Nothing persists an ingest-time or immutable report snapshot.
	addReportFact(t, db, item, "late-same-context", historyAt, 5678)
	check(q, 2)
	fresh, e := service.PlayHistory(ctx, q)
	if e != nil || fresh.From != expected || *fresh.Total != 2 {
		t.Fatal(fresh, e)
	}
	// When retained history is older than telemetry, it supplies the SAME start
	// to telemetry too. No telemetry-only minimum or fact loss is allowed.
	earlier := time.Date(2023, 3, 3, 0, 0, 0, 0, time.UTC).UnixMilli()
	addReportFact(t, db, item, "earliest-history", earlier, 42)
	expected = "2023-02-28T18:15:00Z"
	check(q, 3)
}
