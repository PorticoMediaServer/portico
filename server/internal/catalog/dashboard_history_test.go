package catalog

import (
	"context"
	"fmt"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"testing"
	"time"
)

func TestDashboardPeopleAccountPagingAndHistorySharedFilters(t *testing.T) {
	db := homeFixture(t)
	s := New(db)
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	s.Clock = func() time.Time { return now }
	item := homeNames(t, db)["m1"].ID
	for n := 0; n < 137; n++ {
		id := fmt.Sprintf("a%03d", n)
		_, e := db.Exec(`INSERT INTO play_history(playback_id,authority,account_id,profile_id,user_name,profile_name,item_id,library_id,kind,title,started_ms,updated_ms,watched_ms) VALUES(?,'local',?,'profile',?,'Kids',?,'movies',1,'Movie',?,?,?)`, id, id, id, item, now.Add(-time.Hour).UnixMilli(), now.UnixMilli(), 1000*(n+1))
		if e != nil {
			t.Fatal(e)
		}
		if _, e = db.Exec(`INSERT INTO play_history_time SELECT id,?,? FROM play_history WHERE playback_id=?`, now.Add(-time.Hour).UnixMilli(), 1000*(n+1), id); e != nil {
			t.Fatal(e)
		}
	}
	seen := map[string]bool{}
	q := PlayHistoryQuery{TimeZone: "UTC", Until: now.UTC().Format(time.RFC3339Nano), Period: "7d", Limit: 12}
	for {
		page, e := s.DashboardPeople(context.Background(), q)
		if e != nil {
			t.Fatal(e)
		}
		if len(page.Items) > 12 {
			t.Fatal("unbounded page")
		}
		for _, p := range page.Items {
			if seen[p.AccountID] {
				t.Fatal("duplicate", p.AccountID)
			}
			seen[p.AccountID] = true
			if p.Plays != 1 || len(p.Profiles) != 1 || len(p.Kinds) != 1 || p.Kinds[0].Kind != "movies" {
				t.Fatal(p)
			}
		}
		if page.NextCursor == "" {
			break
		}
		q.Cursor = page.NextCursor
	}
	if len(seen) != 137 {
		t.Fatal(len(seen))
	}
	q = PlayHistoryQuery{TimeZone: "UTC", Until: now.UTC().Format(time.RFC3339Nano), Period: "7d", AccountID: "a002", Media: "movies", Search: "Movie", Limit: 100}
	page, e := s.PlayHistory(context.Background(), q)
	if e != nil || len(page.Entries) != 1 {
		t.Fatal(page, e)
	}
	people, e := s.DashboardPeople(context.Background(), q)
	if e != nil || len(people.Items) != 1 || people.Items[0].Plays != *page.Total {
		t.Fatal(people, e)
	}
	for _, sort := range []string{"title", "person", "player", "when", "watched", "location"} {
		total := 0
		cursor := ""
		for {
			p, e := s.PlayHistory(context.Background(), PlayHistoryQuery{TimeZone: "UTC", Until: now.UTC().Format(time.RFC3339Nano), Period: "7d", Limit: 17, Sort: sort, Direction: "asc", Cursor: cursor})
			if e != nil {
				t.Fatal(sort, e)
			}
			total += len(p.Entries)
			if p.NextCursor == "" {
				break
			}
			cursor = p.NextCursor
		}
		if total != 137 {
			t.Fatal(sort, total)
		}
	}
}

