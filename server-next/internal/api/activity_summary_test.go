package api

import (
	"slices"
	"testing"
	"time"
)

// The owner's activity popover: every stream counted, the three newest shown in Now playing's
// order, with the server's name and the scans and tasks running. Only the owner reads it
// (console rule 9).
func TestTheOwnersActivitySummary(t *testing.T) {
	th := newTheatre(t)
	owner := th.owner.AccessToken
	addMember(t, th.s, owner, "ada", "admin")
	admin := signInAs(t, th.s, "ada", testPassword, 1).AccessToken
	var tokens []string
	for n, name := range []string{"bob", "cyd", "dia"} {
		addMember(t, th.s, owner, name, "member")
		tokens = append(tokens, signInAs(t, th.s, name, testPassword, n+2).AccessToken)
	}
	for _, token := range []string{admin, tokens[0]} {
		if code := status(t, th.s, "GET", "/v1/admin/activity", token, nil); code != 403 {
			t.Errorf("someone other than the owner read the activity summary: %d", code)
		}
	}
	var quiet ActivitySummary
	if w := call(t, th.s, "GET", "/v1/admin/activity", owner, nil, &quiet); w.Code != 200 || quiet.Streams.Count != 0 || len(quiet.Streams.Items) != 0 ||
		quiet.Scans == nil || quiet.Tasks.Items == nil || quiet.Alerts == nil || quiet.ServerName != th.s.serverName() {
		t.Fatalf("a quiet server's summary answered %d: %s", w.Code, w.Body)
	}

	// Four streams, a minute apart: the owner's film first, then each member's.
	var started []string
	for n, play := range []struct{ token, item string }{{owner, th.film}, {tokens[0], th.song}, {tokens[1], th.eps[0]}, {tokens[2], th.eps[1]}} {
		if n > 0 {
			th.clock.Add(time.Minute)
		}
		started = append(started, th.play(play.token, play.item).ID)
	}
	var busy ActivitySummary
	call(t, th.s, "GET", "/v1/admin/activity", owner, nil, &busy)
	var shown []string
	for _, item := range busy.Streams.Items {
		shown = append(shown, item.ID)
	}
	if busy.Streams.Count != 4 || !slices.Equal(shown, []string{started[3], started[2], started[1]}) {
		t.Errorf("the summary counts %d streams and shows %v; want 4, the newest three %v", busy.Streams.Count, shown, started[1:])
	}
	if busy.Streams.Items[0].Username != "dia" || busy.Streams.Items[0].Title == "" {
		t.Errorf("the newest stream reads %+v", busy.Streams.Items[0])
	}
}

// Now playing names an episode's show and its season and episode numbers.
func TestNowPlayingNamesAnEpisodesShowAndNumbers(t *testing.T) {
	th := newTheatre(t)
	owner := th.owner.AccessToken
	session := th.play(owner, th.eps[1])
	var playing Page[NowPlaying]
	call(t, th.s, "GET", "/v1/admin/sessions", owner, nil, &playing)
	if len(playing.Items) != 1 {
		t.Fatalf("Now playing: %+v", playing.Items)
	}
	got := playing.Items[0]
	if got.ID != session.ID || got.ShowTitle != "Dark" || got.ShowID == "" || got.SeasonNumber == nil || *got.SeasonNumber != 1 ||
		got.EpisodeNumber == nil || *got.EpisodeNumber != 2 {
		t.Errorf("the episode reads %+v", got)
	}
}
