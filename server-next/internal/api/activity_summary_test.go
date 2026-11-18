package api

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
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

func TestDashboardCarriesThePlayedTitlesPicture(t *testing.T) {
	th := newTheatre(t)
	s, owner := th.s, th.owner.AccessToken
	ada := addMember(t, s, owner, "ada", "admin")
	music, no := []string{th.libs[db.LibraryMusic].PublicID}, false
	if code := status(t, s, "PATCH", "/v1/admin/members/"+ada.ID, owner, MemberEdit{AllLibraries: &no, Libraries: &music}); code != 200 {
		t.Fatalf("limiting the administrator answered %d", code)
	}
	admin := signInAs(t, s, "ada", testPassword, 1).AccessToken
	for _, sample := range []struct {
		id   string
		role db.ImageRole
		hash string
	}{{th.film, db.ImagePoster, strings.Repeat("a", 64)}, {th.eps[0], db.ImageThumb, strings.Repeat("b", 64)}} {
		if _, err := s.db.UploadPicture(context.Background(), sample.id, db.NewPicture{Role: sample.role, Hash: sample.hash}); err != nil {
			t.Fatal(err)
		}
		session := th.play(owner, sample.id)
		want := "/v1/images/" + sample.hash
		playing := get[Page[NowPlaying]](t, s, owner, "/v1/admin/sessions").Items
		if len(playing) != 1 || playing[0].Image != want {
			t.Fatalf("Now playing for %s: %+v, want %s", sample.id, playing, want)
		}
		held := get[Page[NowPlaying]](t, s, admin, "/v1/admin/sessions").Items
		if len(held) != 1 || held[0].Image != "" {
			t.Fatalf("Now playing for the limited administrator: %+v", held)
		}
		if code := status(t, s, "DELETE", "/v1/playback/sessions/"+session.ID, owner, PlaybackStop{PositionMs: ptr(hour)}); code != 204 {
			t.Fatalf("stopping playback answered %d", code)
		}
		plays := get[Page[PlayEntry]](t, s, owner, "/v1/admin/play-history").Items
		if len(plays) == 0 || plays[0].Image != want {
			t.Fatalf("Play history for %s: %+v, want %s", sample.id, plays, want)
		}
		for _, play := range get[Page[PlayEntry]](t, s, admin, "/v1/admin/play-history").Items {
			if play.Image != "" {
				t.Fatalf("Play history revealed an image for a withheld title: %+v", play)
			}
		}
		th.clock.Add(time.Second)
	}
}