func TestDashboardBucketsUseDailyWindowsWeeklyYearAndObservedTime(t *testing.T) {
	db := homeFixture(t)
	s := New(db)
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	s.Clock = func() time.Time { return now }
	item := homeNames(t, db)["m1"].ID
	result, e := db.Exec(`INSERT INTO play_history(playback_id,authority,account_id,profile_id,user_name,item_id,library_id,kind,title,started_ms,updated_ms,watched_ms) VALUES('actual','local','a','p','Person',?,'movies',1,'Movie',?,?,60000)`, item, now.Add(-time.Hour).UnixMilli(), now.UnixMilli())
	if e != nil {
		t.Fatal(e)
	}
	id, _ := result.LastInsertId()
	if _, e = db.Exec(`INSERT INTO play_history_time VALUES(?,?,60000)`, id, now.Add(-time.Hour).UnixMilli()); e != nil {
		t.Fatal(e)
	}
	for _, period := range []string{"7d", "30d", "90d", "1y", "all"} {
		out, e := s.DashboardHistory(context.Background(), PlayHistoryQuery{TimeZone: "UTC", Until: now.UTC().Format(time.RFC3339Nano), Period: period}, "movies")
		if e != nil {
			t.Fatal(period, e)
		}
		var total int64
		for n, b := range out.Buckets {
			start, e := time.Parse(time.RFC3339Nano, b.Start)
			if e != nil {
				t.Fatal(e)
			}
			if n > 0 {
				if start.Hour() != 0 || start.Minute() != 0 || start.Second() != 0 {
					t.Fatal("calendar boundary not midnight", b)
				}
				if period == "1y" && start.Weekday() != now.AddDate(0, 0, -6).Weekday() {
					t.Fatal("week does not end with today", b)
				}
				if period == "all" && start.Day() != 1 {
					t.Fatal("all-time month not calendar", b)
				}
			}
			for _, k := range b.Kinds {
				total += k.WatchedMs
			}
		}
		if total != 60000 || len(out.Top) != 1 || out.Top[0].Plays != 1 || out.Top[0].People != 1 || out.Top[0].CountUnit != "movie_plays" {
			t.Fatal(period, out)
		}
		if !out.Buckets[len(out.Buckets)-1].Partial {
			t.Fatal("current bucket not partial")
		}
	}
	// Retention/Start over deletes both analytics and time facts.
	if _, e = db.Exec(`DELETE FROM play_history WHERE account_id='a'`); e != nil {
		t.Fatal(e)
	}
	var remaining int
	if e = db.QueryRow(`SELECT count(*) FROM play_history_time`).Scan(&remaining); e != nil || remaining != 0 {
		t.Fatal(remaining, e)
	}
}

