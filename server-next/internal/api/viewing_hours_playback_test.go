package api

import (
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/auth"
)

// When viewing hours end while something plays, the player's next report is answered with the
// reason, so the app stops and says why; the sign-in is kept, and the session ends by itself
// once its reports stop. Inside the hours the same report is taken.
func TestViewingHoursEndPlaybackAtTheNextReport(t *testing.T) {
	th := newTheatre(t)
	day := th.clock.Now().UTC()
	th.clock.Set(time.Date(day.Year(), day.Month(), day.Day(), 12, 0, 0, 0, time.UTC)) // noon: the window below stays inside one day
	max := addMember(t, th.s, th.owner.AccessToken, "max", "member")
	viewer := signInAs(t, th.s, "max", testPassword, 5)
	code, session := th.start(viewer.AccessToken, "", PlaybackStart{ItemID: th.film})
	if code >= 300 {
		t.Fatalf("starting playback answered %d", code)
	}
	report := func() (int, string) {
		r := newRequest("PUT", "/v1/playback/sessions/"+session.ID+"/progress", viewer.AccessToken, PlaybackProgress{State: "playing", PositionMs: 5000})
		w := serve(th.s, r)
		return w.Code, w.Body.String()
	}
	if code, body := report(); code != 200 {
		t.Fatalf("a report with no viewing hours answered %d %s", code, body)
	}
	// Viewing hours that end in five minutes. The owner's change ends the token, so it is
	// renewed inside the hours; the clock then moves past them.
	now := th.clock.Now().UTC()
	limits := noLimits()
	limits.ViewingHours = &auth.ViewingHours{Timezone: "UTC", Windows: []auth.ViewingWindow{{Days: []int{int(now.Weekday())}, StartMinute: 0, EndMinute: now.Hour()*60 + now.Minute() + 5}}}
	hold(t, th.s, th.owner.AccessToken, max.ID, limits)
	var renewed Session
	if code, s := renew(t, th.s, viewer.RefreshToken, 5, "inside"); code != 200 {
		t.Fatalf("renewing inside viewing hours answered %d", code)
	} else {
		renewed = s
	}
	viewer.AccessToken = renewed.AccessToken
	if code, body := report(); code != 200 {
		t.Fatalf("a report inside viewing hours answered %d %s", code, body)
	}
	th.clock.Add(6 * time.Minute)
	if code, body := report(); code != 403 || !strings.Contains(body, "outside_viewing_hours") {
		t.Errorf("a report after viewing hours ended answered %d %s, want 403 outside_viewing_hours", code, body)
	}
	if code := status(t, th.s, "GET", "/v1/me", viewer.AccessToken, nil); code != 200 {
		t.Errorf("the sign-in answered %d after viewing hours ended, want it kept (200)", code)
	}
}
