package api

import (
	"context"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

// History lists a profile's own finished plays newest first, page by page and by period; it
// never shows a play made while history was paused, or another profile's; removing one entry
// and clearing it all leave watched marks alone.
func TestHistory(t *testing.T) {
	th := newTheatre(t)
	token := th.owner.AccessToken
	finish := func(item string) {
		t.Helper()
		end := hour
		session := th.play(token, item)
		status(t, th.s, "DELETE", "/v1/playback/sessions/"+session.ID, token, PlaybackStop{PositionMs: &end})
		th.clock = th.clock.Add(time.Minute)
	}
	finish(th.eps[0])
	finish(th.film)
	// A play while history is paused is the owner's record only.
	if err := th.s.db.SetProfilePreference(context.Background(), 1, db.PrefPauseHistory, "true", th.clock); err != nil {
		t.Fatal(err)
	}
	finish(th.eps[1])

	history := get[HistoryPage](t, th.s, token, "/v1/history?limit=1")
	if len(history.Items) != 1 || history.Items[0].Title != "Heat" || history.Items[0].Item == nil || !history.Items[0].Item.State.Watched ||
		history.NextCursor == "" {
		t.Fatalf("the first page: %+v", history)
	}
	rest := get[HistoryPage](t, th.s, token, "/v1/history?limit=1&cursor="+history.NextCursor)
	if len(rest.Items) != 1 || rest.Items[0].Title != "Secrets" || rest.Items[0].Subtitle != "Dark" || rest.NextCursor != "" {
		t.Fatalf("the second page: %+v", rest)
	}
	if day := get[HistoryPage](t, th.s, token, "/v1/history?period=24h"); len(day.Items) != 2 {
		t.Fatalf("the last 24 hours: %+v", day.Items)
	}
	th.clock = th.clock.Add(48 * time.Hour)
	code, renewed := renew(t, th.s, th.owner.RefreshToken, 0, "two-days-later")
	if code != 200 {
		t.Fatalf("renewing two days later answered %d", code)
	}
	token = renewed.AccessToken
	if day := get[HistoryPage](t, th.s, token, "/v1/history?period=24h"); len(day.Items) != 0 {
		t.Fatalf("the last 24 hours two days later: %+v", day.Items)
	}
	if week := get[HistoryPage](t, th.s, token, "/v1/history?period=7d"); len(week.Items) != 2 {
		t.Fatalf("the last 7 days: %+v", week.Items)
	}

	// Another profile of the account has an empty History and cannot remove the owner's entries.
	var kid Profile
	if w := call(t, th.s, "POST", "/v1/profiles", token, ProfileCreate{Name: "Kid"}, &kid); w.Code != 200 {
		t.Fatalf("adding a profile answered %d", w.Code)
	}
	var onKid Session
	call(t, th.s, "POST", "/v1/profiles/"+kid.ID+"/select", signIn(t, th.s, 4).Session.AccessToken, ProfileSelect{}, &onKid)
	if other := get[HistoryPage](t, th.s, onKid.AccessToken, "/v1/history"); len(other.Items) != 0 {
		t.Fatalf("another profile's History: %+v", other.Items)
	}
	if code := status(t, th.s, "DELETE", "/v1/history/"+rest.Items[0].ID, onKid.AccessToken, nil); code != 404 {
		t.Fatalf("another profile removing an entry answered %d", code)
	}

	// Removing one entry, twice, then clearing the rest.
	for range 2 {
		if code := status(t, th.s, "DELETE", "/v1/history/"+rest.Items[0].ID, token, nil); code != 204 {
			t.Fatalf("removing an entry answered %d", code)
		}
	}
	if left := get[HistoryPage](t, th.s, token, "/v1/history"); len(left.Items) != 1 || left.Items[0].Title != "Heat" {
		t.Fatalf("after removing one: %+v", left.Items)
	}
	if code := status(t, th.s, "DELETE", "/v1/history", token, nil); code != 204 {
		t.Fatalf("clearing answered %d", code)
	}
	if left := get[HistoryPage](t, th.s, token, "/v1/history"); len(left.Items) != 0 {
		t.Fatalf("after clearing: %+v", left.Items)
	}
	if !th.state(th.film).Watched || !th.state(th.eps[0]).Watched {
		t.Fatal("clearing History took away a watched mark")
	}
	if records, err := th.s.db.PlayRecords(context.Background(), 1, 10); err != nil || len(records) != 3 {
		t.Fatalf("the owner's play record after clearing: %+v, %v", records, err)
	}
	if code := status(t, th.s, "GET", "/v1/history?period=week", token, nil); code != 400 {
		t.Fatalf("an unknown period answered %d", code)
	}
}