func TestDashboardHistoryWindowsKeepDailyFactsAndEndAnchoredWeeks(t *testing.T) {
	db := homeFixture(t)
	s := New(db)
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	s.Clock = func() time.Time { return now }
	item := homeNames(t, db)["m1"].ID
	parse := func(t *testing.T, value string) time.Time {
		t.Helper()
		at, e := time.Parse(time.RFC3339Nano, value)
		if e != nil {
			t.Fatal(e)
		}
		return at
	}
	for n, fact := range []struct {
		at, account, library string
		kind                 int
		watched              int64
	}{
		{"2026-10-15T23:50:00Z", "a", "movies", 1, 11000},
		{"2026-10-16T00:10:00Z", "a", "movies", 1, 22000},
		{"2026-10-18T23:40:00Z", "a", "movies", 1, 33000},
		{"2026-10-19T00:10:00Z", "a", "movies", 1, 44000},
		{"2026-10-21T17:20:00Z", "a", "movies", 1, 55000},
		{"2025-10-22T12:05:00Z", "a", "movies", 1, 66000},
		{"2026-10-19T00:10:00Z", "b", "movies", 1, 600000},
		{"2026-10-19T00:10:00Z", "a", "movies", 7, 700000},
		{"2026-10-19T00:10:00Z", "a", "other", 1, 800000},
	} {
		at := parse(t, fact.at)
		result, e := db.Exec(`INSERT INTO play_history(playback_id,authority,account_id,profile_id,user_name,item_id,library_id,kind,title,started_ms,updated_ms,watched_ms) VALUES(?,'local',?,'p','Person',?,?,?,'Window',?,?,?)`, fmt.Sprint(n), fact.account, item, fact.library, fact.kind, at.UnixMilli(), now.UnixMilli(), fact.watched)
		if e != nil {
			t.Fatal(e)
		}
		id, e := result.LastInsertId()
		if e != nil {
			t.Fatal(e)
		}
		if _, e = db.Exec(`INSERT INTO play_history_time VALUES(?,?,?)`, id, at.Truncate(15*time.Minute).UnixMilli(), fact.watched); e != nil {
			t.Fatal(e)
		}
	}
	type totals struct{ plays, watched int64 }
	daily := map[string]totals{
		"2026-10-15": {1, 11000}, "2026-10-16": {1, 22000}, "2026-10-18": {1, 33000},
		"2026-10-19": {1, 44000}, "2026-10-21": {1, 55000},
	}
	for _, tc := range []struct {
		period string
		count  int
		width  time.Duration
		total  totals
		want   map[string]totals
	}{
		{"30d", 30, 24 * time.Hour, totals{5, 165000}, daily},
		{"90d", 90, 24 * time.Hour, totals{5, 165000}, daily},
		{"1y", 52, 7 * 24 * time.Hour, totals{5, 165000}, map[string]totals{
			"2026-10-09": {1, 11000}, "2026-10-16": {4, 154000},
		}},
	} {
		t.Run(tc.period, func(t *testing.T) {
			q := PlayHistoryQuery{TimeZone: "UTC", Until: now.UTC().Format(time.RFC3339Nano), Period: tc.period, AccountID: "a", LibraryID: "movies", Media: "movies", Search: "Window"}
			out, e := s.DashboardHistory(context.Background(), q, "movies")
			if e != nil {
				t.Fatal(e)
			}
			from, until := parse(t, out.From), parse(t, out.Until)
			if from.Hour() != 0 || !until.Equal(now) || out.TimeZone != "UTC" || len(out.Buckets) != tc.count {
				t.Fatal("range coverage changed", out)
			}
			previous := from
			got := map[string]totals{}
			var sum totals
			for i, b := range out.Buckets {
				start, end := parse(t, b.Start), parse(t, b.End)
				if !start.Equal(previous) || !end.After(start) || end.After(until) {
					t.Fatal("gap, overlap or inverted bucket", b)
				}
				edge := i == len(out.Buckets)-1
				if b.Partial != edge || !edge && end.Sub(start) != tc.width {
					t.Fatal("incorrect bucket width or clipping", b)
				}
				previous = end
				for _, k := range b.Kinds {
					if k.Kind != "movies" {
						t.Fatal("unselected kind", k)
					}
					key := start.Format("2006-01-02")
					v := got[key]
					v.plays += k.Plays
					v.watched += k.WatchedMs
					got[key] = v
					sum.plays += k.Plays
					sum.watched += k.WatchedMs
				}
			}
			if !previous.Equal(until) || sum != tc.total || len(got) != len(tc.want) {
				t.Fatal("facts lost, duplicated or padded", got, sum)
			}
			for day, want := range tc.want {
				if got[day] != want {
					t.Fatal("facts placed in wrong calendar window", day, got[day], want)
				}
			}
			var heat int64
			for _, day := range out.BusiestTimes {
				for _, ms := range day {
					heat += ms
				}
			}
			if heat != sum.watched || len(out.Top) != 1 || out.Top[0].Plays != sum.plays {
				t.Fatal("aggregate filters disagree", heat, out.Top)
			}
		})
	}
}

