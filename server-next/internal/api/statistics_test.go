package api

import (
	"context"
	"testing"
	"time"
)

// A play counts towards today's totals at once, and the totals outlast a restart once the
// server has written them, as it does as it stops. Only the owner reads them.
func TestLifetimeStatistics(t *testing.T) {
	th := newTheatre(t)
	// Midday, so moving the clock on never crosses into the next day's totals.
	now := time.Now()
	th.clock = time.Date(now.Year(), now.Month(), now.Day(), 12, 0, 0, 0, time.Local)
	th.owner = onMain(t, th.s, signIn(t, th.s, 0)) // signed in at that time
	owner := th.owner.AccessToken
	session := th.play(owner, th.film)
	th.clock = th.clock.Add(10 * time.Minute)
	if code := status(t, th.s, "PUT", "/v1/playback/sessions/"+session.ID+"/progress", owner,
		PlaybackProgress{State: "playing", PositionMs: 600_000, PlayClockMs: 600_000}); code != 200 {
		t.Fatalf("progress answered %d", code)
	}
	read := func() Statistics {
		var out Statistics
		if w := call(t, th.s, "GET", "/v1/admin/statistics", th.owner.AccessToken, nil, &out); w.Code != 200 {
			t.Fatalf("statistics answered %d: %s", w.Code, w.Body)
		}
		return out
	}
	stats := read()
	if stats.Totals["plays.film.original"] != 1 || stats.Totals["played_ms.film.original"] != 600_000 ||
		stats.Totals["people_most_in_a_day"] != 1 || stats.Totals["streams_peak"] != 1 {
		t.Fatalf("today's totals before they are written: %+v", stats.Totals)
	}
	th.s.Totals().Flush(context.Background())
	th.restart()
	if stats := read(); stats.Totals["plays.film.original"] != 1 || stats.Totals["people_most_in_a_day"] != 1 || stats.FirstDay == "" {
		t.Errorf("the totals after a restart: %+v", stats)
	}
	addMember(t, th.s, th.owner.AccessToken, "ada", "admin")
	admin := signInAs(t, th.s, "ada", testPassword, 1).AccessToken
	if code := status(t, th.s, "GET", "/v1/admin/statistics", admin, nil); code != 403 {
		t.Errorf("an administrator read the statistics: %d", code)
	}
}