func TestObservedPlayedTimeExcludesPauseSeekAndMissingReports(t *testing.T) {
	db := homeFixture(t)
	item := homeNames(t, db)["m1"].ID
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	viewer := identity.Viewer{Authority: "local", AccountID: "fixture", ProfileID: "fixture-profile"}
	tx, e := db.Begin()
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback()
	// Use existing actual history insert producer, then reports at a fake clock.
	for n, report := range []struct {
		after time.Duration
		pos   int64
		state string
	}{
		{0, 30000, "playing"}, {10 * time.Second, 40000, "paused"}, {30 * time.Second, 40000, "playing"},
		{40 * time.Second, 50000, "playing"}, {50 * time.Second, 800000, "playing"}, {3 * time.Minute, 810000, "playing"},
	} {
		// Fixture account is intentionally absent: insertPlay retains only validated
		// canonical names. Add a row directly for this timing test's first report.
		if n == 0 {
			_, e = tx.Exec(`INSERT INTO play_history(playback_id,authority,account_id,profile_id,user_name,item_id,library_id,kind,title,started_ms,updated_ms,last_position_ms,last_state) VALUES('timing','local','fixture','fixture-profile','fixture',?,'movies',1,'Movie',?,?,30000,'playing')`, item, now.UnixMilli(), now.UnixMilli())
		} else {
			_, e = recordPlay(tx, viewer, "timing", item, 1, now.Add(report.after), report.pos, 1000, true, false, false, report.state)
		}
		if e != nil {
			t.Fatal(e)
		}
	}
	var watched int64
	if e = tx.QueryRow(`SELECT watched_ms FROM play_history WHERE playback_id='timing'`).Scan(&watched); e != nil || watched != 20000 {
		t.Fatal(watched, e)
	}
}

func TestHistoryFiltersUseIndexedPersonRangeAndRejectSQLControls(t *testing.T) {
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	db := homeFixture(t)
	s := New(db)
	if _, e := s.PlayHistory(context.Background(), PlayHistoryQuery{TimeZone: "UTC", Until: now.UTC().Format(time.RFC3339Nano), Period: "all", Sort: "title;DROP TABLE accounts", Limit: 10}); e == nil {
		t.Fatal("sort injection accepted")
	}
	rows, e := db.Query(`EXPLAIN QUERY PLAN SELECT h.id FROM play_history h WHERE h.account_id=? AND h.started_ms>=? ORDER BY h.started_ms DESC,h.id DESC LIMIT 101`, "a", 0)
	if e != nil {
		t.Fatal(e)
	}
	defer rows.Close()
	found := false
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if e = rows.Scan(&id, &parent, &unused, &detail); e != nil {
			t.Fatal(e)
		}
		if detail == "SEARCH h USING COVERING INDEX play_history_account (account_id=? AND started_ms>?)" {
			found = true
		}
	}
	if !found {
		t.Fatal("person/range index not used")
	}
}

func TestDashboardPeopleAndHistoryRetainAudiobooksAndLiveKinds(t *testing.T) {
	db := homeFixture(t)
	s := New(db)
	now := time.Date(2026, 10, 22, 17, 0, 0, 0, time.FixedZone("host", -7*3600))
	s.Clock = func() time.Time { return now }
	names := homeNames(t, db)
	for n, row := range []struct {
		item string
		kind int
	}{{"p1", 9}, {"p2", 0}, {"t1", 7}, {"t2", 7}, {"e101", 4}, {"e102", 4}} {
		result, e := db.Exec(`INSERT INTO play_history(playback_id,authority,account_id,profile_id,user_name,item_id,library_id,kind,title,started_ms,updated_ms,watched_ms) VALUES(?,'local','a','p','Person',?,'library',?,'Title',?,?,1000)`, fmt.Sprint(n), names[row.item].ID, row.kind, now.Add(-time.Hour).UnixMilli(), now.UnixMilli())
		if e != nil {
			t.Fatal(e)
		}
		id, _ := result.LastInsertId()
		if _, e = db.Exec(`INSERT INTO play_history_time VALUES(?,?,1000)`, id, now.Add(-time.Hour).UnixMilli()); e != nil {
			t.Fatal(e)
		}
	}
	people, e := s.DashboardPeople(context.Background(), PlayHistoryQuery{TimeZone: "UTC", Until: now.UTC().Format(time.RFC3339Nano), Period: "7d"})
	if e != nil || len(people.Items) != 1 {
		t.Fatal(people, e)
	}
	peopleKinds := map[string]bool{}
	for _, k := range people.Items[0].Kinds {
		peopleKinds[k.Kind] = k.Plays > 0 && k.WatchedMs > 0
	}
	if !peopleKinds["audiobooks"] || !peopleKinds["live"] {
		t.Fatal(people)
	}
	for _, top := range []struct{ kind, target, unit string }{{"music", "album", "track_plays"}, {"tv", "show", "episode_plays"}} {
		history, e := s.DashboardHistory(context.Background(), PlayHistoryQuery{TimeZone: "UTC", Until: now.UTC().Format(time.RFC3339Nano), Period: "7d"}, top.kind)
		if e != nil || len(history.Top) != 1 {
			t.Fatal(history, e)
		}
		v := history.Top[0]
		if v.ItemID != names[top.target].Public || v.Plays != 2 || v.People != 1 || v.CountUnit != top.unit {
			t.Fatal(v)
		}
		if history.TimeZone != "UTC" {
			t.Fatal("timezone did not match UTC facts", history.TimeZone)
		}
		kinds := map[string]bool{}
		for _, b := range history.Buckets {
			for _, k := range b.Kinds {
				kinds[k.Kind] = true
			}
		}
		if !kinds["audiobooks"] || !kinds["live"] {
			t.Fatal("history collapsed the distinct media families", kinds)
		}
	}
}

// Proved played time (playback observations) replaces the inference between
// reports: a backward seek, a slow rate or a long gap neither zeroes nor
// halves it, and it splits at the fifteen-minute bucket boundary.
func TestProvedPlayedTimeIsCreditedExactlyAndBucketed(t *testing.T) {
	db := homeFixture(t)
	item := homeNames(t, db)["m1"].ID
	bucket := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	start := bucket.Add(14 * time.Minute)
	viewer := identity.Viewer{Authority: "local", AccountID: "fixture", ProfileID: "fixture-profile"}
	tx, e := db.Begin()
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback()
	if _, e = tx.Exec(`INSERT INTO play_history(playback_id,authority,account_id,profile_id,user_name,item_id,library_id,kind,title,started_ms,updated_ms,last_position_ms,last_state) VALUES('proved','local','fixture','fixture-profile','fixture',?,'movies',1,'Movie',?,?,600000,'playing')`, item, start.UnixMilli(), start.UnixMilli()); e != nil {
		t.Fatal(e)
	}
	// 90 s of play reported over a 120 s span crossing 12:15, after a seek
	// back to 5 minutes: the old rule would have credited nothing.
	to := start.Add(2 * time.Minute)
	if _, e = recordPlayEvidence(tx, viewer, "proved", item, 1, to, 300000, 1000, true, false, false, &PlayedTime{FromMs: start.UnixMilli(), ToMs: to.UnixMilli(), WatchedMs: 90000}, "playing"); e != nil {
		t.Fatal(e)
	}
	var watched, first, second int64
	if e = tx.QueryRow(`SELECT watched_ms FROM play_history WHERE playback_id='proved'`).Scan(&watched); e != nil || watched != 90000 {
		t.Fatal(watched, e)
	}
	if e = tx.QueryRow(`SELECT COALESCE(sum(CASE WHEN t.bucket_ms=? THEN t.watched_ms END),0),COALESCE(sum(CASE WHEN t.bucket_ms=? THEN t.watched_ms END),0) FROM play_history_time t JOIN play_history h ON h.id=t.history_id WHERE h.playback_id='proved'`, bucket.UnixMilli(), bucket.Add(15*time.Minute).UnixMilli()).Scan(&first, &second); e != nil {
		t.Fatal(e)
	}
	if first != 45000 || second != 45000 {
		t.Fatalf("buckets %d and %d, want 45000 each", first, second)
	}
}
